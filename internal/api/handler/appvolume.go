package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/prexel/prexel/internal/api/apimiddleware"
	"github.com/prexel/prexel/internal/app"
	"github.com/prexel/prexel/internal/appvolume"
	"github.com/prexel/prexel/internal/rbac"
)

// AppVolumeHandler bundles /api/v1/apps/{id}/volumes endpoints. All
// handlers assume they are mounted behind Auth middleware (router.go).
//
// The path param is `id` (matching the rest of the /apps subtree) so Chi
// can reuse a single trie node. The per-volume param is `volID` to avoid
// shadowing.
type AppVolumeHandler struct {
	svc    *appvolume.Service
	appSvc *app.Service
	access *rbac.Service
}

// NewAppVolumeHandler constructs the handler. All three deps are required
// — RBAC has no anonymous fallback for the same reason as AppHandler:
// silently dropping the ACL would open every per-app subresource if a
// future refactor passed nil.
func NewAppVolumeHandler(svc *appvolume.Service, appSvc *app.Service, access *rbac.Service) *AppVolumeHandler {
	if svc == nil {
		panic("handler.NewAppVolumeHandler: appvolume service is required")
	}
	if appSvc == nil {
		panic("handler.NewAppVolumeHandler: app service is required")
	}
	if access == nil {
		panic("handler.NewAppVolumeHandler: rbac service is required (no anonymous fallback)")
	}
	return &AppVolumeHandler{svc: svc, appSvc: appSvc, access: access}
}

// resolveApp accepts either a UUID or app name and returns the canonical
// *App. Mirrors the lookup logic in AppHandler.Get / SecretHandler.
func (h *AppVolumeHandler) resolveApp(r *http.Request) (*app.App, error) {
	raw := chi.URLParam(r, "id")
	return h.appSvc.Get(r.Context(), raw)
}

// List handles GET /api/v1/apps/{id}/volumes.
func (h *AppVolumeHandler) List(w http.ResponseWriter, r *http.Request) {
	target, err := h.resolveApp(r)
	if err != nil {
		writeAppError(w, err)
		return
	}
	if !h.authorizeTeam(w, r, rbac.PermAppsView, target.TeamID) {
		return
	}
	out, err := h.svc.List(r.Context(), target.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db_error")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// Get handles GET /api/v1/apps/{id}/volumes/{volID}.
func (h *AppVolumeHandler) Get(w http.ResponseWriter, r *http.Request) {
	target, err := h.resolveApp(r)
	if err != nil {
		writeAppError(w, err)
		return
	}
	if !h.authorizeTeam(w, r, rbac.PermAppsView, target.TeamID) {
		return
	}
	vol, err := h.svc.Get(r.Context(), chi.URLParam(r, "volID"))
	if err != nil {
		writeVolumeError(w, err)
		return
	}
	// Defence in depth: a volume id from a different app must not be
	// addressable via the wrong /apps/{id}/volumes/{volID} URL.
	if vol.AppID != target.ID {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	writeJSON(w, http.StatusOK, vol)
}

type createVolumeReq struct {
	Service   *string `json:"service,omitempty"`
	MountPath string  `json:"mount_path"`
	HostPath  *string `json:"host_path,omitempty"`
	IsNamed   *bool   `json:"is_named,omitempty"`
	ReadOnly  bool    `json:"read_only,omitempty"`
}

// Create handles POST /api/v1/apps/{id}/volumes.
func (h *AppVolumeHandler) Create(w http.ResponseWriter, r *http.Request) {
	target, err := h.resolveApp(r)
	if err != nil {
		writeAppError(w, err)
		return
	}
	if !h.authorizeTeam(w, r, rbac.PermAppsUpdate, target.TeamID) {
		return
	}
	var req createVolumeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	// Default to is_named=true so the simple case ("just give me a docker
	// volume at /data") doesn't require a payload flag. Matches the
	// migration-level CHECK default.
	isNamed := true
	if req.IsNamed != nil {
		isNamed = *req.IsNamed
	}
	out, err := h.svc.Create(r.Context(), appvolume.CreateInput{
		AppID:     target.ID,
		Service:   req.Service,
		MountPath: req.MountPath,
		HostPath:  req.HostPath,
		IsNamed:   isNamed,
		ReadOnly:  req.ReadOnly,
	})
	if err != nil {
		writeVolumeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

type patchVolumeReq struct {
	MountPath *string `json:"mount_path,omitempty"`
	HostPath  *string `json:"host_path,omitempty"`
	IsNamed   *bool   `json:"is_named,omitempty"`
	ReadOnly  *bool   `json:"read_only,omitempty"`
}

// Patch handles PATCH /api/v1/apps/{id}/volumes/{volID}.
func (h *AppVolumeHandler) Patch(w http.ResponseWriter, r *http.Request) {
	target, err := h.resolveApp(r)
	if err != nil {
		writeAppError(w, err)
		return
	}
	if !h.authorizeTeam(w, r, rbac.PermAppsUpdate, target.TeamID) {
		return
	}
	volID := chi.URLParam(r, "volID")
	// Verify the volume actually belongs to this app before mutating.
	cur, err := h.svc.Get(r.Context(), volID)
	if err != nil {
		writeVolumeError(w, err)
		return
	}
	if cur.AppID != target.ID {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	var req patchVolumeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	out, err := h.svc.Update(r.Context(), volID, appvolume.UpdatePatch{
		MountPath: req.MountPath,
		HostPath:  req.HostPath,
		IsNamed:   req.IsNamed,
		ReadOnly:  req.ReadOnly,
	})
	if err != nil {
		writeVolumeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// Delete handles DELETE /api/v1/apps/{id}/volumes/{volID}.
func (h *AppVolumeHandler) Delete(w http.ResponseWriter, r *http.Request) {
	target, err := h.resolveApp(r)
	if err != nil {
		writeAppError(w, err)
		return
	}
	if !h.authorizeTeam(w, r, rbac.PermAppsUpdate, target.TeamID) {
		return
	}
	volID := chi.URLParam(r, "volID")
	cur, err := h.svc.Get(r.Context(), volID)
	if err != nil {
		writeVolumeError(w, err)
		return
	}
	if cur.AppID != target.ID {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if err := h.svc.Delete(r.Context(), volID); err != nil {
		writeVolumeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// authorizeTeam mirrors AppHandler.authorizeTeam — same RBAC contract,
// same fail-closed semantics. Duplicated rather than promoted to a shared
// helper because the per-handler dependency carry (h.access) is what
// makes the call simple at the use site.
func (h *AppVolumeHandler) authorizeTeam(w http.ResponseWriter, r *http.Request, permission string, teamID *string) bool {
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

func writeVolumeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, appvolume.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, appvolume.ErrDuplicate):
		writeJSON(w, http.StatusConflict, map[string]string{
			"error":   "duplicate_mount",
			"message": "Já existe um volume com esse mount_path para o app/serviço.",
		})
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
