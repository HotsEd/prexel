package setup

import (
	"database/sql"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	_ "modernc.org/sqlite"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?cache=shared&_pragma=busy_timeout(5000)")
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
	}
	for _, q := range stmts {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("schema: %v", err)
		}
	}
	return db
}

func TestNewService_HydratesFromDB(t *testing.T) {
	db := newTestDB(t)
	s, err := NewService(db)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if s.Completed() {
		t.Error("expected fresh DB to be incomplete")
	}

	// Manually set completion and refresh.
	if _, err := db.Exec(`INSERT INTO settings(key, value) VALUES ('setup_completed', 'true')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := s.Refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if !s.Completed() {
		t.Error("Refresh did not pick up DB change")
	}
}

func TestMarkComplete(t *testing.T) {
	db := newTestDB(t)
	s, _ := NewService(db)

	if err := s.MarkComplete(); err != nil {
		t.Fatalf("first MarkComplete: %v", err)
	}
	if !s.Completed() {
		t.Error("Completed should be true after MarkComplete")
	}
	// Second call returns ErrAlreadyCompleted.
	if err := s.MarkComplete(); !errors.Is(err, ErrAlreadyCompleted) {
		t.Errorf("expected ErrAlreadyCompleted, got %v", err)
	}
}

func TestMarkComplete_Concurrent(t *testing.T) {
	db := newTestDB(t)
	// Single writer connection like prod.
	db.SetMaxOpenConns(1)
	s, _ := NewService(db)

	const n = 8
	var wg sync.WaitGroup
	var ok, conflict atomic.Int32
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			err := s.MarkComplete()
			switch {
			case err == nil:
				ok.Add(1)
			case errors.Is(err, ErrAlreadyCompleted):
				conflict.Add(1)
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 1 {
		t.Errorf("expected exactly 1 success, got %d", ok.Load())
	}
	if conflict.Load() != n-1 {
		t.Errorf("expected %d ErrAlreadyCompleted, got %d", n-1, conflict.Load())
	}
}

func TestEnsureInstanceID_Idempotent(t *testing.T) {
	db := newTestDB(t)
	id1, err := EnsureInstanceID(db)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if !strings.HasPrefix(id1, "prx_") || len(id1) != 12 {
		t.Errorf("bad instance id format: %q", id1)
	}
	id2, err := EnsureInstanceID(db)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if id1 != id2 {
		t.Errorf("EnsureInstanceID not idempotent: %q vs %q", id1, id2)
	}
}

func TestHasAnyUser(t *testing.T) {
	db := newTestDB(t)
	s, _ := NewService(db)
	has, err := s.HasAnyUser()
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if has {
		t.Error("expected false on empty DB")
	}
	if _, err := db.Exec(`INSERT INTO users(id, email, password_hash) VALUES ('u1', 'a@b.com', 'h')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	has, _ = s.HasAnyUser()
	if !has {
		t.Error("expected true after user inserted")
	}
}

func TestSaveInstance(t *testing.T) {
	db := newTestDB(t)
	if err := SaveInstance(db, "example.com", "letsencrypt"); err != nil {
		t.Fatalf("save: %v", err)
	}
	var url, mode string
	if err := db.QueryRow(`SELECT value FROM settings WHERE key='instance_url'`).Scan(&url); err != nil {
		t.Fatalf("scan url: %v", err)
	}
	if err := db.QueryRow(`SELECT value FROM settings WHERE key='tls_mode'`).Scan(&mode); err != nil {
		t.Fatalf("scan mode: %v", err)
	}
	if url != "example.com" || mode != "letsencrypt" {
		t.Errorf("got url=%q mode=%q", url, mode)
	}
	// Upsert.
	if err := SaveInstance(db, "new.example.com", "self-signed"); err != nil {
		t.Fatalf("update: %v", err)
	}
	_ = db.QueryRow(`SELECT value FROM settings WHERE key='instance_url'`).Scan(&url)
	if url != "new.example.com" {
		t.Errorf("upsert failed: %q", url)
	}
}
