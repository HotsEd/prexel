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
	"github.com/prexel/prexel/internal/auth"
)

func (s *Service) UserContext(ctx context.Context, userID string) (*UserContext, error) {
	var (
		id, email, legacyRole string
		name, avatar          sql.NullString
		status                string
		roleID                sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT id, email, role, name, avatar_path, status, role_id
		FROM users WHERE id = ?
	`, userID).Scan(&id, &email, &legacyRole, &name, &avatar, &status, &roleID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	out := &UserContext{
		ID:        id,
		Email:     email,
		Name:      name.String,
		AvatarURL: avatarURL(avatar.String),
		Status:    status,
		RoleSlug:  legacyRole,
	}
	if roleID.Valid && roleID.String != "" {
		role, err := s.GetRole(ctx, roleID.String)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return nil, err
		}
		if role != nil {
			out.Role = role
			out.RoleSlug = role.Slug
			out.IsAdmin = role.IsAdmin
			out.Permissions = role.Permissions
			if role.IsAdmin {
				out.Permissions = AllPermissions()
			}
		}
	}
	if out.Role == nil && legacyRole == "admin" {
		out.IsAdmin = true
		out.Permissions = AllPermissions()
	}
	sort.Strings(out.Permissions)
	return out, nil
}

func (s *Service) ListMembers(ctx context.Context) ([]Member, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT u.id, u.email, u.name, u.avatar_path, u.status, u.created_at,
		       r.id, r.slug, r.name, r.description, r.scope, r.is_admin, r.is_system, r.permissions, r.created_at, r.updated_at
		FROM users u
		LEFT JOIN roles r ON r.id = u.role_id
		ORDER BY u.created_at ASC
	`)
	if err != nil {
		return nil, err
	}
	var out []Member
	for rows.Next() {
		m, err := scanMemberRow(rows)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		out = append(out, *m)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range out {
		teams, err := s.teamsForMember(ctx, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Teams = teams
	}
	return out, nil
}

func (s *Service) CreateMember(ctx context.Context, in CreateMemberInput) (*Member, error) {
	email := strings.ToLower(strings.TrimSpace(in.Email))
	if !strings.Contains(email, "@") {
		return nil, fmt.Errorf("%w: invalid email", ErrInvalidInput)
	}
	if err := auth.ValidateStrong(in.Password); err != nil {
		return nil, fmt.Errorf("%w: weak password", ErrInvalidInput)
	}
	var globalRole any
	if strings.TrimSpace(in.RoleID) != "" {
		role, err := s.GetRole(ctx, in.RoleID)
		if err != nil {
			return nil, err
		}
		if !roleAllowsScope(role, RoleScopeGlobal) {
			return nil, fmt.Errorf("%w: role cannot be used globally", ErrInvalidInput)
		}
		globalRole = role.ID
	}
	teamInputs := in.Teams
	if len(teamInputs) == 0 && len(in.TeamIDs) > 0 {
		teamInputs = s.defaultMemberTeamInputs(in.TeamIDs)
	}
	if err := s.validateMemberTeamInputs(ctx, teamInputs); err != nil {
		return nil, err
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		return nil, err
	}
	id := uuid.NewString()
	if _, err := s.db.ExecContext(ctx, `INSERT INTO users(id, email, password_hash, role, role_id, name, status) VALUES (?, ?, ?, 'member', ?, ?, 'active')`,
		id, email, hash, globalRole, strings.TrimSpace(in.Name)); err != nil {
		return nil, err
	}
	if err := s.replaceMemberTeams(ctx, id, teamInputs); err != nil {
		return nil, err
	}
	return s.GetMember(ctx, id)
}

func (s *Service) GetMember(ctx context.Context, id string) (*Member, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT u.id, u.email, u.name, u.avatar_path, u.status, u.created_at,
		       r.id, r.slug, r.name, r.description, r.scope, r.is_admin, r.is_system, r.permissions, r.created_at, r.updated_at
		FROM users u
		LEFT JOIN roles r ON r.id = u.role_id
		WHERE u.id = ?
	`, id)
	m, err := scanMemberRow(row)
	if err != nil {
		return nil, err
	}
	teams, err := s.teamsForMember(ctx, m.ID)
	if err != nil {
		return nil, err
	}
	m.Teams = teams
	return m, nil
}

func (s *Service) UpdateMember(ctx context.Context, id string, in UpdateMemberInput) (*Member, error) {
	if _, err := s.GetMember(ctx, id); err != nil {
		return nil, err
	}
	sets := []string{}
	args := []any{}
	if in.Name != nil {
		sets = append(sets, "name = ?")
		args = append(args, strings.TrimSpace(*in.Name))
	}
	if in.RoleID != nil {
		roleID := strings.TrimSpace(*in.RoleID)
		if roleID == "" {
			sets = append(sets, "role_id = NULL")
		} else {
			role, err := s.GetRole(ctx, roleID)
			if err != nil {
				return nil, err
			}
			if !roleAllowsScope(role, RoleScopeGlobal) {
				return nil, fmt.Errorf("%w: role cannot be used globally", ErrInvalidInput)
			}
			sets = append(sets, "role_id = ?")
			args = append(args, roleID)
		}
	}
	if in.Status != nil {
		status := strings.TrimSpace(*in.Status)
		if _, ok := statusAllowed[status]; !ok {
			return nil, fmt.Errorf("%w: invalid status", ErrInvalidInput)
		}
		sets = append(sets, "status = ?")
		args = append(args, status)
	}
	if len(sets) > 0 {
		args = append(args, id)
		if _, err := s.db.ExecContext(ctx, `UPDATE users SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...); err != nil {
			return nil, err
		}
	}
	if in.TeamsSet {
		teamInputs := in.Teams
		if len(teamInputs) == 0 && len(in.TeamIDs) > 0 {
			teamInputs = s.defaultMemberTeamInputs(in.TeamIDs)
		}
		if err := s.validateMemberTeamInputs(ctx, teamInputs); err != nil {
			return nil, err
		}
		if err := s.replaceMemberTeams(ctx, id, teamInputs); err != nil {
			return nil, err
		}
	}
	return s.GetMember(ctx, id)
}

func (s *Service) DeleteMember(ctx context.Context, id, currentUserID string) error {
	if id == currentUserID {
		return ErrSelfDelete
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Service) validateMembers(ctx context.Context, ids []string) error {
	for _, id := range uniqueStrings(ids) {
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE id = ?`, id).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
	}
	return nil
}

func (s *Service) validateMemberTeamInputs(ctx context.Context, teams []TeamMemberInput) error {
	for _, m := range normaliseTeamMembers(teams) {
		if strings.TrimSpace(m.TeamID) == "" {
			return ErrNotFound
		}
		if err := s.validateTeams(ctx, []string{m.TeamID}); err != nil {
			return err
		}
		role, err := s.GetRole(ctx, m.RoleID)
		if err != nil {
			return err
		}
		if !roleAllowsScope(role, RoleScopeTeam) {
			return fmt.Errorf("%w: role cannot be used in a team", ErrInvalidInput)
		}
	}
	return nil
}

func (s *Service) replaceMemberTeams(ctx context.Context, userID string, teams []TeamMemberInput) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM team_members WHERE user_id = ?`, userID); err != nil {
		return err
	}
	for _, m := range normaliseTeamMembers(teams) {
		if _, err := tx.ExecContext(ctx, `INSERT INTO team_members(team_id, user_id, role_id) VALUES (?, ?, ?)`, m.TeamID, userID, m.RoleID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Service) defaultMemberTeamInputs(teamIDs []string) []TeamMemberInput {
	roleID := s.defaultTeamRoleID(context.Background())
	out := make([]TeamMemberInput, 0, len(teamIDs))
	for _, id := range uniqueStrings(teamIDs) {
		out = append(out, TeamMemberInput{TeamID: id, RoleID: roleID})
	}
	return out
}

func scanMemberRow(row interface{ Scan(dest ...any) error }) (*Member, error) {
	var (
		m                  Member
		name, avatar       sql.NullString
		roleID, roleSlug   sql.NullString
		roleName, roleDesc sql.NullString
		roleScope          sql.NullString
		rolePerms          sql.NullString
		roleAdmin, roleSys sql.NullInt64
		roleCreated        sql.NullInt64
		roleUpdated        sql.NullInt64
	)
	err := row.Scan(
		&m.ID, &m.Email, &name, &avatar, &m.Status, &m.CreatedAt,
		&roleID, &roleSlug, &roleName, &roleDesc, &roleScope, &roleAdmin, &roleSys, &rolePerms, &roleCreated, &roleUpdated,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	m.Name = name.String
	m.AvatarURL = avatarURL(avatar.String)
	if roleID.Valid {
		perms := []string{}
		_ = json.Unmarshal([]byte(rolePerms.String), &perms)
		m.Role = &Role{
			ID:          roleID.String,
			Slug:        roleSlug.String,
			Name:        roleName.String,
			Description: roleDesc.String,
			Scope:       normaliseScope(roleScope.String),
			IsAdmin:     roleAdmin.Int64 == 1,
			IsSystem:    roleSys.Int64 == 1,
			Permissions: perms,
			CreatedAt:   roleCreated.Int64,
			UpdatedAt:   roleUpdated.Int64,
		}
	}
	return &m, nil
}
