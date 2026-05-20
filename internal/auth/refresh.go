package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// RefreshTokenTTL is the lifetime of issued refresh tokens. 7 days per Setup&Auth.
const RefreshTokenTTL = 7 * 24 * time.Hour

// ErrTokenReuse is returned when a revoked refresh token is presented again;
// the entire family is invalidated when this happens.
var ErrTokenReuse = errors.New("auth: refresh token reuse detected")

// ErrInvalidRefreshToken signals an unknown or expired refresh token.
var ErrInvalidRefreshToken = errors.New("auth: invalid refresh token")

// generateRawToken returns 32 bytes of randomness encoded as base64url (no pad).
func generateRawToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("rand: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// HashToken returns the hex SHA-256 of the raw token. Raw tokens never touch
// the database.
func HashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// IssueRefreshTokenInFamily creates a new refresh token row with a fresh
// family_id and returns the raw token, the family id, and absolute expiry.
func IssueRefreshTokenInFamily(db *sql.DB, userID string) (string, string, time.Time, error) {
	raw, err := generateRawToken()
	if err != nil {
		return "", "", time.Time{}, err
	}
	family := uuid.NewString()
	id := uuid.NewString()
	exp := time.Now().Add(RefreshTokenTTL)
	_, err = db.Exec(
		`INSERT INTO refresh_tokens(id, user_id, family_id, token_hash, expires_at) VALUES (?, ?, ?, ?, ?)`,
		id, userID, family, HashToken(raw), exp.Unix(),
	)
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("insert refresh: %w", err)
	}
	return raw, family, exp, nil
}

// issueInFamily inserts a fresh refresh token row reusing an existing family.
func issueInFamily(db *sql.DB, userID, familyID string) (string, time.Time, error) {
	raw, err := generateRawToken()
	if err != nil {
		return "", time.Time{}, err
	}
	id := uuid.NewString()
	exp := time.Now().Add(RefreshTokenTTL)
	_, err = db.Exec(
		`INSERT INTO refresh_tokens(id, user_id, family_id, token_hash, expires_at) VALUES (?, ?, ?, ?, ?)`,
		id, userID, familyID, HashToken(raw), exp.Unix(),
	)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("insert refresh: %w", err)
	}
	return raw, exp, nil
}

// RotateRefreshToken implements token-family rotation (Tech Review §9). If the
// presented raw token matches a still-valid row it is revoked and a new row is
// issued in the same family. If the row exists but is already revoked, the
// entire family is invalidated and ErrTokenReuse is returned. The caller is
// expected to also issue a new access token alongside.
func RotateRefreshToken(db *sql.DB, jwtSecret, raw string) (newRaw, accessToken string, expiresAt time.Time, err error) {
	if raw == "" {
		return "", "", time.Time{}, ErrInvalidRefreshToken
	}
	hash := HashToken(raw)

	tx, err := db.Begin()
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("begin: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var (
		id, userID, familyID string
		revokedAt            sql.NullInt64
		expires              int64
	)
	row := tx.QueryRow(
		`SELECT id, user_id, family_id, revoked_at, expires_at FROM refresh_tokens WHERE token_hash = ?`,
		hash,
	)
	if scanErr := row.Scan(&id, &userID, &familyID, &revokedAt, &expires); scanErr != nil {
		if errors.Is(scanErr, sql.ErrNoRows) {
			err = ErrInvalidRefreshToken
			return "", "", time.Time{}, err
		}
		err = fmt.Errorf("select refresh: %w", scanErr)
		return "", "", time.Time{}, err
	}

	now := time.Now().Unix()
	if expires < now {
		err = ErrInvalidRefreshToken
		return "", "", time.Time{}, err
	}

	if revokedAt.Valid {
		// Reuse detected. Invalidate the entire family.
		if _, execErr := tx.Exec(
			`UPDATE refresh_tokens SET revoked_at = ? WHERE family_id = ? AND revoked_at IS NULL`,
			now, familyID,
		); execErr != nil {
			err = fmt.Errorf("revoke family: %w", execErr)
			return "", "", time.Time{}, err
		}
		if commitErr := tx.Commit(); commitErr != nil {
			err = fmt.Errorf("commit: %w", commitErr)
			return "", "", time.Time{}, err
		}
		slog.Warn("refresh token reuse detected — family invalidated", "family_id", familyID, "user_id", userID)
		return "", "", time.Time{}, ErrTokenReuse
	}

	// Revoke the current row.
	if _, execErr := tx.Exec(`UPDATE refresh_tokens SET revoked_at = ? WHERE id = ?`, now, id); execErr != nil {
		err = fmt.Errorf("revoke current: %w", execErr)
		return "", "", time.Time{}, err
	}

	// Issue a new row in the same family.
	newRawToken, err := generateRawToken()
	if err != nil {
		return "", "", time.Time{}, err
	}
	newID := uuid.NewString()
	exp := time.Now().Add(RefreshTokenTTL)
	if _, execErr := tx.Exec(
		`INSERT INTO refresh_tokens(id, user_id, family_id, token_hash, expires_at) VALUES (?, ?, ?, ?, ?)`,
		newID, userID, familyID, HashToken(newRawToken), exp.Unix(),
	); execErr != nil {
		err = fmt.Errorf("insert new refresh: %w", execErr)
		return "", "", time.Time{}, err
	}

	access, _, accErr := IssueAccessToken(jwtSecret, userID)
	if accErr != nil {
		err = accErr
		return "", "", time.Time{}, err
	}

	if commitErr := tx.Commit(); commitErr != nil {
		err = fmt.Errorf("commit: %w", commitErr)
		return "", "", time.Time{}, err
	}
	return newRawToken, access, exp, nil
}

// RevokeRefreshToken marks a single refresh token as revoked (idempotent).
// Used by logout. Unknown tokens are silently ignored.
func RevokeRefreshToken(db *sql.DB, raw string) error {
	if raw == "" {
		return nil
	}
	_, err := db.Exec(
		`UPDATE refresh_tokens SET revoked_at = ? WHERE token_hash = ? AND revoked_at IS NULL`,
		time.Now().Unix(), HashToken(raw),
	)
	return err
}
