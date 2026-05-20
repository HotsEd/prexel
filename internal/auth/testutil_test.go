package auth

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

// newTestDB returns an in-memory SQLite with the minimum schema required to
// exercise the refresh-token rotation logic.
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	stmts := []string{
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
		`INSERT INTO users(id, email, password_hash) VALUES ('u1', 'admin@example.com', 'hash')`,
	}
	for _, q := range stmts {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("schema: %v\nsql: %s", err, q)
		}
	}
	return db
}
