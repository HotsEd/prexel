package handler

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/prexel/prexel/internal/auth"
	"github.com/prexel/prexel/internal/server"
	"github.com/prexel/prexel/internal/setup"
)

// SetupDeps bundles dependencies used by the setup wizard handlers.
type SetupDeps struct {
	DB      *sql.DB
	Setup   *setup.Service
	Servers *server.Service
	Version string
}

// NewSetupHandler wires the setup wizard endpoints onto a Setup type that
// router.go can mount.
func NewSetupHandler(d SetupDeps) *Setup {
	return &Setup{d: d}
}

// Setup groups the setup wizard endpoints.
type Setup struct {
	d SetupDeps
}

type adminReq struct {
	Email                string `json:"email"`
	Password             string `json:"password"`
	PasswordConfirmation string `json:"password_confirmation"`
}

// CreateAdmin handles POST /api/v1/setup/admin.
func (h *Setup) CreateAdmin(w http.ResponseWriter, r *http.Request) {
	var req adminReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	addr, err := mail.ParseAddress(strings.TrimSpace(req.Email))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_email")
		return
	}
	if req.Password != req.PasswordConfirmation {
		writeError(w, http.StatusBadRequest, "password_mismatch")
		return
	}
	if err := auth.ValidateStrong(req.Password); err != nil {
		writeError(w, http.StatusBadRequest, "weak_password")
		return
	}

	hasUser, err := h.d.Setup.HasAnyUser()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db_error")
		return
	}
	if hasUser {
		writeError(w, http.StatusConflict, "admin_already_exists")
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "hash_error")
		return
	}
	id := uuid.NewString()
	if _, err := h.d.DB.Exec(
		`INSERT INTO users(id, email, password_hash, role) VALUES (?, ?, ?, 'admin')`,
		id, addr.Address, hash,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "db_insert_failed")
		return
	}
	_, _ = h.d.DB.Exec(
		`UPDATE users SET role_id = (SELECT id FROM roles WHERE slug = 'admin') WHERE id = ? AND role_id IS NULL`,
		id,
	)
	writeJSON(w, http.StatusOK, map[string]string{"id": id, "email": addr.Address})
}

type instanceReq struct {
	InstanceURL string `json:"instance_url"`
	TLSMode     string `json:"tls_mode"`
}

// SaveInstance handles POST /api/v1/setup/instance. The DNS check is best-effort
// and fires in the background so the wizard never blocks on a slow resolver.
func (h *Setup) SaveInstance(w http.ResponseWriter, r *http.Request) {
	var req instanceReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	tlsMode := req.TLSMode
	if tlsMode == "" {
		tlsMode = "self-signed"
	}
	if tlsMode != "self-signed" && tlsMode != "letsencrypt" {
		writeError(w, http.StatusBadRequest, "invalid_tls_mode")
		return
	}

	instanceURL := strings.TrimSpace(req.InstanceURL)
	if instanceURL != "" {
		normalized, err := normalizeInstanceURL(instanceURL)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_instance_url")
			return
		}
		instanceURL = normalized
		// Best-effort DNS lookup in background.
		host := instanceURL
		go func() {
			if _, err := net.LookupHost(host); err != nil {
				slog.Warn("setup: DNS lookup failed for instance URL", "host", host, "err", err)
			}
		}()
	} else if tlsMode != "self-signed" {
		// Empty URL with letsencrypt makes no sense.
		writeError(w, http.StatusBadRequest, "letsencrypt_requires_domain")
		return
	}

	if err := setup.SaveInstance(h.d.DB, instanceURL, tlsMode); err != nil {
		writeError(w, http.StatusInternalServerError, "db_error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"instance_url": instanceURL,
		"tls_mode":     tlsMode,
	})
}

func normalizeInstanceURL(in string) (string, error) {
	s := in
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return "", errors.New("invalid url")
	}
	if u.Path != "" && u.Path != "/" {
		return "", errors.New("path not allowed")
	}
	host := strings.TrimPrefix(u.Host, "www.")
	host = strings.TrimSuffix(host, "/")
	if host == "" {
		return "", errors.New("empty host")
	}
	return host, nil
}

type serverReq struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	User        string `json:"user"`
	GenerateKey bool   `json:"generate_key"`
	PrivateKey  string `json:"private_key"`
}

// CreateServer handles POST /api/v1/setup/server. Delegates to
// server.Service.Create so wizard-time validation matches the standalone
// /api/v1/servers endpoint (Docker probe for local, key generation for remote).
func (h *Setup) CreateServer(w http.ResponseWriter, r *http.Request) {
	var req serverReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	out, err := h.d.Servers.Create(r.Context(), server.CreateInput{
		Type:        req.Type,
		Name:        strings.TrimSpace(req.Name),
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
	writeJSON(w, http.StatusOK, out)
}

// Complete handles POST /api/v1/setup/complete.
func (h *Setup) Complete(w http.ResponseWriter, _ *http.Request) {
	if err := h.d.Setup.MarkComplete(); err != nil {
		if errors.Is(err, setup.ErrAlreadyCompleted) {
			writeError(w, http.StatusConflict, "already_completed")
			return
		}
		writeError(w, http.StatusInternalServerError, "db_error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// Status handles GET /api/v1/setup/status. Always public, even after completion,
// so the SPA can decide whether to redirect to /setup.
func (h *Setup) Status(w http.ResponseWriter, _ *http.Request) {
	id, err := h.d.Setup.InstanceID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db_error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"completed":   h.d.Setup.Completed(),
		"instance_id": id,
		"version":     h.d.Version,
	})
}

// writeJSON / writeError live in http_helpers.go and delegate to
// internal/api/httpx. They used to be defined here, which is how every
// other handler ended up reimplementing them slightly differently.
