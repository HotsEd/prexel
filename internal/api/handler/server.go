package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/prexel/prexel/internal/dockersvc"
	"github.com/prexel/prexel/internal/server"
)

// ServerHandler bundles the /api/v1/servers endpoints. All endpoints assume
// they are mounted behind Auth middleware (router.go).
type ServerHandler struct {
	svc *server.Service
}

// NewServerHandler constructs the handler.
func NewServerHandler(svc *server.Service) *ServerHandler {
	return &ServerHandler{svc: svc}
}

type createServerReq struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	User        string `json:"user"`
	GenerateKey bool   `json:"generate_key"`
	PrivateKey  string `json:"private_key"`
}

// Create handles POST /api/v1/servers.
func (h *ServerHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req createServerReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	out, err := h.svc.Create(r.Context(), server.CreateInput{
		Type:        req.Type,
		Name:        req.Name,
		Host:        req.Host,
		Port:        req.Port,
		User:        req.User,
		GenerateKey: req.GenerateKey,
		PrivateKey:  req.PrivateKey,
	})
	if err != nil {
		writeServerError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

// List handles GET /api/v1/servers.
func (h *ServerHandler) List(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db_error")
		return
	}
	if out == nil {
		out = []server.Server{}
	}
	writeJSON(w, http.StatusOK, out)
}

// Get handles GET /api/v1/servers/{id}.
func (h *ServerHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	out, err := h.svc.Get(r.Context(), id)
	if err != nil {
		writeServerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type patchServerReq struct {
	Name *string `json:"name,omitempty"`
	Host *string `json:"host,omitempty"`
	Port *int    `json:"port,omitempty"`
	User *string `json:"user,omitempty"`
}

// Patch handles PATCH /api/v1/servers/{id}.
func (h *ServerHandler) Patch(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req patchServerReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	out, err := h.svc.Update(r.Context(), id, server.Patch{
		Name: req.Name,
		Host: req.Host,
		Port: req.Port,
		User: req.User,
	})
	if err != nil {
		writeServerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// Delete handles DELETE /api/v1/servers/{id}.
func (h *ServerHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.svc.Delete(r.Context(), id); err != nil {
		writeServerError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Test handles POST /api/v1/servers/{id}/test.
func (h *ServerHandler) Test(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	rep, err := h.svc.TestConnection(r.Context(), id)
	if err != nil {
		writeServerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

// writeServerError maps domain errors to HTTP responses.
func writeServerError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, server.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, server.ErrLocalAlreadyExists):
		writeJSON(w, http.StatusConflict, map[string]string{
			"error":   "local_already_exists",
			"message": "Já existe um servidor local. Apenas um é permitido por instância.",
		})
	case errors.Is(err, server.ErrHasApps):
		writeJSON(w, http.StatusConflict, map[string]string{
			"error":   "has_apps",
			"message": "Servidor possui apps. Remova as apps primeiro.",
		})
	case errors.Is(err, dockersvc.ErrUnsupportedDockerVersion):
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":   "docker_too_old",
			"message": err.Error(),
		})
	default:
		msg := err.Error()
		// Map a few well-known message prefixes to specific HTTP codes.
		switch {
		case strings.HasPrefix(msg, "missing_") ||
			strings.HasPrefix(msg, "invalid_") ||
			strings.HasPrefix(msg, "docker unreachable") ||
			strings.HasPrefix(msg, "docker version") ||
			strings.HasPrefix(msg, "generate key") ||
			strings.HasPrefix(msg, "encrypt"):
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
