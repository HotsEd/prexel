// Package secret implements encrypted at-rest storage of per-app secrets
// (env vars containing credentials, tokens, build-time inputs).
//
// All `value` blobs are encrypted with internal/crypto. The HTTP handlers
// never return plaintext values — Resolve* methods are reserved for the
// Deploy Engine (A8) to inject into container builds and runtime env.
package secret

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrNotFound is returned when a key lookup misses.
var ErrNotFound = errors.New("secret: not found")

// Secret is the public projection — value omitted by design.
type Secret struct {
	ID          string    `json:"id"`
	AppID       string    `json:"app_id"`
	Key         string    `json:"key"`
	IsBuildTime bool      `json:"is_build_time"`
	IsMultiline bool      `json:"is_multiline"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// SecretWithValue is the internal struct used by Resolve*. It MUST NOT be
// serialised back to API callers.
type SecretWithValue struct {
	Secret
	Value string
}

type repo struct {
	db *sql.DB
}

func newRepo(db *sql.DB) *repo { return &repo{db: db} }

// upsert inserts or updates a single secret row. The value is already encrypted.
func (r *repo) upsert(ctx context.Context, id, appID, key string, encrypted []byte, isBuildTime, isMultiline bool) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO secrets(id, app_id, key, value, is_build_time, is_multiline)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(app_id, key) DO UPDATE SET
			value = excluded.value,
			is_build_time = excluded.is_build_time,
			is_multiline = excluded.is_multiline,
			updated_at = unixepoch()
	`, id, appID, key, encrypted, boolToInt(isBuildTime), boolToInt(isMultiline))
	if err != nil {
		return fmt.Errorf("upsert secret: %w", err)
	}
	return nil
}

func (r *repo) list(ctx context.Context, appID string) ([]Secret, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, app_id, key, is_build_time, is_multiline, created_at, updated_at
		FROM secrets WHERE app_id = ? ORDER BY key ASC`, appID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Secret
	for rows.Next() {
		var (
			s                              Secret
			isBuildTime, isMultiline       int
			createdAt, updatedAt           int64
		)
		if err := rows.Scan(&s.ID, &s.AppID, &s.Key, &isBuildTime, &isMultiline, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		s.IsBuildTime = isBuildTime != 0
		s.IsMultiline = isMultiline != 0
		s.CreatedAt = time.Unix(createdAt, 0).UTC()
		s.UpdatedAt = time.Unix(updatedAt, 0).UTC()
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *repo) listWithValues(ctx context.Context, appID string, buildTimeFilter *bool) ([]struct {
	Secret
	Value []byte
}, error) {
	q := `SELECT id, app_id, key, value, is_build_time, is_multiline, created_at, updated_at
	      FROM secrets WHERE app_id = ?`
	args := []any{appID}
	if buildTimeFilter != nil {
		q += ` AND is_build_time = ?`
		args = append(args, boolToInt(*buildTimeFilter))
	}
	q += ` ORDER BY key ASC`
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []struct {
		Secret
		Value []byte
	}
	for rows.Next() {
		var (
			row                          struct {
				Secret
				Value []byte
			}
			isBuildTime, isMultiline     int
			createdAt, updatedAt         int64
		)
		if err := rows.Scan(&row.ID, &row.AppID, &row.Key, &row.Value,
			&isBuildTime, &isMultiline, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		row.IsBuildTime = isBuildTime != 0
		row.IsMultiline = isMultiline != 0
		row.CreatedAt = time.Unix(createdAt, 0).UTC()
		row.UpdatedAt = time.Unix(updatedAt, 0).UTC()
		out = append(out, row)
	}
	return out, rows.Err()
}

func (r *repo) delete(ctx context.Context, appID, key string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM secrets WHERE app_id = ? AND key = ?`, appID, key)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
