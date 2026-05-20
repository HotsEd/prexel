package rbac

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

func (s *Service) SetUserAvatar(ctx context.Context, userID, path string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE users SET avatar_path = ? WHERE id = ?`, path, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Service) AvatarPath(ctx context.Context, userID string) (string, error) {
	var p sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT avatar_path FROM users WHERE id = ?`, userID).Scan(&p); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", err
	}
	return p.String, nil
}

func avatarURL(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	return "/api/v1/avatars/" + strings.TrimSpace(path)
}
