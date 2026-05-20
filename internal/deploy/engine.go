// Package deploy implements the zero-downtime deployment lifecycle for Prexel
// apps. It depends on the build engine for image production, the docker
// provider abstraction for container ops, and Caddy for traffic switchover.
//
// The high-level deploy flow is:
//
//	 1. Acquire a per-app mutex so concurrent deploys for the same app are rejected.
//	 2. Validate (app + server reachable, no other deploy in flight).
//	 3. Insert a `deployments` row in status=pending; open the log file.
//	 4. Build (delegates to internal/build).
//	 5. Run new container `prexel-<app>-deploying-<sha>`, wait for health check.
//	 6. On success: caddy.UpsertRoute(domain -> temp_name), stop+remove old
//	    container, rename temp to canonical name, re-UpsertRoute, prune images.
//	 7. On failure: stop+remove temp, delete new image, keep old container running.
//
// Rollback and Restart reuse the same fase-2 plumbing.
package deploy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/google/uuid"
	"github.com/prexel/prexel/internal/app"
	"github.com/prexel/prexel/internal/appvolume"
	"github.com/prexel/prexel/internal/build"
	"github.com/prexel/prexel/internal/caddy"
	"github.com/prexel/prexel/internal/domains"
	"github.com/prexel/prexel/internal/eventbus"
	"github.com/prexel/prexel/internal/server"
	"github.com/prexel/prexel/internal/secret"
	"github.com/prexel/prexel/internal/dockersvc"
)

// AppVolumeLister is the small surface the deploy engine needs to load
// per-app volume mounts before container creation. *appvolume.Service
// satisfies it; we depend on an interface here so tests can fake it
// without spinning up the full SQLite layer.
type AppVolumeLister interface {
	// ListForApp returns every app_volumes row for the app, in a stable
	// order (mount_path ASC). Implementations should return an empty
	// slice, not nil, when the app has no volumes.
	ListForApp(ctx context.Context, appID string) ([]appvolume.Volume, error)
}

// ConcurrencyLimiter is the small surface the deploy engine needs to learn
// the instance-wide cap. instance.Service.MaxConcurrentDeploys satisfies
// it; we depend on the interface so engine_test.go can supply a fake.
type ConcurrencyLimiter interface {
	// MaxConcurrentDeploys returns a positive integer — the maximum number
	// of deploys (across all apps) that may run at the same time. Implementers
	// SHOULD reflect live config changes; the engine reads it on every
	// Deploy/Rollback call.
	MaxConcurrentDeploys() int
}

// Engine wires the deploy lifecycle together. It is safe for concurrent use:
// per-app deploys are serialised via the per-app locks map, and the whole
// instance is capped by a global semaphore (see ConcurrencyLimiter).
type Engine struct {
	DB         *sql.DB
	Apps       *app.Service
	Servers    *server.Service
	Secrets    *secret.Service
	Domains    *domains.Service
	Build      *build.Engine
	Caddy      *caddy.Client
	Bus        *eventbus.Bus
	LogDir     string
	Limit      ConcurrencyLimiter // may be nil — unlimited if so
	AppVolumes AppVolumeLister    // may be nil — apps deploy with no mounts

	repo *repo

	locks sync.Map // map[string]*sync.Mutex (per-app)

	// Global semaphore. Buffered channel; capacity is resolved lazily on
	// first use because Limit can be nil (tests) and we want zero overhead
	// for unlimited mode.
	globalSlots     chan struct{}
	globalSlotsOnce sync.Once
}

// Deps bundles the constructor arguments.
type Deps struct {
	DB         *sql.DB
	Apps       *app.Service
	Servers    *server.Service
	Secrets    *secret.Service
	Domains    *domains.Service
	Build      *build.Engine
	Caddy      *caddy.Client
	Bus        *eventbus.Bus
	LogDir     string
	Limit      ConcurrencyLimiter // optional
	AppVolumes AppVolumeLister    // optional
}

// New constructs an Engine.
func New(d Deps) *Engine {
	logDir := d.LogDir
	if logDir == "" {
		logDir = "/var/lib/prexel/logs"
	}
	_ = os.MkdirAll(logDir, 0o755)
	return &Engine{
		DB:         d.DB,
		Apps:       d.Apps,
		Servers:    d.Servers,
		Secrets:    d.Secrets,
		Domains:    d.Domains,
		Build:      d.Build,
		Caddy:      d.Caddy,
		Bus:        d.Bus,
		LogDir:     logDir,
		Limit:      d.Limit,
		AppVolumes: d.AppVolumes,
		repo:       newRepo(d.DB),
	}
}

// DeployOptions controls a single Deploy invocation.
type DeployOptions struct {
	Branch    string
	CommitSHA string
	ImageTag  string // for docker_image apps

	// DeploymentID is an optional pre-allocated UUID for the new
	// deployment row. The HTTP handler uses this so it can return
	// the row id in its 202 response without having to wait for the
	// background goroutine to actually create the row. When empty
	// (default — CLI / cron / webhook callers), runDeploy generates
	// a fresh id internally as before.
	DeploymentID string
}

// ErrDeployInProgress signals that another deploy for the app is still running.
var ErrDeployInProgress = errors.New("deploy: in progress")

// Repo exposes the internal repository for handlers that need to read
// deployment rows (list, get, etc.).
func (e *Engine) Repo() *repo { return e.repo } //nolint:revive // intentional package-internal accessor

// CreateDeployment exposes a way for handlers to fetch single deployments.
func (e *Engine) GetDeployment(ctx context.Context, id string) (*Deployment, error) {
	return e.repo.get(ctx, id)
}

// ListDeployments returns the most recent deployments for an app.
func (e *Engine) ListDeployments(ctx context.Context, appID string, limit int) ([]Deployment, error) {
	return e.repo.listByApp(ctx, appID, limit)
}

// LogPath returns the on-disk path of a deployment log.
func (e *Engine) LogPath(deploymentID string) string {
	return filepath.Join(e.LogDir, deploymentID+".log")
}

// acquireLock returns the per-app mutex, creating it on first use. The caller
// must Unlock it.
func (e *Engine) acquireLock(appID string) (*sync.Mutex, bool) {
	m, _ := e.locks.LoadOrStore(appID, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	if !mu.TryLock() {
		return mu, false
	}
	return mu, true
}

// ErrTooManyDeploys signals that the instance-wide concurrent-deploys cap is
// saturated. Distinct from ErrDeployInProgress: that one is per-app, this
// one is global. The HTTP handler maps both to 409 Conflict with different
// codes so the UI can show the right hint.
var ErrTooManyDeploys = errors.New("deploy: too many concurrent deploys")

// acquireGlobalSlot reserves a slot in the global semaphore. Returns a
// release func the caller must defer. If Limit is nil, returns a no-op
// release immediately (unlimited mode for tests / disabled instances).
//
// Capacity is read once and stored — admins changing max_concurrent_deploys
// at runtime will affect future deploys but won't shrink an already-allocated
// channel (Go channels are immutable). That's acceptable: lowering the cap
// during heavy traffic lets the in-flight batch drain before the new cap
// takes effect, which is the safer behaviour.
func (e *Engine) acquireGlobalSlot() (release func(), ok bool) {
	if e.Limit == nil {
		return func() {}, true
	}
	e.globalSlotsOnce.Do(func() {
		capacity := e.Limit.MaxConcurrentDeploys()
		if capacity < 1 {
			capacity = 1
		}
		e.globalSlots = make(chan struct{}, capacity)
	})
	select {
	case e.globalSlots <- struct{}{}:
		return func() { <-e.globalSlots }, true
	default:
		return nil, false
	}
}

// Deploy runs the full build + zero-downtime swap pipeline. It is synchronous
// — the HTTP handler typically calls it in a goroutine. The returned
// *Deployment carries the row id, useful for status polling and log fetch.
func (e *Engine) Deploy(ctx context.Context, appID string, opts DeployOptions) (*Deployment, error) {
	a, err := e.Apps.Get(ctx, appID)
	if err != nil {
		return nil, err
	}
	mu, ok := e.acquireLock(a.ID)
	if !ok {
		_ = mu
		return nil, ErrDeployInProgress
	}
	defer mu.Unlock()
	// Global semaphore — checked AFTER the per-app lock so a saturated
	// instance doesn't keep the app's mutex held while waiting.
	release, ok := e.acquireGlobalSlot()
	if !ok {
		return nil, ErrTooManyDeploys
	}
	defer release()

	return e.runDeploy(ctx, a, opts, "")
}

// Rollback redeploys a previously-successful image as a new deployment.
// targetDeploymentID may be empty — in that case the most-recent successful
// deployment (other than the current one) is used.
func (e *Engine) Rollback(ctx context.Context, appID, targetDeploymentID string, opts DeployOptions) (*Deployment, error) {
	a, err := e.Apps.Get(ctx, appID)
	if err != nil {
		return nil, err
	}
	mu, ok := e.acquireLock(a.ID)
	if !ok {
		return nil, ErrDeployInProgress
	}
	defer mu.Unlock()
	release, ok := e.acquireGlobalSlot()
	if !ok {
		return nil, ErrTooManyDeploys
	}
	defer release()

	var target *Deployment
	if targetDeploymentID != "" {
		target, err = e.repo.get(ctx, targetDeploymentID)
		if err != nil {
			return nil, err
		}
		if target.AppID != a.ID {
			return nil, errors.New("deploy: target deployment belongs to a different app")
		}
		if target.Status != "success" {
			return nil, fmt.Errorf("deploy: target deployment status is %q (need success)", target.Status)
		}
	} else {
		target, err = e.repo.lastSuccess(ctx, a.ID, "")
		if errors.Is(err, ErrNotFound) {
			return nil, errors.New("deploy: no successful deployment to roll back to")
		}
		if err != nil {
			return nil, err
		}
	}
	if target.ImageTag == nil || *target.ImageTag == "" {
		return nil, errors.New("deploy: target deployment has no image tag")
	}

	// Verify image still exists.
	provider, err := e.Servers.Provider(ctx, derefStr(a.ServerID))
	if err != nil {
		return nil, fmt.Errorf("deploy: provider: %w", err)
	}
	defer func() { _ = provider.Close() }()
	cli, err := provider.Client(ctx)
	if err != nil {
		return nil, fmt.Errorf("deploy: docker client: %w", err)
	}
	if _, _, err := cli.ImageInspectWithRaw(ctx, *target.ImageTag); err != nil {
		return nil, fmt.Errorf("deploy: image_not_found: %s", *target.ImageTag)
	}

	return e.runFromImage(ctx, a, provider, *target.ImageTag, target.ID, opts.DeploymentID)
}

// Restart restarts the canonical container for an app using its current image.
func (e *Engine) Restart(ctx context.Context, appID string) error {
	a, err := e.Apps.Get(ctx, appID)
	if err != nil {
		return err
	}
	if a.ContainerName == nil {
		return errors.New("deploy: app has no container_name")
	}
	provider, err := e.Servers.Provider(ctx, derefStr(a.ServerID))
	if err != nil {
		return fmt.Errorf("deploy: provider: %w", err)
	}
	defer func() { _ = provider.Close() }()

	cli, err := provider.Client(ctx)
	if err != nil {
		return err
	}
	// Use Docker's restart so we keep settings/env/network bindings intact.
	timeout := 10
	return cli.ContainerRestart(ctx, *a.ContainerName, container.StopOptions{Timeout: &timeout})
}

// Stop stops the canonical container and marks the app stopped.
func (e *Engine) Stop(ctx context.Context, appID string) error {
	a, err := e.Apps.Get(ctx, appID)
	if err != nil {
		return err
	}
	if a.ContainerName != nil {
		provider, perr := e.Servers.Provider(ctx, derefStr(a.ServerID))
		if perr == nil {
			defer func() { _ = provider.Close() }()
			if err := dockersvc.StopContainer(ctx, provider, *a.ContainerName, 10*time.Second); err != nil && !errors.Is(err, dockersvc.ErrContainerNotFound) {
				slog.Warn("deploy: stop container failed", "app", a.Name, "err", err)
			}
		}
	}
	return e.Apps.SetStatus(ctx, a.ID, "stopped")
}

// ---------------------------------------------------------------------------
// Core pipeline
// ---------------------------------------------------------------------------

// runDeploy executes a fresh build + swap. rollbackOf is empty for normal
// deploys, populated for rollback flows.
func (e *Engine) runDeploy(ctx context.Context, a *app.App, opts DeployOptions, rollbackOf string) (*Deployment, error) {
	if a.ServerID == nil {
		return nil, errors.New("deploy: app has no server")
	}
	srv, err := e.Servers.Get(ctx, *a.ServerID)
	if err != nil {
		return nil, err
	}
	if srv.Status != "connected" {
		return nil, fmt.Errorf("deploy: server status is %q (need connected)", srv.Status)
	}

	// Honour a caller-provided deployment id (HTTP handler does this so it
	// can return the row id immediately in its 202 response). Falls back
	// to a fresh UUID for CLI / webhook callers that don't pre-allocate.
	deploymentID := strings.TrimSpace(opts.DeploymentID)
	if deploymentID == "" {
		deploymentID = uuid.NewString()
	}
	logPath := e.LogPath(deploymentID)
	branch := strings.TrimSpace(opts.Branch)
	if branch == "" {
		branch = a.Branch
	}

	d := &Deployment{
		ID:      deploymentID,
		AppID:   a.ID,
		Branch:  strPtr(branch),
		Status:  "pending",
		LogPath: strPtr(logPath),
	}
	if commit := strings.TrimSpace(opts.CommitSHA); commit != "" {
		d.CommitSHA = strPtr(commit)
	} else if a.GitCommitSHA != nil && strings.TrimSpace(*a.GitCommitSHA) != "" {
		d.CommitSHA = a.GitCommitSHA
	}
	if rollbackOf != "" {
		d.RollbackOf = strPtr(rollbackOf)
	}
	if err := e.repo.insert(ctx, d); err != nil {
		return nil, fmt.Errorf("deploy: insert: %w", err)
	}

	// Open log file.
	logFile, err := os.Create(logPath)
	if err != nil {
		_ = e.repo.updateFields(ctx, d.ID, map[string]any{
			"status":      "failed",
			"finished_at": time.Now().Unix(),
		})
		return nil, fmt.Errorf("deploy: log file: %w", err)
	}
	defer func() { _ = logFile.Close() }()

	e.publish(a.ID, deploymentID, "deploy.started", map[string]any{
		"deployment_id": deploymentID,
		"app_id":        a.ID,
		"branch":        branch,
	})

	provider, err := e.Servers.Provider(ctx, *a.ServerID)
	if err != nil {
		e.failDeploy(ctx, a, d, logFile, fmt.Errorf("provider: %w", err))
		return d, err
	}
	defer func() { _ = provider.Close() }()

	if err := dockersvc.EnsureNetwork(ctx, provider, dockersvc.PrexelNetwork); err != nil {
		e.failDeploy(ctx, a, d, logFile, fmt.Errorf("ensure network: %w", err))
		return d, err
	}

	// Phase 1: build.
	_ = e.repo.updateFields(ctx, d.ID, map[string]any{
		"status":     "building",
		"started_at": time.Now().Unix(),
	})
	_ = e.Apps.SetStatus(ctx, a.ID, "building")
	e.publish(a.ID, deploymentID, "app.status_changed", map[string]any{"status": "building"})

	res, err := e.Build.Build(ctx, provider, a, build.BuildOptions{
		DeploymentID: deploymentID,
		AppID:        a.ID,
		Branch:       branch,
		CommitSHA:    opts.CommitSHA,
		ImageTag:     opts.ImageTag,
	}, logFile)
	if err != nil {
		e.failDeploy(ctx, a, d, logFile, fmt.Errorf("build: %w", err))
		return d, err
	}
	if res.CommitSHA != "" {
		_ = e.repo.updateFields(ctx, d.ID, map[string]any{
			"commit_sha": res.CommitSHA,
		})
		d.CommitSHA = strPtr(res.CommitSHA)
	}

	// Phase 2: deploy swap. Compose apps take a different code path
	// because each service is its own container — see swapCompose for
	// the per-service start / swap / route loop.
	if a.BuildType == "docker_compose" {
		if err := e.swapCompose(ctx, a, provider, res, d, logFile); err != nil {
			e.failDeploy(ctx, a, d, logFile, err)
			return d, err
		}
	} else {
		if err := e.swap(ctx, a, provider, res.ImageRef, d, logFile); err != nil {
			e.failDeploy(ctx, a, d, logFile, err)
			return d, err
		}
	}

	// Success.
	now := time.Now().Unix()
	_ = e.repo.updateFields(ctx, d.ID, map[string]any{
		"status":      "success",
		"image_tag":   res.ImageRef,
		"finished_at": now,
	})
	d.Status = "success"
	d.ImageTag = strPtr(res.ImageRef)
	d.FinishedAt = &now

	_ = e.Apps.SetStatus(ctx, a.ID, "running")
	e.publish(a.ID, deploymentID, "deploy.success", map[string]any{
		"deployment_id": deploymentID,
		"app_id":        a.ID,
		"image_tag":     res.ImageRef,
	})
	e.publish(a.ID, deploymentID, "app.status_changed", map[string]any{"status": "running"})

	// Retain only the latest 5 images per app.
	e.pruneImages(ctx, provider, a, 5)
	return d, nil
}

// runFromImage skips the build phase and goes directly to the swap. Used by Rollback.
//
// preallocatedID accepts a caller-supplied UUID (HTTP handler does this so it
// can return the id in its 202 response). Empty = generate fresh.
func (e *Engine) runFromImage(ctx context.Context, a *app.App, provider dockersvc.Provider, imageRef, rollbackOf, preallocatedID string) (*Deployment, error) {
	deploymentID := strings.TrimSpace(preallocatedID)
	if deploymentID == "" {
		deploymentID = uuid.NewString()
	}
	logPath := e.LogPath(deploymentID)

	d := &Deployment{
		ID:         deploymentID,
		AppID:      a.ID,
		Status:     "deploying",
		LogPath:    strPtr(logPath),
		RollbackOf: strPtr(rollbackOf),
		ImageTag:   strPtr(imageRef),
	}
	now := time.Now().Unix()
	d.StartedAt = &now
	if err := e.repo.insert(ctx, d); err != nil {
		return nil, fmt.Errorf("deploy: insert: %w", err)
	}
	logFile, err := os.Create(logPath)
	if err != nil {
		_ = e.repo.updateFields(ctx, d.ID, map[string]any{
			"status":      "failed",
			"finished_at": time.Now().Unix(),
		})
		return nil, fmt.Errorf("deploy: log file: %w", err)
	}
	defer func() { _ = logFile.Close() }()

	fmt.Fprintf(logFile, "%s [stdout] Rolling back to image %s (deployment %s)\n",
		time.Now().UTC().Format(time.RFC3339Nano), imageRef, rollbackOf)
	e.publish(a.ID, deploymentID, "deploy.started", map[string]any{
		"deployment_id": deploymentID,
		"app_id":        a.ID,
		"rollback_of":   rollbackOf,
	})

	if err := e.swap(ctx, a, provider, imageRef, d, logFile); err != nil {
		e.failDeploy(ctx, a, d, logFile, err)
		return d, err
	}

	now = time.Now().Unix()
	_ = e.repo.updateFields(ctx, d.ID, map[string]any{
		"status":      "success",
		"finished_at": now,
	})
	d.Status = "success"
	d.FinishedAt = &now
	_ = e.Apps.SetStatus(ctx, a.ID, "running")
	e.publish(a.ID, deploymentID, "deploy.success", map[string]any{
		"deployment_id": deploymentID,
		"app_id":        a.ID,
		"image_tag":     imageRef,
		"rollback_of":   rollbackOf,
	})
	return d, nil
}

// swap is the zero-downtime container swap.
func (e *Engine) swap(ctx context.Context, a *app.App, provider dockersvc.Provider, imageRef string, d *Deployment, logFile io.Writer) error {
	_ = e.repo.updateFields(ctx, d.ID, map[string]any{"status": "deploying"})
	_ = e.Apps.SetStatus(ctx, a.ID, "deploying")
	e.publish(a.ID, d.ID, "app.status_changed", map[string]any{"status": "deploying"})

	containerName := derefStr(a.ContainerName)
	if containerName == "" {
		containerName = a.Name
	}
	// Legacy name (pre-prefix-removal) — kept around so existing
	// apps that deployed under the old `prexel-<name>` scheme have
	// their stale canonical container removed when they redeploy.
	// No-op when the app never used the old name.
	legacyName := "prexel-" + a.Name
	shortSHA := imageShortSHA(imageRef, a.Name)
	tempName := containerName + "-deploying-" + shortSHA

	// Pre-deploy hook (Coolify-compat): runs IN the previous canonical
	// container BEFORE we touch the new one. The operator's typical use
	// is `rails db:migrate` or `npm run migrate`, so it must execute
	// against the still-running old version with all its env/network in
	// place. Failures are a hard stop — we don't proceed with a deploy
	// when migrations broke. First-ever deploys have no previous
	// container and skip silently.
	if a.PreDeployCommand != nil && strings.TrimSpace(*a.PreDeployCommand) != "" {
		exists, err := dockersvc.ContainerExists(ctx, provider, containerName)
		if err != nil {
			fmt.Fprintf(logFile, "%s [stderr] pre-deploy: container exists check failed: %v\n",
				time.Now().UTC().Format(time.RFC3339Nano), err)
		}
		if exists {
			fmt.Fprintf(logFile, "%s [stdout] Running pre-deploy hook in %s: %s\n",
				time.Now().UTC().Format(time.RFC3339Nano), containerName, *a.PreDeployCommand)
			out, err := dockersvc.Exec(ctx, provider, containerName,
				[]string{"sh", "-c", *a.PreDeployCommand}, 5*time.Minute)
			if len(out) > 0 {
				fmt.Fprintf(logFile, "%s [stdout] pre-deploy output:\n%s\n",
					time.Now().UTC().Format(time.RFC3339Nano), string(out))
			}
			if err != nil {
				return fmt.Errorf("pre-deploy command failed: %w", err)
			}
		} else {
			fmt.Fprintf(logFile, "%s [stdout] pre-deploy: no previous container %q — skipping\n",
				time.Now().UTC().Format(time.RFC3339Nano), containerName)
		}
	}

	// Build env + secrets.
	envMap := map[string]string{}
	for k, v := range a.EnvVars {
		envMap[k] = v
	}
	if e.Secrets != nil {
		runtimeSecs, err := e.Secrets.ResolveRuntime(ctx, a.ID)
		if err != nil {
			return fmt.Errorf("resolve runtime secrets: %w", err)
		}
		for k, v := range runtimeSecs {
			envMap[k] = v
		}
	}

	// Determine port + host port binding.
	containerPort := 80
	if a.Port != nil && *a.Port > 0 {
		containerPort = *a.Port
	}

	primary, aliases, hostForceHTTPS, err := e.appDomains(ctx, a.ID)
	if err != nil {
		return fmt.Errorf("load domains: %w", err)
	}

	var bindings []dockersvc.PortBinding
	hasDomain := primary != ""
	if !hasDomain {
		hostPort := 0
		if a.HostPort != nil && *a.HostPort > 0 {
			hostPort = *a.HostPort
		} else {
			hp, err := domains.AssignHostPort(ctx, e.DB, a.ID)
			if err != nil {
				return fmt.Errorf("assign host port: %w", err)
			}
			hostPort = hp
		}
		bindings = append(bindings, dockersvc.PortBinding{
			HostPort: hostPort, ContainerPort: containerPort, Protocol: "tcp",
		})
		fmt.Fprintf(logFile, "%s [stdout] No domain — binding host port %d -> container port %d\n",
			time.Now().UTC().Format(time.RFC3339Nano), hostPort, containerPort)
	}

	// Apps without a domain bind a fixed host port. Two containers cannot bind
	// the same host port simultaneously, so for no-domain apps we stop the old
	// canonical container BEFORE starting the candidate. This sacrifices the
	// zero-downtime invariant for those apps (Tech Review §16) — by design.
	if !hasDomain {
		// Stop+remove the canonical container AND its legacy `prexel-`
		// prefixed name — an app that historically deployed under the
		// old scheme would otherwise hold its host port hostage when
		// the new candidate tries to bind.
		for _, name := range legacyDedupe(containerName, legacyName) {
			if err := dockersvc.StopContainer(ctx, provider, name, 10*time.Second); err != nil && !errors.Is(err, dockersvc.ErrContainerNotFound) {
				fmt.Fprintf(logFile, "%s [stderr] Warning: stop old container %s: %v\n",
					time.Now().UTC().Format(time.RFC3339Nano), name, err)
			}
			if err := dockersvc.RemoveContainer(ctx, provider, name, true); err != nil && !errors.Is(err, dockersvc.ErrContainerNotFound) {
				fmt.Fprintf(logFile, "%s [stderr] Warning: remove old container %s: %v\n",
					time.Now().UTC().Format(time.RFC3339Nano), name, err)
			}
		}
	}

	fmt.Fprintf(logFile, "%s [stdout] Starting new container %s (image=%s)\n",
		time.Now().UTC().Format(time.RFC3339Nano), tempName, imageRef)

	// Remove any stale temp container with the same name (e.g. left over from
	// a crash during a previous deploy).
	_ = dockersvc.RemoveContainer(ctx, provider, tempName, true)

	memLimit := ""
	if a.LimitsMemory != nil {
		memLimit = *a.LimitsMemory
	}
	cpuLimit := ""
	if a.LimitsCPUs != nil {
		cpuLimit = *a.LimitsCPUs
	}

	// Operator-defined labels merged first so the prexel.* ones win on
	// conflict — operators must never be able to forge our ownership
	// labels (the exec/logs handlers gate on prexel.app_id).
	labels := map[string]string{}
	for k, v := range a.DockerLabels {
		labels[k] = v
	}
	labels["prexel.app_id"] = a.ID
	labels["prexel.app_name"] = a.Name
	labels["prexel.deployment"] = d.ID
	labels["prexel.role"] = "candidate"

	// Per-app volumes. Single-container path only — compose service-scoped
	// volumes (v.Service != nil) are handled by the compose code path
	// elsewhere in the engine.
	// TODO(compose): wire app_volumes with non-nil Service in the compose path.
	var mounts []dockersvc.Mount
	if e.AppVolumes != nil {
		vols, vErr := e.AppVolumes.ListForApp(ctx, a.ID)
		if vErr != nil {
			fmt.Fprintf(logFile, "%s [stderr] Warning: load app_volumes: %v — continuing with no mounts\n",
				time.Now().UTC().Format(time.RFC3339Nano), vErr)
		}
		for _, v := range vols {
			if v.Service != nil {
				continue
			}
			m := dockersvc.Mount{
				Target:   v.MountPath,
				ReadOnly: v.ReadOnly,
			}
			if v.IsNamed {
				m.Type = "volume"
				m.Source = namedVolumeName(a.Name, v.MountPath)
				// Tag with app_id so the cleanup worker can find +
				// remove this volume when the app is deleted.
				if err := dockersvc.EnsureVolume(ctx, provider, m.Source, map[string]string{
					"prexel.app_id":   a.ID,
					"prexel.app_name": a.Name,
				}); err != nil {
					return fmt.Errorf("ensure volume %s: %w", m.Source, err)
				}
			} else {
				if v.HostPath == nil {
					// Validation should have rejected this upstream, but
					// guard anyway — a bind with no source is unrecoverable.
					return fmt.Errorf("bind mount for %s has no host_path", v.MountPath)
				}
				m.Type = "bind"
				m.Source = *v.HostPath
			}
			mounts = append(mounts, m)
		}
	}

	restartPolicy := a.RestartPolicy
	if restartPolicy == "" {
		restartPolicy = "unless-stopped"
	}

	if _, err := dockersvc.RunContainer(ctx, provider, dockersvc.ContainerOpts{
		Name:              tempName,
		Image:             imageRef,
		Env:               envMap,
		Aliases:           []string{tempName},
		Labels:            labels,
		PortBindings:      bindings,
		RestartPolicy:     restartPolicy,
		MemoryLimit:       memLimit,
		CPULimit:          cpuLimit,
		MemorySwap:        derefStr(a.LimitsMemorySwap),
		MemorySwappiness:  intPtrToInt64Ptr(a.LimitsMemorySwappiness),
		MemoryReservation: derefStr(a.LimitsMemoryReservation),
		CPUSet:            derefStr(a.LimitsCPUSet),
		CPUShares:         intPtrToInt64(a.LimitsCPUShares),
		Mounts:            mounts,
	}); err != nil {
		return fmt.Errorf("run container: %w", err)
	}

	// Health check.
	if err := e.healthCheck(ctx, a, tempName, containerPort, logFile); err != nil {
		fmt.Fprintf(logFile, "%s [stderr] Health check failed: %v. Rolling back.\n",
			time.Now().UTC().Format(time.RFC3339Nano), err)
		_ = dockersvc.StopContainer(ctx, provider, tempName, 10*time.Second)
		_ = dockersvc.RemoveContainer(ctx, provider, tempName, true)
		_ = dockersvc.RemoveImage(ctx, provider, imageRef)
		return fmt.Errorf("health check: %w", err)
	}

	// Caddy: route domains to the temp container first so traffic is served
	// even while we tear down the old one.
	if hasDomain && e.Caddy != nil {
		allHosts := append([]string{primary}, aliases...)
		for _, host := range allHosts {
			force := hostForceHTTPS[host]
			if err := e.Caddy.UpsertRoute(ctx, host, tempName, containerPort, force); err != nil {
				return fmt.Errorf("caddy upsert %s -> %s: %w", host, tempName, err)
			}
		}
		// Caddy's admin API commits config changes synchronously — the
		// PATCH/POST returns after the new route is live on srv0, so no
		// extra "settle" sleep is needed here.
	}

	// Stop + remove the old canonical container if it exists, AND
	// its legacy `prexel-<name>` predecessor — apps that existed
	// under the old naming scheme would otherwise leave their old
	// container running silently alongside the new one.
	for _, name := range legacyDedupe(containerName, legacyName) {
		if err := dockersvc.StopContainer(ctx, provider, name, 10*time.Second); err != nil && !errors.Is(err, dockersvc.ErrContainerNotFound) {
			fmt.Fprintf(logFile, "%s [stderr] Warning: stop old container %s: %v\n",
				time.Now().UTC().Format(time.RFC3339Nano), name, err)
		}
		if err := dockersvc.RemoveContainer(ctx, provider, name, true); err != nil && !errors.Is(err, dockersvc.ErrContainerNotFound) {
			fmt.Fprintf(logFile, "%s [stderr] Warning: remove old container %s: %v\n",
				time.Now().UTC().Format(time.RFC3339Nano), name, err)
		}
	}

	// Rename candidate -> canonical name.
	if err := dockersvc.RenameContainer(ctx, provider, tempName, containerName); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", tempName, containerName, err)
	}

	// Re-UpsertRoute pointing at the canonical name now that we own it.
	if hasDomain && e.Caddy != nil {
		allHosts := append([]string{primary}, aliases...)
		for _, host := range allHosts {
			force := hostForceHTTPS[host]
			if err := e.Caddy.UpsertRoute(ctx, host, containerName, containerPort, force); err != nil {
				fmt.Fprintf(logFile, "%s [stderr] Warning: caddy upsert canonical %s: %v\n",
					time.Now().UTC().Format(time.RFC3339Nano), host, err)
			}
		}
	}

	fmt.Fprintf(logFile, "%s [stdout] Container %s is now serving traffic.\n",
		time.Now().UTC().Format(time.RFC3339Nano), containerName)

	// Post-deploy hook (Coolify-compat): runs in the NEW canonical
	// container after the swap completes. Used for cache warmups, slack
	// pings, or any cleanup. Non-zero exits are LOGGED LOUDLY but do NOT
	// fail the deploy — operator hooks are best-effort by design.
	if a.PostDeployCommand != nil && strings.TrimSpace(*a.PostDeployCommand) != "" {
		fmt.Fprintf(logFile, "%s [stdout] Running post-deploy hook in %s: %s\n",
			time.Now().UTC().Format(time.RFC3339Nano), containerName, *a.PostDeployCommand)
		out, err := dockersvc.Exec(ctx, provider, containerName,
			[]string{"sh", "-c", *a.PostDeployCommand}, 5*time.Minute)
		if len(out) > 0 {
			fmt.Fprintf(logFile, "%s [stdout] post-deploy output:\n%s\n",
				time.Now().UTC().Format(time.RFC3339Nano), string(out))
		}
		if err != nil {
			fmt.Fprintf(logFile, "%s [stderr] post-deploy command failed (continuing): %v\n",
				time.Now().UTC().Format(time.RFC3339Nano), err)
		}
	}
	return nil
}

// failDeploy marks d as failed and notifies subscribers.
func (e *Engine) failDeploy(ctx context.Context, a *app.App, d *Deployment, logFile *os.File, cause error) {
	now := time.Now().Unix()
	causeMsg := cause.Error()
	fmt.Fprintf(logFile, "%s [stderr] Deploy failed: %v\n",
		time.Now().UTC().Format(time.RFC3339Nano), cause)
	// Persist the wrapped error chain so the UI can render it without
	// parsing the log file. Plain string in the column; the engine
	// always formats it via %v (fmt.Errorf wrap chain) so callers see
	// the same message they got back from Deploy().
	_ = e.repo.updateFields(ctx, d.ID, map[string]any{
		"status":        "failed",
		"finished_at":   now,
		"error_message": causeMsg,
	})
	d.Status = "failed"
	d.FinishedAt = &now
	d.ErrorMessage = &causeMsg

	// Decide app status: keep running if we had a previous success, else error.
	prev, perr := e.repo.lastSuccess(ctx, a.ID, d.ID)
	if perr == nil && prev != nil {
		_ = e.Apps.SetStatus(ctx, a.ID, "running")
	} else {
		_ = e.Apps.SetStatus(ctx, a.ID, "error")
	}

	e.publish(a.ID, d.ID, "deploy.failed", map[string]any{
		"deployment_id": d.ID,
		"app_id":        a.ID,
		"error":         cause.Error(),
	})
}

// healthCheck waits until the container reports healthy on its configured
// health_check endpoint. Returns nil on success, error on timeout/unhealthy.
func (e *Engine) healthCheck(ctx context.Context, a *app.App, containerName string, containerPort int, logFile io.Writer) error {
	if !a.HealthCheckEnabled {
		// No health check — small settle, then assume healthy.
		fmt.Fprintf(logFile, "%s [stdout] Health check disabled — waiting 5s for container to settle.\n",
			time.Now().UTC().Format(time.RFC3339Nano))
		select {
		case <-time.After(5 * time.Second):
		case <-ctx.Done():
			return ctx.Err()
		}
		return nil
	}
	path := a.HealthCheckPath
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	port := containerPort
	if a.HealthCheckPort != nil && *a.HealthCheckPort > 0 {
		port = *a.HealthCheckPort
	}
	method := strings.ToUpper(a.HealthCheckMethod)
	if method == "" {
		method = "GET"
	}
	wantCode := a.HealthCheckReturnCode
	if wantCode == 0 {
		wantCode = 200
	}
	timeout := time.Duration(a.HealthCheckTimeout) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	interval := time.Duration(a.HealthCheckInterval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	startPeriod := time.Duration(a.HealthCheckStartPeriod) * time.Second

	url := fmt.Sprintf("http://%s:%d%s", containerName, port, path)
	fmt.Fprintf(logFile, "%s [stdout] Health check: %s %s (expect %d) — start_period=%s interval=%s timeout=%s\n",
		time.Now().UTC().Format(time.RFC3339Nano), method, url, wantCode, startPeriod, interval, timeout)

	if startPeriod > 0 {
		select {
		case <-time.After(startPeriod):
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	httpClient := &http.Client{Timeout: 5 * time.Second}
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		if time.Now().After(deadline) {
			break
		}
		req, err := http.NewRequestWithContext(ctx, method, url, nil)
		if err != nil {
			return fmt.Errorf("build request: %w", err)
		}
		resp, err := httpClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == wantCode {
				fmt.Fprintf(logFile, "%s [stdout] Health check passed (status=%d).\n",
					time.Now().UTC().Format(time.RFC3339Nano), resp.StatusCode)
				return nil
			}
			lastErr = fmt.Errorf("status=%d, want %d", resp.StatusCode, wantCode)
		} else {
			lastErr = err
		}
		fmt.Fprintf(logFile, "%s [stdout] Health check pending: %v\n",
			time.Now().UTC().Format(time.RFC3339Nano), lastErr)
		select {
		case <-time.After(interval):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if lastErr == nil {
		lastErr = errors.New("timeout")
	}
	return fmt.Errorf("after %s: %w", timeout, lastErr)
}

// appDomains returns (primary, aliases, forceHTTPSByHost) for an app.
// Empty primary means the app has no domain. The third return value
// lets the caller pass the per-domain force_https toggle through to
// caddy.UpsertRoute without re-querying the domains table.
func (e *Engine) appDomains(ctx context.Context, appID string) (string, []string, map[string]bool, error) {
	id := appID
	doms, err := e.Domains.List(ctx, &id)
	if err != nil {
		return "", nil, nil, err
	}
	var primary string
	var aliases []string
	force := make(map[string]bool, len(doms))
	for _, d := range doms {
		force[d.Name] = d.ForceHTTPS
		if d.IsPrimary && primary == "" {
			primary = d.Name
			continue
		}
		aliases = append(aliases, d.Name)
	}
	// If no primary flagged, use the first available.
	if primary == "" && len(aliases) > 0 {
		primary = aliases[0]
		aliases = aliases[1:]
	}
	return primary, aliases, force, nil
}

// pruneImages keeps only the most recent `keep` images for an app.
func (e *Engine) pruneImages(ctx context.Context, provider dockersvc.Provider, a *app.App, keep int) {
	cli, err := provider.Client(ctx)
	if err != nil {
		return
	}
	imgs, err := cli.ImageList(ctx, image.ListOptions{})
	if err != nil {
		return
	}
	prefix := "prexel-" + a.Name + "-"
	type imgInfo struct {
		Ref     string
		Created int64
	}
	var matched []imgInfo
	for _, img := range imgs {
		for _, tag := range img.RepoTags {
			if strings.HasPrefix(tag, prefix) {
				matched = append(matched, imgInfo{Ref: tag, Created: img.Created})
				break
			}
		}
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].Created > matched[j].Created })
	for i := keep; i < len(matched); i++ {
		_ = dockersvc.RemoveImage(ctx, provider, matched[i].Ref)
	}
}

// publish emits an event on a couple of related topics so subscribers can
// listen narrowly (per deployment) or per-app.
func (e *Engine) publish(appID, deploymentID, eventType string, payload any) {
	if e.Bus == nil {
		return
	}
	e.Bus.Publish("deploy."+deploymentID+"."+suffixForType(eventType), eventType, payload)
	e.Bus.Publish("app."+appID+"."+suffixForType(eventType), eventType, payload)
}

// suffixForType maps an event type to a topic-suffix segment. The deploy.*
// topics use bare suffix names to keep them human-readable.
func suffixForType(t string) string {
	switch t {
	case "deploy.started":
		return "started"
	case "deploy.log":
		return "log"
	case "deploy.success":
		return "success"
	case "deploy.failed":
		return "failed"
	case "app.status_changed":
		return "status_changed"
	}
	return strings.ReplaceAll(t, ".", "_")
}

// imageShortSHA extracts the short sha from an imageRef of the form
// prexel-<app>-<sha>:<ts>. Falls back to a random hex if parsing fails.
func imageShortSHA(imageRef, appName string) string {
	// strip tag
	r := imageRef
	if i := strings.LastIndex(r, ":"); i >= 0 {
		r = r[:i]
	}
	prefix := "prexel-" + appName + "-"
	if strings.HasPrefix(r, prefix) {
		return r[len(prefix):]
	}
	return uuid.NewString()[:7]
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func strPtr(s string) *string { return &s }

// intPtrToInt64Ptr converts an *int into an *int64 with the same
// nil-ness — used to thread the optional cgroup knobs through to the
// docker layer without leaking the int/int64 split.
func intPtrToInt64Ptr(p *int) *int64 {
	if p == nil {
		return nil
	}
	v := int64(*p)
	return &v
}

// intPtrToInt64 dereferences an *int (0 when nil).
func intPtrToInt64(p *int) int64 {
	if p == nil {
		return 0
	}
	return int64(*p)
}

// namedVolumeName builds a deterministic, docker-volume-legal name from
// (app name, in-container mount path). The rules:
//
//   - Always prefixed `prexel-vol-` so the operator can list/prune what
//     we own via `docker volume ls --filter name=prexel-vol-`.
//   - The mount path is slugified: leading slash dropped, remaining
//     slashes flattened to `-`, and any non-[a-z0-9_-] char replaced
//     with `-`. Uppercase folded to lowercase.
//   - Collapsed runs of `-` and trimmed at the edges so we never emit
//     `prexel-vol-myapp---etc` which is ugly but legal.
//
// Docker requires `[a-zA-Z0-9][a-zA-Z0-9_.-]*` (~255 chars). Our output
// always starts with `p` and only contains `[a-z0-9_-]` so it's a
// strict subset of legal.
func namedVolumeName(appName, mountPath string) string {
	return "prexel-vol-" + slugify(appName) + "-" + slugify(mountPath)
}

// legacyDedupe returns the input names with empty strings dropped and
// duplicates removed (in order). Used by the swap path so we can
// pass `[current, legacy]` through the cleanup loop without acting
// twice when the names happen to be equal (operators who set their
// own `container_name` would otherwise see the same stop/remove
// fire twice).
func legacyDedupe(names ...string) []string {
	seen := make(map[string]struct{}, len(names))
	out := make([]string, 0, len(names))
	for _, n := range names {
		if n == "" {
			continue
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	return out
}

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s))
	prevDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		case r == '_':
			b.WriteRune(r)
			prevDash = false
		default:
			// Slash, dot, space, anything else — fold to a single dash.
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := b.String()
	out = strings.Trim(out, "-")
	return out
}

// container imports are deferred until used below; we keep this stub to satisfy
// the linter (the container package is imported via the explicit alias when
// building locally).
var _ = client.IsErrNotFound
