// Package apitoken implements personal API tokens (PATs) — long-lived
// credentials a user creates for non-interactive callers (CI, scripts,
// CLI on a remote box) to authenticate against the API.
//
// Design notes worth restating in code:
//   - Tokens authenticate AS the owning user; there is no separate scope
//     matrix. Permission checks elsewhere read the user's RBAC, same as
//     a session-cookie request would. This deliberately keeps the auth
//     surface boring — if you can't deploy logged in, your PAT can't
//     either.
//   - Raw token strings live in memory exactly once: at Create time we
//     return the string to the handler so the UI can show it; everything
//     persisted is `sha256(raw)`. Lose the raw and there's no recovery.
//   - last_used_at is "best effort" — we update it on every authenticated
//     request, but if the underlying UPDATE fails we don't fail the
//     request. Stats UI is informational, not authoritative.
package apitoken

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// TokenPrefix is the human-recognisable namespace at the start of every
// raw token string. We use this to route requests in the auth middleware
// (PAT vs. JWT) without parsing the full payload.
const TokenPrefix = "prx_pat_"

// Token is the persisted view of a PAT. The raw secret is NEVER part of
// this struct — only the SHA-256 hash and the last four characters for
// the UI listing.
type Token struct {
	ID         string     `json:"id"`
	UserID     string     `json:"user_id"`
	Name       string     `json:"name"`
	LastFour   string     `json:"last_four"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// CreateResult is what Create returns. `Raw` is the only place the
// plaintext token ever shows up — the handler relays it to the UI which
// shows it ONCE and discards. We deliberately model this as a separate
// type so callers can't accidentally serialise a Token and leak the
// secret (it isn't on the Token struct at all).
type CreateResult struct {
	Token Token
	Raw   string
}

// Errors used by Authenticate so callers can distinguish "no such token"
// from "token expired/revoked" if they want richer telemetry. Most
// production paths just map all of them to 401.
var (
	ErrNotFound   = errors.New("apitoken: not found")
	ErrRevoked    = errors.New("apitoken: revoked")
	ErrExpired    = errors.New("apitoken: expired")
	ErrBadName    = errors.New("apitoken: name required (1-100 chars)")
	ErrBadExpiry  = errors.New("apitoken: expiry must be in the future")
	ErrBadFormat  = errors.New("apitoken: malformed token")
	ErrForbidden  = errors.New("apitoken: not your token")
)

type Service struct {
	db *sql.DB
}

func NewService(db *sql.DB) *Service {
	if db == nil {
		panic("apitoken: db is required")
	}
	return &Service{db: db}
}

// Create issues a new token for the given user. `expiresAt` may be nil
// (token never expires) or any future time. Returns the raw token
// string exactly once — the caller MUST surface it to the human and
// then drop it; the DB only keeps the hash.
func (s *Service) Create(ctx context.Context, userID, name string, expiresAt *time.Time) (*CreateResult, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 {
		return nil, ErrBadName
	}
	if expiresAt != nil && !expiresAt.After(time.Now()) {
		return nil, ErrBadExpiry
	}

	raw, err := generateRaw()
	if err != nil {
		return nil, fmt.Errorf("apitoken: generate: %w", err)
	}
	hash := hashRaw(raw)
	lastFour := raw[len(raw)-4:]

	idBytes := make([]byte, 6)
	if _, err := rand.Read(idBytes); err != nil {
		return nil, fmt.Errorf("apitoken: id: %w", err)
	}
	id := "tok_" + hex.EncodeToString(idBytes)
	now := time.Now()

	var expiresEpoch *int64
	if expiresAt != nil {
		e := expiresAt.Unix()
		expiresEpoch = &e
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO api_tokens
			(id, user_id, name, token_hash, last_four, expires_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, id, userID, name, hash, lastFour, expiresEpoch, now.Unix(), now.Unix())
	if err != nil {
		return nil, fmt.Errorf("apitoken: insert: %w", err)
	}

	tok := Token{
		ID:        id,
		UserID:    userID,
		Name:      name,
		LastFour:  lastFour,
		ExpiresAt: expiresAt,
		CreatedAt: now,
		UpdatedAt: now,
	}
	return &CreateResult{Token: tok, Raw: raw}, nil
}

// List returns every active token for the user, ordered newest-first.
// Revoked tokens are excluded (we keep the row for audit but the UI
// doesn't need to show them — at least not in v0.1).
func (s *Service) List(ctx context.Context, userID string) ([]Token, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, name, last_four, expires_at, last_used_at, revoked_at, created_at, updated_at
		FROM api_tokens
		WHERE user_id = ? AND revoked_at IS NULL
		ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("apitoken: list: %w", err)
	}
	defer rows.Close()

	out := []Token{}
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Revoke marks a token as no longer usable. We require userID so a
// compromised handler can't accidentally revoke someone else's token
// just by passing the right id; the caller is expected to use the
// authenticated user's id here.
func (s *Service) Revoke(ctx context.Context, userID, tokenID string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE api_tokens
		   SET revoked_at = strftime('%s', 'now')
		 WHERE id = ? AND user_id = ? AND revoked_at IS NULL
	`, tokenID, userID)
	if err != nil {
		return fmt.Errorf("apitoken: revoke: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		// Either it doesn't exist, was already revoked, or belongs to a
		// different user. We don't tell the caller which — same response
		// either way avoids leaking token existence.
		return ErrNotFound
	}
	return nil
}

// Authenticate is the hot path called by the auth middleware on every
// PAT-bearing request. Returns the owning user_id on success.
//
// We do NOT update last_used_at synchronously in this function — the
// middleware fires that off after the request completes so a slow
// network on the audit write never delays the request itself.
func (s *Service) Authenticate(ctx context.Context, raw string) (string, error) {
	if !strings.HasPrefix(raw, TokenPrefix) || len(raw) < len(TokenPrefix)+16 {
		return "", ErrBadFormat
	}
	hash := hashRaw(raw)

	var (
		id, userID string
		revokedAt  sql.NullInt64
		expiresAt  sql.NullInt64
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT id, user_id, revoked_at, expires_at
		  FROM api_tokens
		 WHERE token_hash = ?
	`, hash).Scan(&id, &userID, &revokedAt, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("apitoken: authenticate: %w", err)
	}
	if revokedAt.Valid {
		return "", ErrRevoked
	}
	if expiresAt.Valid && time.Unix(expiresAt.Int64, 0).Before(time.Now()) {
		return "", ErrExpired
	}
	return userID, nil
}

// TouchLastUsed updates the timestamp asynchronously from the request's
// happy path. Pass the token id returned alongside the user_id from
// Authenticate (we look it up by hash there). Errors are swallowed:
// last_used_at is best-effort telemetry, not auth state.
func (s *Service) TouchLastUsed(ctx context.Context, raw string) {
	hash := hashRaw(raw)
	_, _ = s.db.ExecContext(ctx, `
		UPDATE api_tokens SET last_used_at = strftime('%s', 'now') WHERE token_hash = ?
	`, hash)
}

// ── helpers ──────────────────────────────────────────────────────────

// generateRaw produces a fresh `prx_pat_<…>` string. 32 bytes of CSPRNG
// → 256 bits of entropy, base64-url encoded (no padding) gives 43 chars
// of body. Total length: 8 + 43 = 51 chars, all URL-safe.
func generateRaw() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return TokenPrefix + base64.RawURLEncoding.EncodeToString(buf), nil
}

func hashRaw(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// scanRow narrows the *sql.Rows / *sql.Row interface so we can share
// scanning between List and a future Get.
type scanRow interface {
	Scan(dest ...any) error
}

func scanToken(r scanRow) (Token, error) {
	var (
		t           Token
		expiresAt   sql.NullInt64
		lastUsedAt  sql.NullInt64
		revokedAt   sql.NullInt64
		createdAt   int64
		updatedAt   int64
	)
	err := r.Scan(&t.ID, &t.UserID, &t.Name, &t.LastFour,
		&expiresAt, &lastUsedAt, &revokedAt, &createdAt, &updatedAt)
	if err != nil {
		return Token{}, fmt.Errorf("apitoken: scan: %w", err)
	}
	if expiresAt.Valid {
		v := time.Unix(expiresAt.Int64, 0)
		t.ExpiresAt = &v
	}
	if lastUsedAt.Valid {
		v := time.Unix(lastUsedAt.Int64, 0)
		t.LastUsedAt = &v
	}
	if revokedAt.Valid {
		v := time.Unix(revokedAt.Int64, 0)
		t.RevokedAt = &v
	}
	t.CreatedAt = time.Unix(createdAt, 0)
	t.UpdatedAt = time.Unix(updatedAt, 0)
	return t, nil
}
