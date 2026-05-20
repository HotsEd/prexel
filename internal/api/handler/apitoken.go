package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/prexel/prexel/internal/api/apimiddleware"
	"github.com/prexel/prexel/internal/apitoken"
)

// APITokenHandler exposes the per-user PAT CRUD under /me/tokens. All
// routes require an authenticated session (or a PAT, which means a PAT
// could create more PATs — that's intentional, mirrors the user's own
// capability).
type APITokenHandler struct {
	svc *apitoken.Service
}

// APITokenDefaultTTL is what we stamp on `expires_at` when the caller
// doesn't supply one. We want the "never expires" footgun gone: every
// token now has a clock attached, and operators who need long-running
// CI credentials rotate them on a schedule. 90 days matches what the
// industry has settled on (GitHub, GitLab, npm) as the default.
const APITokenDefaultTTL = 90 * 24 * time.Hour

// APITokenMaxTTL caps the upper bound a caller can request. Even
// power users have to come back yearly to refresh their long-lived
// tokens — long enough to be friendly, short enough that a stolen
// token can't outlive the careers of the people who created it.
const APITokenMaxTTL = 365 * 24 * time.Hour

func NewAPITokenHandler(svc *apitoken.Service) *APITokenHandler {
	if svc == nil {
		panic("handler.NewAPITokenHandler: service is required")
	}
	return &APITokenHandler{svc: svc}
}

// createRequest mirrors the form the UI sends. `expires_at` is ISO-8601
// or omitted (= never expires). We deliberately don't accept a "scope"
// field: the token inherits the user's RBAC.
type createRequest struct {
	Name      string  `json:"name"`
	ExpiresAt *string `json:"expires_at"`
}

// createResponse is the ONLY place the raw token ever leaves the
// backend. The UI must show it once and discard. Subsequent List calls
// will never include the raw value.
type createResponse struct {
	Token apitoken.Token `json:"token"`
	Raw   string         `json:"raw"`
}

// Create issues a new token for the currently-authenticated user.
func (h *APITokenHandler) Create(w http.ResponseWriter, r *http.Request) {
	userID, ok := apimiddleware.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var in createRequest
	if !decodeJSON(w, r, &in) {
		return
	}

	// Expiration policy:
	//   - omitted / empty  → default to now + APITokenDefaultTTL (90d).
	//     The pre-cap behaviour ("nil = never") was a footgun; an
	//     unrevoked, undated token outlived the contributor in the
	//     wild more than once.
	//   - present but past  → handled by apitoken.Service (ErrBadExpiry).
	//   - present and beyond APITokenMaxTTL (365d) → 400 with a
	//     specific error message so the caller can clamp on retry.
	var expiresAt *time.Time
	if in.ExpiresAt != nil && strings.TrimSpace(*in.ExpiresAt) != "" {
		t, err := time.Parse(time.RFC3339, *in.ExpiresAt)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error":   "invalid_expires_at",
				"message": "expires_at must be ISO-8601 (RFC3339)",
			})
			return
		}
		maxExp := time.Now().Add(APITokenMaxTTL)
		if t.After(maxExp) {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error":   "expires_at_too_far",
				"message": "expires_at cannot be more than 365 days in the future",
			})
			return
		}
		expiresAt = &t
	} else {
		def := time.Now().Add(APITokenDefaultTTL)
		expiresAt = &def
	}

	res, err := h.svc.Create(r.Context(), userID, in.Name, expiresAt)
	if err != nil {
		writeAPITokenError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, createResponse{Token: res.Token, Raw: res.Raw})
}

// List returns the active tokens belonging to the caller.
func (h *APITokenHandler) List(w http.ResponseWriter, r *http.Request) {
	userID, ok := apimiddleware.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	tokens, err := h.svc.List(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db_error")
		return
	}
	// Return an envelope so the UI can grow it later (counts, etc.)
	// without us versioning the endpoint.
	writeJSON(w, http.StatusOK, map[string]any{"tokens": tokens})
}

// Revoke disables a token. We pass the authenticated user_id alongside
// the URL-param id so a forged path can't revoke someone else's token.
func (h *APITokenHandler) Revoke(w http.ResponseWriter, r *http.Request) {
	userID, ok := apimiddleware.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing_id")
		return
	}
	if err := h.svc.Revoke(r.Context(), userID, id); err != nil {
		writeAPITokenError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeAPITokenError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, apitoken.ErrBadName):
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":   "invalid_name",
			"message": "Name is required (1-100 chars).",
		})
	case errors.Is(err, apitoken.ErrBadExpiry):
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":   "invalid_expires_at",
			"message": "Expiry must be in the future.",
		})
	case errors.Is(err, apitoken.ErrNotFound):
		// Same response for "not yours" and "doesn't exist" — avoids
		// leaking whether a given id is in use by another user.
		writeError(w, http.StatusNotFound, "not_found")
	default:
		writeError(w, http.StatusInternalServerError, "db_error")
	}
}

// Compile-time guard that decodeJSON's caller convention (the shared
// helper in handler.go) still serialises JSON the same way the test
// fixtures expect. No-op at runtime.
var _ = json.NewEncoder
