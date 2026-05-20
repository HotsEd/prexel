package handler

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/prexel/prexel/internal/app"
	"github.com/prexel/prexel/internal/deploy"
)

// DeploymentHandler exposes the deploy/rollback/restart/stop endpoints + the
// readonly /deployments/{id} routes.
type DeploymentHandler struct {
	apps   *app.Service
	deploy *deploy.Engine
}

// NewDeploymentHandler wires the handler.
func NewDeploymentHandler(apps *app.Service, eng *deploy.Engine) *DeploymentHandler {
	return &DeploymentHandler{apps: apps, deploy: eng}
}

// deployReq mirrors the JSON body accepted by Deploy.
type deployReq struct {
	Branch    string `json:"branch,omitempty"`
	CommitSHA string `json:"commit_sha,omitempty"`
	Tag       string `json:"tag,omitempty"`
}

// Deploy handles POST /api/v1/apps/{id}/deploy. The build runs in a goroutine
// so the request returns 202 immediately with the new deployment_id.
func (h *DeploymentHandler) Deploy(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	a, err := h.apps.Get(r.Context(), id)
	if err != nil {
		writeAppError(w, err)
		return
	}
	var req deployReq
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json")
			return
		}
	}

	// Pre-allocate the deployment id so we can return it in the 202.
	// The engine accepts the id via DeployOptions and uses it instead
	// of generating one — allowing the UI to navigate to the
	// deployment-detail page immediately without polling for the new
	// row. See engine.go's runDeploy.
	deploymentID := uuid.NewString()

	go func(appID string, opts deploy.DeployOptions) {
		// Use a fresh context so we don't get cancelled when the HTTP request
		// goroutine returns. Cap at 30 minutes — generous bound for slow
		// builds; reconcile cleans up if we crash.
		ctx, cancel := newDeployContext(30 * time.Minute)
		defer cancel()
		if _, err := h.deploy.Deploy(ctx, appID, opts); err != nil {
			// Both back-pressure errors are expected when the user triggers
			// many deploys; just log at info so they don't drown real failures.
			switch {
			case errors.Is(err, deploy.ErrDeployInProgress),
				errors.Is(err, deploy.ErrTooManyDeploys):
				slog.Info("deploy: rejected (back-pressure)", "app_id", appID, "reason", err.Error())
			default:
				slog.Warn("deploy: failed", "app_id", appID, "err", err)
			}
		}
	}(a.ID, deploy.DeployOptions{
		Branch:       req.Branch,
		CommitSHA:    req.CommitSHA,
		ImageTag:     req.Tag,
		DeploymentID: deploymentID,
	})

	writeJSON(w, http.StatusAccepted, map[string]any{
		"deployment_id": deploymentID,
		"status":        "accepted",
		"app_id":        a.ID,
		"message":       "deploy started",
	})
}

// rollbackReq mirrors the JSON body accepted by Rollback.
type rollbackReq struct {
	To string `json:"to,omitempty"`
}

// Rollback handles POST /api/v1/apps/{id}/rollback.
func (h *DeploymentHandler) Rollback(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	a, err := h.apps.Get(r.Context(), id)
	if err != nil {
		writeAppError(w, err)
		return
	}
	var req rollbackReq
	if r.ContentLength > 0 {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	// Same pre-allocation trick as Deploy() — the UI navigates to the
	// new deployment-detail page using this id.
	deploymentID := uuid.NewString()

	go func(appID, target string) {
		ctx, cancel := newDeployContext(15 * time.Minute)
		defer cancel()
		if _, err := h.deploy.Rollback(ctx, appID, target, deploy.DeployOptions{DeploymentID: deploymentID}); err != nil {
			slog.Warn("rollback: failed", "app_id", appID, "err", err)
		}
	}(a.ID, req.To)

	writeJSON(w, http.StatusAccepted, map[string]any{
		"deployment_id": deploymentID,
		"status":        "accepted",
		"app_id":        a.ID,
		"message":       "rollback started",
	})
}

// Restart handles POST /api/v1/apps/{id}/restart.
func (h *DeploymentHandler) Restart(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	a, err := h.apps.Get(r.Context(), id)
	if err != nil {
		writeAppError(w, err)
		return
	}
	if err := h.deploy.Restart(r.Context(), a.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "restart_failed", "message": err.Error(),
		})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Stop handles POST /api/v1/apps/{id}/stop with real container halt semantics.
func (h *DeploymentHandler) Stop(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	a, err := h.apps.Get(r.Context(), id)
	if err != nil {
		writeAppError(w, err)
		return
	}
	if err := h.deploy.Stop(r.Context(), a.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "stop_failed", "message": err.Error(),
		})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListByApp handles GET /api/v1/apps/{id}/deployments?limit=N.
func (h *DeploymentHandler) ListByApp(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	a, err := h.apps.Get(r.Context(), id)
	if err != nil {
		writeAppError(w, err)
		return
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	out, err := h.deploy.ListDeployments(r.Context(), a.ID, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db_error", "message": err.Error()})
		return
	}
	if out == nil {
		out = []deploy.Deployment{}
	}
	writeJSON(w, http.StatusOK, out)
}

// Get handles GET /api/v1/deployments/{id}.
func (h *DeploymentHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	d, err := h.deploy.GetDeployment(r.Context(), id)
	if err != nil {
		if errors.Is(err, deploy.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db_error", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// Logs handles GET /api/v1/deployments/{id}/logs?tail=N — returns the (build)
// log file as plain text. Not SSE — the SSE-style live build feed flows via
// /api/v1/apps/{id}/events.
func (h *DeploymentHandler) Logs(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	d, err := h.deploy.GetDeployment(r.Context(), id)
	if err != nil {
		if errors.Is(err, deploy.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db_error", "message": err.Error()})
		return
	}
	if d.LogPath == nil {
		writeError(w, http.StatusNotFound, "no_log")
		return
	}
	f, err := os.Open(*d.LogPath)
	if err != nil {
		writeError(w, http.StatusNotFound, "log_missing")
		return
	}
	defer func() { _ = f.Close() }()

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	tail := 0
	if v := r.URL.Query().Get("tail"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			tail = n
		}
	}
	if tail <= 0 {
		_, _ = io.Copy(w, f)
		return
	}
	// Tail mode: keep the last `tail` lines in a ring.
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1<<16), 1<<22)
	lines := make([]string, 0, tail+1)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
		if len(lines) > tail {
			lines = lines[1:]
		}
	}
	for _, l := range lines {
		_, _ = fmt.Fprintln(w, l)
	}
}

// newDeployContext returns a fresh background context with a timeout —
// detaches a deploy goroutine from the HTTP request lifecycle.
func newDeployContext(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}
