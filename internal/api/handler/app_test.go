package handler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/prexel/prexel/internal/api/apimiddleware"
	"github.com/prexel/prexel/internal/app"
	"github.com/prexel/prexel/internal/crypto"
	"github.com/prexel/prexel/internal/rbac"
	"github.com/prexel/prexel/internal/secret"
	"github.com/prexel/prexel/internal/server"

	_ "modernc.org/sqlite"
)

const testSecretKey = "test-secret-key-32-chars-minimum-aaaaaa"

// appTestSetup wires the in-memory schema + an app.Service with a fake server
// already in the DB (status=connected). Returns a global-admin userID that
// callers must stamp on requests via apimiddleware.WithUserID so handlers can
// resolve permissions.
func appTestSetup(t *testing.T) (*AppHandler, *SecretHandler, string, *sql.DB, string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?cache=shared&_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	stmts := []string{
		`CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at INTEGER NOT NULL DEFAULT (unixepoch()))`,
		// RBAC tables (migrations 004 + 005). The AppHandler now requires a real
		// rbac.Service, which seeds default roles via EnsureDefaults. We need
		// roles/users/teams/team_members so the in-memory DB doesn't blow up.
		`CREATE TABLE users (
			id TEXT PRIMARY KEY, email TEXT UNIQUE NOT NULL, password_hash TEXT NOT NULL,
			role TEXT NOT NULL DEFAULT 'admin',
			name TEXT, avatar_path TEXT,
			status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','inactive','blocked')),
			role_id TEXT,
			two_factor_enabled INTEGER NOT NULL DEFAULT 0,
			two_factor_secret BLOB, two_factor_recovery_codes TEXT, two_factor_confirmed_at INTEGER,
			created_at INTEGER NOT NULL DEFAULT (unixepoch()),
			updated_at INTEGER NOT NULL DEFAULT (unixepoch())
		)`,
		`CREATE TABLE roles (
			id TEXT PRIMARY KEY, slug TEXT UNIQUE NOT NULL, name TEXT NOT NULL, description TEXT,
			is_admin INTEGER NOT NULL DEFAULT 0, is_system INTEGER NOT NULL DEFAULT 0,
			permissions TEXT NOT NULL DEFAULT '[]',
			scope TEXT NOT NULL DEFAULT 'both' CHECK (scope IN ('global','team','both')),
			created_at INTEGER NOT NULL DEFAULT (unixepoch()),
			updated_at INTEGER NOT NULL DEFAULT (unixepoch())
		)`,
		`CREATE TABLE teams (
			id TEXT PRIMARY KEY, name TEXT UNIQUE NOT NULL, slug TEXT UNIQUE NOT NULL,
			description TEXT, color TEXT NOT NULL DEFAULT '#10b981', avatar_path TEXT,
			created_at INTEGER NOT NULL DEFAULT (unixepoch()),
			updated_at INTEGER NOT NULL DEFAULT (unixepoch())
		)`,
		`CREATE TABLE team_members (
			team_id TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
			user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			role_id TEXT REFERENCES roles(id),
			created_at INTEGER NOT NULL DEFAULT (unixepoch()),
			PRIMARY KEY (team_id, user_id)
		)`,
		`CREATE TABLE servers (
			id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE,
			type TEXT NOT NULL CHECK (type IN ('local','remote')),
			host TEXT, port INTEGER NOT NULL DEFAULT 22, user TEXT,
			private_key BLOB, host_key_fingerprint TEXT,
			status TEXT NOT NULL DEFAULT 'unknown',
			docker_version TEXT, last_checked_at INTEGER,
			created_at INTEGER NOT NULL DEFAULT (unixepoch()),
			updated_at INTEGER NOT NULL DEFAULT (unixepoch())
		)`,
		`CREATE TABLE git_sources (
			id TEXT PRIMARY KEY, type TEXT NOT NULL, name TEXT NOT NULL,
			installation_id TEXT, private_key BLOB, public_key TEXT, token BLOB,
			created_at INTEGER NOT NULL DEFAULT (unixepoch())
		)`,
		`CREATE TABLE apps (
			id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, description TEXT,
			team_id TEXT,
			server_id TEXT REFERENCES servers(id), git_source_id TEXT REFERENCES git_sources(id),
			repo_url TEXT, branch TEXT NOT NULL DEFAULT 'main', git_commit_sha TEXT,
			build_type TEXT NOT NULL DEFAULT 'dockerfile' CHECK (build_type IN ('dockerfile','docker_image','docker_compose')),
			dockerfile_path TEXT NOT NULL DEFAULT 'Dockerfile',
			build_context TEXT NOT NULL DEFAULT '.',
			dockerfile_inline TEXT, compose_file TEXT, compose_inline TEXT, image_name TEXT,
			image_tag TEXT NOT NULL DEFAULT 'latest',
			install_command TEXT, build_command TEXT, start_command TEXT,
			pre_deploy_command TEXT, post_deploy_command TEXT,
			port INTEGER, host_port INTEGER, container_name TEXT,
			health_check_enabled INTEGER NOT NULL DEFAULT 1,
			health_check_path TEXT NOT NULL DEFAULT '/',
			health_check_method TEXT NOT NULL DEFAULT 'GET',
			health_check_port INTEGER, health_check_return_code INTEGER NOT NULL DEFAULT 200,
			health_check_interval INTEGER NOT NULL DEFAULT 30,
			health_check_timeout INTEGER NOT NULL DEFAULT 60,
			health_check_retries INTEGER NOT NULL DEFAULT 3,
			health_check_start_period INTEGER NOT NULL DEFAULT 10,
			limits_memory TEXT, limits_cpus TEXT,
			limits_memory_swap TEXT, limits_memory_swappiness INTEGER,
			limits_memory_reservation TEXT, limits_cpuset TEXT, limits_cpu_shares INTEGER,
			restart_policy TEXT NOT NULL DEFAULT 'unless-stopped',
			auto_deploy_branch TEXT,
			build_args_inject INTEGER NOT NULL DEFAULT 1,
			build_args_source_commit INTEGER NOT NULL DEFAULT 0,
			docker_labels TEXT NOT NULL DEFAULT '{}',
			env_vars TEXT NOT NULL DEFAULT '{}',
			status TEXT NOT NULL DEFAULT 'idle',
			created_at INTEGER NOT NULL DEFAULT (unixepoch()),
			updated_at INTEGER NOT NULL DEFAULT (unixepoch())
		)`,
		`CREATE TABLE secrets (
			id TEXT PRIMARY KEY,
			app_id TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
			key TEXT NOT NULL, value BLOB NOT NULL,
			is_build_time INTEGER NOT NULL DEFAULT 0,
			is_multiline INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL DEFAULT (unixepoch()),
			updated_at INTEGER NOT NULL DEFAULT (unixepoch()),
			UNIQUE (app_id, key)
		)`,
	}
	for _, q := range stmts {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("schema: %v", err)
		}
	}

	// Insert a fake connected local server so app.Service.Create passes.
	serverID := uuid.NewString()
	if _, err := db.Exec(`INSERT INTO servers(id, name, type, status) VALUES (?, 'local', 'local', 'connected')`, serverID); err != nil {
		t.Fatalf("seed server: %v", err)
	}

	cipher, err := crypto.New(testSecretKey)
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	serverSvc := server.NewService(db, cipher)
	appSvc := app.NewService(db, serverSvc, nil)
	secretSvc := secret.NewService(db, cipher)

	// The handler now requires a real RBAC service — tests had to be updated
	// because the previous handler silently allowed everything when access
	// was nil. We seed default roles and an admin user so handlers can resolve
	// permissions through the normal codepath.
	rbacSvc := rbac.NewService(db)
	if err := rbacSvc.EnsureDefaults(context.Background()); err != nil {
		t.Fatalf("rbac defaults: %v", err)
	}
	adminUserID := uuid.NewString()
	if _, err := db.Exec(`
		INSERT INTO users(id, email, password_hash, role, role_id)
		VALUES (?, 'qa@prexel.test', 'x', 'admin',
		        (SELECT id FROM roles WHERE slug = 'admin'))
	`, adminUserID); err != nil {
		t.Fatalf("seed admin user: %v", err)
	}
	// Tests don't exercise the Stats endpoint; pass nil and let
	// handler.Stats degrade to a 503 if anything ever calls it.
	return NewAppHandler(appSvc, rbacSvc, nil), NewSecretHandler(appSvc, secretSvc), serverID, db, adminUserID
}

// asAdmin attaches the test admin's userID to the request context so the
// handler's permission checks see an authenticated principal. Tests that
// drove handlers directly used to bypass the Auth middleware completely;
// now that NewAppHandler insists on RBAC, every request needs identity.
func asAdmin(r *http.Request, adminID string) *http.Request {
	return r.WithContext(apimiddleware.WithUserID(r.Context(), adminID))
}

func TestApp_CreateListGetDelete(t *testing.T) {
	appH, _, serverID, _, adminID := appTestSetup(t)

	// Create.
	body, _ := json.Marshal(map[string]any{
		"name":       "web",
		"server_id":  serverID,
		"build_type": "dockerfile",
		"repo_url":   "https://github.com/example/repo",
		"port":       8080,
	})
	rec := httptest.NewRecorder()
	req := asAdmin(httptest.NewRequest(http.MethodPost, "/api/v1/apps", bytes.NewReader(body)), adminID)
	appH.Create(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d body=%s", rec.Code, rec.Body.String())
	}
	created := decode(t, rec.Body.Bytes())
	appID, _ := created["id"].(string)
	if appID == "" {
		t.Fatal("missing id")
	}

	// List.
	rec = httptest.NewRecorder()
	req = asAdmin(httptest.NewRequest(http.MethodGet, "/api/v1/apps", nil), adminID)
	appH.List(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d", rec.Code)
	}
	var arr []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &arr); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(arr) != 1 {
		t.Errorf("want 1 app, got %d", len(arr))
	}

	// Get by id via chi URL param.
	rec = httptest.NewRecorder()
	req = asAdmin(httptest.NewRequest(http.MethodGet, "/api/v1/apps/"+appID, nil), adminID)
	req = withChiParam(req, "id", appID)
	appH.Get(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("get: %d", rec.Code)
	}

	// Delete.
	rec = httptest.NewRecorder()
	req = asAdmin(httptest.NewRequest(http.MethodDelete, "/api/v1/apps/"+appID, nil), adminID)
	req = withChiParam(req, "id", appID)
	appH.Delete(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("delete: %d", rec.Code)
	}
}

func TestApp_CreateRejectsInvalidName(t *testing.T) {
	appH, _, serverID, _, adminID := appTestSetup(t)
	body, _ := json.Marshal(map[string]any{
		"name":       "ab", // too short
		"server_id":  serverID,
		"build_type": "dockerfile",
		"repo_url":   "https://github.com/example/repo",
	})
	rec := httptest.NewRecorder()
	req := asAdmin(httptest.NewRequest(http.MethodPost, "/api/v1/apps", bytes.NewReader(body)), adminID)
	appH.Create(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestApp_CreateRejectsDuplicateName(t *testing.T) {
	appH, _, serverID, _, adminID := appTestSetup(t)

	body, _ := json.Marshal(map[string]any{
		"name":       "web",
		"server_id":  serverID,
		"build_type": "dockerfile",
		"repo_url":   "https://github.com/example/repo",
	})
	rec := httptest.NewRecorder()
	appH.Create(rec, asAdmin(httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body)), adminID))
	if rec.Code != http.StatusCreated {
		t.Fatalf("first create: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	appH.Create(rec, asAdmin(httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body)), adminID))
	if rec.Code != http.StatusConflict {
		t.Errorf("dup: expected 409, got %d", rec.Code)
	}
}

func TestSecret_UpsertListDelete(t *testing.T) {
	appH, secretH, serverID, _, adminID := appTestSetup(t)

	// Create an app first.
	body, _ := json.Marshal(map[string]any{
		"name": "web", "server_id": serverID, "build_type": "dockerfile",
		"repo_url": "https://x/y",
	})
	rec := httptest.NewRecorder()
	appH.Create(rec, asAdmin(httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body)), adminID))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d", rec.Code)
	}
	created := decode(t, rec.Body.Bytes())
	appID, _ := created["id"].(string)

	// Upsert.
	put, _ := json.Marshal(map[string]any{
		"DB_URL":  map[string]any{"value": "postgres://secret"},
		"BUILD_X": map[string]any{"value": "v", "is_build_time": true},
	})
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/", bytes.NewReader(put))
	req = withChiParam(req, "id", appID)
	secretH.Upsert(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upsert: %d body=%s", rec.Code, rec.Body.String())
	}

	// List — must not return plaintext.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req = withChiParam(req, "id", appID)
	secretH.List(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d", rec.Code)
	}
	s := rec.Body.String()
	if bytes.Contains([]byte(s), []byte("postgres://secret")) {
		t.Errorf("secret value leaked in List response: %s", s)
	}

	// Delete.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/", nil)
	req = withChiParam(req, "id", appID)
	req = withChiParam(req, "key", "DB_URL")
	secretH.Delete(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("delete: %d body=%s", rec.Code, rec.Body.String())
	}

	// Delete missing -> 404.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/", nil)
	req = withChiParam(req, "id", appID)
	req = withChiParam(req, "key", "NOPE")
	secretH.Delete(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("delete missing: %d", rec.Code)
	}
}

// withChiParam wraps the request context with a chi RouteContext carrying the
// URL parameter. Required to test handlers that read chi.URLParam directly.
// Calling repeatedly extends the same route context so multiple params can be
// stacked.
func withChiParam(r *http.Request, key, value string) *http.Request {
	rctx, ok := r.Context().Value(chi.RouteCtxKey).(*chi.Context)
	if !ok || rctx == nil {
		rctx = chi.NewRouteContext()
	}
	rctx.URLParams.Add(key, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}
