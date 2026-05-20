// Package server is the domain layer for "servers" (the noun) — the hosts
// where Prexel runs Docker containers. It owns the servers table CRUD, the
// connection test pipeline, and the background status loop.
//
// Persistence rules:
//
//   - private_key is always encrypted at rest using internal/crypto and is
//     decrypted only when needed (TestConnection, status loop).
//   - Server values returned to the API layer NEVER carry the private key. A
//     dedicated repo method (getPrivateKey) is used by the service to fetch it
//     under demand.
package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrNotFound is returned when a server lookup misses.
var ErrNotFound = errors.New("server: not found")

// ErrLocalAlreadyExists is returned by Create when a "local" server row is
// already present — there can be at most one per instance.
var ErrLocalAlreadyExists = errors.New("server: a local server already exists")

// ErrHasApps is returned by Delete when apps still reference the server.
var ErrHasApps = errors.New("server: has apps")

// Server is the API/service-facing representation. Note: PrivateKey is
// intentionally absent — the repo never returns it on List/Get.
type Server struct {
	ID                  string  `json:"id"`
	Name                string  `json:"name"`
	Type                string  `json:"type"` // "local" | "remote"
	Host                *string `json:"host,omitempty"`
	Port                int     `json:"port"`
	User                *string `json:"user,omitempty"`
	HostKeyFingerprint  *string `json:"host_key_fingerprint,omitempty"`
	Status              string  `json:"status"`
	DockerVersion       *string `json:"docker_version,omitempty"`
	LastCheckedAt       *int64  `json:"last_checked_at,omitempty"`
	HasEncryptedKey     bool    `json:"has_private_key"`
	PublicKey           string  `json:"public_key,omitempty"` // populated on Create when generated
	CreatedAt           int64   `json:"created_at"`
	UpdatedAt           int64   `json:"updated_at"`
}

type repo struct {
	db *sql.DB
}

func newRepo(db *sql.DB) *repo { return &repo{db: db} }

func (r *repo) countLocal(ctx context.Context) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM servers WHERE type = 'local'`).Scan(&n)
	return n, err
}

// insert inserts a new server row. privateKey is the already-encrypted blob.
func (r *repo) insert(ctx context.Context, s *Server, privateKey []byte) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO servers(id, name, type, host, port, user, private_key, host_key_fingerprint,
		                     status, docker_version, last_checked_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.Name, s.Type, s.Host, s.Port, s.User, nullableBytes(privateKey),
		s.HostKeyFingerprint, s.Status, s.DockerVersion, s.LastCheckedAt,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return fmt.Errorf("server: name already in use: %w", err)
		}
		return err
	}
	return nil
}

func (r *repo) list(ctx context.Context) ([]Server, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, name, type, host, port, user, host_key_fingerprint, status,
		        docker_version, last_checked_at, (private_key IS NOT NULL),
		        created_at, updated_at
		 FROM servers
		 ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Server
	for rows.Next() {
		s, err := scanServer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}

func (r *repo) get(ctx context.Context, id string) (*Server, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, name, type, host, port, user, host_key_fingerprint, status,
		        docker_version, last_checked_at, (private_key IS NOT NULL),
		        created_at, updated_at
		 FROM servers WHERE id = ?`, id)
	s, err := scanServer(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return s, err
}

// getPrivateKey returns the (still-encrypted) private key blob for a server.
// Callers must decrypt with internal/crypto.
func (r *repo) getPrivateKey(ctx context.Context, id string) ([]byte, error) {
	var b []byte
	err := r.db.QueryRowContext(ctx, `SELECT private_key FROM servers WHERE id = ?`, id).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return b, err
}

// updatePatch applies a non-empty patch. Only Name/Host/Port/User are mutable
// here; status/docker_version/last_checked_at/host_key_fingerprint flow through
// dedicated helpers below.
func (r *repo) updatePatch(ctx context.Context, id string, p Patch) (*Server, error) {
	sets := []string{}
	args := []any{}
	if p.Name != nil {
		sets = append(sets, "name = ?")
		args = append(args, *p.Name)
	}
	if p.Host != nil {
		sets = append(sets, "host = ?")
		args = append(args, *p.Host)
	}
	if p.Port != nil {
		sets = append(sets, "port = ?")
		args = append(args, *p.Port)
	}
	if p.User != nil {
		sets = append(sets, "user = ?")
		args = append(args, *p.User)
	}
	if len(sets) == 0 {
		return r.get(ctx, id)
	}
	sets = append(sets, "updated_at = unixepoch()")
	args = append(args, id)
	q := fmt.Sprintf(`UPDATE servers SET %s WHERE id = ?`, strings.Join(sets, ", "))
	res, err := r.db.ExecContext(ctx, q, args...)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, fmt.Errorf("server: name already in use: %w", err)
		}
		return nil, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, ErrNotFound
	}
	return r.get(ctx, id)
}

func (r *repo) delete(ctx context.Context, id string) error {
	// Block delete if any app references this server.
	var n int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM apps WHERE server_id = ?`, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("%w: %d apps still attached", ErrHasApps, n)
	}
	res, err := r.db.ExecContext(ctx, `DELETE FROM servers WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if k, _ := res.RowsAffected(); k == 0 {
		return ErrNotFound
	}
	return nil
}

// updateStatus persists the result of a health probe.
func (r *repo) updateStatus(ctx context.Context, id, status string, dockerVersion *string) error {
	now := time.Now().Unix()
	_, err := r.db.ExecContext(ctx,
		`UPDATE servers SET status = ?, docker_version = ?, last_checked_at = ?, updated_at = unixepoch() WHERE id = ?`,
		status, dockerVersion, now, id,
	)
	return err
}

// updateHostKey persists the captured fingerprint after a successful first
// SSH handshake.
func (r *repo) updateHostKey(ctx context.Context, id, fp string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE servers SET host_key_fingerprint = ?, updated_at = unixepoch() WHERE id = ?`, fp, id)
	return err
}

// markAppsUnreachable flips status of every app on this server to "unreachable".
// Used by the status loop when a server goes down.
func (r *repo) markAppsUnreachable(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE apps SET status = 'unreachable', updated_at = unixepoch()
		 WHERE server_id = ? AND status NOT IN ('unreachable')`, id)
	return err
}

type scanner interface {
	Scan(dest ...any) error
}

func scanServer(s scanner) (*Server, error) {
	var out Server
	var host, user, fp, dockerVersion sql.NullString
	var lastChecked sql.NullInt64
	if err := s.Scan(&out.ID, &out.Name, &out.Type, &host, &out.Port, &user, &fp,
		&out.Status, &dockerVersion, &lastChecked, &out.HasEncryptedKey,
		&out.CreatedAt, &out.UpdatedAt); err != nil {
		return nil, err
	}
	if host.Valid {
		out.Host = &host.String
	}
	if user.Valid {
		out.User = &user.String
	}
	if fp.Valid {
		out.HostKeyFingerprint = &fp.String
	}
	if dockerVersion.Valid {
		out.DockerVersion = &dockerVersion.String
	}
	if lastChecked.Valid {
		out.LastCheckedAt = &lastChecked.Int64
	}
	return &out, nil
}

func nullableBytes(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}
