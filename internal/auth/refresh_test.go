package auth

import (
	"errors"
	"testing"
	"time"
)

func TestIssueRefreshTokenInFamily(t *testing.T) {
	db := newTestDB(t)
	raw, family, exp, err := IssueRefreshTokenInFamily(db, "u1")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if raw == "" || family == "" {
		t.Fatal("empty raw/family")
	}
	if time.Until(exp) > RefreshTokenTTL+time.Minute || time.Until(exp) <= 0 {
		t.Errorf("bad expiry: %v", exp)
	}
	// Verify row exists with hashed token.
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM refresh_tokens WHERE token_hash = ?`, HashToken(raw)).Scan(&count); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 row, got %d", count)
	}
}

func TestRotateRefreshToken_HappyPath(t *testing.T) {
	db := newTestDB(t)
	raw, family, _, _ := IssueRefreshTokenInFamily(db, "u1")

	newRaw, access, _, err := RotateRefreshToken(db, jwtTestSecret, raw)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if newRaw == raw {
		t.Error("new raw equals old")
	}
	if access == "" {
		t.Error("empty access token")
	}

	// Old token must be revoked.
	var revoked int64
	if err := db.QueryRow(`SELECT IFNULL(revoked_at, 0) FROM refresh_tokens WHERE token_hash = ?`, HashToken(raw)).Scan(&revoked); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if revoked == 0 {
		t.Error("old token should be revoked")
	}

	// New token must be in same family.
	var newFamily string
	if err := db.QueryRow(`SELECT family_id FROM refresh_tokens WHERE token_hash = ?`, HashToken(newRaw)).Scan(&newFamily); err != nil {
		t.Fatalf("scan family: %v", err)
	}
	if newFamily != family {
		t.Errorf("family mismatch: got %s want %s", newFamily, family)
	}
}

func TestRotateRefreshToken_ReuseInvalidatesFamily(t *testing.T) {
	db := newTestDB(t)
	raw, family, _, _ := IssueRefreshTokenInFamily(db, "u1")

	// First rotation: ok.
	newRaw, _, _, err := RotateRefreshToken(db, jwtTestSecret, raw)
	if err != nil {
		t.Fatalf("first rotate: %v", err)
	}

	// Reuse old (already revoked) token — must trip ErrTokenReuse + invalidate family.
	_, _, _, err = RotateRefreshToken(db, jwtTestSecret, raw)
	if !errors.Is(err, ErrTokenReuse) {
		t.Fatalf("expected ErrTokenReuse, got %v", err)
	}

	// All rows in the family must now be revoked.
	var live int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM refresh_tokens WHERE family_id = ? AND revoked_at IS NULL`, family,
	).Scan(&live); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if live != 0 {
		t.Errorf("expected 0 live tokens in family after reuse, got %d", live)
	}

	// The new raw issued after first rotation should now also be rejected.
	if _, _, _, err := RotateRefreshToken(db, jwtTestSecret, newRaw); err == nil {
		t.Error("expected rotation of family-invalidated token to fail")
	}
}

func TestRotateRefreshToken_RejectsUnknown(t *testing.T) {
	db := newTestDB(t)
	_, _, _, err := RotateRefreshToken(db, jwtTestSecret, "totally-bogus-token")
	if !errors.Is(err, ErrInvalidRefreshToken) {
		t.Errorf("expected ErrInvalidRefreshToken, got %v", err)
	}
}

func TestRotateRefreshToken_RejectsExpired(t *testing.T) {
	db := newTestDB(t)
	// Insert a row directly with a past expiry.
	raw := "expired-raw-token"
	hash := HashToken(raw)
	past := time.Now().Add(-time.Hour).Unix()
	_, err := db.Exec(
		`INSERT INTO refresh_tokens(id, user_id, family_id, token_hash, expires_at) VALUES (?, ?, ?, ?, ?)`,
		"id-x", "u1", "fam-x", hash, past,
	)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, _, _, err := RotateRefreshToken(db, jwtTestSecret, raw); !errors.Is(err, ErrInvalidRefreshToken) {
		t.Errorf("expected ErrInvalidRefreshToken for expired, got %v", err)
	}
}

func TestRotateRefreshToken_RejectsEmpty(t *testing.T) {
	db := newTestDB(t)
	if _, _, _, err := RotateRefreshToken(db, jwtTestSecret, ""); !errors.Is(err, ErrInvalidRefreshToken) {
		t.Errorf("expected ErrInvalidRefreshToken, got %v", err)
	}
}

func TestRevokeRefreshToken(t *testing.T) {
	db := newTestDB(t)
	raw, _, _, _ := IssueRefreshTokenInFamily(db, "u1")

	if err := RevokeRefreshToken(db, raw); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	// Idempotent.
	if err := RevokeRefreshToken(db, raw); err != nil {
		t.Fatalf("second revoke: %v", err)
	}
	if err := RevokeRefreshToken(db, ""); err != nil {
		t.Fatalf("empty: %v", err)
	}

	// Once revoked, rotation must trip reuse.
	if _, _, _, err := RotateRefreshToken(db, jwtTestSecret, raw); !errors.Is(err, ErrTokenReuse) {
		t.Errorf("expected ErrTokenReuse after manual revoke, got %v", err)
	}
}

func TestHashToken_Stable(t *testing.T) {
	if HashToken("abc") != HashToken("abc") {
		t.Error("HashToken not deterministic")
	}
	if HashToken("a") == HashToken("b") {
		t.Error("HashToken collision for different inputs")
	}
}
