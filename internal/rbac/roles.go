package rbac

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
)

func (s *Service) EnsureDefaults(ctx context.Context) error {
	for _, d := range defaultRoles() {
		perms, err := json.Marshal(d.Permissions)
		if err != nil {
			return err
		}
		id := uuid.NewString()
		scope := normaliseScope(d.Scope)
		if _, err := s.db.ExecContext(ctx, `
			INSERT INTO roles(id, slug, name, description, scope, is_admin, is_system, permissions)
			VALUES (?, ?, ?, ?, ?, ?, 1, ?)
			ON CONFLICT(slug) DO UPDATE SET
				name = excluded.name,
				description = excluded.description,
				scope = excluded.scope,
				is_admin = excluded.is_admin,
				is_system = 1,
				permissions = excluded.permissions,
				updated_at = unixepoch()
		`, id, d.Slug, d.Name, d.Description, scope, boolToInt(d.IsAdmin), string(perms)); err != nil {
			return fmt.Errorf("seed role %s: %w", d.Slug, err)
		}
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE users
		SET role_id = (SELECT id FROM roles WHERE slug = 'admin')
		WHERE role_id IS NULL AND role = 'admin'
	`)
	return err
}

func (s *Service) ListRoles(ctx context.Context) ([]Role, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, slug, name, description, scope, is_admin, is_system, permissions, created_at, updated_at FROM roles ORDER BY is_system DESC, scope ASC, name ASC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Role
	for rows.Next() {
		r, err := scanRole(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

func (s *Service) GetRole(ctx context.Context, id string) (*Role, error) {
	r, err := scanRole(s.db.QueryRowContext(ctx, `SELECT id, slug, name, description, scope, is_admin, is_system, permissions, created_at, updated_at FROM roles WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return r, err
}

func (s *Service) CreateRole(ctx context.Context, in CreateRoleInput) (*Role, error) {
	name := strings.TrimSpace(in.Name)
	if len(name) < 2 || len(name) > 80 {
		return nil, fmt.Errorf("%w: invalid role name", ErrInvalidInput)
	}
	perms, err := normalisePermissions(in.Permissions)
	if err != nil {
		return nil, err
	}
	scope := normaliseScope(in.Scope)
	slug := uniqueSlug(ctx, s.db, "roles", slugify(name))
	id := uuid.NewString()
	raw, _ := json.Marshal(perms)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO roles(id, slug, name, description, scope, is_admin, is_system, permissions) VALUES (?, ?, ?, ?, ?, ?, 0, ?)`,
		id, slug, name, strings.TrimSpace(in.Description), scope, boolToInt(in.IsAdmin), string(raw)); err != nil {
		return nil, err
	}
	return s.GetRole(ctx, id)
}

func (s *Service) UpdateRole(ctx context.Context, id string, in UpdateRoleInput) (*Role, error) {
	role, err := s.GetRole(ctx, id)
	if err != nil {
		return nil, err
	}
	if role.IsSystem {
		return nil, ErrSystemRole
	}
	sets := []string{}
	args := []any{}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if len(name) < 2 || len(name) > 80 {
			return nil, fmt.Errorf("%w: invalid role name", ErrInvalidInput)
		}
		sets = append(sets, "name = ?")
		args = append(args, name)
	}
	if in.Description != nil {
		sets = append(sets, "description = ?")
		args = append(args, strings.TrimSpace(*in.Description))
	}
	if in.Scope != nil {
		sets = append(sets, "scope = ?")
		args = append(args, normaliseScope(*in.Scope))
	}
	if in.PermissionsSet {
		perms, err := normalisePermissions(in.Permissions)
		if err != nil {
			return nil, err
		}
		raw, _ := json.Marshal(perms)
		sets = append(sets, "permissions = ?")
		args = append(args, string(raw))
	}
	if len(sets) == 0 {
		return role, nil
	}
	args = append(args, id)
	if _, err := s.db.ExecContext(ctx, `UPDATE roles SET `+strings.Join(sets, ", ")+`, updated_at = unixepoch() WHERE id = ?`, args...); err != nil {
		return nil, err
	}
	return s.GetRole(ctx, id)
}

func (s *Service) DeleteRole(ctx context.Context, id string) error {
	role, err := s.GetRole(ctx, id)
	if err != nil {
		return err
	}
	if role.IsSystem {
		return ErrSystemRole
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE role_id = ?`, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return ErrRoleInUse
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM team_members WHERE role_id = ?`, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return ErrRoleInUse
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM roles WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func scanRole(row interface{ Scan(dest ...any) error }) (*Role, error) {
	var r Role
	var isAdmin, isSystem int
	var perms string
	if err := row.Scan(&r.ID, &r.Slug, &r.Name, &r.Description, &r.Scope, &isAdmin, &isSystem, &perms, &r.CreatedAt, &r.UpdatedAt); err != nil {
		return nil, err
	}
	r.Scope = normaliseScope(r.Scope)
	r.IsAdmin = isAdmin == 1
	r.IsSystem = isSystem == 1
	if err := json.Unmarshal([]byte(perms), &r.Permissions); err != nil {
		return nil, err
	}
	if r.Permissions == nil {
		r.Permissions = []string{}
	}
	return &r, nil
}

func normalisePermissions(in []string) ([]string, error) {
	allowed := permissionSet()
	set := map[string]struct{}{}
	for _, p := range in {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if _, ok := allowed[p]; !ok {
			return nil, fmt.Errorf("%w: unknown permission %q", ErrInvalidInput, p)
		}
		set[p] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}

func normaliseScope(scope string) string {
	switch strings.TrimSpace(scope) {
	case RoleScopeGlobal, RoleScopeTeam, RoleScopeBoth:
		return strings.TrimSpace(scope)
	default:
		return RoleScopeBoth
	}
}

func roleAllowsScope(role *Role, scope string) bool {
	if role == nil {
		return false
	}
	return role.Scope == RoleScopeBoth || role.Scope == scope
}
