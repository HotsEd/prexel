package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/prexel/prexel/internal/domains"
)

// DomainHandler exposes /api/v1/domains. All endpoints assume the Auth
// middleware is upstream.
type DomainHandler struct {
	svc *domains.Service
}

// NewDomainHandler wires the handler.
func NewDomainHandler(svc *domains.Service) *DomainHandler {
	return &DomainHandler{svc: svc}
}

type createDomainReq struct {
	Name      string  `json:"name"`
	AppID     *string `json:"app_id,omitempty"`
	IsPrimary bool    `json:"is_primary,omitempty"`
	// ForceHTTPS opts out of (or back into) Caddy's HTTP→HTTPS 308
	// redirect for this host. Pointer so an omitted field falls back
	// to the service default (true) instead of "explicitly false".
	ForceHTTPS *bool `json:"force_https,omitempty"`
}

// Create handles POST /api/v1/domains.
func (h *DomainHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req createDomainReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	if req.AppID != nil && strings.TrimSpace(*req.AppID) == "" {
		// Treat empty string as "no app" rather than "look up the empty id".
		req.AppID = nil
	}
	out, err := h.svc.Create(r.Context(), domains.CreateInput{
		Name:       req.Name,
		AppID:      req.AppID,
		IsPrimary:  req.IsPrimary,
		ForceHTTPS: req.ForceHTTPS,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

// List handles GET /api/v1/domains?app_id=...
func (h *DomainHandler) List(w http.ResponseWriter, r *http.Request) {
	var appID *string
	if v := r.URL.Query().Get("app_id"); v != "" {
		appID = &v
	}
	out, err := h.svc.List(r.Context(), appID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db_error")
		return
	}
	if out == nil {
		out = []domains.Domain{}
	}
	writeJSON(w, http.StatusOK, out)
}

// Get handles GET /api/v1/domains/{id}.
func (h *DomainHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	out, err := h.svc.Get(r.Context(), id)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// Delete handles DELETE /api/v1/domains/{id}.
func (h *DomainHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.svc.Delete(r.Context(), id); err != nil {
		writeDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type patchDomainReq struct {
	AppID        *string `json:"app_id,omitempty"`
	ClearAppID   bool    `json:"clear_app_id,omitempty"`
	IsPrimary    *bool   `json:"is_primary,omitempty"`
	// Per-service routing (Compose apps). Setting service+port
	// makes Caddy proxy to `prexel-<app>-<service>:<port>` instead
	// of the app's default container. Both must be provided
	// together; ClearService=true wipes both to NULL.
	Service      *string `json:"service,omitempty"`
	Port         *int    `json:"port,omitempty"`
	ClearService bool    `json:"clear_service,omitempty"`
	// ForceHTTPS toggles the HTTP→HTTPS 308 redirect Caddy installs
	// by default. Pointer so an absent field is "leave alone" while
	// `"force_https": false` explicitly opts out.
	ForceHTTPS *bool `json:"force_https,omitempty"`
}

// Patch handles PATCH /api/v1/domains/{id}. Supports re-linking the
// domain (AppID/ClearAppID), flipping is_primary, and binding the
// domain to a specific Compose service+port. Separate Clear* flags
// because JSON can't distinguish "field absent" from "field
// explicitly null".
func (h *DomainHandler) Patch(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req patchDomainReq
	if !decodeJSON(w, r, &req) {
		return
	}
	// Service + port are a unit. Accepting one without the other
	// would silently store half-state that Caddy can't act on.
	if (req.Service != nil) != (req.Port != nil) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":   "service_port_pair_required",
			"message": "service and port must be provided together",
		})
		return
	}
	out, err := h.svc.Update(r.Context(), id, domains.UpdateInput{
		AppID:        req.AppID,
		ClearAppID:   req.ClearAppID,
		IsPrimary:    req.IsPrimary,
		Service:      req.Service,
		Port:         req.Port,
		ClearService: req.ClearService,
		ForceHTTPS:   req.ForceHTTPS,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// Retry handles POST /api/v1/domains/{id}/retry.
func (h *DomainHandler) Retry(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.svc.Retry(r.Context(), id); err != nil {
		writeDomainError(w, err)
		return
	}
	out, err := h.svc.Get(r.Context(), id)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// Verify handles POST /api/v1/domains/{id}/verify. It forces an immediate DNS
// check for the given domain.
func (h *DomainHandler) Verify(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.svc.Verify(r.Context(), id); err != nil {
		writeDomainError(w, err)
		return
	}
	out, err := h.svc.Get(r.Context(), id)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// ListByApp handles GET /api/v1/apps/{app}/domains — convenience shortcut.
func (h *DomainHandler) ListByApp(w http.ResponseWriter, r *http.Request) {
	app := chi.URLParam(r, "app")
	out, err := h.svc.List(r.Context(), &app)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db_error")
		return
	}
	if out == nil {
		out = []domains.Domain{}
	}
	writeJSON(w, http.StatusOK, out)
}

// writeDomainError maps domain.* sentinels to HTTP responses.
func writeDomainError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domains.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, domains.ErrDuplicate):
		writeJSON(w, http.StatusConflict, map[string]string{
			"error":   "duplicate",
			"message": "Já existe um domínio com esse nome.",
		})
	case errors.Is(err, domains.ErrAppNotFound):
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":   "app_not_found",
			"message": "App referenciada não existe.",
		})
	case errors.Is(err, domains.ErrInUseByApp):
		writeJSON(w, http.StatusConflict, map[string]string{
			"error":   "in_use_by_app",
			"message": "Este domínio está vinculado a uma app. Desvincule a app antes de remover.",
		})
	case errors.Is(err, domains.ErrInUseByInstance):
		writeJSON(w, http.StatusConflict, map[string]string{
			"error":   "in_use_by_instance",
			"message": "Este domínio está em uso pelo painel (Settings → Instância). Troque o domínio do painel antes de remover.",
		})
	default:
		msg := err.Error()
		if strings.HasPrefix(msg, "invalid_domain") {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error":   "invalid_domain",
				"message": msg,
			})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error":   "internal_error",
			"message": msg,
		})
	}
}
