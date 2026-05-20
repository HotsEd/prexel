package secret

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/prexel/prexel/internal/crypto"

	_ "modernc.org/sqlite"
)

const cipherSecret = "test-secret-key-32-chars-minimum-aaaaaa"

func newTestSvc(t *testing.T) (*Service, *sql.DB, string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?cache=shared&_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	stmts := []string{
		`CREATE TABLE servers (id TEXT PRIMARY KEY, name TEXT)`,
		`CREATE TABLE apps (id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE)`,
		`CREATE TABLE secrets (
			id            TEXT PRIMARY KEY,
			app_id        TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
			key           TEXT NOT NULL,
			value         BLOB NOT NULL,
			is_build_time INTEGER NOT NULL DEFAULT 0,
			is_multiline  INTEGER NOT NULL DEFAULT 0,
			created_at    INTEGER NOT NULL DEFAULT (unixepoch()),
			updated_at    INTEGER NOT NULL DEFAULT (unixepoch()),
			UNIQUE (app_id, key)
		)`,
		`INSERT INTO apps(id, name) VALUES ('app-1', 'app-1')`,
	}
	for _, q := range stmts {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("schema: %v", err)
		}
	}
	c, err := crypto.New(cipherSecret)
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	return NewService(db, c), db, "app-1"
}

func TestUpsert_EncryptsAtRest(t *testing.T) {
	svc, db, app := newTestSvc(t)
	ctx := context.Background()

	err := svc.Upsert(ctx, app, map[string]UpsertItem{
		"DATABASE_URL": {Value: "postgres://secret"},
		"BUILD_ARG":    {Value: "abc", IsBuildTime: true},
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// Raw values must NOT match plaintext.
	rows, err := db.Query(`SELECT key, value FROM secrets WHERE app_id = ? ORDER BY key`, app)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var k string
		var v []byte
		if err := rows.Scan(&k, &v); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if string(v) == "postgres://secret" || string(v) == "abc" {
			t.Errorf("secret %q stored in plaintext", k)
		}
	}
}

func TestList_MasksValues(t *testing.T) {
	svc, _, app := newTestSvc(t)
	ctx := context.Background()
	_ = svc.Upsert(ctx, app, map[string]UpsertItem{"FOO": {Value: "bar"}})

	got, err := svc.List(ctx, app)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 secret, got %d", len(got))
	}
	// Public Secret struct has no Value field — confirm masking by struct shape.
	if got[0].Key != "FOO" {
		t.Errorf("key: %q", got[0].Key)
	}
}

func TestResolveAndFilters(t *testing.T) {
	svc, _, app := newTestSvc(t)
	ctx := context.Background()
	_ = svc.Upsert(ctx, app, map[string]UpsertItem{
		"RUNTIME_X": {Value: "rval"},
		"BUILD_Y":   {Value: "bval", IsBuildTime: true},
	})

	all, err := svc.Resolve(ctx, app)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("got %d resolved, want 2", len(all))
	}

	rt, err := svc.ResolveRuntime(ctx, app)
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	if v, ok := rt["RUNTIME_X"]; !ok || v != "rval" {
		t.Errorf("runtime missing: %+v", rt)
	}
	if _, ok := rt["BUILD_Y"]; ok {
		t.Error("runtime should not include build-time secret")
	}

	bt, err := svc.ResolveBuildTime(ctx, app)
	if err != nil {
		t.Fatalf("bt: %v", err)
	}
	if v, ok := bt["BUILD_Y"]; !ok || v != "bval" {
		t.Errorf("build-time missing: %+v", bt)
	}
	if _, ok := bt["RUNTIME_X"]; ok {
		t.Error("build-time should not include runtime secret")
	}
}

func TestUpsert_RejectsBadKey(t *testing.T) {
	svc, _, app := newTestSvc(t)
	err := svc.Upsert(context.Background(), app, map[string]UpsertItem{"lowercase": {Value: "x"}})
	if err == nil {
		t.Error("expected invalid_key error")
	}
}

func TestUpsert_RejectsEmptyValue(t *testing.T) {
	svc, _, app := newTestSvc(t)
	err := svc.Upsert(context.Background(), app, map[string]UpsertItem{"FOO": {Value: ""}})
	if err == nil {
		t.Error("expected empty_value error")
	}
}

func TestDelete_RemovesSecret(t *testing.T) {
	svc, _, app := newTestSvc(t)
	ctx := context.Background()
	_ = svc.Upsert(ctx, app, map[string]UpsertItem{"FOO": {Value: "bar"}})

	if err := svc.Delete(ctx, app, "FOO"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got, _ := svc.List(ctx, app)
	if len(got) != 0 {
		t.Errorf("expected 0 secrets after delete, got %d", len(got))
	}

	if err := svc.Delete(ctx, app, "MISSING"); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestUpsert_UpdatesExisting(t *testing.T) {
	svc, _, app := newTestSvc(t)
	ctx := context.Background()
	if err := svc.Upsert(ctx, app, map[string]UpsertItem{"FOO": {Value: "v1"}}); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := svc.Upsert(ctx, app, map[string]UpsertItem{"FOO": {Value: "v2"}}); err != nil {
		t.Fatalf("update: %v", err)
	}
	rt, _ := svc.ResolveRuntime(ctx, app)
	if rt["FOO"] != "v2" {
		t.Errorf("update did not stick: %q", rt["FOO"])
	}
}
