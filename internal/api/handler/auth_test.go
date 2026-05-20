package handler

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/prexel/prexel/internal/auth"
	"github.com/prexel/prexel/internal/setup"

	_ "modernc.org/sqlite"
)

const jwtTestSecret = "test-jwt-secret-32-chars-minimum-aaaaaa"

// authTestSetup spins up an in-memory DB with the minimum schema required for
// the auth + setup handlers and returns wiring usable from each test.
func authTestSetup(t *testing.T) (*Auth, *Setup, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	stmts := []string{
		`CREATE TABLE settings (
			key        TEXT PRIMARY KEY,
			value      TEXT NOT NULL,
			updated_at INTEGER NOT NULL DEFAULT (unixepoch())
		)`,
		`CREATE TABLE users (
			id            TEXT PRIMARY KEY,
			email         TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			role          TEXT NOT NULL DEFAULT 'admin',
			created_at    INTEGER NOT NULL DEFAULT (unixepoch())
		)`,
		`CREATE TABLE refresh_tokens (
			id         TEXT PRIMARY KEY,
			user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			family_id  TEXT NOT NULL,
			token_hash TEXT NOT NULL UNIQUE,
			revoked_at INTEGER,
			expires_at INTEGER NOT NULL,
			created_at INTEGER NOT NULL DEFAULT (unixepoch())
		)`,
	}
	for _, q := range stmts {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("schema: %v", err)
		}
	}

	setupSvc, err := setup.NewService(db)
	if err != nil {
		t.Fatalf("setup svc: %v", err)
	}

	authH := NewAuthHandler(AuthDeps{DB: db, Setup: setupSvc, JWTSecret: jwtTestSecret, Secure: false})
	setupH := NewSetupHandler(SetupDeps{DB: db, Setup: setupSvc, Servers: nil, Version: "test"})

	return authH, setupH, db
}

// seedUser inserts a test user and returns its id.
func seedUser(t *testing.T, db *sql.DB, email, plain string) string {
	t.Helper()
	hash, err := auth.HashPassword(plain)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	id := uuid.NewString()
	if _, err := db.Exec(`INSERT INTO users(id, email, password_hash) VALUES (?, ?, ?)`, id, email, hash); err != nil {
		t.Fatalf("insert: %v", err)
	}
	return id
}

// markSetupComplete flips the gate so login can succeed.
func markSetupComplete(t *testing.T, setupH *Setup) {
	t.Helper()
	if err := setupH.d.Setup.MarkComplete(); err != nil && !errors.Is(err, setup.ErrAlreadyCompleted) {
		t.Fatalf("mark complete: %v", err)
	}
}

func decode(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("decode: %v\nbody: %s", err, string(body))
	}
	return m
}

func TestLogin_HappyPath(t *testing.T) {
	authH, setupH, db := authTestSetup(t)
	markSetupComplete(t, setupH)
	seedUser(t, db, "admin@example.com", "Strong#Pass1ord")

	body, _ := json.Marshal(map[string]string{
		"email":    "admin@example.com",
		"password": "Strong#Pass1ord",
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	authH.Login(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("login: got %d, body=%s", rec.Code, rec.Body.String())
	}
	resp := decode(t, rec.Body.Bytes())
	if resp["access_token"] == nil || resp["access_token"] == "" {
		t.Error("missing access_token")
	}

	// Refresh cookie present.
	found := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == "refresh_token" && c.Value != "" {
			found = true
		}
	}
	if !found {
		t.Error("refresh_token cookie not set")
	}
}

func TestLogin_RejectsBadPassword(t *testing.T) {
	authH, setupH, db := authTestSetup(t)
	markSetupComplete(t, setupH)
	seedUser(t, db, "admin@example.com", "Strong#Pass1ord")

	body, _ := json.Marshal(map[string]string{"email": "admin@example.com", "password": "wrong"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	authH.Login(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestLogin_RejectsUnknownEmail(t *testing.T) {
	authH, setupH, _ := authTestSetup(t)
	markSetupComplete(t, setupH)

	body, _ := json.Marshal(map[string]string{"email": "ghost@example.com", "password": "Strong#Pass1ord"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	authH.Login(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestLogin_BlockedPreSetup(t *testing.T) {
	authH, _, db := authTestSetup(t)
	seedUser(t, db, "admin@example.com", "Strong#Pass1ord")

	body, _ := json.Marshal(map[string]string{"email": "admin@example.com", "password": "Strong#Pass1ord"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	authH.Login(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 setup_not_completed, got %d", rec.Code)
	}
}

func TestRefresh_RotatesAndDetectsReuse(t *testing.T) {
	authH, setupH, db := authTestSetup(t)
	markSetupComplete(t, setupH)
	seedUser(t, db, "admin@example.com", "Strong#Pass1ord")

	// Login to obtain refresh cookie.
	body, _ := json.Marshal(map[string]string{"email": "admin@example.com", "password": "Strong#Pass1ord"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	authH.Login(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login: %d", rec.Code)
	}
	var initialRefresh *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "refresh_token" {
			initialRefresh = c
		}
	}
	if initialRefresh == nil {
		t.Fatal("no refresh cookie")
	}

	// First refresh succeeds and rotates.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil)
	req.AddCookie(initialRefresh)
	authH.Refresh(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("first refresh: %d, body=%s", rec.Code, rec.Body.String())
	}

	// Reusing the original (now revoked) refresh token must trip family invalidation.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil)
	req.AddCookie(initialRefresh)
	authH.Refresh(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("reuse: expected 401, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "refresh_token_reuse") {
		t.Errorf("expected refresh_token_reuse error, got: %s", rec.Body.String())
	}
}

func TestRefresh_RejectsMissingCookie(t *testing.T) {
	authH, _, _ := authTestSetup(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil)
	authH.Refresh(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestSetupWizard_HappyPath(t *testing.T) {
	_, setupH, _ := authTestSetup(t)

	// Status before completion.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/setup/status", nil)
	setupH.Status(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d", rec.Code)
	}
	stat := decode(t, rec.Body.Bytes())
	if stat["completed"] != false {
		t.Error("expected completed=false")
	}
	if id, _ := stat["instance_id"].(string); !strings.HasPrefix(id, "prx_") {
		t.Errorf("instance_id: %v", stat["instance_id"])
	}

	// CreateAdmin.
	body, _ := json.Marshal(map[string]string{
		"email":                 "admin@example.com",
		"password":              "Strong#Pass1ord",
		"password_confirmation": "Strong#Pass1ord",
	})
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/setup/admin", bytes.NewReader(body))
	setupH.CreateAdmin(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin: %d, body=%s", rec.Code, rec.Body.String())
	}

	// Second create should 409.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/setup/admin", bytes.NewReader(body))
	setupH.CreateAdmin(rec, req)
	if rec.Code != http.StatusConflict {
		t.Errorf("duplicate admin: expected 409, got %d", rec.Code)
	}

	// SaveInstance with self-signed + empty URL is allowed.
	body, _ = json.Marshal(map[string]string{"tls_mode": "self-signed"})
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/setup/instance", bytes.NewReader(body))
	setupH.SaveInstance(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("instance: %d, body=%s", rec.Code, rec.Body.String())
	}

	// Complete.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/setup/complete", nil)
	setupH.Complete(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("complete: %d", rec.Code)
	}

	// Second complete returns 409.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/setup/complete", nil)
	setupH.Complete(rec, req)
	if rec.Code != http.StatusConflict {
		t.Errorf("re-complete: expected 409, got %d", rec.Code)
	}
}

func TestSetupAdmin_RejectsWeakPassword(t *testing.T) {
	_, setupH, _ := authTestSetup(t)
	body, _ := json.Marshal(map[string]string{
		"email":                 "admin@example.com",
		"password":              "weak",
		"password_confirmation": "weak",
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup/admin", bytes.NewReader(body))
	setupH.CreateAdmin(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 weak_password, got %d", rec.Code)
	}
}

func TestSetupAdmin_RejectsPasswordMismatch(t *testing.T) {
	_, setupH, _ := authTestSetup(t)
	body, _ := json.Marshal(map[string]string{
		"email":                 "admin@example.com",
		"password":              "Strong#Pass1ord",
		"password_confirmation": "Different#Pass1",
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup/admin", bytes.NewReader(body))
	setupH.CreateAdmin(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "password_mismatch") {
		t.Errorf("body: %s", rec.Body.String())
	}
}

func TestSetupInstance_RejectsLetsEncryptWithoutDomain(t *testing.T) {
	_, setupH, _ := authTestSetup(t)
	body, _ := json.Marshal(map[string]string{"tls_mode": "letsencrypt"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup/instance", bytes.NewReader(body))
	setupH.SaveInstance(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}
