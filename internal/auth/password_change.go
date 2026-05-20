package auth

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrUserNotFound is returned when the user id does not match any row.
var ErrUserNotFound = errors.New("auth: user not found")

// ChangePassword updates the user's bcrypt hash and invalidates every active
// refresh token belonging to that user (Tech Review §10). The new password is
// expected to have been validated by the caller.
func ChangePassword(db *sql.DB, userID, newPlain string) error {
	hash, err := HashPassword(newPlain)
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.Exec(`UPDATE users SET password_hash = ? WHERE id = ?`, hash, userID)
	if err != nil {
		return fmt.Errorf("update user: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrUserNotFound
	}
	if _, err := tx.Exec(
		`UPDATE refresh_tokens SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL`,
		time.Now().Unix(), userID,
	); err != nil {
		return fmt.Errorf("revoke refresh tokens: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}
