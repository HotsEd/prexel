// Package tag is the domain layer for app tags — a flat, instance-wide
// dictionary used to label apps so the dashboard can filter them. The
// many-to-many join lives in `app_tags`; the dictionary itself in `tags`.
//
// Tags are intentionally not scoped to a team. They behave more like
// hashtags than ACL'd resources: any signed-in user can read the full
// list, and the same "prod" tag means the same thing regardless of who
// applied it. RBAC for *attaching* a tag to a given app rides on the
// containing app's TeamID — enforced by the HTTP layer, not here.
package tag

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// ErrNotFound is returned when a tag lookup misses.
var ErrNotFound = errors.New("tag: not found")

// ErrNameInUse is returned when the unique constraint on `tags.name` trips.
// Surfaced as a distinct error so the service can swap an INSERT for a
// "fetch existing" lookup without inspecting driver-specific error text
// in two places.
var ErrNameInUse = errors.New("tag: name already in use")

// Tag is the API/service-facing projection of a row in `tags`.
type Tag struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Color     *string `json:"color,omitempty"`
	CreatedAt int64   `json:"created_at"`
}

type repo struct {
	db *sql.DB
}

func newRepo(db *sql.DB) *repo { return &repo{db: db} }

// insert writes a freshly-built tag row. UNIQUE(name) violations surface as
// ErrNameInUse so the service can convert the failed Create into an upsert
// (return the existing row) without parsing driver text twice.
func (r *repo) insert(ctx context.Context, t *Tag) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO tags(id, name, color) VALUES (?, ?, ?)
	`, t.ID, t.Name, t.Color)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return ErrNameInUse
		}
		return err
	}
	return nil
}

func (r *repo) get(ctx context.Context, id string) (*Tag, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, name, color, created_at FROM tags WHERE id = ?`, id)
	return scanTag(row)
}

func (r *repo) getByName(ctx context.Context, name string) (*Tag, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, name, color, created_at FROM tags WHERE name = ?`, name)
	return scanTag(row)
}

// list returns every tag, ordered by name so the dashboard renders in a
// stable, human-friendly order without a client-side sort.
func (r *repo) list(ctx context.Context) ([]Tag, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, name, color, created_at FROM tags ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Tag{}
	for rows.Next() {
		t, err := scanTag(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

func (r *repo) delete(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM tags WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// attach binds a tag to an app. The PRIMARY KEY on (app_id, tag_id) makes
// the operation naturally idempotent — re-binding the same pair is a no-op
// rather than an error, which matches how SetAppTags wants to behave.
func (r *repo) attach(ctx context.Context, appID, tagID string) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO app_tags(app_id, tag_id) VALUES (?, ?)
		ON CONFLICT(app_id, tag_id) DO NOTHING
	`, appID, tagID)
	return err
}

// detach removes a single (app, tag) link. Missing rows are not an error —
// the post-condition (link absent) holds either way.
func (r *repo) detach(ctx context.Context, appID, tagID string) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM app_tags WHERE app_id = ? AND tag_id = ?`, appID, tagID)
	return err
}

// listForApp returns the tags attached to a given app, ordered by name so
// the App detail response renders chips in a deterministic order.
func (r *repo) listForApp(ctx context.Context, appID string) ([]Tag, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT t.id, t.name, t.color, t.created_at
		FROM tags t
		INNER JOIN app_tags at ON at.tag_id = t.id
		WHERE at.app_id = ?
		ORDER BY t.name ASC
	`, appID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Tag{}
	for rows.Next() {
		t, err := scanTag(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// listAppsForTag returns the IDs of apps carrying a given tag. The dashboard
// uses it to drive its "filter by tag" view without joining client-side.
func (r *repo) listAppsForTag(ctx context.Context, tagID string) ([]string, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT app_id FROM app_tags WHERE tag_id = ?`, tagID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

type scanner interface {
	Scan(dest ...any) error
}

func scanTag(s scanner) (*Tag, error) {
	var (
		t     Tag
		color sql.NullString
	)
	if err := s.Scan(&t.ID, &t.Name, &color, &t.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if color.Valid {
		v := color.String
		t.Color = &v
	}
	return &t, nil
}
