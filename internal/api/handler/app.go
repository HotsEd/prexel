package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	dockertypes "github.com/docker/docker/api/types/container"
	dockerfilters "github.com/docker/docker/api/types/filters"
	"github.com/go-chi/chi/v5"
	"github.com/prexel/prexel/internal/api/apimiddleware"
	"github.com/prexel/prexel/internal/app"
	"github.com/prexel/prexel/internal/composespec"
	"github.com/prexel/prexel/internal/rbac"
	"github.com/prexel/prexel/internal/server"
)

// AppHandler bundles the /api/v1/apps endpoints. All handlers assume they are
// mounted behind Auth middleware (router.go).
//
// `servers` is optional — the Stats endpoint uses it to resolve the
// Docker provider for an app's host. Without it, /apps/:id/stats
// returns 503 instead of crashing. Other endpoints don't touch it.
//
// `composeFetcher` is the out-of-band loader for a Compose YAML
// hosted on the app's git repo. Kept as a function so the handler
// package stays free of git/gitsrc imports (wired in router.go).
// Optional — when nil, repo-hosted Compose preview is empty until
// the operator's first deploy.
type AppHandler struct {
	svc            *app.Service
	access         *rbac.Service
	servers        *server.Service
	composeFetcher func(ctx context.Context, target *app.App) ([]byte, error)
	// cleanup is the async garbage collector invoked when an app is
	// deleted. Optional — nil disables the off-DB cleanup (the row
	// still gets removed). Wired in router.go after appcleanup.Worker
	// is constructed.
	cleanup CleanupSubmitter
}

// CleanupSubmitter is the slim interface AppHandler uses to enqueue
// an async cleanup task. Production wiring binds this to
// *appcleanup.Worker.
type CleanupSubmitter interface {
	Submit(t CleanupTask) error
}

// CleanupTask mirrors appcleanup.Task so this handler doesn't have
// to import internal/appcleanup. The router converts between them
// when wiring.
type CleanupTask struct {
	AppID         string
	AppName       string
	ServerID      string
	Domains       []string
	DeploymentIDs []string
}

// SetCleanup registers the cleanup worker. Optional; nil is OK.
func (h *AppHandler) SetCleanup(c CleanupSubmitter) {
	h.cleanup = c
}

// SetComposeFetcher registers the loader used by Containers() to
// fetch the docker-compose.yml of repo-hosted Compose apps. Call
// once at boot; passing nil is OK and disables the feature.
func (h *AppHandler) SetComposeFetcher(f func(ctx context.Context, target *app.App) ([]byte, error)) {
	h.composeFetcher = f
}

// NewAppHandler constructs the handler.
//
// Both `svc` and `access` are required. `servers` is optional (Stats
// degrades gracefully if absent — useful for tests that don't wire
// the full server graph). Passing a nil RBAC service used to be
// tolerated (variadic + fallback to "allow all"), which meant any
// refactor that silently dropped the ACL would open every team-scoped
// route. We crash the process at boot instead — fail-closed beats
// surprise-open every time.
func NewAppHandler(svc *app.Service, access *rbac.Service, servers *server.Service) *AppHandler {
	if svc == nil {
		panic("handler.NewAppHandler: app service is required")
	}
	if access == nil {
		panic("handler.NewAppHandler: rbac service is required (no anonymous fallback)")
	}
	return &AppHandler{svc: svc, access: access, servers: servers}
}

// healthCheckReq mirrors HealthCheckOpts but with all fields optional so the
// JSON decoder can detect "absent" vs "zero".
type healthCheckReq struct {
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

func (h healthCheckReq) toOpts() app.HealthCheckOpts {
	return app.HealthCheckOpts{
		Enabled:     h.Enabled,
		Path:        h.Path,
		Port:        h.Port,
		Method:      h.Method,
		ReturnCode:  h.ReturnCode,
		Interval:    h.Interval,
		Timeout:     h.Timeout,
		Retries:     h.Retries,
		StartPeriod: h.StartPeriod,
	}
}

type createAppReq struct {
	Name                    string            `json:"name"`
	Description             string            `json:"description"`
	TeamID                  *string           `json:"team_id,omitempty"`
	ServerID                string            `json:"server_id"`
	GitSourceID             *string           `json:"git_source_id,omitempty"`
	RepoURL                 *string           `json:"repo_url,omitempty"`
	Branch                  string            `json:"branch"`
	GitCommitSHA            *string           `json:"git_commit_sha,omitempty"`
	BuildType               string            `json:"build_type"`
	DockerfilePath          string            `json:"dockerfile_path"`
	BuildContext            string            `json:"build_context"`
	DockerfileInline        *string           `json:"dockerfile_inline,omitempty"`
	ComposeFile             *string           `json:"compose_file,omitempty"`
	ComposeInline           *string           `json:"compose_inline,omitempty"`
	ImageName               *string           `json:"image_name,omitempty"`
	ImageTag                string            `json:"image_tag"`
	InstallCommand          *string           `json:"install_command,omitempty"`
	BuildCommand            *string           `json:"build_command,omitempty"`
	StartCommand            *string           `json:"start_command,omitempty"`
	PreDeployCommand        *string           `json:"pre_deploy_command,omitempty"`
	PostDeployCommand       *string           `json:"post_deploy_command,omitempty"`
	Port                    *int              `json:"port,omitempty"`
	HostPort                *int              `json:"host_port,omitempty"`
	HealthCheck             healthCheckReq    `json:"health_check"`
	LimitsMemory            *string           `json:"limits_memory,omitempty"`
	LimitsCPUs              *string           `json:"limits_cpus,omitempty"`
	LimitsMemorySwap        *string           `json:"limits_memory_swap,omitempty"`
	LimitsMemorySwappiness  *int              `json:"limits_memory_swappiness,omitempty"`
	LimitsMemoryReservation *string           `json:"limits_memory_reservation,omitempty"`
	LimitsCPUSet            *string           `json:"limits_cpuset,omitempty"`
	LimitsCPUShares         *int              `json:"limits_cpu_shares,omitempty"`
	RestartPolicy           string            `json:"restart_policy,omitempty"`
	AutoDeployBranch        *string           `json:"auto_deploy_branch,omitempty"`
	BuildArgsInject         *bool             `json:"build_args_inject,omitempty"`
	BuildArgsSourceCommit   *bool             `json:"build_args_source_commit,omitempty"`
	DockerLabels            map[string]string `json:"docker_labels,omitempty"`
	EnvVars                 map[string]string `json:"env_vars,omitempty"`
}

// Create handles POST /api/v1/apps.
func (h *AppHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req createAppReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	if !h.authorizeTeam(w, r, rbac.PermAppsCreate, req.TeamID) {
		return
	}
	out, err := h.svc.Create(r.Context(), app.CreateInput{
		Name:                    req.Name,
		Description:             req.Description,
		TeamID:                  req.TeamID,
		ServerID:                req.ServerID,
		GitSourceID:             req.GitSourceID,
		RepoURL:                 req.RepoURL,
		Branch:                  req.Branch,
		GitCommitSHA:            req.GitCommitSHA,
		BuildType:               req.BuildType,
		DockerfilePath:          req.DockerfilePath,
		BuildContext:            req.BuildContext,
		DockerfileInline:        req.DockerfileInline,
		ComposeFile:             req.ComposeFile,
		ComposeInline:           req.ComposeInline,
		ImageName:               req.ImageName,
		ImageTag:                req.ImageTag,
		InstallCommand:          req.InstallCommand,
		BuildCommand:            req.BuildCommand,
		StartCommand:            req.StartCommand,
		PreDeployCommand:        req.PreDeployCommand,
		PostDeployCommand:       req.PostDeployCommand,
		Port:                    req.Port,
		HostPort:                req.HostPort,
		HealthCheck:             req.HealthCheck.toOpts(),
		LimitsMemory:            req.LimitsMemory,
		LimitsCPUs:              req.LimitsCPUs,
		LimitsMemorySwap:        req.LimitsMemorySwap,
		LimitsMemorySwappiness:  req.LimitsMemorySwappiness,
		LimitsMemoryReservation: req.LimitsMemoryReservation,
		LimitsCPUSet:            req.LimitsCPUSet,
		LimitsCPUShares:         req.LimitsCPUShares,
		RestartPolicy:           req.RestartPolicy,
		AutoDeployBranch:        req.AutoDeployBranch,
		BuildArgsInject:         req.BuildArgsInject,
		BuildArgsSourceCommit:   req.BuildArgsSourceCommit,
		DockerLabels:            req.DockerLabels,
		EnvVars:                 req.EnvVars,
	})
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

// List handles GET /api/v1/apps?server_id=...
func (h *AppHandler) List(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.List(r.Context(), r.URL.Query().Get("server_id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db_error")
		return
	}
	if h.access != nil {
		userID, ok := apimiddleware.UserIDFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		filtered := make([]app.App, 0, len(out))
		for i := range out {
			allowed, err := h.access.CanAccessTeam(r.Context(), userID, out[i].TeamID, rbac.PermAppsView)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "db_error")
				return
			}
			if allowed {
				filtered = append(filtered, out[i])
			}
		}
		out = filtered
	}
	if out == nil {
		out = []app.App{}
	}
	writeJSON(w, http.StatusOK, out)
}

// Get handles GET /api/v1/apps/{idOrName}.
func (h *AppHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	out, err := h.svc.Get(r.Context(), id)
	if err != nil {
		writeAppError(w, err)
		return
	}
	if !h.authorizeTeam(w, r, rbac.PermAppsView, out.TeamID) {
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type patchAppReq struct {
	Description             *string           `json:"description,omitempty"`
	TeamID                  *string           `json:"team_id,omitempty"`
	GitSourceID             *string           `json:"git_source_id,omitempty"`
	RepoURL                 *string           `json:"repo_url,omitempty"`
	Branch                  *string           `json:"branch,omitempty"`
	GitCommitSHA            *string           `json:"git_commit_sha,omitempty"`
	DockerfilePath          *string           `json:"dockerfile_path,omitempty"`
	BuildContext            *string           `json:"build_context,omitempty"`
	DockerfileInline        *string           `json:"dockerfile_inline,omitempty"`
	ComposeFile             *string           `json:"compose_file,omitempty"`
	ComposeInline           *string           `json:"compose_inline,omitempty"`
	ImageName               *string           `json:"image_name,omitempty"`
	ImageTag                *string           `json:"image_tag,omitempty"`
	InstallCommand          *string           `json:"install_command,omitempty"`
	BuildCommand            *string           `json:"build_command,omitempty"`
	StartCommand            *string           `json:"start_command,omitempty"`
	PreDeployCommand        *string           `json:"pre_deploy_command,omitempty"`
	PostDeployCommand       *string           `json:"post_deploy_command,omitempty"`
	Port                    *int              `json:"port,omitempty"`
	HostPort                *int              `json:"host_port,omitempty"`
	HealthCheck             *healthCheckReq   `json:"health_check,omitempty"`
	LimitsMemory            *string           `json:"limits_memory,omitempty"`
	LimitsCPUs              *string           `json:"limits_cpus,omitempty"`
	LimitsMemorySwap        *string           `json:"limits_memory_swap,omitempty"`
	LimitsMemorySwappiness  *int              `json:"limits_memory_swappiness,omitempty"`
	LimitsMemoryReservation *string           `json:"limits_memory_reservation,omitempty"`
	LimitsCPUSet            *string           `json:"limits_cpuset,omitempty"`
	LimitsCPUShares         *int              `json:"limits_cpu_shares,omitempty"`
	RestartPolicy           *string           `json:"restart_policy,omitempty"`
	AutoDeployBranch        *string           `json:"auto_deploy_branch,omitempty"`
	BuildArgsInject         *bool             `json:"build_args_inject,omitempty"`
	BuildArgsSourceCommit   *bool             `json:"build_args_source_commit,omitempty"`
	DockerLabels            map[string]string `json:"docker_labels,omitempty"`
	EnvVars                 map[string]string `json:"env_vars,omitempty"`
}

// Patch handles PATCH /api/v1/apps/{id}.
func (h *AppHandler) Patch(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req patchAppReq
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(body, &raw)
	current, err := h.svc.Get(r.Context(), id)
	if err != nil {
		writeAppError(w, err)
		return
	}
	if !h.authorizeTeam(w, r, rbac.PermAppsUpdate, current.TeamID) {
		return
	}
	if req.TeamID != nil && !sameOptionalString(current.TeamID, req.TeamID) {
		if !h.authorizeTeam(w, r, rbac.PermAppsUpdate, req.TeamID) {
			return
		}
	}
	patch := app.UpdatePatch{
		Description:             req.Description,
		TeamID:                  req.TeamID,
		GitSourceID:             req.GitSourceID,
		RepoURL:                 req.RepoURL,
		Branch:                  req.Branch,
		GitCommitSHA:            req.GitCommitSHA,
		DockerfilePath:          req.DockerfilePath,
		BuildContext:            req.BuildContext,
		DockerfileInline:        req.DockerfileInline,
		ComposeFile:              req.ComposeFile,
		ComposeInline:           req.ComposeInline,
		ImageName:               req.ImageName,
		ImageTag:                req.ImageTag,
		InstallCommand:          req.InstallCommand,
		BuildCommand:            req.BuildCommand,
		StartCommand:            req.StartCommand,
		PreDeployCommand:        req.PreDeployCommand,
		PostDeployCommand:       req.PostDeployCommand,
		Port:                    req.Port,
		HostPort:                req.HostPort,
		LimitsMemory:            req.LimitsMemory,
		LimitsCPUs:              req.LimitsCPUs,
		LimitsMemorySwap:        req.LimitsMemorySwap,
		LimitsMemorySwappiness:  req.LimitsMemorySwappiness,
		LimitsMemoryReservation: req.LimitsMemoryReservation,
		LimitsCPUSet:            req.LimitsCPUSet,
		LimitsCPUShares:         req.LimitsCPUShares,
		RestartPolicy:           req.RestartPolicy,
		AutoDeployBranch:        req.AutoDeployBranch,
		BuildArgsInject:         req.BuildArgsInject,
		BuildArgsSourceCommit:   req.BuildArgsSourceCommit,
		DockerLabels:            req.DockerLabels,
		EnvVars:                 req.EnvVars,
	}
	if req.HealthCheck != nil {
		opts := req.HealthCheck.toOpts()
		patch.HealthCheck = &opts
	}
	if rawTeamID, ok := raw["team_id"]; ok && strings.TrimSpace(string(rawTeamID)) == "null" {
		patch.ClearTeamID = true
	}
	// Treat explicit nulls in the JSON body as "clear" for nullable
	// integer-typed fields. The PATCH semantics elsewhere in the
	// handler do the same trick for team_id; staying consistent
	// avoids surprising the operator.
	if v, ok := raw["limits_memory_swappiness"]; ok && strings.TrimSpace(string(v)) == "null" {
		patch.ClearMemorySwappiness = true
	}
	if v, ok := raw["limits_cpu_shares"]; ok && strings.TrimSpace(string(v)) == "null" {
		patch.ClearCPUShares = true
	}
	if v, ok := raw["auto_deploy_branch"]; ok && strings.TrimSpace(string(v)) == "null" {
		patch.ClearAutoDeployBranch = true
	}
	out, err := h.svc.Update(r.Context(), id, patch)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// Delete handles DELETE /api/v1/apps/{id}.
//
// Flow:
//  1. Resolve the app + authorise.
//  2. Gather cleanup metadata (domain hostnames, deployment IDs) —
//     must happen BEFORE the DB cascade wipes the source tables.
//  3. DB delete (ON DELETE CASCADE clears domains/secrets/volumes/
//     deployments/tags/app_volumes). Returns synchronously so the
//     SPA sees the deletion immediately.
//  4. Submit the cleanup task to the async worker — containers,
//     images, named volumes, Caddy routes and build-log files all
//     get wiped in the background. Returning 204 doesn't wait on
//     this; failures are logged in the worker, not surfaced to
//     the operator.
func (h *AppHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	target, err := h.svc.Get(r.Context(), id)
	if err != nil {
		writeAppError(w, err)
		return
	}
	if !h.authorizeTeam(w, r, rbac.PermAppsDelete, target.TeamID) {
		return
	}

	// Snapshot cleanup targets before the cascade fires.
	targets, _ := h.svc.GatherCleanupTargets(r.Context(), target.ID)

	if err := h.svc.Delete(r.Context(), id); err != nil {
		writeAppError(w, err)
		return
	}

	// Fire-and-forget cleanup. The DB deletion already succeeded;
	// queue-full or worker-unavailable is logged but doesn't
	// rollback the delete.
	if h.cleanup != nil {
		serverID := ""
		if target.ServerID != nil {
			serverID = *target.ServerID
		}
		_ = h.cleanup.Submit(CleanupTask{
			AppID:         target.ID,
			AppName:       target.Name,
			ServerID:      serverID,
			Domains:       targets.Domains,
			DeploymentIDs: targets.DeploymentIDs,
		})
	}

	w.WriteHeader(http.StatusNoContent)
}

// Stop handles POST /api/v1/apps/{id}/stop.
func (h *AppHandler) Stop(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	target, err := h.svc.Get(r.Context(), id)
	if err != nil {
		writeAppError(w, err)
		return
	}
	if !h.authorizeTeam(w, r, rbac.PermAppsUpdate, target.TeamID) {
		return
	}
	if err := h.svc.Stop(r.Context(), id); err != nil {
		writeAppError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// SetEnvVars handles PUT /api/v1/apps/{id}/env-vars — replaces the full document.
func (h *AppHandler) SetEnvVars(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	// Resolve to canonical id (supports lookup by name too).
	target, err := h.svc.Get(r.Context(), id)
	if err != nil {
		writeAppError(w, err)
		return
	}
	if !h.authorizeTeam(w, r, rbac.PermAppsUpdate, target.TeamID) {
		return
	}
	var env map[string]string
	if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	out, err := h.svc.SetEnvVars(r.Context(), target.ID, env)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// Stats handles GET /api/v1/apps/{id}/stats.
//
// Returns a single-shot snapshot of CPU% and memory usage for the
// app's container. The Docker API only exposes counters (not rates),
// so to compute CPU% we ask for a streamed stats response and read
// two samples ~1s apart, then derive the delta. Total handler
// latency is therefore ~1s — acceptable for a manually-triggered or
// polled overview card, not appropriate for a tight loop.
//
// 404 when the app has no container yet (never deployed, or stopped
// and pruned). 503 when the server provider isn't reachable. Both
// states the UI translates to "stats unavailable" — operator action
// is "deploy first" or "wait for the server to come back".
func (h *AppHandler) Stats(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	target, err := h.svc.Get(r.Context(), id)
	if err != nil {
		writeAppError(w, err)
		return
	}
	if !h.authorizeTeam(w, r, rbac.PermAppsView, target.TeamID) {
		return
	}
	if target.ContainerName == nil || strings.TrimSpace(*target.ContainerName) == "" {
		writeError(w, http.StatusNotFound, "no_container")
		return
	}
	if h.servers == nil {
		writeError(w, http.StatusServiceUnavailable, "stats_unavailable")
		return
	}
	if target.ServerID == nil || strings.TrimSpace(*target.ServerID) == "" {
		writeError(w, http.StatusServiceUnavailable, "no_server")
		return
	}

	// Resolve the Docker provider for the app's host. RemoteProvider
	// holds an SSH tunnel — we close it as soon as we're done so we
	// don't leak FDs per polled request.
	provider, err := h.servers.Provider(r.Context(), *target.ServerID)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "provider_unavailable")
		return
	}
	defer func() { _ = provider.Close() }()

	cli, err := provider.Client(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "docker_unreachable")
		return
	}

	// Hard cap on the handler latency so a stuck Docker daemon
	// doesn't tie up the request worker. Two samples need ~1.2s at
	// best; 5s is a generous safety net.
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	snap, err := readContainerStats(ctx, cli, *target.ContainerName)
	if err != nil {
		writeError(w, http.StatusBadGateway, "docker_stats_failed")
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

// containerStatsResp is the wire shape the API returns. Tiny on
// purpose — the UI only needs current CPU %, used/limit bytes, and
// the moment we sampled at.
type containerStatsResp struct {
	CPUPercent      float64 `json:"cpu_percent"`
	MemoryUsedBytes uint64  `json:"memory_used_bytes"`
	MemoryLimitBytes uint64 `json:"memory_limit_bytes"`
	SampledAt       string  `json:"sampled_at"`
}

// readContainerStats opens a streamed stats reader and consumes the
// first two samples to compute CPU%. The Docker daemon emits a
// sample every ~1s by default; we close as soon as we have what we
// need so we never wait for a third tick.
func readContainerStats(ctx context.Context, cli dockerStatsClient, name string) (*containerStatsResp, error) {
	stream, err := cli.ContainerStats(ctx, name, true)
	if err != nil {
		return nil, fmt.Errorf("container stats: %w", err)
	}
	defer func() { _ = stream.Body.Close() }()

	dec := json.NewDecoder(stream.Body)
	var prev, curr dockertypes.StatsResponse
	if err := dec.Decode(&prev); err != nil {
		return nil, fmt.Errorf("decode first sample: %w", err)
	}
	if err := dec.Decode(&curr); err != nil {
		// Single-sample mode: some old daemons / non-Linux hosts only
		// emit one frame. We still produce a useful memory reading,
		// just zero CPU% (better than failing the whole request).
		return memoryOnlySnapshot(&prev), nil
	}

	resp := &containerStatsResp{
		CPUPercent:       computeCPUPercent(&prev, &curr),
		MemoryUsedBytes:  memoryUsage(&curr),
		MemoryLimitBytes: curr.MemoryStats.Limit,
		SampledAt:        time.Now().UTC().Format(time.RFC3339),
	}
	return resp, nil
}

// computeCPUPercent mirrors Docker CLI's own formula — see
// https://github.com/docker/cli/blob/master/cli/command/container/stats_helpers.go.
// `prev` and `curr` are consecutive samples from the stats stream.
func computeCPUPercent(prev, curr *dockertypes.StatsResponse) float64 {
	cpuDelta := float64(curr.CPUStats.CPUUsage.TotalUsage) - float64(prev.CPUStats.CPUUsage.TotalUsage)
	sysDelta := float64(curr.CPUStats.SystemUsage) - float64(prev.CPUStats.SystemUsage)
	if cpuDelta <= 0 || sysDelta <= 0 {
		return 0
	}
	cpus := float64(curr.CPUStats.OnlineCPUs)
	if cpus == 0 {
		cpus = float64(len(curr.CPUStats.CPUUsage.PercpuUsage))
	}
	if cpus == 0 {
		cpus = 1
	}
	return (cpuDelta / sysDelta) * cpus * 100.0
}

// memoryUsage returns memory used excluding cache, matching `docker
// stats`. On cgroups v2 the cache field moves to `inactive_file`.
func memoryUsage(s *dockertypes.StatsResponse) uint64 {
	used := s.MemoryStats.Usage
	if cache, ok := s.MemoryStats.Stats["cache"]; ok && cache <= used {
		return used - cache
	}
	if inactive, ok := s.MemoryStats.Stats["inactive_file"]; ok && inactive <= used {
		return used - inactive
	}
	return used
}

func memoryOnlySnapshot(s *dockertypes.StatsResponse) *containerStatsResp {
	return &containerStatsResp{
		CPUPercent:       0,
		MemoryUsedBytes:  memoryUsage(s),
		MemoryLimitBytes: s.MemoryStats.Limit,
		SampledAt:        time.Now().UTC().Format(time.RFC3339),
	}
}

// Containers handles GET /api/v1/apps/{id}/containers.
//
// Returns a unified view: every container actually running on the
// host (via Docker label `prexel.app_id=<id>`) PLUS, for Compose
// apps, every service the YAML declares. Services that have a live
// container are merged into one row; services with only YAML
// (pre-deploy or declared-but-not-started) appear as preview rows.
//
// The combined list is what the AppDetail "Containers" tab renders.
// Operators can then assign domains to specific services without
// needing to deploy first.
func (h *AppHandler) Containers(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	target, err := h.svc.Get(r.Context(), id)
	if err != nil {
		writeAppError(w, err)
		return
	}
	if !h.authorizeTeam(w, r, rbac.PermAppsView, target.TeamID) {
		return
	}

	// Start with the preview from the YAML (when Compose). Same shape
	// as live so they merge cleanly into one slice. composePreview
	// covers the inline and single-container cases synchronously;
	// repo-hosted Compose needs an out-of-band fetch (gitsrc), which
	// we layer on here so the handler stays free of git/gitsrc imports.
	preview := composePreview(target)
	if len(preview) == 0 && target.BuildType == "docker_compose" && h.composeFetcher != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		yaml, err := h.composeFetcher(ctx, target)
		cancel()
		if err == nil && len(yaml) > 0 {
			preview = composePreviewFromYAML(yaml)
		}
	}

	// Live containers — only attempt when we know which server to
	// hit and have one wired. Missing server is non-fatal; preview
	// alone is still useful (pre-deploy operators).
	live := []containerRow{}
	if h.servers != nil && target.ServerID != nil && strings.TrimSpace(*target.ServerID) != "" {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if provider, perr := h.servers.Provider(ctx, *target.ServerID); perr == nil {
			defer func() { _ = provider.Close() }()
			if cli, cerr := provider.Client(ctx); cerr == nil {
				live, _ = listLiveContainers(ctx, cli, target.ID)
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"containers": mergeContainers(preview, live),
	})
}

// containerRow is the unified shape the UI consumes. Optional
// fields stay nil when only one side (preview/live) contributed.
type containerRow struct {
	Service        string `json:"service"`         // service name (Compose) OR app name (single-container)
	Image          string `json:"image,omitempty"`
	State          string `json:"state,omitempty"` // "running" / "exited" / ... / "preview" when YAML-only
	ContainerName  string `json:"container_name,omitempty"`
	Ports          []int  `json:"ports"`
	HasHealthcheck bool   `json:"has_healthcheck"`
}

// composePreview extracts services from the app's Compose YAML. For
// single-container apps (Dockerfile / Image / Inline) we synthesize a
// one-row preview so the UI surface is uniform — service name = app
// name, port = app.port.
//
// Repo-hosted Compose note: this function is synchronous and stays
// free of git/HTTP imports on purpose. When the YAML lives in a git
// repo, the caller is responsible for fetching it out-of-band and
// using composePreviewFromYAML below. The Containers() endpoint does
// exactly that via AppHandler.composeFetcher (wired in router.go,
// today only github_app sources are supported because a full clone is
// too costly for a UI render path; non-github_app repos fall back to
// the empty preview and only render the live containers).
func composePreview(a *app.App) []containerRow {
	if a.BuildType == "docker_compose" {
		if a.ComposeInline != nil && strings.TrimSpace(*a.ComposeInline) != "" {
			return composePreviewFromYAML([]byte(*a.ComposeInline))
		}
		// Repo-hosted compose with no inline fallback: caller must
		// invoke composeFetcher and pass the bytes to
		// composePreviewFromYAML. Returning nil here is the documented
		// "no preview available synchronously" signal.
		return nil
	}
	// Single-container app: synthesize a one-row preview. The "service"
	// name aligns with how the deploy engine names the container
	// (prexel-<app>) so the merge by service-name finds the live row
	// when there is one.
	ports := []int{}
	if a.Port != nil && *a.Port > 0 {
		ports = append(ports, *a.Port)
	}
	return []containerRow{{
		Service: a.Name,
		Image:   imageOf(a),
		State:   "preview",
		Ports:   ports,
	}}
}

// composePreviewFromYAML parses arbitrary docker-compose YAML bytes into
// the same containerRow slice composePreview() produces. Used by the
// repo-hosted Compose path where the YAML is fetched out-of-band (e.g.
// via the GitHub Contents API) instead of read from app.ComposeInline.
// Returns nil on parse error or empty service set so callers can fall
// back to "no preview available".
func composePreviewFromYAML(yaml []byte) []containerRow {
	spec, err := composespec.Parse(yaml)
	if err != nil || len(spec.Services) == 0 {
		return nil
	}
	out := make([]containerRow, 0, len(spec.Services))
	for _, s := range spec.Services {
		out = append(out, containerRow{
			Service:        s.Name,
			Image:          s.Image,
			State:          "preview",
			Ports:          s.Ports,
			HasHealthcheck: s.HasHealthcheck,
		})
	}
	return out
}

func imageOf(a *app.App) string {
	switch a.BuildType {
	case "docker_image":
		name := ""
		if a.ImageName != nil {
			name = *a.ImageName
		}
		tag := a.ImageTag
		if tag == "" {
			tag = "latest"
		}
		return name + ":" + tag
	default:
		return "" // built locally; image tag is generated per deploy
	}
}

// ContainerDetail handles GET /api/v1/apps/{id}/containers/{name}.
//
// Returns the rich per-container projection (image, state, env, mounts,
// labels, etc.) used by the dedicated container-detail page. Falls
// back to a `preview` row when the operator is looking at a Compose
// service that doesn't have a live container yet — same UX rule as
// the list endpoint (operator can browse the YAML's services without
// having to deploy first).
//
// Access control: app's team gates this — `containerBelongsToApp`
// double-checks the container's prexel.app_id label so an operator
// who knows another team's container name can't fish.
func (h *AppHandler) ContainerDetail(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	containerName := strings.TrimSpace(chi.URLParam(r, "name"))
	if containerName == "" {
		writeError(w, http.StatusBadRequest, "missing_container_name")
		return
	}
	target, err := h.svc.Get(r.Context(), id)
	if err != nil {
		writeAppError(w, err)
		return
	}
	if !h.authorizeTeam(w, r, rbac.PermAppsView, target.TeamID) {
		return
	}
	if h.servers == nil || target.ServerID == nil || strings.TrimSpace(*target.ServerID) == "" {
		writeError(w, http.StatusServiceUnavailable, "no_server")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	provider, err := h.servers.Provider(ctx, *target.ServerID)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "provider_unavailable")
		return
	}
	defer func() { _ = provider.Close() }()
	cli, err := provider.Client(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway, "docker_unreachable")
		return
	}

	insp, err := cli.ContainerInspect(ctx, containerName)
	if err != nil {
		// Container missing — surface as preview if the operator is
		// looking at a Compose service that hasn't deployed yet.
		// Otherwise 404 so the SPA can render its own empty state.
		previewRow := containerDetailPreview(target, containerName)
		if previewRow == nil && target.BuildType == "docker_compose" && h.composeFetcher != nil {
			// Repo-hosted Compose: fetch YAML out-of-band and retry.
			// Best-effort with a tight budget — we don't want one slow
			// GitHub call to hold up a 404.
			fctx, fcancel := context.WithTimeout(r.Context(), 10*time.Second)
			yaml, ferr := h.composeFetcher(fctx, target)
			fcancel()
			if ferr == nil && len(yaml) > 0 {
				previewRow = containerDetailPreviewFromYAML(target, containerName, yaml)
			}
		}
		if previewRow != nil {
			writeJSON(w, http.StatusOK, previewRow)
			return
		}
		writeError(w, http.StatusNotFound, "container_not_found")
		return
	}

	// Defence in depth: the container MUST belong to this app. A
	// shared Docker host with another Prexel instance could otherwise
	// leak adjacent containers through this endpoint just by guessing
	// the name.
	if insp.Config == nil || insp.Config.Labels["prexel.app_id"] != target.ID {
		writeError(w, http.StatusNotFound, "container_not_found")
		return
	}

	writeJSON(w, http.StatusOK, containerDetailFromInspect(insp, target))
}

// ContainerStats handles GET /api/v1/apps/{id}/containers/{name}/stats.
//
// Same single-shot ~1s sampling as /apps/{id}/stats, but for a
// specific container name (useful for Compose apps where the
// app-level stats endpoint only covers the canonical container).
// Read-only — the same scope check + label verification keeps this
// from leaking adjacent containers.
func (h *AppHandler) ContainerStats(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	containerName := strings.TrimSpace(chi.URLParam(r, "name"))
	if containerName == "" {
		writeError(w, http.StatusBadRequest, "missing_container_name")
		return
	}
	target, err := h.svc.Get(r.Context(), id)
	if err != nil {
		writeAppError(w, err)
		return
	}
	if !h.authorizeTeam(w, r, rbac.PermAppsView, target.TeamID) {
		return
	}
	if h.servers == nil || target.ServerID == nil || strings.TrimSpace(*target.ServerID) == "" {
		writeError(w, http.StatusServiceUnavailable, "no_server")
		return
	}

	provider, err := h.servers.Provider(r.Context(), *target.ServerID)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "provider_unavailable")
		return
	}
	defer func() { _ = provider.Close() }()
	cli, err := provider.Client(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "docker_unreachable")
		return
	}

	// Label check before the stats RPC — same rationale as
	// ContainerDetail. Cheap; the inspect is local on the daemon.
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	insp, ierr := cli.ContainerInspect(ctx, containerName)
	if ierr != nil {
		writeError(w, http.StatusNotFound, "container_not_found")
		return
	}
	if insp.Config == nil || insp.Config.Labels["prexel.app_id"] != target.ID {
		writeError(w, http.StatusNotFound, "container_not_found")
		return
	}
	if !strings.EqualFold(insp.State.Status, "running") {
		// Stats only make sense for running containers — exited ones
		// return all-zero counters that the UI would mis-render as
		// "100% CPU" depending on the math.
		writeError(w, http.StatusConflict, "container_not_running")
		return
	}

	snap, err := readContainerStats(ctx, cli, containerName)
	if err != nil {
		writeError(w, http.StatusBadGateway, "docker_stats_failed")
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

// ContainerDetailDTO is the wire shape returned by ContainerDetail.
// Trimmed from the raw `ContainerJSON` because most of what docker
// returns is internal plumbing the UI never reads (lots of arrays of
// nulls, capabilities, security opts, etc.). We expose only the
// fields the dedicated container view actually renders, keeping the
// JSON payload small and the contract stable.
type ContainerDetailDTO struct {
	Service       string             `json:"service,omitempty"`
	ContainerName string             `json:"container_name"`
	Image         string             `json:"image"`
	State         string             `json:"state"`
	Status        string             `json:"status"`
	ExitCode      int                `json:"exit_code"`
	Ports         []int              `json:"ports"`
	Env           map[string]string  `json:"env"`
	Mounts        []containerMount   `json:"mounts"`
	Labels        map[string]string  `json:"labels"`
	Cmd           []string           `json:"cmd"`
	Entrypoint    []string           `json:"entrypoint"`
	Networks      []string           `json:"networks"`
	IPAddress     string             `json:"ip_address,omitempty"`
	RestartPolicy string             `json:"restart_policy"`
	CreatedAt     int64              `json:"created_at"`
	StartedAt     int64              `json:"started_at"`
	FinishedAt    int64              `json:"finished_at"`
	// Preview is true when the row is synthesised from the Compose
	// YAML because no live container exists yet — the UI hides
	// runtime-only sections (logs, terminal, stats) in this case.
	Preview bool `json:"preview"`
}

type containerMount struct {
	Type        string `json:"type"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	ReadOnly    bool   `json:"read_only"`
}

// containerDetailFromInspect projects a docker ContainerJSON into the
// trimmed UI DTO. Most fields map 1:1; the env slice gets folded into
// a map and timestamps are normalised to unix seconds.
//
// IMPORTANT: every slice field is pre-allocated as an empty slice (not
// nil) so the JSON encoder emits `[]` instead of `null`. The Vue side
// reads things like `detail.mounts.length` directly in v-show panels;
// a JSON `null` would crash the render loop on containers that happen
// to have zero mounts / zero exposed ports / no entrypoint override.
func containerDetailFromInspect(insp types.ContainerJSON, a *app.App) *ContainerDetailDTO {
	cmd := insp.Config.Cmd
	if cmd == nil {
		cmd = []string{}
	}
	entrypoint := insp.Config.Entrypoint
	if entrypoint == nil {
		entrypoint = []string{}
	}
	labels := insp.Config.Labels
	if labels == nil {
		labels = map[string]string{}
	}
	out := &ContainerDetailDTO{
		ContainerName: strings.TrimPrefix(insp.Name, "/"),
		Image:         insp.Config.Image,
		State:         insp.State.Status,
		Ports:         []int{},
		Env:           map[string]string{},
		Mounts:        []containerMount{},
		Labels:        labels,
		Cmd:           cmd,
		Entrypoint:    entrypoint,
		Networks:      []string{},
	}
	if insp.State.ExitCode != 0 {
		out.ExitCode = insp.State.ExitCode
	}
	// Human-readable "Up 2 minutes" / "Exited (1) 5s ago" — Docker
	// stores it but ContainerInspect doesn't include it; we
	// reconstruct a minimal version from the timestamps below.
	out.Status = humanContainerStatus(insp.State)

	// Service label is the closest thing we have to a stable
	// projection back to the operator's compose YAML. Fall back to
	// the prexel.app_name (single-container apps) so the UI always
	// has SOMETHING to show in the breadcrumb.
	if v := insp.Config.Labels["prexel.service"]; v != "" {
		out.Service = v
	} else {
		out.Service = insp.Config.Labels["prexel.app_name"]
	}

	for _, e := range insp.Config.Env {
		k, v, ok := strings.Cut(e, "=")
		if !ok {
			out.Env[strings.TrimSpace(k)] = ""
			continue
		}
		out.Env[strings.TrimSpace(k)] = v
	}

	for port := range insp.Config.ExposedPorts {
		// nat.Port format is "<num>/<proto>"; we only care about the
		// numeric portion.
		raw := port.Port()
		if raw == "" {
			continue
		}
		var n int
		_, _ = fmt.Sscanf(raw, "%d", &n)
		if n > 0 {
			out.Ports = append(out.Ports, n)
		}
	}
	sort.Ints(out.Ports)

	for _, m := range insp.Mounts {
		out.Mounts = append(out.Mounts, containerMount{
			Type:        string(m.Type),
			Source:      m.Source,
			Destination: m.Destination,
			ReadOnly:    !m.RW,
		})
	}

	if insp.HostConfig != nil {
		out.RestartPolicy = string(insp.HostConfig.RestartPolicy.Name)
	}
	if insp.NetworkSettings != nil {
		for name, n := range insp.NetworkSettings.Networks {
			out.Networks = append(out.Networks, name)
			if out.IPAddress == "" && n.IPAddress != "" {
				out.IPAddress = n.IPAddress
			}
		}
		sort.Strings(out.Networks)
	}
	out.CreatedAt = parseDockerTime(insp.Created)
	out.StartedAt = parseDockerTime(insp.State.StartedAt)
	out.FinishedAt = parseDockerTime(insp.State.FinishedAt)
	_ = a // reserved for future per-app projections (e.g. surface app.RestartPolicy here too)
	return out
}

// humanContainerStatus mirrors `docker ps`'s "Up 2m / Exited (1) 5s
// ago" string from the State block. We don't have the daemon's
// formatter so we reconstruct a tiny version — good enough for the
// UI's status line.
func humanContainerStatus(s *types.ContainerState) string {
	if s == nil {
		return ""
	}
	switch s.Status {
	case "running":
		if t := parseDockerTime(s.StartedAt); t > 0 {
			d := time.Since(time.Unix(t, 0))
			return "Up " + shortDuration(d)
		}
		return "Running"
	case "exited":
		if t := parseDockerTime(s.FinishedAt); t > 0 {
			d := time.Since(time.Unix(t, 0))
			return fmt.Sprintf("Exited (%d) %s ago", s.ExitCode, shortDuration(d))
		}
		return fmt.Sprintf("Exited (%d)", s.ExitCode)
	case "restarting":
		return "Restarting"
	case "paused":
		return "Paused"
	case "created":
		return "Created"
	default:
		return strings.Title(s.Status) //nolint:staticcheck // intentional title-case for UI label
	}
}

// shortDuration produces "5s" / "12m" / "3h" / "2d" — the same
// vocabulary the dashboard uses for last-deploy timestamps.
func shortDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours())/24)
}

// parseDockerTime converts Docker's RFC3339Nano string into unix
// seconds. Empty/zero values come back as 0 so the UI can branch on
// "never started" without having to parse "0001-01-01T00:00:00Z".
func parseDockerTime(s string) int64 {
	if s == "" || strings.HasPrefix(s, "0001-") {
		return 0
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return 0
	}
	return t.Unix()
}

// containerDetailPreview returns a preview DTO when the operator is
// looking at a Compose service that hasn't been deployed yet. Returns
// nil when there's no matching preview (then the caller responds 404).
func containerDetailPreview(a *app.App, name string) *ContainerDetailDTO {
	return matchContainerDetailPreview(a, name, composePreview(a))
}

// containerDetailPreviewFromYAML mirrors containerDetailPreview but
// matches against a service set parsed from arbitrary Compose YAML
// bytes (i.e. fetched out-of-band for repo-hosted Compose apps).
func containerDetailPreviewFromYAML(a *app.App, name string, yaml []byte) *ContainerDetailDTO {
	return matchContainerDetailPreview(a, name, composePreviewFromYAML(yaml))
}

func matchContainerDetailPreview(a *app.App, name string, previews []containerRow) *ContainerDetailDTO {
	for _, p := range previews {
		// Match by service name OR by the predicted container name.
		match := p.Service == name
		if !match {
			expected := "prexel-" + a.Name
			if p.Service != "" && p.Service != a.Name {
				expected = expected + "-" + p.Service
			}
			match = expected == name
		}
		if !match {
			continue
		}
		// Same slice/map nil-safety as containerDetailFromInspect —
		// the Vue side relies on every collection field being a
		// non-null value to render its v-show panels safely.
		ports := p.Ports
		if ports == nil {
			ports = []int{}
		}
		return &ContainerDetailDTO{
			Service:       p.Service,
			ContainerName: name,
			Image:         p.Image,
			State:         "preview",
			Status:        "Pendente — sem container ativo",
			Ports:         ports,
			Env:           map[string]string{},
			Mounts:        []containerMount{},
			Labels:        map[string]string{},
			Cmd:           []string{},
			Entrypoint:    []string{},
			Networks:      []string{},
			Preview:       true,
		}
	}
	return nil
}

// listLiveContainers asks the Docker daemon for every container
// tagged with prexel.app_id=<id>. We don't filter by state — the UI
// renders "exited" / "restarting" / etc. usefully too.
func listLiveContainers(ctx context.Context, cli dockerContainerLister, appID string) ([]containerRow, error) {
	f := dockerfilters.NewArgs()
	f.Add("label", "prexel.app_id="+appID)
	list, err := cli.ContainerList(ctx, dockertypes.ListOptions{All: true, Filters: f})
	if err != nil {
		return nil, fmt.Errorf("container list: %w", err)
	}
	out := make([]containerRow, 0, len(list))
	for _, c := range list {
		// The deploy engine writes `prexel.service` for Compose
		// services; falls back to app_name for single-container apps.
		service := c.Labels["prexel.service"]
		if service == "" {
			service = c.Labels["prexel.app_name"]
		}
		ports := make([]int, 0, len(c.Ports))
		for _, p := range c.Ports {
			if p.PrivatePort > 0 {
				ports = append(ports, int(p.PrivatePort))
			}
		}
		name := ""
		if len(c.Names) > 0 {
			// Docker prefixes container names with "/" — strip it.
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		out = append(out, containerRow{
			Service:       service,
			Image:         c.Image,
			State:         c.State,
			ContainerName: name,
			Ports:         ports,
		})
	}
	return out, nil
}

// mergeContainers folds preview + live by service name. Live wins
// for every field except Ports — preview ports are the declared
// ones (more complete for services exposing many internal ports
// that aren't currently bound). Result is sorted by service name
// for stable rendering.
func mergeContainers(preview, live []containerRow) []containerRow {
	by := make(map[string]containerRow, len(preview)+len(live))
	for _, p := range preview {
		by[p.Service] = p
	}
	for _, l := range live {
		if l.Service == "" {
			by["_"+l.ContainerName] = l // orphan (no service label)
			continue
		}
		merged, ok := by[l.Service]
		if !ok {
			by[l.Service] = l
			continue
		}
		// Live state replaces preview state. Preserve preview's port
		// list when richer than live's (running container may only
		// expose a subset of the declared ports).
		merged.State = l.State
		merged.ContainerName = l.ContainerName
		if merged.Image == "" {
			merged.Image = l.Image
		}
		if len(merged.Ports) == 0 {
			merged.Ports = l.Ports
		}
		by[l.Service] = merged
	}
	out := make([]containerRow, 0, len(by))
	for _, v := range by {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Service < out[j].Service })
	return out
}

// dockerContainerLister is the bit of *client.Client we need for the
// container-list path. Same testability rationale as dockerStatsClient.
type dockerContainerLister interface {
	ContainerList(ctx context.Context, options dockertypes.ListOptions) ([]types.Container, error)
}

// dockerStatsClient is the minimum surface readContainerStats needs.
// Lets unit tests pass a fake without spinning up Docker. *client.Client
// (production) satisfies this — its ContainerStats has the same shape.
type dockerStatsClient interface {
	ContainerStats(ctx context.Context, container string, stream bool) (dockertypes.StatsResponseReader, error)
}

func (h *AppHandler) authorizeTeam(w http.ResponseWriter, r *http.Request, permission string, teamID *string) bool {
	// h.access is guaranteed non-nil by NewAppHandler (panics on construction
	// otherwise). No anonymous fallback is permitted here.
	userID, ok := apimiddleware.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return false
	}
	allowed, err := h.access.CanAccessTeam(r.Context(), userID, teamID, permission)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db_error")
		return false
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "forbidden")
		return false
	}
	return true
}

func sameOptionalString(a, b *string) bool {
	if a == nil || strings.TrimSpace(*a) == "" {
		return b == nil || strings.TrimSpace(*b) == ""
	}
	return b != nil && strings.TrimSpace(*a) == strings.TrimSpace(*b)
}

func writeAppError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, app.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, app.ErrNameInUse):
		writeJSON(w, http.StatusConflict, map[string]string{
			"error":   "name_in_use",
			"message": "Já existe um app com esse nome.",
		})
	default:
		msg := err.Error()
		switch {
		case strings.HasPrefix(msg, "invalid_") ||
			strings.HasPrefix(msg, "missing_") ||
			strings.HasPrefix(msg, "server_not_connected") ||
			strings.HasPrefix(msg, "empty_value") ||
			strings.HasPrefix(msg, "git_sources_disabled"):
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error":   "bad_request",
				"message": msg,
			})
		default:
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"error":   "internal_error",
				"message": msg,
			})
		}
	}
}
