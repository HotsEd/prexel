package auth

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"
	_ "modernc.org/sqlite"
)

func TestGenerateTOTPSecret_ReturnsValidOtpauth(t *testing.T) {
	secret, url, err := GenerateTOTPSecret("admin@example.com", "Prexel")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if secret == "" {
		t.Fatal("empty secret")
	}
	if !strings.HasPrefix(url, "otpauth://totp/") {
		t.Errorf("unexpected otpauth url: %s", url)
	}
	if !strings.Contains(url, "Prexel") {
		t.Errorf("issuer not embedded: %s", url)
	}
	if !strings.Contains(url, "admin@example.com") {
		t.Errorf("account not embedded: %s", url)
	}
}

func TestValidateTOTP_AcceptsCurrentCode(t *testing.T) {
	secret, _, err := GenerateTOTPSecret("admin@example.com", "Prexel")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("code: %v", err)
	}
	if !ValidateTOTP(secret, code) {
		t.Error("current code rejected")
	}
}

func TestValidateTOTP_RejectsWrongCode(t *testing.T) {
	secret, _, err := GenerateTOTPSecret("admin@example.com", "Prexel")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if ValidateTOTP(secret, "000000") {
		// Astronomically rare to be the actual code, but defensible.
		t.Log("000000 happened to be the live code; rerun the test")
	}
	if ValidateTOTP(secret, "") {
		t.Error("empty code accepted")
	}
	if ValidateTOTP("", "123456") {
		t.Error("empty secret accepted")
	}
}

func TestValidateTOTP_RejectsAncientCode(t *testing.T) {
	secret, _, err := GenerateTOTPSecret("admin@example.com", "Prexel")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	// A code from 10 minutes ago is far outside the ±1 step skew window.
	old, err := totp.GenerateCode(secret, time.Now().Add(-10*time.Minute))
	if err != nil {
		t.Fatalf("old code: %v", err)
	}
	if ValidateTOTP(secret, old) {
		t.Errorf("stale code accepted: %s", old)
	}
}

func TestTOTPQRCodePNG_NonEmpty(t *testing.T) {
	_, url, err := GenerateTOTPSecret("admin@example.com", "Prexel")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	png, err := TOTPQRCodePNG(url)
	if err != nil {
		t.Fatalf("qr: %v", err)
	}
	if len(png) < 100 {
		t.Errorf("png suspiciously small: %d bytes", len(png))
	}
	// PNG magic.
	if !(png[0] == 0x89 && png[1] == 0x50 && png[2] == 0x4E && png[3] == 0x47) {
		t.Error("not a PNG")
	}
}

// openReplayDB spins up the smallest schema ValidateAndConsumeTOTP
// needs: just the totp_used table. We skip the users FK referenced by
// migration 014 (ON DELETE CASCADE only matters when users disappear,
// which doesn't happen in these tests) so the fixture stays focused.
func openReplayDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := "file:totp_replay_" + uuid.NewString() + "?mode=memory&cache=shared"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE totp_used (
		user_id   TEXT    NOT NULL,
		code      TEXT    NOT NULL,
		window_ts INTEGER NOT NULL,
		PRIMARY KEY (user_id, code, window_ts)
	)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	return db
}

func TestValidateAndConsumeTOTP_RejectsReplayInSameWindow(t *testing.T) {
	db := openReplayDB(t)
	secret, _, err := GenerateTOTPSecret("admin@example.com", "Prexel")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("code: %v", err)
	}
	ok, err := ValidateAndConsumeTOTP(context.Background(), db, "user-1", secret, code)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if !ok {
		t.Fatal("first call rejected a valid code")
	}
	// Same code in the same 30s window — must be rejected with ErrCodeReused.
	ok2, err := ValidateAndConsumeTOTP(context.Background(), db, "user-1", secret, code)
	if !errors.Is(err, ErrCodeReused) {
		t.Errorf("second call: expected ErrCodeReused, got err=%v ok=%v", err, ok2)
	}
	if ok2 {
		t.Error("second call accepted")
	}
}

func TestValidateAndConsumeTOTP_DifferentUsersAreIndependent(t *testing.T) {
	db := openReplayDB(t)
	secret, _, _ := GenerateTOTPSecret("admin@example.com", "Prexel")
	code, _ := totp.GenerateCode(secret, time.Now())

	if ok, err := ValidateAndConsumeTOTP(context.Background(), db, "alice", secret, code); err != nil || !ok {
		t.Fatalf("alice: %v %v", ok, err)
	}
	// Bob using the same code (he wouldn't normally — different secret —
	// but the replay table is keyed on user, so this proves alice's row
	// doesn't poison bob's bucket).
	if ok, err := ValidateAndConsumeTOTP(context.Background(), db, "bob", secret, code); err != nil || !ok {
		t.Fatalf("bob: %v %v", ok, err)
	}
}

func TestValidateAndConsumeTOTP_RejectsInvalidCodeWithoutTouchingDB(t *testing.T) {
	db := openReplayDB(t)
	secret, _, _ := GenerateTOTPSecret("admin@example.com", "Prexel")
	ok, err := ValidateAndConsumeTOTP(context.Background(), db, "user-1", secret, "000000")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if ok {
		t.Error("invalid code accepted")
	}
	// Table must be empty — a wrong code should never claim a slot.
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM totp_used`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 rows, got %d", n)
	}
}
