package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/prexel/prexel/internal/api/apimiddleware"
	"github.com/prexel/prexel/internal/backup"
	"github.com/prexel/prexel/internal/rbac"
)

// BackupHandler exposes the snapshot CRUD. Every mutation requires the
// settings.instance.manage permission — backups carry the entire DB
// AND the master secret key, so the bar to create or download one is
// the same as managing the instance.
type BackupHandler struct {
	svc    *backup.Service
	access *rbac.Service
}

func NewBackupHandler(svc *backup.Service, access *rbac.Service) *BackupHandler {
	if svc == nil {
		panic("handler.NewBackupHandler: service is required")
	}
	if access == nil {
		panic("handler.NewBackupHandler: rbac service is required")
	}
	return &BackupHandler{svc: svc, access: access}
}

// List returns metadata for every backup currently on disk.
// View permission is the same as creating — see handler-level comment.
func (h *BackupHandler) List(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r) {
		return
	}
	entries, err := h.svc.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"backups": entries})
}

type createBackupRequest struct {
	Passphrase string `json:"passphrase"`
}

// Create runs a snapshot. The body carries the operator-chosen
// passphrase; we never store it — the file is encrypted with a
// scrypt-derived key from this value, and the operator is told to
// remember it.
func (h *BackupHandler) Create(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r) {
		return
	}
	var in createBackupRequest
	if !decodeJSON(w, r, &in) {
		return
	}
	entry, err := h.svc.Create(r.Context(), in.Passphrase)
	if err != nil {
		writeBackupError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, entry)
}

// Download streams the file with a Content-Disposition that prompts
// the browser's save dialog. The file is already encrypted on disk;
// we don't transform anything en route.
func (h *BackupHandler) Download(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	path, err := h.svc.Path(id)
	if err != nil {
		writeBackupError(w, err)
		return
	}
	// http.ServeFile handles range requests, Last-Modified, etc.,
	// which matters when the operator resumes a partial download
	// from a flaky connection.
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+id+backup.FileExtension+`"`)
	http.ServeFile(w, r, path)
}

// Delete removes the file from disk. There is no soft-delete: backups
// are operator-owned external state, leaving "tombstone rows" around
// would be misleading.
func (h *BackupHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	if err := h.svc.Delete(id); err != nil {
		writeBackupError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *BackupHandler) authorize(w http.ResponseWriter, r *http.Request) bool {
	userID, ok := apimiddleware.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return false
	}
	allowed, err := h.access.HasPermission(r.Context(), userID, rbac.PermSettingsInstanceManage)
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

func writeBackupError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, backup.ErrWeakPassphrase):
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":   "weak_passphrase",
			"message": "Passphrase must be at least 12 characters.",
		})
	case errors.Is(err, backup.ErrInvalidID):
		writeError(w, http.StatusBadRequest, "invalid_id")
	case errors.Is(err, backup.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found")
	default:
		writeError(w, http.StatusInternalServerError, "backup_failed")
	}
}

// _ silences the unused-import linter for strconv during dev. The
// download handler will eventually grow Range parsing if we need it.
var _ = strconv.Itoa
