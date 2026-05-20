package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/prexel/prexel/internal/api/apimiddleware"
	"github.com/prexel/prexel/internal/app"
	"github.com/prexel/prexel/internal/rbac"
	"github.com/prexel/prexel/internal/tag"
)

// TagHandler bundles the /api/v1/tags and /api/v1/apps/{id}/tags endpoints.
// All handlers assume they are mounted behind Auth middleware.
//
// Tags are an instance-wide dictionary — listing/creating/deleting is open
// to any authenticated user (same pattern as /instance/settings GET), while
// per-app attach/detach borrows the containing app's TeamID for RBAC so
// "filter my team's apps by tag" can't double as a leak.
type TagHandler struct {
	apps   *app.Service
	tags   *tag.Service
	access *rbac.Service
}

// NewTagHandler constructs the handler. RBAC is mandatory for the per-app
// endpoints; we fail-closed (panic at boot) rather than risk a refactor
// silently dropping the ACL and opening every per-app tag write.
func NewTagHandler(apps *app.Service, tags *tag.Service, access *rbac.Service) *TagHandler {
	if apps == nil {
		panic("handler.NewTagHandler: app service is required")
	}
	if tags == nil {
		panic("handler.NewTagHandler: tag service is required")
	}
	if access == nil {
		panic("handler.NewTagHandler: rbac service is required (no anonymous fallback)")
	}
	return &TagHandler{apps: apps, tags: tags, access: access}
}

// List handles GET /api/v1/tags — returns every tag in the dictionary.
// No team filter: tags are global (see package doc on internal/tag).
func (h *TagHandler) List(w http.ResponseWriter, r *http.Request) {
	out, err := h.tags.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db_error")
		return
	}
	if out == nil {
		out = []tag.Tag{}
	}
	writeJSON(w, http.StatusOK, out)
}

type createTagReq struct {
	Name  string  `json:"name"`
	Color *string `json:"color,omitempty"`
}

// Create handles POST /api/v1/tags — upsert by normalised name. Returns 200
// (with the existing row) when the tag already exists rather than 409; the
// dictionary contract is "POST is idempotent".
func (h *TagHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req createTagReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	out, err := h.tags.Create(r.Context(), req.Name, req.Color)
	if err != nil {
		writeTagError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// Delete handles DELETE /api/v1/tags/{id}. CASCADE on app_tags clears
// every attachment so the dashboard filter view also drops the entry.
func (h *TagHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.tags.Delete(r.Context(), id); err != nil {
		writeTagError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListForApp handles GET /api/v1/apps/{id}/tags — returns full Tag rows so
// the UI can render coloured chips without a follow-up dictionary fetch.
func (h *TagHandler) ListForApp(w http.ResponseWriter, r *http.Request) {
	target, err := h.resolveApp(r)
	if err != nil {
		writeAppError(w, err)
		return
	}
	if !h.authorizeTeam(w, r, rbac.PermAppsView, target.TeamID) {
		return
	}
	out, err := h.tags.ForAppDetailed(r.Context(), target.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db_error")
		return
	}
	if out == nil {
		out = []tag.Tag{}
	}
	writeJSON(w, http.StatusOK, out)
}

type setAppTagsReq struct {
	Tags []string `json:"tags"`
}

// SetAppTags handles PUT /api/v1/apps/{id}/tags — replaces the full set.
// Body: {"tags": ["prod","api"]}. Unknown names are created on the fly so
// the front-end can ship one PUT after the user finishes editing chips
// instead of orchestrating create-then-attach round-trips.
func (h *TagHandler) SetAppTags(w http.ResponseWriter, r *http.Request) {
	target, err := h.resolveApp(r)
	if err != nil {
		writeAppError(w, err)
		return
	}
	if !h.authorizeTeam(w, r, rbac.PermAppsUpdate, target.TeamID) {
		return
	}
	var req setAppTagsReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	out, err := h.tags.SetAppTags(r.Context(), target.ID, req.Tags)
	if err != nil {
		writeTagError(w, err)
		return
	}
	if out == nil {
		out = []tag.Tag{}
	}
	writeJSON(w, http.StatusOK, out)
}

// resolveApp accepts either a UUID or app name (mirrors AppHandler.Get) and
// returns the resolved row so callers can both check RBAC against TeamID
// and pass the canonical id downstream.
func (h *TagHandler) resolveApp(r *http.Request) (*app.App, error) {
	raw := chi.URLParam(r, "id")
	return h.apps.Get(r.Context(), raw)
}

func (h *TagHandler) authorizeTeam(w http.ResponseWriter, r *http.Request, permission string, teamID *string) bool {
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

func writeTagError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, tag.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found")
	default:
		msg := err.Error()
		switch {
		case strings.HasPrefix(msg, "invalid_") ||
			strings.HasPrefix(msg, "missing_"):
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
