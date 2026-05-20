package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/prexel/prexel/internal/api/apimiddleware"
	"github.com/prexel/prexel/internal/dnszone"
	"github.com/prexel/prexel/internal/rbac"
)

// DNSZoneHandler exposes the dns_zones surface to the API:
//
//   - GET    /api/v1/dns-zones         list every zone, with subdomain counts
//   - POST   /api/v1/dns-zones         pre-register an apex (used by the wizard
//                                       when the user wants wildcard setup
//                                       before adding any subdomain)
//   - GET    /api/v1/dns-zones/{id}    full snapshot of a zone
//   - PATCH  /api/v1/dns-zones/{id}    edit notes
//   - DELETE /api/v1/dns-zones/{id}    drops the zone; child domains keep
//                                       working but lose their zone link
//   - POST   /api/v1/dns-zones/{id}/verify   run apex + wildcard probes now
//
// The handler reuses the existing settings.instance.manage permission for
// every write. That permission already gates instance-wide DNS / TLS knobs,
// and zones live in the same conceptual layer.
type DNSZoneHandler struct {
	svc      *dnszone.Service
	access   *rbac.Service
	publicIP PublicIPReader
}

// PublicIPReader is the read-side of the public-IP provider already wired
// up in internal/domains. Defined here as a tiny interface so we depend
// on the surface, not on the concrete type — sidesteps the cyclic
// import (handler ← domains ← handler) we'd otherwise create.
type PublicIPReader interface {
	Get(ctx context.Context) (string, error)
}

func NewDNSZoneHandler(svc *dnszone.Service, access *rbac.Service) *DNSZoneHandler {
	if svc == nil {
		panic("handler.NewDNSZoneHandler: dnszone service is required")
	}
	if access == nil {
		panic("handler.NewDNSZoneHandler: rbac service is required")
	}
	return &DNSZoneHandler{svc: svc, access: access}
}

func (h *DNSZoneHandler) List(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r, rbac.PermDomainsView) {
		return
	}
	out, err := h.svc.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db_error")
		return
	}
	if out == nil {
		out = []dnszone.Zone{}
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *DNSZoneHandler) Get(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r, rbac.PermDomainsView) {
		return
	}
	id := chi.URLParam(r, "id")
	z, err := h.svc.Get(r.Context(), id)
	if err != nil {
		writeZoneError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, z)
}

type createZoneReq struct {
	Apex  string `json:"apex"`
	Notes string `json:"notes,omitempty"`
}

func (h *DNSZoneHandler) Create(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r, rbac.PermSettingsInstanceManage) {
		return
	}
	var req createZoneReq
	if !decodeJSON(w, r, &req) {
		return
	}
	apex := strings.TrimSpace(req.Apex)
	if apex == "" {
		writeError(w, http.StatusBadRequest, "apex_required")
		return
	}
	z, err := h.svc.Create(r.Context(), apex)
	if err != nil {
		writeZoneError(w, err)
		return
	}
	if req.Notes != "" {
		if err := h.svc.UpdateNotes(r.Context(), z.ID, req.Notes); err == nil {
			z.Notes = req.Notes
		}
	}
	writeJSON(w, http.StatusCreated, z)
}

type patchZoneReq struct {
	Notes *string `json:"notes,omitempty"`
}

func (h *DNSZoneHandler) Patch(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r, rbac.PermSettingsInstanceManage) {
		return
	}
	var req patchZoneReq
	if !decodeJSON(w, r, &req) {
		return
	}
	id := chi.URLParam(r, "id")
	if req.Notes != nil {
		if err := h.svc.UpdateNotes(r.Context(), id, *req.Notes); err != nil {
			writeZoneError(w, err)
			return
		}
	}
	z, err := h.svc.Get(r.Context(), id)
	if err != nil {
		writeZoneError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, z)
}

func (h *DNSZoneHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r, rbac.PermSettingsInstanceManage) {
		return
	}
	id := chi.URLParam(r, "id")
	if err := h.svc.Delete(r.Context(), id); err != nil {
		writeZoneError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Verify kicks off both probes (apex A + wildcard random subdomain).
// The expected IP is read from a public-IP provider injected at boot.
// Empty expectedIP is still useful — the wildcard probe will mark "ok"
// when ANYTHING resolves, telling the operator "you have a wildcard, just
// pin the IP later".
func (h *DNSZoneHandler) Verify(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r, rbac.PermSettingsInstanceManage) {
		return
	}
	id := chi.URLParam(r, "id")
	expectedIP := ""
	if h.publicIP != nil {
		if ip, err := h.publicIP.Get(r.Context()); err == nil {
			expectedIP = ip
		}
	}
	// Optional `?hostname=api.foo.com` adds a third probe for the specific
	// FQDN the operator is adding. Used by the add-domain wizard at Step 4
	// so the operator sees a probe of the thing they actually configured,
	// not just the zone-wide apex/wildcard.
	host := strings.TrimSpace(r.URL.Query().Get("hostname"))
	res, err := h.svc.Verify(r.Context(), id, expectedIP, dnszone.VerifyOptions{Hostname: host})
	if err != nil {
		writeZoneError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// SetPublicIP wires the public-IP reader. We keep it as a setter so the
// handler can be constructed before publicIP is ready (rare, but happens
// in tests).
func (h *DNSZoneHandler) SetPublicIP(p PublicIPReader) { h.publicIP = p }

func (h *DNSZoneHandler) authorize(w http.ResponseWriter, r *http.Request, perm string) bool {
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

func writeZoneError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, dnszone.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, dnszone.ErrInvalidApex):
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":   "invalid_apex",
			"message": err.Error(),
		})
	case errors.Is(err, dnszone.ErrInternalName):
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":   "internal_name",
			"message": "Apex must be a publicly registrable domain (e.g. foo.com). Single-label hostnames and IP literals are not zones.",
		})
	default:
		writeError(w, http.StatusInternalServerError, "db_error")
	}
}
