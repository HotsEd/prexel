package rbac

import (
	"context"
	"errors"
	"strings"
)

func (s *Service) HasPermission(ctx context.Context, userID, permission string) (bool, error) {
	u, err := s.UserContext(ctx, userID)
	if err != nil {
		return false, err
	}
	if u.Status != "active" {
		return false, nil
	}
	if u.IsAdmin {
		return true, nil
	}
	for _, p := range u.Permissions {
		if p == permission {
			return true, nil
		}
	}
	return false, nil
}

func (s *Service) Require(ctx context.Context, userID, permission string) error {
	ok, err := s.HasPermission(ctx, userID, permission)
	if err != nil {
		return err
	}
	if !ok {
		return ErrForbidden
	}
	return nil
}

func (s *Service) CanAccessTeam(ctx context.Context, userID string, teamID *string, permission string) (bool, error) {
	u, err := s.UserContext(ctx, userID)
	if err != nil {
		return false, err
	}
	if u.Status != "active" {
		return false, nil
	}
	if u.IsAdmin || hasPermission(u.Permissions, permission) {
		return true, nil
	}
	if teamID == nil || strings.TrimSpace(*teamID) == "" {
		return false, nil
	}
	return s.HasTeamPermission(ctx, userID, strings.TrimSpace(*teamID), permission)
}

func (s *Service) HasTeamPermission(ctx context.Context, userID, teamID, permission string) (bool, error) {
	u, err := s.UserContext(ctx, userID)
	if err != nil {
		return false, err
	}
	if u.Status != "active" {
		return false, nil
	}
	if u.IsAdmin {
		return true, nil
	}
	role, err := s.teamRoleForMember(ctx, teamID, userID)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if role == nil {
		return false, nil
	}
	if role.IsAdmin {
		return true, nil
	}
	return hasPermission(role.Permissions, permission), nil
}

func (s *Service) RequireTeam(ctx context.Context, userID, teamID, permission string) error {
	ok, err := s.HasTeamPermission(ctx, userID, teamID, permission)
	if err != nil {
		return err
	}
	if !ok {
		return ErrForbidden
	}
	return nil
}

func hasPermission(perms []string, permission string) bool {
	for _, p := range perms {
		if p == permission {
			return true
		}
	}
	return false
}
