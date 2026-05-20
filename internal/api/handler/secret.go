package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/prexel/prexel/internal/app"
	"github.com/prexel/prexel/internal/secret"
)

// SecretHandler bundles /api/v1/apps/{appID}/secrets endpoints. All assume
// they are mounted behind Auth middleware.
type SecretHandler struct {
	apps    *app.Service
	secrets *secret.Service
}

// NewSecretHandler constructs the handler.
func NewSecretHandler(apps *app.Service, secrets *secret.Service) *SecretHandler {
	return &SecretHandler{apps: apps, secrets: secrets}
}

// resolveAppID accepts either a UUID or app name and returns the canonical
// app id. Mirrors the lookup logic in AppHandler.Get.
func (h *SecretHandler) resolveAppID(r *http.Request) (string, error) {
	raw := chi.URLParam(r, "id")
	a, err := h.apps.Get(r.Context(), raw)
	if err != nil {
		return "", err
	}
	return a.ID, nil
}

// List handles GET /api/v1/apps/{appID}/secrets — returns metadata only.
func (h *SecretHandler) List(w http.ResponseWriter, r *http.Request) {
	appID, err := h.resolveAppID(r)
	if err != nil {
		writeAppError(w, err)
		return
	}
	items, err := h.secrets.List(r.Context(), appID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db_error")
		return
	}
	writeJSON(w, http.StatusOK, items)
}

type upsertSecretItem struct {
	Value       string `json:"value"`
	IsBuildTime bool   `json:"is_build_time"`
	IsMultiline bool   `json:"is_multiline"`
}

// Upsert handles PUT /api/v1/apps/{appID}/secrets — batch upsert.
func (h *SecretHandler) Upsert(w http.ResponseWriter, r *http.Request) {
	appID, err := h.resolveAppID(r)
	if err != nil {
		writeAppError(w, err)
		return
	}
	var body map[string]upsertSecretItem
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	items := make(map[string]secret.UpsertItem, len(body))
	for k, v := range body {
		items[k] = secret.UpsertItem{
			Value:       v.Value,
			IsBuildTime: v.IsBuildTime,
			IsMultiline: v.IsMultiline,
		}
	}
	if err := h.secrets.Upsert(r.Context(), appID, items); err != nil {
		writeSecretError(w, err)
		return
	}
	out, err := h.secrets.List(r.Context(), appID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db_error")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// Delete handles DELETE /api/v1/apps/{appID}/secrets/{key}.
func (h *SecretHandler) Delete(w http.ResponseWriter, r *http.Request) {
	appID, err := h.resolveAppID(r)
	if err != nil {
		writeAppError(w, err)
		return
	}
	key := chi.URLParam(r, "key")
	if err := h.secrets.Delete(r.Context(), appID, key); err != nil {
		writeSecretError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeSecretError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, secret.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found")
	default:
		msg := err.Error()
		switch {
		case strings.HasPrefix(msg, "invalid_") ||
			strings.HasPrefix(msg, "missing_") ||
			strings.HasPrefix(msg, "empty_value"):
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
