package rbac

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

func (s *Service) IsTeamMember(ctx context.Context, teamID, userID string) (bool, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM team_members WHERE team_id = ? AND user_id = ?`, teamID, userID).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

func (s *Service) ListTeams(ctx context.Context) ([]Team, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.id, t.name, t.slug, t.description, t.color, t.avatar_path, t.created_at, t.updated_at,
		       (SELECT COUNT(*) FROM team_members tm WHERE tm.team_id = t.id),
		       (SELECT COUNT(*) FROM apps a WHERE a.team_id = t.id)
		FROM teams t
		ORDER BY t.name ASC
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Team
	for rows.Next() {
		t, err := scanTeam(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

func (s *Service) GetTeam(ctx context.Context, id string) (*Team, error) {
	t, err := scanTeam(s.db.QueryRowContext(ctx, `
		SELECT t.id, t.name, t.slug, t.description, t.color, t.avatar_path, t.created_at, t.updated_at,
		       (SELECT COUNT(*) FROM team_members tm WHERE tm.team_id = t.id),
		       (SELECT COUNT(*) FROM apps a WHERE a.team_id = t.id)
		FROM teams t
		WHERE t.id = ?
	`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return t, err
}

func (s *Service) CreateTeam(ctx context.Context, in CreateTeamInput) (*Team, error) {
	name := strings.TrimSpace(in.Name)
	if len(name) < 2 || len(name) > 80 {
		return nil, fmt.Errorf("%w: invalid team name", ErrInvalidInput)
	}
	slug := strings.TrimSpace(in.Slug)
	if slug == "" {
		slug = slugify(name)
	}
	if !slugRegex.MatchString(slug) {
		return nil, fmt.Errorf("%w: invalid team slug", ErrInvalidInput)
	}
	color := strings.TrimSpace(in.Color)
	if color == "" {
		color = "#10b981"
	}
	if !colorRegex.MatchString(color) {
		return nil, fmt.Errorf("%w: invalid color", ErrInvalidInput)
	}
	memberInputs := in.Members
	if len(memberInputs) == 0 && len(in.MemberIDs) > 0 {
		memberInputs = s.defaultTeamMemberInputs(in.MemberIDs)
	}
	memberInputs = s.withDefaultTeamRole(ctx, memberInputs)
	if err := s.validateTeamMemberInputs(ctx, memberInputs); err != nil {
		return nil, err
	}
	id := uuid.NewString()
	if _, err := s.db.ExecContext(ctx, `INSERT INTO teams(id, name, slug, description, color) VALUES (?, ?, ?, ?, ?)`,
		id, name, slug, strings.TrimSpace(in.Description), color); err != nil {
		return nil, err
	}
	if err := s.replaceTeamMembers(ctx, id, memberInputs); err != nil {
		return nil, err
	}
	return s.GetTeam(ctx, id)
}

func (s *Service) UpdateTeam(ctx context.Context, id string, in UpdateTeamInput) (*Team, error) {
	if _, err := s.GetTeam(ctx, id); err != nil {
		return nil, err
	}
	sets := []string{}
	args := []any{}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if len(name) < 2 || len(name) > 80 {
			return nil, fmt.Errorf("%w: invalid team name", ErrInvalidInput)
		}
		sets = append(sets, "name = ?")
		args = append(args, name)
	}
	if in.Slug != nil {
		slug := strings.TrimSpace(*in.Slug)
		if !slugRegex.MatchString(slug) {
			return nil, fmt.Errorf("%w: invalid team slug", ErrInvalidInput)
		}
		sets = append(sets, "slug = ?")
		args = append(args, slug)
	}
	if in.Description != nil {
		sets = append(sets, "description = ?")
		args = append(args, strings.TrimSpace(*in.Description))
	}
	if in.Color != nil {
		color := strings.TrimSpace(*in.Color)
		if !colorRegex.MatchString(color) {
			return nil, fmt.Errorf("%w: invalid color", ErrInvalidInput)
		}
		sets = append(sets, "color = ?")
		args = append(args, color)
	}
	if len(sets) == 0 {
		return s.GetTeam(ctx, id)
	}
	args = append(args, id)
	if _, err := s.db.ExecContext(ctx, `UPDATE teams SET `+strings.Join(sets, ", ")+`, updated_at = unixepoch() WHERE id = ?`, args...); err != nil {
		return nil, err
	}
	return s.GetTeam(ctx, id)
}

func (s *Service) DeleteTeam(ctx context.Context, id string) error {
	var appCount int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM apps WHERE team_id = ?`, id).Scan(&appCount); err != nil {
		return err
	}
	if appCount > 0 {
		return ErrTeamHasApps
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM teams WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Service) SetTeamMembers(ctx context.Context, teamID string, members []TeamMemberInput) (*Team, error) {
	if _, err := s.GetTeam(ctx, teamID); err != nil {
		return nil, err
	}
	members = s.withDefaultTeamRole(ctx, members)
	if err := s.validateTeamMemberInputs(ctx, members); err != nil {
		return nil, err
	}
	if err := s.replaceTeamMembers(ctx, teamID, members); err != nil {
		return nil, err
	}
	return s.GetTeam(ctx, teamID)
}

func (s *Service) MembersForTeam(ctx context.Context, teamID string) ([]Member, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT u.id, u.email, u.name, u.avatar_path, u.status, u.created_at,
		       r.id, r.slug, r.name, r.description, r.scope, r.is_admin, r.is_system, r.permissions, r.created_at, r.updated_at
		FROM users u
		JOIN team_members tm ON tm.user_id = u.id
		LEFT JOIN roles r ON r.id = u.role_id
		WHERE tm.team_id = ?
		ORDER BY u.email ASC
	`, teamID)
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
		role, err := s.teamRoleForMember(ctx, teamID, out[i].ID)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return nil, err
		}
		out[i].TeamRole = role
		teams, err := s.teamsForMember(ctx, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Teams = teams
	}
	return out, nil
}

func (s *Service) validateTeams(ctx context.Context, ids []string) error {
	for _, id := range uniqueStrings(ids) {
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM teams WHERE id = ?`, id).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
	}
	return nil
}

func (s *Service) validateTeamMemberInputs(ctx context.Context, members []TeamMemberInput) error {
	for _, m := range normaliseTeamMembers(members) {
		if strings.TrimSpace(m.TeamID) != "" {
			if err := s.validateTeams(ctx, []string{m.TeamID}); err != nil {
				return err
			}
		}
		if strings.TrimSpace(m.UserID) == "" {
			return ErrNotFound
		}
		if err := s.validateMembers(ctx, []string{m.UserID}); err != nil {
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

func (s *Service) replaceTeamMembers(ctx context.Context, teamID string, members []TeamMemberInput) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM team_members WHERE team_id = ?`, teamID); err != nil {
		return err
	}
	for _, m := range normaliseTeamMembers(members) {
		if _, err := tx.ExecContext(ctx, `INSERT INTO team_members(team_id, user_id, role_id) VALUES (?, ?, ?)`, teamID, m.UserID, m.RoleID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Service) teamsForMember(ctx context.Context, userID string) ([]Team, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.id, t.name, t.slug, t.description, t.color, t.avatar_path, t.created_at, t.updated_at,
		       (SELECT COUNT(*) FROM team_members tm WHERE tm.team_id = t.id),
		       (SELECT COUNT(*) FROM apps a WHERE a.team_id = t.id)
		FROM teams t
		JOIN team_members tm ON tm.team_id = t.id
		WHERE tm.user_id = ?
		ORDER BY t.name ASC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Team
	for rows.Next() {
		t, err := scanTeam(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

func (s *Service) teamRoleForMember(ctx context.Context, teamID, userID string) (*Role, error) {
	role, err := scanRole(s.db.QueryRowContext(ctx, `
		SELECT r.id, r.slug, r.name, r.description, r.scope, r.is_admin, r.is_system, r.permissions, r.created_at, r.updated_at
		FROM team_members tm
		JOIN roles r ON r.id = tm.role_id
		WHERE tm.team_id = ? AND tm.user_id = ?
	`, teamID, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return role, err
}

func scanTeam(row interface{ Scan(dest ...any) error }) (*Team, error) {
	var t Team
	var desc, avatar sql.NullString
	if err := row.Scan(&t.ID, &t.Name, &t.Slug, &desc, &t.Color, &avatar, &t.CreatedAt, &t.UpdatedAt, &t.MemberCount, &t.AppCount); err != nil {
		return nil, err
	}
	t.Description = desc.String
	t.AvatarPath = avatar.String
	t.AvatarURL = avatarURL(avatar.String)
	return &t, nil
}

func (s *Service) defaultTeamMemberInputs(memberIDs []string) []TeamMemberInput {
	roleID := s.defaultTeamRoleID(context.Background())
	out := make([]TeamMemberInput, 0, len(memberIDs))
	for _, id := range uniqueStrings(memberIDs) {
		out = append(out, TeamMemberInput{UserID: id, RoleID: roleID})
	}
	return out
}

func (s *Service) defaultTeamRoleID(ctx context.Context) string {
	var id string
	for _, slug := range []string{"developer", "viewer"} {
		if err := s.db.QueryRowContext(ctx, `SELECT id FROM roles WHERE slug = ?`, slug).Scan(&id); err == nil && id != "" {
			return id
		}
	}
	return ""
}

func (s *Service) withDefaultTeamRole(ctx context.Context, in []TeamMemberInput) []TeamMemberInput {
	roleID := s.defaultTeamRoleID(ctx)
	out := make([]TeamMemberInput, 0, len(in))
	for _, m := range in {
		if strings.TrimSpace(m.RoleID) == "" {
			m.RoleID = roleID
		}
		out = append(out, m)
	}
	return out
}

func normaliseTeamMembers(in []TeamMemberInput) []TeamMemberInput {
	seen := map[string]struct{}{}
	out := []TeamMemberInput{}
	for _, m := range in {
		m.TeamID = strings.TrimSpace(m.TeamID)
		m.UserID = strings.TrimSpace(m.UserID)
		m.RoleID = strings.TrimSpace(m.RoleID)
		key := m.TeamID + "\x00" + m.UserID
		if m.RoleID == "" || key == "\x00" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, m)
	}
	return out
}
