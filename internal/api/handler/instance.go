package handler

import (
	"errors"
	"net/http"

	"github.com/prexel/prexel/internal/api/apimiddleware"
	"github.com/prexel/prexel/internal/instance"
	"github.com/prexel/prexel/internal/rbac"
)

// InstanceHandler exposes the typed instance_settings row to the API.
// Both Get/Patch require an authenticated principal; PATCH additionally
// demands the settings.instance.manage permission so non-admins cannot
// toggle maintenance mode behind the admin's back.
//
// LatestVersion is an unrelated read-only helper that lives here because
// the InstanceInfoCard already consumes both pieces of data side-by-side
// — keeping them on one handler avoids a second injection ceremony for
// what is conceptually "instance facts".
type InstanceHandler struct {
	svc     *instance.Service
	access  *rbac.Service
	updates *instance.LatestVersionChecker
}

// NewInstanceHandler wires the dependencies. `updates` is optional: if
// nil, the LatestVersion endpoint returns an empty payload (the UI
// already treats empty as "I don't know" and hides the badge).
func NewInstanceHandler(svc *instance.Service, access *rbac.Service, updates *instance.LatestVersionChecker) *InstanceHandler {
	if svc == nil {
		panic("handler.NewInstanceHandler: instance service is required")
	}
	if access == nil {
		panic("handler.NewInstanceHandler: rbac service is required")
	}
	return &InstanceHandler{svc: svc, access: access, updates: updates}
}

// Get returns the current settings snapshot. Auth-only — read is broad
// because the UI populates the Settings → Instance section for any signed-in
// member; sensitive fields (none today) would be redacted server-side.
func (h *InstanceHandler) Get(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.Get(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db_error")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// Patch applies a partial update. Returns the full snapshot so the UI can
// re-render without a follow-up GET.
func (h *InstanceHandler) Patch(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r, rbac.PermSettingsInstanceManage) {
		return
	}
	var in instance.UpdateInput
	if !decodeJSON(w, r, &in) {
		return
	}
	out, err := h.svc.Update(r.Context(), in)
	if err != nil {
		writeInstanceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// LatestVersion proxies the cached GitHub-releases-derived "what's the
// newest tagged release?" answer. Any authenticated user may call it;
// the data is not sensitive (it's literally a public release tag) and
// the UI uses it to render a passive "Update available" hint.
//
// We always return 200 with the same shape — never a 5xx — because the
// outbound call is best-effort and an empty payload is a meaningful
// "I don't know" signal that the UI handles gracefully.
func (h *InstanceHandler) LatestVersion(w http.ResponseWriter, r *http.Request) {
	if h.updates == nil {
		writeJSON(w, http.StatusOK, instance.VersionInfo{})
		return
	}
	writeJSON(w, http.StatusOK, h.updates.Get(r.Context()))
}

func (h *InstanceHandler) authorize(w http.ResponseWriter, r *http.Request, perm string) bool {
	userID, ok := apimiddleware.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return false
	}
	allowed, err := h.access.HasPermission(r.Context(), userID, perm)
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

// writeInstanceError maps domain errors to wire codes. Validation failures
// carry their message through so the UI can render context inline.
func writeInstanceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, instance.ErrLetsEncryptNeedsURL):
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":   "letsencrypt_needs_url",
			"message": "Configure a domain before enabling Let's Encrypt.",
		})
	case errors.Is(err, instance.ErrZoneNotRegistered):
		// Surface the registry gate explicitly so the UI can route the
		// user to /domains to register the zone — the user explicitly
		// chose this fail-loud behaviour over silently auto-creating.
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":   "zone_not_registered",
			"message": err.Error(),
		})
	case errors.Is(err, instance.ErrInvalidInput):
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":   "invalid_input",
			"message": err.Error(),
		})
	default:
		writeError(w, http.StatusInternalServerError, "db_error")
	}
}
