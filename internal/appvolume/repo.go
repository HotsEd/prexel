// Package appvolume owns the per-app persistent storage configuration —
// the `app_volumes` rows added in migration 011. A row is either a Docker
// named volume (docker-managed path) or a host bind mount (operator-chosen
// path). The deploy engine reads these rows and applies them to container
// create calls; this package only persists them.
//
// Compose apps store one row per (service, mount_path) so the engine can
// target the right service in the YAML. Single-container apps leave the
// `service` column NULL.
package appvolume

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// ErrNotFound is returned when a lookup misses.
var ErrNotFound = errors.New("appvolume: not found")

// ErrDuplicate is returned when the UNIQUE(app_id, service, mount_path)
// constraint trips. The service layer translates this into the
// `invalid_duplicate_mount` user-facing error.
var ErrDuplicate = errors.New("appvolume: duplicate mount")

// Volume is the API/service-facing representation of a row in `app_volumes`.
// Pointer fields encode SQL NULL: a `Service == nil` value means the volume
// belongs to a single-container app (no compose service), and `HostPath == nil`
// means a Docker-managed named volume.
type Volume struct {
	ID        string  `json:"id"`
	AppID     string  `json:"app_id"`
	Service   *string `json:"service,omitempty"`
	MountPath string  `json:"mount_path"`
	HostPath  *string `json:"host_path,omitempty"`
	IsNamed   bool    `json:"is_named"`
	ReadOnly  bool    `json:"read_only"`
	CreatedAt int64   `json:"created_at"`
	UpdatedAt int64   `json:"updated_at"`
}

type repo struct {
	db *sql.DB
}

func newRepo(db *sql.DB) *repo { return &repo{db: db} }

// insert writes a freshly-built volume row. The UNIQUE constraint surfaces
// as ErrDuplicate so the service layer can return a structured error.
func (r *repo) insert(ctx context.Context, v *Volume) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO app_volumes(
			id, app_id, service, mount_path, host_path, is_named, read_only
		) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		v.ID, v.AppID, nullableStr(v.Service), v.MountPath, nullableStr(v.HostPath),
		boolToInt(v.IsNamed), boolToInt(v.ReadOnly),
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return ErrDuplicate
		}
		return err
	}
	return nil
}

func (r *repo) get(ctx context.Context, id string) (*Volume, error) {
	row := r.db.QueryRowContext(ctx, selectColumns+` FROM app_volumes WHERE id = ?`, id)
	return scanVolume(row)
}

// listByApp returns every volume of an app ordered by mount_path so the UI
// table is stable across reloads (the trigger updates updated_at on every
// mutation, which would otherwise reshuffle the list).
func (r *repo) listByApp(ctx context.Context, appID string) ([]Volume, error) {
	rows, err := r.db.QueryContext(ctx,
		selectColumns+` FROM app_volumes WHERE app_id = ? ORDER BY mount_path ASC`, appID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Volume
	for rows.Next() {
		v, err := scanVolume(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

// updateFields runs a partial UPDATE built from the entries in `sets`. Keys
// are SQL column names trusted by the caller (service layer). Mirrors the
// same shape used in internal/app/repo.go so we stay consistent with the
// rest of the codebase.
func (r *repo) updateFields(ctx context.Context, id string, sets map[string]any) (*Volume, error) {
	if len(sets) == 0 {
		return r.get(ctx, id)
	}
	cols := make([]string, 0, len(sets))
	args := make([]any, 0, len(sets)+1)
	for col, v := range sets {
		cols = append(cols, col+" = ?")
		args = append(args, v)
	}
	args = append(args, id)
	q := fmt.Sprintf(`UPDATE app_volumes SET %s WHERE id = ?`, strings.Join(cols, ", "))
	res, err := r.db.ExecContext(ctx, q, args...)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, ErrDuplicate
		}
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	// The AFTER UPDATE trigger bumps updated_at — re-read so the response
	// carries the freshest timestamps.
	return r.get(ctx, id)
}

func (r *repo) delete(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM app_volumes WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

const selectColumns = `SELECT
	id, app_id, service, mount_path, host_path, is_named, read_only,
	created_at, updated_at`

type scanner interface {
	Scan(dest ...any) error
}

func scanVolume(s scanner) (*Volume, error) {
	var (
		v                 Volume
		service, hostPath sql.NullString
		isNamed, readOnly int
	)
	err := s.Scan(
		&v.ID, &v.AppID, &service, &v.MountPath, &hostPath,
		&isNamed, &readOnly, &v.CreatedAt, &v.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	v.Service = nullStr(service)
	v.HostPath = nullStr(hostPath)
	v.IsNamed = isNamed != 0
	v.ReadOnly = readOnly != 0
	return &v, nil
}

func nullStr(s sql.NullString) *string {
	if !s.Valid {
		return nil
	}
	v := s.String
	return &v
}

// nullableStr converts a *string into something the SQLite driver maps to
// SQL NULL when the pointer is nil — preserving the difference between
// "explicit empty string" (which we never want here) and "unset".
func nullableStr(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
