package handler

import (
	"net/http"
	"strconv"
	"strings"

	dockertypes "github.com/docker/docker/api/types/container"
	dockerfilters "github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/go-chi/chi/v5"
	"github.com/prexel/prexel/internal/api/apimiddleware"
	"github.com/prexel/prexel/internal/app"
	"github.com/prexel/prexel/internal/dockersvc"
	"github.com/prexel/prexel/internal/rbac"
	"github.com/prexel/prexel/internal/server"
)

// ContainerLogsHandler exposes GET /api/v1/apps/{id}/containers/{name}/logs
// as an SSE stream. Unlike EventHandler.Logs (single-container assumption
// via app.ContainerName), this handler accepts a container name explicitly
// so Compose apps with N services can be streamed individually.
//
// It enforces RBAC (apps.view per-team) and verifies — against the Docker
// daemon — that the named container actually carries the
// prexel.app_id=<app.ID> label. Without that check, any authenticated user
// with apps.view on ANY team could tail a container belonging to another
// team just by guessing its name.
type ContainerLogsHandler struct {
	apps    *app.Service
	servers *server.Service
	access  *rbac.Service
}

// NewContainerLogsHandler wires the handler. All three deps are required.
func NewContainerLogsHandler(apps *app.Service, servers *server.Service, access *rbac.Service) *ContainerLogsHandler {
	if apps == nil {
		panic("handler.NewContainerLogsHandler: app service is required")
	}
	if servers == nil {
		panic("handler.NewContainerLogsHandler: server service is required")
	}
	if access == nil {
		panic("handler.NewContainerLogsHandler: rbac service is required (no anonymous fallback)")
	}
	return &ContainerLogsHandler{apps: apps, servers: servers, access: access}
}

// Stream handles GET /api/v1/apps/{id}/containers/{name}/logs?tail=N.
//
// Flow:
//  1. Resolve app + RBAC check (apps.view).
//  2. Resolve Docker provider for the app's server.
//  3. List containers with label prexel.app_id=<id> and verify {name}
//     is among them. Refuses 403 otherwise — same code as RBAC denial
//     because from the caller's POV the resource isn't theirs.
//  4. Open ContainerLogs + stdcopy demux into the same SSE format as
//     EventHandler.Logs (event: container.log, data: {stream, line, ts}).
func (h *ContainerLogsHandler) Stream(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "id")
	containerName := strings.TrimSpace(chi.URLParam(r, "name"))
	if containerName == "" {
		writeError(w, http.StatusBadRequest, "missing_container_name")
		return
	}

	a, err := h.apps.Get(r.Context(), appID)
	if err != nil {
		writeAppError(w, err)
		return
	}

	// RBAC: apps.view on the team that owns this app.
	userID, ok := apimiddleware.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	allowed, err := h.access.CanAccessTeam(r.Context(), userID, a.TeamID, rbac.PermAppsView)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db_error")
		return
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	if a.ServerID == nil || strings.TrimSpace(*a.ServerID) == "" {
		writeError(w, http.StatusBadRequest, "no_server")
		return
	}

	tail := 100
	if v := r.URL.Query().Get("tail"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			tail = n
		}
	}

	provider, err := h.servers.Provider(r.Context(), *a.ServerID)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": "provider_unavailable", "message": err.Error(),
		})
		return
	}
	defer func() { _ = provider.Close() }()

	// Ownership check: container MUST carry the prexel.app_id label.
	cli, err := provider.Client(r.Context())
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": "provider_unavailable", "message": err.Error(),
		})
		return
	}
	if !containerBelongsToApp(r, cli, appID, containerName) {
		// 403 (not 404) — matches the RBAC denial above. We don't want
		// to leak existence of containers in other teams.
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	rc, err := dockersvc.StreamLogs(r.Context(), provider, containerName, true, tail)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": "logs_unavailable", "message": err.Error(),
		})
		return
	}
	defer func() { _ = rc.Close() }()

	setSSEHeaders(w)
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	flusher.Flush()

	// Same SSE shape as EventHandler.Logs: event "container.log" with
	// {stream, line, ts}. Frontend/CLI don't need a new parser.
	stdoutW := newSSEStreamWriter(w, flusher, "container.log", "stdout")
	stderrW := newSSEStreamWriter(w, flusher, "container.log", "stderr")
	done := make(chan struct{})
	go func() {
		_, _ = stdcopy.StdCopy(stdoutW, stderrW, rc)
		close(done)
	}()
	select {
	case <-r.Context().Done():
	case <-done:
	}
}

// containerBelongsToApp asks the daemon for every container labelled
// prexel.app_id=<appID> and returns true if `name` is among them. Names
// are compared with the leading "/" stripped to match what Docker reports.
//
// We use a label filter rather than `Names: []{name}` because:
//  1. Docker's name filter is a substring match (you'd need ^name$ regex).
//  2. We want to validate ownership, not just existence — listing by
//     label and intersecting is the most defensive shape.
func containerBelongsToApp(r *http.Request, cli dockerContainerLister, appID, name string) bool {
	f := dockerfilters.NewArgs()
	f.Add("label", "prexel.app_id="+appID)
	list, err := cli.ContainerList(r.Context(), dockertypes.ListOptions{All: true, Filters: f})
	if err != nil {
		return false
	}
	for _, c := range list {
		for _, n := range c.Names {
			if strings.TrimPrefix(n, "/") == name {
				return true
			}
		}
	}
	return false
}
