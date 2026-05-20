package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	qrcode "github.com/skip2/go-qrcode"
)

// TOTP wraps github.com/pquerna/otp/totp with the parameters Prexel uses
// across the 2FA flow. RFC 6238 with 30-second period, SHA-1, six digits —
// the de-facto profile understood by Google Authenticator, Authy, 1Password.

// ErrCodeReused is returned by ValidateAndConsumeTOTP when the same code
// has already been accepted within its 30-second window. Callers that
// see this error should map it to the same user-visible response as
// "invalid code" — leaking the distinction back to the client would
// confirm to a phisher that the stolen code was correct.
var ErrCodeReused = errors.New("auth: totp code already used")

// totpWindowSec is the RFC 6238 step size we use. Mirrored from
// GenerateTOTPSecret so the replay-window arithmetic stays in lockstep
// with the validator.
const totpWindowSec int64 = 30

// totpReplayCleanupSec is the TTL we apply to totp_used rows before
// they're garbage-collected inline. 90s = current window ± one step
// of skew the library allows. Older rows can't possibly collide.
const totpReplayCleanupSec int64 = 90

// GenerateTOTPSecret returns a base32 secret plus its otpauth:// URL suitable
// for embedding in a QR code. issuer is typically "Prexel" (config-driven so
// operators can rename the entry in their authenticator app).
func GenerateTOTPSecret(accountName, issuer string) (secret, otpauthURL string, err error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      issuer,
		AccountName: accountName,
		Period:      30,
		Digits:      otp.DigitsSix,
		Algorithm:   otp.AlgorithmSHA1,
	})
	if err != nil {
		return "", "", fmt.Errorf("totp generate: %w", err)
	}
	return key.Secret(), key.URL(), nil
}

// ValidateTOTP reports whether code is the current valid TOTP for secret.
// The underlying library tolerates a one-step skew (±30s) which is what we
// want for clock drift on the user's phone.
//
// This helper is replay-blind on purpose: it's used by setup paths where
// no user_id exists yet (the pending secret is bound to a row, not yet
// activated). For authenticated flows (login challenge, password change,
// 2FA disable) callers should use ValidateAndConsumeTOTP which keys the
// guard off user_id.
func ValidateTOTP(secret, code string) bool {
	if secret == "" || code == "" {
		return false
	}
	return totp.Validate(code, secret)
}

// ValidateAndConsumeTOTP performs ValidateTOTP and, on success, claims
// the (user_id, code, window) tuple via INSERT into totp_used. A second
// call inside the same 30s window with the same code returns
// ErrCodeReused without re-validating — the user must wait for a new
// code or fall back to a recovery code.
//
// Returns:
//   - (true, nil) when the code is valid AND this is the first time
//     it's been accepted in this window;
//   - (false, nil) when the code is invalid (wrong digits, ancient,
//     etc.) — caller treats this as a normal failed attempt;
//   - (false, ErrCodeReused) when the code is valid but already
//     consumed — caller treats this as a failed attempt too, but may
//     want to log it specifically (replay attempts are noteworthy).
//   - (false, err) for storage errors.
//
// The function deletes rows older than 90s on every call so the table
// never grows unbounded. We accept the small extra cost of the DELETE
// to avoid a background sweeper goroutine that would need its own
// lifecycle plumbing in main.go.
func ValidateAndConsumeTOTP(ctx context.Context, db *sql.DB, userID, secret, code string) (bool, error) {
	if !ValidateTOTP(secret, code) {
		return false, nil
	}
	if db == nil || strings.TrimSpace(userID) == "" {
		// Replay guard requires a DB binding + identity. If neither is
		// configured we still want to short-circuit so tests of the
		// pure validation path don't need to wire a DB. Treat this as
		// "accepted, not recorded" — the production paths always pass
		// both.
		return true, nil
	}
	now := time.Now().Unix()
	windowTS := (now / totpWindowSec) * totpWindowSec
	cutoff := now - totpReplayCleanupSec

	// Cleanup runs first so the table stays small. Errors here are
	// non-fatal — if cleanup fails the INSERT still enforces the
	// uniqueness guarantee.
	_, _ = db.ExecContext(ctx, `DELETE FROM totp_used WHERE window_ts < ?`, cutoff)

	_, err := db.ExecContext(ctx,
		`INSERT INTO totp_used(user_id, code, window_ts) VALUES (?, ?, ?)`,
		userID, code, windowTS,
	)
	if err != nil {
		if isUniqueConstraintErr(err) {
			return false, ErrCodeReused
		}
		return false, fmt.Errorf("totp replay guard: %w", err)
	}
	return true, nil
}

// isUniqueConstraintErr detects the SQLite "UNIQUE / PRIMARY KEY
// constraint failed" error emitted by modernc.org/sqlite. The driver
// exposes the constraint kind only through the error string, so we
// match on the canonical substrings. SQLite uses the same wording
// across versions, and we cross-check the numeric extended codes
// (1555 = SQLITE_CONSTRAINT_PRIMARYKEY, 2067 = SQLITE_CONSTRAINT_UNIQUE)
// so a future driver upgrade that flips between message styles still
// trips the right branch.
func isUniqueConstraintErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") ||
		strings.Contains(msg, "PRIMARY KEY constraint failed") ||
		strings.Contains(msg, "constraint failed: UNIQUE") ||
		strings.Contains(msg, "constraint failed (1555)") ||
		strings.Contains(msg, "constraint failed (2067)")
}

// TOTPQRCodePNG renders an otpauth URL as a 200×200 PNG suitable for the
// setup wizard. The caller is responsible for base64-encoding the bytes
// when shipping over JSON.
func TOTPQRCodePNG(otpauthURL string) ([]byte, error) {
	if otpauthURL == "" {
		return nil, fmt.Errorf("totp: empty otpauth url")
	}
	png, err := qrcode.Encode(otpauthURL, qrcode.Medium, 200)
	if err != nil {
		return nil, fmt.Errorf("qrcode encode: %w", err)
	}
	return png, nil
}
