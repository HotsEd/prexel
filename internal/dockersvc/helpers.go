package dockersvc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/system"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/docker/go-connections/nat"
)

// minSupportedDockerVersion is the lowest acceptable Docker engine version,
// per the Spec (Servers, Tech Review §6). Compared semver-style.
const minSupportedDockerVersion = "20.10"

// Info wraps `docker info`.
func Info(ctx context.Context, p Provider) (system.Info, error) {
	cli, err := p.Client(ctx)
	if err != nil {
		return system.Info{}, err
	}
	return cli.Info(ctx)
}

// Version wraps `docker version` and enforces the >=20.10 floor. The
// returned types.Version is whatever the server reported. When the server is
// too old, ErrUnsupportedDockerVersion is wrapped with the version string.
func Version(ctx context.Context, p Provider) (types.Version, error) {
	cli, err := p.Client(ctx)
	if err != nil {
		return types.Version{}, err
	}
	v, err := cli.ServerVersion(ctx)
	if err != nil {
		return types.Version{}, err
	}
	if !versionAtLeast(v.Version, minSupportedDockerVersion) {
		return v, fmt.Errorf("%w: server reports %q, need %q", ErrUnsupportedDockerVersion, v.Version, minSupportedDockerVersion)
	}
	return v, nil
}

// versionAtLeast compares two dotted decimal version strings ("20.10.7",
// "23.0", "20.10.7-ce") and reports whether got >= want. Pre-release/build
// suffixes are ignored (anything past the first non-digit non-dot character).
func versionAtLeast(got, want string) bool {
	gp := parseDottedVersion(got)
	wp := parseDottedVersion(want)
	for i := 0; i < len(wp); i++ {
		var g int
		if i < len(gp) {
			g = gp[i]
		}
		if g > wp[i] {
			return true
		}
		if g < wp[i] {
			return false
		}
	}
	return true
}

func parseDottedVersion(s string) []int {
	parts := strings.Split(s, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		end := 0
		for end < len(p) && p[end] >= '0' && p[end] <= '9' {
			end++
		}
		if end == 0 {
			break
		}
		n, err := strconv.Atoi(p[:end])
		if err != nil {
			break
		}
		out = append(out, n)
		if end < len(p) {
			break
		}
	}
	return out
}

// PortBinding maps a container port (e.g. "3000/tcp") to a host port.
type PortBinding struct {
	HostPort      int    // 0 means "let Docker pick"
	ContainerPort int    // required
	Protocol      string // "tcp" (default) or "udp"
}

// ContainerOpts is the (small) subset of container parameters we expose. We
// keep it map/struct-shaped rather than leaking docker/api/types so callers
// don't accidentally depend on Moby SDK internals.
type ContainerOpts struct {
	Name          string            // required; also used as the prexel-net alias
	Image         string            // required; must already exist locally
	Cmd           []string          // optional; overrides image CMD
	Env           map[string]string // env vars merged into the container
	Labels        map[string]string // labels (prexel.* labels are merged in)
	Network       string            // defaults to PrexelNetwork
	Aliases       []string          // extra network aliases (Name is always added)
	PortBindings  []PortBinding     // optional; for apps without a domain
	RestartPolicy string            // "no" | "always" | "unless-stopped" | "on-failure"
	MemoryLimit   string            // e.g. "512m", "1g", or "" for unlimited
	CPULimit      string            // e.g. "0.5", "1", "1.5", or "" for unlimited
	AutoRemove    bool              // sets HostConfig.AutoRemove
	Healthcheck   *container.HealthConfig

	// Mounts attaches bind mounts and named volumes to the container.
	// Named volumes must be pre-created with EnsureVolume — RunContainer
	// does NOT create them implicitly.
	Mounts []Mount

	// Advanced cgroup knobs. All optional; empty/zero/nil = no override.
	MemorySwap        string // matches `--memory-swap`; "-1" = unlimited swap
	MemorySwappiness  *int64 // matches `--memory-swappiness` (0..100)
	MemoryReservation string // soft memory limit, same parser as MemoryLimit
	CPUSet            string // matches `--cpuset-cpus`, e.g. "0,2-4"
	CPUShares         int64  // matches `--cpu-shares`; 0 = no override
}

// Mount describes a single bind or named volume mount on a container.
// Type is "bind" or "volume". For "bind", Source is a host path; for
// "volume" Source is the named volume name (must already exist — call
// EnsureVolume first).
type Mount struct {
	Source   string
	Target   string
	Type     string
	ReadOnly bool
}

// RunContainer creates and starts a container with the given options and
// returns the container ID. The container is attached to PrexelNetwork (or
// opts.Network) with Name as a network alias.
//
// RunContainer does NOT pull images. Use the build engine / image puller
// before invoking it.
func RunContainer(ctx context.Context, p Provider, opts ContainerOpts) (string, error) {
	if opts.Name == "" {
		return "", errors.New("docker: RunContainer: empty name")
	}
	if opts.Image == "" {
		return "", errors.New("docker: RunContainer: empty image")
	}

	cli, err := p.Client(ctx)
	if err != nil {
		return "", err
	}

	netName := opts.Network
	if netName == "" {
		netName = PrexelNetwork
	}

	env := make([]string, 0, len(opts.Env))
	for k, v := range opts.Env {
		env = append(env, k+"="+v)
	}

	labels := map[string]string{
		"prexel.managed": "true",
	}
	for k, v := range opts.Labels {
		labels[k] = v
	}

	exposedPorts := nat.PortSet{}
	portBindings := nat.PortMap{}
	for _, b := range opts.PortBindings {
		proto := b.Protocol
		if proto == "" {
			proto = "tcp"
		}
		port, perr := nat.NewPort(proto, strconv.Itoa(b.ContainerPort))
		if perr != nil {
			return "", fmt.Errorf("docker: invalid container port %d/%s: %w", b.ContainerPort, proto, perr)
		}
		exposedPorts[port] = struct{}{}
		host := ""
		if b.HostPort > 0 {
			host = strconv.Itoa(b.HostPort)
		}
		portBindings[port] = append(portBindings[port], nat.PortBinding{
			HostIP:   "0.0.0.0",
			HostPort: host,
		})
	}

	resources, err := parseResources(opts.MemoryLimit, opts.CPULimit)
	if err != nil {
		return "", err
	}
	// Advanced cgroup knobs — overlay onto the base Resources struct.
	if opts.MemorySwap != "" {
		swap, err := parseMemorySwap(opts.MemorySwap)
		if err != nil {
			return "", err
		}
		resources.MemorySwap = swap
	}
	if opts.MemoryReservation != "" {
		reservation, err := parseMemory(opts.MemoryReservation)
		if err != nil {
			return "", err
		}
		resources.MemoryReservation = reservation
	}
	if opts.MemorySwappiness != nil {
		// Copy so callers can't mutate after-the-fact (the moby SDK keeps
		// the pointer; aliasing would be a foot-gun in concurrent use).
		v := *opts.MemorySwappiness
		resources.MemorySwappiness = &v
	}
	if opts.CPUSet != "" {
		resources.CpusetCpus = opts.CPUSet
	}
	if opts.CPUShares > 0 {
		resources.CPUShares = opts.CPUShares
	}

	restartName := opts.RestartPolicy
	if restartName == "" {
		restartName = "unless-stopped"
	}

	// Translate our Mount shape into the moby mount.Mount slice. We
	// validate the type here rather than at the call site so callers can
	// stay decoupled from the moby SDK.
	mounts := make([]mount.Mount, 0, len(opts.Mounts))
	for _, m := range opts.Mounts {
		mt := mount.Type(m.Type)
		switch mt {
		case mount.TypeBind, mount.TypeVolume:
		default:
			return "", fmt.Errorf("docker: invalid mount type %q (want bind or volume)", m.Type)
		}
		if m.Target == "" {
			return "", errors.New("docker: mount: target required")
		}
		if m.Source == "" {
			return "", errors.New("docker: mount: source required")
		}
		mounts = append(mounts, mount.Mount{
			Type:     mt,
			Source:   m.Source,
			Target:   m.Target,
			ReadOnly: m.ReadOnly,
		})
	}

	cfg := &container.Config{
		Image:        opts.Image,
		Env:          env,
		Labels:       labels,
		ExposedPorts: exposedPorts,
		Healthcheck:  opts.Healthcheck,
	}
	if len(opts.Cmd) > 0 {
		cfg.Cmd = opts.Cmd
	}

	hostCfg := &container.HostConfig{
		PortBindings:  portBindings,
		RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyMode(restartName)},
		AutoRemove:    opts.AutoRemove,
		Resources:     resources,
		Mounts:        mounts,
	}

	aliases := append([]string{opts.Name}, opts.Aliases...)
	netCfg := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			netName: {
				Aliases: aliases,
			},
		},
	}

	created, err := cli.ContainerCreate(ctx, cfg, hostCfg, netCfg, nil, opts.Name)
	if err != nil {
		return "", fmt.Errorf("docker: create %q: %w", opts.Name, err)
	}
	if err := cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		// Best-effort: remove the half-created container so the caller can
		// retry with the same name.
		_ = cli.ContainerRemove(ctx, created.ID, container.RemoveOptions{Force: true})
		return "", fmt.Errorf("docker: start %q: %w", opts.Name, err)
	}
	return created.ID, nil
}

// parseResources turns "512m"/"1g"/"" and "0.5"/"1"/"" into container.Resources.
func parseResources(memory, cpus string) (container.Resources, error) {
	var r container.Resources
	if memory != "" {
		mem, err := parseMemory(memory)
		if err != nil {
			return r, err
		}
		r.Memory = mem
	}
	if cpus != "" {
		ncpus, err := parseCPUs(cpus)
		if err != nil {
			return r, err
		}
		r.NanoCPUs = ncpus
	}
	return r, nil
}

func parseMemory(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0, nil
	}
	mult := int64(1)
	switch {
	case strings.HasSuffix(s, "k"), strings.HasSuffix(s, "kb"):
		mult = 1024
	case strings.HasSuffix(s, "m"), strings.HasSuffix(s, "mb"):
		mult = 1024 * 1024
	case strings.HasSuffix(s, "g"), strings.HasSuffix(s, "gb"):
		mult = 1024 * 1024 * 1024
	}
	num := strings.TrimRight(s, "kmgb")
	v, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: memory %q", ErrInvalidResource, s)
	}
	return int64(v * float64(mult)), nil
}

// parseMemorySwap parses a memory-swap value. The literal "-1" means
// "unlimited swap" (matches docker --memory-swap=-1); otherwise the same
// suffix rules as parseMemory apply.
func parseMemorySwap(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	if s == "-1" {
		return -1, nil
	}
	return parseMemory(s)
}

func parseCPUs(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: cpus %q", ErrInvalidResource, s)
	}
	return int64(v * 1e9), nil
}

// StopContainer issues a graceful stop with the given timeout (seconds).
func StopContainer(ctx context.Context, p Provider, name string, timeout time.Duration) error {
	cli, err := p.Client(ctx)
	if err != nil {
		return err
	}
	secs := int(timeout.Seconds())
	if secs <= 0 {
		secs = 10
	}
	if err := cli.ContainerStop(ctx, name, container.StopOptions{Timeout: &secs}); err != nil {
		if client.IsErrNotFound(err) {
			return ErrContainerNotFound
		}
		return fmt.Errorf("docker: stop %q: %w", name, err)
	}
	return nil
}

// RemoveContainer deletes the container by name. If force=true the container
// is killed first. Missing containers are reported as ErrContainerNotFound.
func RemoveContainer(ctx context.Context, p Provider, name string, force bool) error {
	cli, err := p.Client(ctx)
	if err != nil {
		return err
	}
	if err := cli.ContainerRemove(ctx, name, container.RemoveOptions{Force: force, RemoveVolumes: false}); err != nil {
		if client.IsErrNotFound(err) {
			return ErrContainerNotFound
		}
		return fmt.Errorf("docker: remove %q: %w", name, err)
	}
	return nil
}

// RenameContainer renames a container atomically.
func RenameContainer(ctx context.Context, p Provider, oldName, newName string) error {
	cli, err := p.Client(ctx)
	if err != nil {
		return err
	}
	if err := cli.ContainerRename(ctx, oldName, newName); err != nil {
		if client.IsErrNotFound(err) {
			return ErrContainerNotFound
		}
		return fmt.Errorf("docker: rename %q -> %q: %w", oldName, newName, err)
	}
	return nil
}

// ListContainers returns containers matching the given filters. Pass a nil or
// empty map to list everything (including stopped).
func ListContainers(ctx context.Context, p Provider, filterMap map[string][]string) ([]types.Container, error) {
	cli, err := p.Client(ctx)
	if err != nil {
		return nil, err
	}
	args := filters.NewArgs()
	for k, vs := range filterMap {
		for _, v := range vs {
			args.Add(k, v)
		}
	}
	return cli.ContainerList(ctx, container.ListOptions{All: true, Filters: args})
}

// RemoveImage deletes an image by reference. Missing images are returned as
// ErrImageNotFound.
func RemoveImage(ctx context.Context, p Provider, ref string) error {
	cli, err := p.Client(ctx)
	if err != nil {
		return err
	}
	if _, err := cli.ImageRemove(ctx, ref, image.RemoveOptions{Force: false, PruneChildren: true}); err != nil {
		if client.IsErrNotFound(err) {
			return ErrImageNotFound
		}
		return fmt.Errorf("docker: remove image %q: %w", ref, err)
	}
	return nil
}

// ListImages returns all images on the host that match the optional label
// filters. Pass a nil/empty map to list everything.
func ListImages(ctx context.Context, p Provider, labels map[string]string) ([]image.Summary, error) {
	cli, err := p.Client(ctx)
	if err != nil {
		return nil, err
	}
	args := filters.NewArgs()
	for k, v := range labels {
		if v == "" {
			args.Add("label", k)
		} else {
			args.Add("label", k+"="+v)
		}
	}
	return cli.ImageList(ctx, image.ListOptions{All: false, Filters: args})
}

// PruneImages deletes dangling images (untagged or no tag references) and
// returns the bytes reclaimed.
func PruneImages(ctx context.Context, p Provider) (int64, error) {
	cli, err := p.Client(ctx)
	if err != nil {
		return 0, err
	}
	args := filters.NewArgs()
	args.Add("dangling", "true")
	report, err := cli.ImagesPrune(ctx, args)
	if err != nil {
		return 0, fmt.Errorf("docker: prune images: %w", err)
	}
	return int64(report.SpaceReclaimed), nil
}

// PruneContainers removes stopped containers and returns the bytes reclaimed.
func PruneContainers(ctx context.Context, p Provider) (int64, error) {
	cli, err := p.Client(ctx)
	if err != nil {
		return 0, err
	}
	report, err := cli.ContainersPrune(ctx, filters.NewArgs())
	if err != nil {
		return 0, fmt.Errorf("docker: prune containers: %w", err)
	}
	return int64(report.SpaceReclaimed), nil
}

// EnsureVolume creates a docker-managed named volume if it doesn't exist
// already. Idempotent: VolumeCreate returns the existing volume when
// called with the same name, which matches our needs.
//
// Volumes are tagged with `prexel.managed=true` plus any operator-
// provided extra labels (typically `prexel.app_id` so the cleanup
// worker can wipe orphan volumes when the app is deleted). Pass an
// empty map / nil if no extras are needed.
func EnsureVolume(ctx context.Context, p Provider, name string, extraLabels map[string]string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("docker: ensure volume: empty name")
	}
	cli, err := p.Client(ctx)
	if err != nil {
		return err
	}
	labels := map[string]string{"prexel.managed": "true"}
	for k, v := range extraLabels {
		labels[k] = v
	}
	_, err = cli.VolumeCreate(ctx, volume.CreateOptions{
		Name:   name,
		Labels: labels,
	})
	if err != nil {
		return fmt.Errorf("docker: ensure volume %q: %w", name, err)
	}
	return nil
}

// ListVolumes returns named volumes matching the optional label
// filters. Pass nil/empty to list everything. The app-cleanup worker
// uses this with `prexel.app_id=<id>` to enumerate volumes for
// removal when an app is deleted.
func ListVolumes(ctx context.Context, p Provider, labels map[string]string) ([]*volume.Volume, error) {
	cli, err := p.Client(ctx)
	if err != nil {
		return nil, err
	}
	args := filters.NewArgs()
	for k, v := range labels {
		if v == "" {
			args.Add("label", k)
		} else {
			args.Add("label", k+"="+v)
		}
	}
	resp, err := cli.VolumeList(ctx, volume.ListOptions{Filters: args})
	if err != nil {
		return nil, fmt.Errorf("docker: list volumes: %w", err)
	}
	return resp.Volumes, nil
}

// RemoveVolume deletes a named volume. `force=true` removes it even
// when a stopped container still references it. Returns nil if the
// volume already doesn't exist (idempotent).
func RemoveVolume(ctx context.Context, p Provider, name string, force bool) error {
	cli, err := p.Client(ctx)
	if err != nil {
		return err
	}
	if err := cli.VolumeRemove(ctx, name, force); err != nil {
		if client.IsErrNotFound(err) {
			return nil
		}
		return fmt.Errorf("docker: remove volume %q: %w", name, err)
	}
	return nil
}

// ContainerExists reports whether a container with the given name (or id)
// exists on the engine. A missing container returns (false, nil) — not an
// error — because callers typically branch on existence.
func ContainerExists(ctx context.Context, p Provider, name string) (bool, error) {
	cli, err := p.Client(ctx)
	if err != nil {
		return false, err
	}
	if _, err := cli.ContainerInspect(ctx, name); err != nil {
		if client.IsErrNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("docker: inspect %q: %w", name, err)
	}
	return true, nil
}

// Exec runs `cmd` inside the named container as a one-shot `docker exec`
// equivalent. The combined stdout+stderr is returned regardless of the
// exit code; non-zero exits surface as an error wrapping the captured
// output. The supplied timeout caps both the attach and the exec itself.
//
// This helper is for short server-side operations (pre/post-deploy hooks,
// migrations) — for interactive TTY sessions use the WebSocket exec
// handler in internal/api/handler/container_exec.go.
func Exec(ctx context.Context, p Provider, name string, cmd []string, timeout time.Duration) ([]byte, error) {
	if len(cmd) == 0 {
		return nil, errors.New("docker: exec: empty command")
	}
	cli, err := p.Client(ctx)
	if err != nil {
		return nil, err
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	idResp, err := cli.ContainerExecCreate(ctx, name, container.ExecOptions{
		AttachStdout: true,
		AttachStderr: true,
		Tty:          false,
		Cmd:          cmd,
	})
	if err != nil {
		if client.IsErrNotFound(err) {
			return nil, ErrContainerNotFound
		}
		return nil, fmt.Errorf("docker: exec create %q: %w", name, err)
	}
	attach, err := cli.ContainerExecAttach(ctx, idResp.ID, container.ExecAttachOptions{Tty: false})
	if err != nil {
		return nil, fmt.Errorf("docker: exec attach %q: %w", name, err)
	}
	defer attach.Close()

	// Drain the muxed stream into a single buffer. stdcopy splits
	// stdout/stderr by Docker's framing, but for hook output we just
	// want one blob — operators read it from the deploy log.
	var buf bytes.Buffer
	if _, err := stdcopy.StdCopy(&buf, &buf, attach.Reader); err != nil && !errors.Is(err, io.EOF) {
		return buf.Bytes(), fmt.Errorf("docker: exec read %q: %w", name, err)
	}

	insp, err := cli.ContainerExecInspect(ctx, idResp.ID)
	if err != nil {
		return buf.Bytes(), fmt.Errorf("docker: exec inspect %q: %w", name, err)
	}
	if insp.ExitCode != 0 {
		return buf.Bytes(), fmt.Errorf("docker: exec %q: exit code %d", name, insp.ExitCode)
	}
	return buf.Bytes(), nil
}

// StreamLogs opens a stdout+stderr log stream for the named container. Use
// follow=true to keep the stream open (suitable for SSE); set tail<=0 to
// fetch the entire backlog or N to limit to the last N lines.
//
// Important: the returned ReadCloser yields the raw Docker multiplexed
// stream. Callers that need to demultiplex stdout/stderr should use
// stdcopy.StdCopy from github.com/docker/docker/pkg/stdcopy.
func StreamLogs(ctx context.Context, p Provider, name string, follow bool, tail int) (io.ReadCloser, error) {
	cli, err := p.Client(ctx)
	if err != nil {
		return nil, err
	}
	tailStr := "all"
	if tail > 0 {
		tailStr = strconv.Itoa(tail)
	}
	rc, err := cli.ContainerLogs(ctx, name, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     follow,
		Tail:       tailStr,
		Timestamps: false,
	})
	if err != nil {
		if client.IsErrNotFound(err) {
			return nil, ErrContainerNotFound
		}
		return nil, fmt.Errorf("docker: logs %q: %w", name, err)
	}
	return rc, nil
}
