package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/prexel/prexel/internal/gitsrc"
	"github.com/prexel/prexel/internal/server"
)

// Status constants used across the API.
const (
	StatusIdle        = "idle"
	StatusStopped     = "stopped"
	StatusUnreachable = "unreachable"
)

// Validation regexes (Spec: Apps & Deploy).
var (
	nameRegex   = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]*[a-zA-Z0-9])?$`)
	envKeyRegex = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
	memRegex    = regexp.MustCompile(`^\d+(?:\.\d+)?[mgkbMGKB]?$`)
	cpuRegex    = regexp.MustCompile(`^\d+(?:\.\d+)?$`)
	uuidRegex   = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	// cpuset accepts "0,2-4,7" — comma-separated single ints or ranges.
	cpusetRegex = regexp.MustCompile(`^\d+(?:-\d+)?(?:,\d+(?:-\d+)?)*$`)
	// docker label keys: lowercase reverse-DNS-ish, no spaces, no '='.
	labelKeyRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)
)

// validRestartPolicies is the closed set of values accepted by the
// docker restart policy field. Matches the CHECK constraint on the
// `apps.restart_policy` column.
var validRestartPolicies = map[string]struct{}{
	"no":             {},
	"always":         {},
	"on-failure":     {},
	"unless-stopped": {},
}

func validateRestartPolicy(p string) error {
	if _, ok := validRestartPolicies[p]; !ok {
		return fmt.Errorf("invalid_restart_policy: %q", p)
	}
	return nil
}

// validateAdvancedLimits checks the cgroups-style limits that map
// directly to docker HostConfig knobs. Each field is independently
// validated; nil values are skipped.
func validateAdvancedLimits(memSwap, memReservation *string, swappiness *int, cpuset *string, cpuShares *int) error {
	if memSwap != nil {
		v := strings.TrimSpace(*memSwap)
		if v != "" && !memRegex.MatchString(v) {
			return errors.New("invalid_limits_memory_swap")
		}
	}
	if memReservation != nil {
		v := strings.TrimSpace(*memReservation)
		if v != "" && !memRegex.MatchString(v) {
			return errors.New("invalid_limits_memory_reservation")
		}
	}
	if swappiness != nil {
		if *swappiness < 0 || *swappiness > 100 {
			return errors.New("invalid_limits_memory_swappiness: 0-100")
		}
	}
	if cpuset != nil {
		v := strings.TrimSpace(*cpuset)
		if v != "" && !cpusetRegex.MatchString(v) {
			return errors.New("invalid_limits_cpuset")
		}
	}
	if cpuShares != nil {
		if *cpuShares < 2 || *cpuShares > 262144 {
			return errors.New("invalid_limits_cpu_shares: 2-262144")
		}
	}
	return nil
}

// validateDockerLabels rejects keys that would be unsafe / unusable
// when fed to docker. The value side is anything (matches docker).
// We do NOT reject the prexel.* namespace here — the merge at
// container-create time overrides anything that conflicts, so an
// operator setting prexel.foo=bar just gets shadowed silently.
func validateDockerLabels(m map[string]string) error {
	for k := range m {
		if k == "" || len(k) > 253 {
			return fmt.Errorf("invalid_docker_label_key: %q (1-253 chars)", k)
		}
		if !labelKeyRegex.MatchString(k) {
			return fmt.Errorf("invalid_docker_label_key: %q", k)
		}
	}
	return nil
}

// ResourceDefaults is anything that can answer "if the operator didn't
// specify memory/cpu limits for a new app, what should we use?". The
// concrete production wiring is instance.Service — we accept the small
// interface here to keep app/ free of an instance/ import.
type ResourceDefaults interface {
	// DefaultLimits returns ("", "") if no defaults are configured. Callers
	// must treat empty strings as "no limit", matching the per-app meaning.
	DefaultLimits() (memory, cpu string)
}

// TagLister is the slim view of internal/tag.Service that this package
// needs to decorate App responses with their attached tag names. We accept
// the interface rather than the concrete service to keep this package free
// of an internal/tag import (which would cycle: tag handler imports app,
// app would then import tag).
type TagLister interface {
	ForApp(ctx context.Context, appID string) ([]string, error)
}

// Service is the domain entry point for the apps table.
type Service struct {
	repo     *repo
	servers  *server.Service
	gitSrcs  *gitsrc.Repo
	defaults ResourceDefaults
	tags     TagLister
}

// NewService wires a Service. gitSrcs may be nil only when git is disabled —
// otherwise A6 will still happily create apps but cannot validate
// git_source_id linkage. defaults may be nil — apps will then be created
// with whatever the user passes (empty = "no limit").
func NewService(db *sql.DB, servers *server.Service, gitSrcs *gitsrc.Repo) *Service {
	return &Service{repo: newRepo(db), servers: servers, gitSrcs: gitSrcs}
}

// WithDefaults attaches a ResourceDefaults provider. Returns the receiver so
// it can chain in a wire-up builder. Idempotent — re-attaching overrides.
func (s *Service) WithDefaults(d ResourceDefaults) *Service {
	s.defaults = d
	return s
}

// WithTags attaches a TagLister so App responses include the attached tag
// names. Optional — when unset, Tags is left as an empty slice. Returns the
// receiver for fluent wiring. Idempotent — re-attaching overrides.
func (s *Service) WithTags(t TagLister) *Service {
	s.tags = t
	return s
}

// HealthCheckOpts mirrors the health-check sub-document accepted by Create.
// Zero values trigger defaults at validation time.
type HealthCheckOpts struct {
	Enabled     *bool  `json:"enabled,omitempty"`
	Path        string `json:"path,omitempty"`
	Port        *int   `json:"port,omitempty"`
	Method      string `json:"method,omitempty"`
	ReturnCode  int    `json:"return_code,omitempty"`
	Interval    int    `json:"interval,omitempty"`
	Timeout     int    `json:"timeout,omitempty"`
	Retries     int    `json:"retries,omitempty"`
	StartPeriod int    `json:"start_period,omitempty"`
}

// CreateInput is the union of fields accepted by Create.
type CreateInput struct {
	Name                    string
	Description             string
	TeamID                  *string
	ServerID                string
	GitSourceID             *string
	RepoURL                 *string
	Branch                  string
	GitCommitSHA            *string
	BuildType               string
	DockerfilePath          string
	BuildContext            string
	DockerfileInline        *string
	ComposeFile             *string
	ComposeInline           *string
	ImageName               *string
	ImageTag                string
	InstallCommand          *string
	BuildCommand            *string
	StartCommand            *string
	PreDeployCommand        *string
	PostDeployCommand       *string
	Port                    *int
	HostPort                *int
	HealthCheck             HealthCheckOpts
	LimitsMemory            *string
	LimitsCPUs              *string
	LimitsMemorySwap        *string
	LimitsMemorySwappiness  *int
	LimitsMemoryReservation *string
	LimitsCPUSet            *string
	LimitsCPUShares         *int
	RestartPolicy           string
	AutoDeployBranch        *string
	BuildArgsInject         *bool
	BuildArgsSourceCommit   *bool
	DockerLabels            map[string]string
	EnvVars                 map[string]string
}

// UpdatePatch is the partial-update payload for Update. Name, ServerID and
// BuildType are intentionally absent — they are not mutable after Create.
type UpdatePatch struct {
	Description             *string
	TeamID                  *string
	ClearTeamID             bool
	GitSourceID             *string
	ClearGitSourceID        bool
	RepoURL                 *string
	Branch                  *string
	GitCommitSHA            *string
	DockerfilePath          *string
	BuildContext            *string
	DockerfileInline        *string
	ComposeFile             *string
	ComposeInline           *string
	ImageName               *string
	ImageTag                *string
	InstallCommand          *string
	BuildCommand            *string
	StartCommand            *string
	PreDeployCommand        *string
	PostDeployCommand       *string
	Port                    *int
	HostPort                *int
	HealthCheck             *HealthCheckOpts
	LimitsMemory            *string
	LimitsCPUs              *string
	LimitsMemorySwap        *string
	LimitsMemorySwappiness  *int
	ClearMemorySwappiness   bool
	LimitsMemoryReservation *string
	LimitsCPUSet            *string
	LimitsCPUShares         *int
	ClearCPUShares          bool
	RestartPolicy           *string
	AutoDeployBranch        *string
	ClearAutoDeployBranch   bool
	BuildArgsInject         *bool
	BuildArgsSourceCommit   *bool
	DockerLabels            map[string]string
	EnvVars                 map[string]string
}

// Create validates the input, materialises defaults, persists the row, and
// returns the fully-populated App.
func (s *Service) Create(ctx context.Context, in CreateInput) (*App, error) {
	name := strings.TrimSpace(in.Name)
	if len(name) < 3 || len(name) > 50 {
		return nil, errors.New("invalid_name: must be 3-50 chars")
	}
	if !nameRegex.MatchString(name) {
		return nil, errors.New("invalid_name: must match [a-zA-Z0-9][a-zA-Z0-9-]*[a-zA-Z0-9]")
	}

	// Server must exist and be connected.
	srv, err := s.servers.Get(ctx, strings.TrimSpace(in.ServerID))
	if err != nil {
		if errors.Is(err, server.ErrNotFound) {
			return nil, fmt.Errorf("invalid_server_id: %w", err)
		}
		return nil, err
	}
	if srv.Status != "connected" {
		return nil, fmt.Errorf("server_not_connected: server status is %q", srv.Status)
	}
	teamID := trimmedPtr(in.TeamID)
	if teamID != nil {
		if err := s.validateTeam(ctx, *teamID); err != nil {
			return nil, err
		}
	}

	buildType := strings.TrimSpace(in.BuildType)
	switch buildType {
	case "dockerfile":
		hasRepo := in.RepoURL != nil && strings.TrimSpace(*in.RepoURL) != ""
		hasInline := in.DockerfileInline != nil && strings.TrimSpace(*in.DockerfileInline) != ""
		if !hasRepo && !hasInline {
			return nil, errors.New("invalid_build: dockerfile requires repo_url or dockerfile_inline")
		}
		if hasRepo && hasInline {
			return nil, errors.New("invalid_build: cannot set both repo_url and dockerfile_inline")
		}
	case "docker_image":
		if in.ImageName == nil || strings.TrimSpace(*in.ImageName) == "" {
			return nil, errors.New("invalid_build: docker_image requires image_name")
		}
	case "docker_compose":
		hasRepo := in.RepoURL != nil && strings.TrimSpace(*in.RepoURL) != ""
		hasInline := in.ComposeInline != nil && strings.TrimSpace(*in.ComposeInline) != ""
		if !hasRepo && !hasInline {
			return nil, errors.New("invalid_build: docker_compose requires repo_url or compose_inline")
		}
		if hasRepo && hasInline {
			return nil, errors.New("invalid_build: cannot set both repo_url and compose_inline")
		}
	default:
		return nil, fmt.Errorf("invalid_build_type: %q", buildType)
	}

	// Optional git_source linkage validation.
	if in.GitSourceID != nil && strings.TrimSpace(*in.GitSourceID) != "" {
		if s.gitSrcs == nil {
			return nil, errors.New("git_sources_disabled")
		}
		if _, err := s.gitSrcs.Get(strings.TrimSpace(*in.GitSourceID)); err != nil {
			if errors.Is(err, gitsrc.ErrNotFound) {
				return nil, fmt.Errorf("invalid_git_source_id: %w", err)
			}
			return nil, err
		}
	}

	if err := validatePort(in.Port); err != nil {
		return nil, err
	}
	if err := validatePort(in.HostPort); err != nil {
		return nil, err
	}
	// Inherit instance-level defaults when the caller didn't specify either
	// limit. Pointer-vs-empty distinction: nil OR an explicit empty string
	// both count as "use the default". Anything non-empty wins.
	if s.defaults != nil {
		dm, dc := s.defaults.DefaultLimits()
		if isBlankPtr(in.LimitsMemory) && dm != "" {
			in.LimitsMemory = &dm
		}
		if isBlankPtr(in.LimitsCPUs) && dc != "" {
			in.LimitsCPUs = &dc
		}
	}
	if err := validateLimits(in.LimitsMemory, in.LimitsCPUs); err != nil {
		return nil, err
	}
	if err := validateAdvancedLimits(in.LimitsMemorySwap, in.LimitsMemoryReservation, in.LimitsMemorySwappiness, in.LimitsCPUSet, in.LimitsCPUShares); err != nil {
		return nil, err
	}
	if err := validateEnvMap(in.EnvVars); err != nil {
		return nil, err
	}
	restartPolicy := strings.TrimSpace(in.RestartPolicy)
	if restartPolicy == "" {
		restartPolicy = "unless-stopped"
	}
	if err := validateRestartPolicy(restartPolicy); err != nil {
		return nil, err
	}
	if err := validateDockerLabels(in.DockerLabels); err != nil {
		return nil, err
	}

	hcOpts, err := normaliseHealthCheck(in.HealthCheck)
	if err != nil {
		return nil, err
	}
	hc := hcOpts.Unwrap()

	branch := strings.TrimSpace(in.Branch)
	if branch == "" {
		branch = "main"
	}
	dockerfilePath := strings.TrimSpace(in.DockerfilePath)
	if dockerfilePath == "" {
		dockerfilePath = "Dockerfile"
	}
	buildContext := strings.TrimSpace(in.BuildContext)
	if buildContext == "" {
		buildContext = "."
	}
	composeFile := strings.TrimSpace(deref(in.ComposeFile))
	if composeFile == "" && buildType == "docker_compose" && in.ComposeInline == nil {
		composeFile = "docker-compose.yml"
	}
	imageTag := strings.TrimSpace(in.ImageTag)
	if imageTag == "" {
		imageTag = "latest"
	}
	// Canonical Docker container name = app name, no prefix. Operators
	// recognise their apps by name; the `prexel.*` labels already track
	// ownership. (Earlier versions used `prexel-<name>` — engine.go's
	// swap path still tries the legacy name when removing old
	// containers so existing apps redeploy cleanly.)
	containerName := name

	srvID := srv.ID
	buildArgsInject := true
	if in.BuildArgsInject != nil {
		buildArgsInject = *in.BuildArgsInject
	}
	buildArgsSourceCommit := false
	if in.BuildArgsSourceCommit != nil {
		buildArgsSourceCommit = *in.BuildArgsSourceCommit
	}
	a := &App{
		ID:                      uuid.NewString(),
		Name:                    name,
		Description:             strPtrOrNil(in.Description),
		TeamID:                  teamID,
		ServerID:                &srvID,
		GitSourceID:             trimmedPtr(in.GitSourceID),
		RepoURL:                 trimmedPtr(in.RepoURL),
		Branch:                  branch,
		GitCommitSHA:            trimmedPtr(in.GitCommitSHA),
		BuildType:               buildType,
		DockerfilePath:          dockerfilePath,
		BuildContext:            buildContext,
		DockerfileInline:        trimmedPtr(in.DockerfileInline),
		ComposeFile:             strPtrOrNil(composeFile),
		ComposeInline:           trimmedPtr(in.ComposeInline),
		ImageName:               trimmedPtr(in.ImageName),
		ImageTag:                imageTag,
		InstallCommand:          trimmedPtr(in.InstallCommand),
		BuildCommand:            trimmedPtr(in.BuildCommand),
		StartCommand:            trimmedPtr(in.StartCommand),
		PreDeployCommand:        trimmedPtr(in.PreDeployCommand),
		PostDeployCommand:       trimmedPtr(in.PostDeployCommand),
		Port:                    in.Port,
		HostPort:                in.HostPort,
		ContainerName:           &containerName,
		HealthCheckEnabled:      hc.Enabled,
		HealthCheckPath:         hc.Path,
		HealthCheckMethod:       hc.Method,
		HealthCheckPort:         hc.Port,
		HealthCheckReturnCode:   hc.ReturnCode,
		HealthCheckInterval:     hc.Interval,
		HealthCheckTimeout:      hc.Timeout,
		HealthCheckRetries:      hc.Retries,
		HealthCheckStartPeriod:  hc.StartPeriod,
		LimitsMemory:            trimmedPtr(in.LimitsMemory),
		LimitsCPUs:              trimmedPtr(in.LimitsCPUs),
		LimitsMemorySwap:        trimmedPtr(in.LimitsMemorySwap),
		LimitsMemorySwappiness:  in.LimitsMemorySwappiness,
		LimitsMemoryReservation: trimmedPtr(in.LimitsMemoryReservation),
		LimitsCPUSet:            trimmedPtr(in.LimitsCPUSet),
		LimitsCPUShares:         in.LimitsCPUShares,
		RestartPolicy:           restartPolicy,
		AutoDeployBranch:        trimmedPtr(in.AutoDeployBranch),
		BuildArgsInject:         buildArgsInject,
		BuildArgsSourceCommit:   buildArgsSourceCommit,
		DockerLabels:            in.DockerLabels,
		EnvVars:                 in.EnvVars,
		Status:                  StatusIdle,
	}
	if a.EnvVars == nil {
		a.EnvVars = map[string]string{}
	}
	if a.DockerLabels == nil {
		a.DockerLabels = map[string]string{}
	}

	if err := s.repo.insert(ctx, a); err != nil {
		return nil, err
	}
	return s.repo.get(ctx, a.ID)
}

// List returns every app, optionally filtered by server.
func (s *Service) List(ctx context.Context, serverID string) ([]App, error) {
	out, err := s.repo.list(ctx, strings.TrimSpace(serverID))
	if err != nil {
		return nil, err
	}
	for i := range out {
		s.populateTags(ctx, &out[i])
	}
	return out, nil
}

// Get resolves by id OR name. Tolerant lookup: if the value does not parse as a
// UUID, falls back to name. Useful for CLI ergonomics.
func (s *Service) Get(ctx context.Context, idOrName string) (*App, error) {
	v := strings.TrimSpace(idOrName)
	if v == "" {
		return nil, ErrNotFound
	}
	var (
		out *App
		err error
	)
	if uuidRegex.MatchString(v) {
		out, err = s.repo.get(ctx, v)
	} else {
		out, err = s.repo.getByName(ctx, v)
	}
	if err != nil {
		return nil, err
	}
	s.populateTags(ctx, out)
	return out, nil
}

// populateTags decorates the App with the names of its attached tags. Best-
// effort: a failure here is logged and swallowed because the tag panel is a
// dashboard nicety, not load-bearing for any deploy decision. We also make
// sure Tags is always a non-nil slice so JSON marshals "tags":[] instead of
// "tags":null.
func (s *Service) populateTags(ctx context.Context, a *App) {
	if a == nil {
		return
	}
	if a.Tags == nil {
		a.Tags = []string{}
	}
	if s.tags == nil {
		return
	}
	names, err := s.tags.ForApp(ctx, a.ID)
	if err != nil {
		// Non-essential — log and move on. We use the standard
		// library logger to avoid pulling slog into this package's
		// dependency surface.
		log.Printf("app: populate tags for %s: %v", a.ID, err)
		return
	}
	if names == nil {
		names = []string{}
	}
	a.Tags = names
}

// Update applies a partial mutation. Status is preserved when the current
// status is "unreachable" (the server-status loop owns that transition).
func (s *Service) Update(ctx context.Context, id string, p UpdatePatch) (*App, error) {
	cur, err := s.repo.get(ctx, id)
	if err != nil {
		return nil, err
	}

	sets := map[string]any{}

	if p.Description != nil {
		sets["description"] = nullableString(*p.Description)
	}
	if p.ClearTeamID {
		sets["team_id"] = nil
	} else if p.TeamID != nil {
		teamID := strings.TrimSpace(*p.TeamID)
		if teamID == "" {
			sets["team_id"] = nil
		} else {
			if err := s.validateTeam(ctx, teamID); err != nil {
				return nil, err
			}
			sets["team_id"] = teamID
		}
	}
	if p.ClearGitSourceID {
		sets["git_source_id"] = nil
	} else if p.GitSourceID != nil {
		gid := strings.TrimSpace(*p.GitSourceID)
		if gid != "" && s.gitSrcs != nil {
			if _, err := s.gitSrcs.Get(gid); err != nil {
				if errors.Is(err, gitsrc.ErrNotFound) {
					return nil, fmt.Errorf("invalid_git_source_id: %w", err)
				}
				return nil, err
			}
		}
		if gid == "" {
			sets["git_source_id"] = nil
		} else {
			sets["git_source_id"] = gid
		}
	}
	if p.RepoURL != nil {
		sets["repo_url"] = nullableString(*p.RepoURL)
	}
	if p.Branch != nil {
		b := strings.TrimSpace(*p.Branch)
		if b == "" {
			b = "main"
		}
		sets["branch"] = b
	}
	if p.GitCommitSHA != nil {
		sets["git_commit_sha"] = nullableString(*p.GitCommitSHA)
	}
	if p.DockerfilePath != nil {
		v := strings.TrimSpace(*p.DockerfilePath)
		if v == "" {
			v = "Dockerfile"
		}
		sets["dockerfile_path"] = v
	}
	if p.BuildContext != nil {
		v := strings.TrimSpace(*p.BuildContext)
		if v == "" {
			v = "."
		}
		sets["build_context"] = v
	}
	if p.DockerfileInline != nil {
		sets["dockerfile_inline"] = nullableString(*p.DockerfileInline)
	}
	if p.ComposeFile != nil {
		sets["compose_file"] = nullableString(*p.ComposeFile)
	}
	if p.ComposeInline != nil {
		sets["compose_inline"] = nullableString(*p.ComposeInline)
	}
	if p.ImageName != nil {
		sets["image_name"] = nullableString(*p.ImageName)
	}
	if p.ImageTag != nil {
		v := strings.TrimSpace(*p.ImageTag)
		if v == "" {
			v = "latest"
		}
		sets["image_tag"] = v
	}
	if p.InstallCommand != nil {
		sets["install_command"] = nullableString(*p.InstallCommand)
	}
	if p.BuildCommand != nil {
		sets["build_command"] = nullableString(*p.BuildCommand)
	}
	if p.StartCommand != nil {
		sets["start_command"] = nullableString(*p.StartCommand)
	}
	if p.PreDeployCommand != nil {
		sets["pre_deploy_command"] = nullableString(*p.PreDeployCommand)
	}
	if p.PostDeployCommand != nil {
		sets["post_deploy_command"] = nullableString(*p.PostDeployCommand)
	}
	if p.RestartPolicy != nil {
		v := strings.TrimSpace(*p.RestartPolicy)
		if v == "" {
			v = "unless-stopped"
		}
		if err := validateRestartPolicy(v); err != nil {
			return nil, err
		}
		sets["restart_policy"] = v
	}
	if p.ClearAutoDeployBranch {
		sets["auto_deploy_branch"] = nil
	} else if p.AutoDeployBranch != nil {
		sets["auto_deploy_branch"] = nullableString(*p.AutoDeployBranch)
	}
	if p.BuildArgsInject != nil {
		sets["build_args_inject"] = boolToInt(*p.BuildArgsInject)
	}
	if p.BuildArgsSourceCommit != nil {
		sets["build_args_source_commit"] = boolToInt(*p.BuildArgsSourceCommit)
	}
	if p.DockerLabels != nil {
		if err := validateDockerLabels(p.DockerLabels); err != nil {
			return nil, err
		}
		b, err := marshalEnv(p.DockerLabels)
		if err != nil {
			return nil, err
		}
		sets["docker_labels"] = b
	}
	if p.LimitsMemorySwap != nil {
		v := strings.TrimSpace(*p.LimitsMemorySwap)
		if v != "" && !memRegex.MatchString(v) {
			return nil, errors.New("invalid_limits_memory_swap")
		}
		sets["limits_memory_swap"] = nullableString(v)
	}
	if p.LimitsMemoryReservation != nil {
		v := strings.TrimSpace(*p.LimitsMemoryReservation)
		if v != "" && !memRegex.MatchString(v) {
			return nil, errors.New("invalid_limits_memory_reservation")
		}
		sets["limits_memory_reservation"] = nullableString(v)
	}
	if p.ClearMemorySwappiness {
		sets["limits_memory_swappiness"] = nil
	} else if p.LimitsMemorySwappiness != nil {
		if *p.LimitsMemorySwappiness < 0 || *p.LimitsMemorySwappiness > 100 {
			return nil, errors.New("invalid_limits_memory_swappiness: 0-100")
		}
		sets["limits_memory_swappiness"] = *p.LimitsMemorySwappiness
	}
	if p.LimitsCPUSet != nil {
		v := strings.TrimSpace(*p.LimitsCPUSet)
		if v != "" && !cpusetRegex.MatchString(v) {
			return nil, errors.New("invalid_limits_cpuset")
		}
		sets["limits_cpuset"] = nullableString(v)
	}
	if p.ClearCPUShares {
		sets["limits_cpu_shares"] = nil
	} else if p.LimitsCPUShares != nil {
		if *p.LimitsCPUShares < 2 || *p.LimitsCPUShares > 262144 {
			return nil, errors.New("invalid_limits_cpu_shares: 2-262144")
		}
		sets["limits_cpu_shares"] = *p.LimitsCPUShares
	}
	if p.Port != nil {
		if err := validatePort(p.Port); err != nil {
			return nil, err
		}
		sets["port"] = *p.Port
	}
	if p.HostPort != nil {
		if err := validatePort(p.HostPort); err != nil {
			return nil, err
		}
		sets["host_port"] = *p.HostPort
	}
	if p.LimitsMemory != nil {
		v := strings.TrimSpace(*p.LimitsMemory)
		if v != "" && !memRegex.MatchString(v) {
			return nil, errors.New("invalid_limits_memory")
		}
		sets["limits_memory"] = nullableString(v)
	}
	if p.LimitsCPUs != nil {
		v := strings.TrimSpace(*p.LimitsCPUs)
		if v != "" && !cpuRegex.MatchString(v) {
			return nil, errors.New("invalid_limits_cpus")
		}
		sets["limits_cpus"] = nullableString(v)
	}
	if p.HealthCheck != nil {
		// Merge: start from current persisted values, overlay non-zero patch.
		merged := HealthCheckOpts{
			Enabled:     boolPtr(cur.HealthCheckEnabled),
			Path:        cur.HealthCheckPath,
			Port:        cur.HealthCheckPort,
			Method:      cur.HealthCheckMethod,
			ReturnCode:  cur.HealthCheckReturnCode,
			Interval:    cur.HealthCheckInterval,
			Timeout:     cur.HealthCheckTimeout,
			Retries:     cur.HealthCheckRetries,
			StartPeriod: cur.HealthCheckStartPeriod,
		}
		if p.HealthCheck.Enabled != nil {
			merged.Enabled = p.HealthCheck.Enabled
		}
		if p.HealthCheck.Path != "" {
			merged.Path = p.HealthCheck.Path
		}
		if p.HealthCheck.Port != nil {
			merged.Port = p.HealthCheck.Port
		}
		if p.HealthCheck.Method != "" {
			merged.Method = p.HealthCheck.Method
		}
		if p.HealthCheck.ReturnCode != 0 {
			merged.ReturnCode = p.HealthCheck.ReturnCode
		}
		if p.HealthCheck.Interval != 0 {
			merged.Interval = p.HealthCheck.Interval
		}
		if p.HealthCheck.Timeout != 0 {
			merged.Timeout = p.HealthCheck.Timeout
		}
		if p.HealthCheck.Retries != 0 {
			merged.Retries = p.HealthCheck.Retries
		}
		if p.HealthCheck.StartPeriod != 0 {
			merged.StartPeriod = p.HealthCheck.StartPeriod
		}
		hcOpts, err := normaliseHealthCheck(merged)
		if err != nil {
			return nil, err
		}
		hc := hcOpts.Unwrap()
		sets["health_check_enabled"] = boolToInt(hc.Enabled)
		sets["health_check_path"] = hc.Path
		sets["health_check_method"] = hc.Method
		if hc.Port != nil {
			sets["health_check_port"] = *hc.Port
		} else {
			sets["health_check_port"] = nil
		}
		sets["health_check_return_code"] = hc.ReturnCode
		sets["health_check_interval"] = hc.Interval
		sets["health_check_timeout"] = hc.Timeout
		sets["health_check_retries"] = hc.Retries
		sets["health_check_start_period"] = hc.StartPeriod
	}
	if p.EnvVars != nil {
		if err := validateEnvMap(p.EnvVars); err != nil {
			return nil, err
		}
		b, err := marshalEnv(p.EnvVars)
		if err != nil {
			return nil, err
		}
		sets["env_vars"] = b
	}

	out, err := s.repo.updateFields(ctx, id, sets)
	if err != nil {
		return nil, err
	}

	// Preserve unreachable status — server status loop is authoritative.
	if cur.Status == StatusUnreachable && out.Status != StatusUnreachable {
		if err := s.repo.updateStatus(ctx, id, StatusUnreachable); err != nil {
			return nil, err
		}
		out.Status = StatusUnreachable
	}
	return out, nil
}

// SetEnvVars replaces the full env_vars JSON document for an app.
func (s *Service) SetEnvVars(ctx context.Context, id string, env map[string]string) (*App, error) {
	if env == nil {
		env = map[string]string{}
	}
	if err := validateEnvMap(env); err != nil {
		return nil, err
	}
	if _, err := s.repo.get(ctx, id); err != nil {
		return nil, err
	}
	if err := s.repo.updateEnvVars(ctx, id, env); err != nil {
		return nil, err
	}
	return s.repo.get(ctx, id)
}

// Stop sets the app status to stopped. Actual container stop is A8.
func (s *Service) Stop(ctx context.Context, id string) error {
	cur, err := s.repo.get(ctx, id)
	if err != nil {
		return err
	}
	if cur.Status == StatusUnreachable {
		// Don't fight the status loop.
		return nil
	}
	return s.repo.updateStatus(ctx, id, StatusStopped)
}

// Delete removes the app row. ON DELETE CASCADE clears secrets and domains;
// deployments rows survive thanks to their NOT NULL FK (CASCADE on apps).
// In practice CASCADE drops deployments too — Tech Review §17 calls for
// retaining history, but in MVP we accept that constraint until A8 reworks it.
func (s *Service) Delete(ctx context.Context, id string) error {
	return s.repo.delete(ctx, id)
}

// CleanupTargets carries everything the async appcleanup worker needs
// to remove the off-DB side effects of a deleted app. Built BEFORE
// Delete runs because the cascade wipes the source tables and most
// of this info is unreconstructable afterwards.
type CleanupTargets struct {
	// Domains routed to this app — hostnames only, used by the
	// cleanup worker to call caddy.RemoveRoute on each.
	Domains []string
	// DeploymentIDs whose build-log files (`{LogDir}/{id}.log`) need
	// removing from disk.
	DeploymentIDs []string
}

// GatherCleanupTargets reads the related tables for an app and
// returns the metadata required by the async cleanup worker. Run
// BEFORE Delete — once the apps row is gone, ON DELETE CASCADE
// has already wiped these tables and you can't fish the targets
// out.
//
// Best-effort: if either query fails we return whatever we got,
// because partial cleanup is strictly better than skipping it.
func (s *Service) GatherCleanupTargets(ctx context.Context, appID string) (CleanupTargets, error) {
	out := CleanupTargets{}

	// Domains — names only. Direct SELECT against the domains
	// table; we don't depend on internal/domains to avoid the
	// circular import (domains depends on apps, sort of).
	domRows, err := s.repo.db.QueryContext(ctx,
		`SELECT name FROM domains WHERE app_id = ?`, appID)
	if err == nil {
		defer func() { _ = domRows.Close() }()
		for domRows.Next() {
			var name string
			if scanErr := domRows.Scan(&name); scanErr == nil && name != "" {
				out.Domains = append(out.Domains, name)
			}
		}
		_ = domRows.Err()
	}

	// Deployment IDs — same direct-SELECT approach. We don't
	// import internal/deploy here for the same reason. Bounded by
	// LIMIT 10000 — even hyperactive apps don't approach this.
	depRows, derr := s.repo.db.QueryContext(ctx,
		`SELECT id FROM deployments WHERE app_id = ? LIMIT 10000`, appID)
	if derr == nil {
		defer func() { _ = depRows.Close() }()
		for depRows.Next() {
			var id string
			if scanErr := depRows.Scan(&id); scanErr == nil && id != "" {
				out.DeploymentIDs = append(out.DeploymentIDs, id)
			}
		}
		_ = depRows.Err()
	}

	return out, nil
}

// MarkUnreachable is a convenience helper used by the server status loop.
// Kept here so callers don't need to know table internals.
func (s *Service) MarkUnreachable(ctx context.Context, id string) error {
	return s.repo.updateStatus(ctx, id, StatusUnreachable)
}

// SetStatus persists a status transition for the given app. Preserves the
// "unreachable" state owned by the server status loop: a SetStatus call from
// the deploy engine only clears unreachable when the new status is "running"
// (i.e. the app recovered and is provably reachable again).
func (s *Service) SetStatus(ctx context.Context, id, status string) error {
	cur, err := s.repo.get(ctx, id)
	if err != nil {
		return err
	}
	if cur.Status == StatusUnreachable && status != "running" {
		// Don't fight the status loop while the server is still unreachable.
		return nil
	}
	return s.repo.updateStatus(ctx, id, status)
}

// ListAll returns every app row regardless of server (used by reconcile loops).
func (s *Service) ListAll(ctx context.Context) ([]App, error) {
	return s.repo.list(ctx, "")
}

func (s *Service) validateTeam(ctx context.Context, id string) error {
	var n int
	if err := s.repo.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM teams WHERE id = ?`, id).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("invalid_team_id: %s", id)
	}
	return nil
}

// ---------------------------------------------------------------------------
// validation helpers
// ---------------------------------------------------------------------------

func validatePort(p *int) error {
	if p == nil {
		return nil
	}
	if *p < 1 || *p > 65535 {
		return fmt.Errorf("invalid_port: %d", *p)
	}
	return nil
}

func validateLimits(mem, cpus *string) error {
	if mem != nil {
		v := strings.TrimSpace(*mem)
		if v != "" && !memRegex.MatchString(v) {
			return errors.New("invalid_limits_memory: expected <num>(m|g|k|b)?")
		}
	}
	if cpus != nil {
		v := strings.TrimSpace(*cpus)
		if v != "" && !cpuRegex.MatchString(v) {
			return errors.New("invalid_limits_cpus: expected <float>")
		}
	}
	return nil
}

func validateEnvMap(m map[string]string) error {
	for k := range m {
		if !envKeyRegex.MatchString(k) {
			return fmt.Errorf("invalid_env_key: %q (must match ^[A-Z][A-Z0-9_]*$)", k)
		}
	}
	return nil
}

// normaliseHealthCheck applies defaults and validates ranges.
func normaliseHealthCheck(in HealthCheckOpts) (HealthCheckOpts, error) {
	out := in
	if out.Enabled == nil {
		t := true
		out.Enabled = &t
	}
	if out.Path == "" {
		out.Path = "/"
	}
	if out.Method == "" {
		out.Method = "GET"
	}
	out.Method = strings.ToUpper(out.Method)
	switch out.Method {
	case "GET", "HEAD":
	default:
		return out, fmt.Errorf("invalid_health_check_method: %q", out.Method)
	}
	if out.ReturnCode == 0 {
		out.ReturnCode = 200
	}
	if out.ReturnCode < 100 || out.ReturnCode > 599 {
		return out, fmt.Errorf("invalid_health_check_return_code: %d", out.ReturnCode)
	}
	if out.Interval == 0 {
		out.Interval = 30
	}
	if out.Timeout == 0 {
		out.Timeout = 60
	}
	if out.Timeout < 1 || out.Timeout > 300 {
		return out, fmt.Errorf("invalid_health_check_timeout: %d (1-300)", out.Timeout)
	}
	if out.Retries == 0 {
		out.Retries = 3
	}
	if out.Retries < 1 || out.Retries > 10 {
		return out, fmt.Errorf("invalid_health_check_retries: %d (1-10)", out.Retries)
	}
	if out.StartPeriod == 0 {
		out.StartPeriod = 10
	}
	if out.Port != nil {
		if err := validatePort(out.Port); err != nil {
			return out, err
		}
	}
	finalEnabled := *out.Enabled
	out.Enabled = &finalEnabled
	return out, nil
}

// resolvedHealth is the post-normalisation value flattened for repo writes.
type resolvedHealth struct {
	Enabled     bool
	Path        string
	Method      string
	Port        *int
	ReturnCode  int
	Interval    int
	Timeout     int
	Retries     int
	StartPeriod int
}

// Unwrap resolves the optional Enabled flag and returns a value-only struct.
func (o HealthCheckOpts) Unwrap() resolvedHealth {
	enabled := true
	if o.Enabled != nil {
		enabled = *o.Enabled
	}
	return resolvedHealth{
		Enabled:     enabled,
		Path:        o.Path,
		Method:      o.Method,
		Port:        o.Port,
		ReturnCode:  o.ReturnCode,
		Interval:    o.Interval,
		Timeout:     o.Timeout,
		Retries:     o.Retries,
		StartPeriod: o.StartPeriod,
	}
}

// ---------------------------------------------------------------------------
// pointer helpers
// ---------------------------------------------------------------------------

func strPtrOrNil(s string) *string {
	v := strings.TrimSpace(s)
	if v == "" {
		return nil
	}
	return &v
}

func trimmedPtr(in *string) *string {
	if in == nil {
		return nil
	}
	v := strings.TrimSpace(*in)
	if v == "" {
		return nil
	}
	return &v
}

func deref(in *string) string {
	if in == nil {
		return ""
	}
	return *in
}

func boolPtr(b bool) *bool { return &b }

func nullableString(s string) any {
	v := strings.TrimSpace(s)
	if v == "" {
		return nil
	}
	return v
}

// isBlankPtr reports whether the pointer is nil OR points at a string that
// trims to empty. Used by Create to decide whether to inherit a resource
// default — an explicit empty string from the API ("clear this field")
// counts as blank, matching the user's intent.
func isBlankPtr(p *string) bool {
	return p == nil || strings.TrimSpace(*p) == ""
}

func marshalEnv(m map[string]string) (string, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("marshal env_vars: %w", err)
	}
	return string(b), nil
}
