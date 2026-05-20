package domains

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	_ "modernc.org/sqlite"
)

// setupTestDB mirrors the schema the service touches (apps + domains +
// dns_zones). Keeping it inline matches the convention used by the other
// repo-level tests in this codebase and avoids dragging golang-migrate
// into a unit-test path.
func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?cache=shared&_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	stmts := []string{
		// `port` is consulted by Create when resolving the Caddy
		// upstream for app-bound domains; keep the column even though
		// the older tests never set it (NULL falls back to 80).
		`CREATE TABLE apps (id TEXT PRIMARY KEY, name TEXT NOT NULL, port INTEGER)`,
		`CREATE TABLE dns_zones (id TEXT PRIMARY KEY, apex TEXT NOT NULL UNIQUE)`,
		`CREATE TABLE domains (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			app_id TEXT REFERENCES apps(id),
			is_primary INTEGER NOT NULL DEFAULT 0,
			ssl_status TEXT NOT NULL DEFAULT 'pending',
			ssl_expires_at INTEGER,
			dns_verified INTEGER NOT NULL DEFAULT 0,
			dns_verified_at INTEGER,
			dns_last_check INTEGER,
			dns_check_count INTEGER NOT NULL DEFAULT 0,
			zone_id TEXT REFERENCES dns_zones(id) ON DELETE SET NULL,
			covered_by_wildcard INTEGER NOT NULL DEFAULT 0,
			-- Mirrors migration 010: per-service routing for Compose apps.
			service TEXT,
			port INTEGER,
			-- Mirrors migration 011: per-domain HTTP→HTTPS redirect toggle.
			force_https INTEGER NOT NULL DEFAULT 1,
			created_at INTEGER NOT NULL DEFAULT (unixepoch()),
			updated_at INTEGER NOT NULL DEFAULT (unixepoch())
		)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("schema: %v", err)
		}
	}
	return db
}

// fakeInstance lets the test inject the panel's current URL.
type fakeInstance struct{ url string }

func (f *fakeInstance) CurrentInstanceURL() string { return f.url }

// insertDomain seeds a row directly so we don't have to drag Caddy / zones
// through every test.
func insertDomain(t *testing.T, db *sql.DB, id, name string, appID *string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO domains(id, name, app_id) VALUES (?, ?, ?)`,
		id, name, appID,
	); err != nil {
		t.Fatalf("seed domain: %v", err)
	}
}

func TestDelete_RejectsWhenBoundToApp(t *testing.T) {
	db := setupTestDB(t)
	if _, err := db.Exec(`INSERT INTO apps(id, name) VALUES ('app-1', 'web')`); err != nil {
		t.Fatalf("seed app: %v", err)
	}
	appID := "app-1"
	insertDomain(t, db, "d1", "api.example.com", &appID)

	svc := NewService(Deps{DB: db})
	if err := svc.Delete(context.Background(), "d1"); !errors.Is(err, ErrInUseByApp) {
		t.Fatalf("want ErrInUseByApp, got %v", err)
	}

	// Row must still be present — gate is read-only.
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM domains WHERE id = 'd1'`).Scan(&n)
	if n != 1 {
		t.Errorf("domain row should still exist after gated delete, got %d", n)
	}
}

func TestDelete_RejectsWhenInstanceURL(t *testing.T) {
	db := setupTestDB(t)
	insertDomain(t, db, "d1", "panel.example.com", nil)

	svc := NewService(Deps{DB: db}).
		SetInstanceURLReader(&fakeInstance{url: "panel.example.com"})

	if err := svc.Delete(context.Background(), "d1"); !errors.Is(err, ErrInUseByInstance) {
		t.Fatalf("want ErrInUseByInstance, got %v", err)
	}
}

func TestDelete_AllowsWhenUnbound(t *testing.T) {
	db := setupTestDB(t)
	insertDomain(t, db, "d1", "orphan.example.com", nil)

	svc := NewService(Deps{DB: db}).
		SetInstanceURLReader(&fakeInstance{url: ""})

	if err := svc.Delete(context.Background(), "d1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM domains WHERE id = 'd1'`).Scan(&n)
	if n != 0 {
		t.Errorf("domain row should be gone, got %d", n)
	}
}

func TestUpdate_RebindAppAndPrimary(t *testing.T) {
	db := setupTestDB(t)
	if _, err := db.Exec(`INSERT INTO apps(id, name) VALUES ('app-1','one'),('app-2','two')`); err != nil {
		t.Fatalf("seed apps: %v", err)
	}
	one := "app-1"
	insertDomain(t, db, "d1", "api.example.com", &one)
	// Seed a second domain already primary on app-2 — Update should demote it.
	if _, err := db.Exec(
		`INSERT INTO domains(id, name, app_id, is_primary) VALUES ('d2', 'old.example.com', 'app-2', 1)`,
	); err != nil {
		t.Fatalf("seed: %v", err)
	}

	svc := NewService(Deps{DB: db})
	two := "app-2"
	primary := true
	out, err := svc.Update(context.Background(), "d1", UpdateInput{AppID: &two, IsPrimary: &primary})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if out.AppID == nil || *out.AppID != "app-2" {
		t.Errorf("appID: want app-2, got %+v", out.AppID)
	}
	if !out.IsPrimary {
		t.Error("expected primary=true after update")
	}

	// Old primary on app-2 should have been demoted.
	var oldPrimary int
	_ = db.QueryRow(`SELECT is_primary FROM domains WHERE id = 'd2'`).Scan(&oldPrimary)
	if oldPrimary != 0 {
		t.Error("old primary on app-2 was not demoted")
	}
}

func TestUpdate_ClearAppDetaches(t *testing.T) {
	db := setupTestDB(t)
	if _, err := db.Exec(`INSERT INTO apps(id, name) VALUES ('app-1','one')`); err != nil {
		t.Fatalf("seed apps: %v", err)
	}
	one := "app-1"
	insertDomain(t, db, "d1", "api.example.com", &one)
	if _, err := db.Exec(`UPDATE domains SET is_primary = 1 WHERE id = 'd1'`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	svc := NewService(Deps{DB: db})
	out, err := svc.Update(context.Background(), "d1", UpdateInput{ClearAppID: true})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if out.AppID != nil {
		t.Errorf("expected nil app_id after ClearAppID, got %+v", out.AppID)
	}
	// is_primary doesn't apply to instance domains — must be auto-cleared.
	if out.IsPrimary {
		t.Error("primary must be false on a domain with no app")
	}
}
