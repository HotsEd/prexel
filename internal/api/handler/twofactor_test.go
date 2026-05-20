package handler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"
	"github.com/prexel/prexel/internal/api/apimiddleware"
	"github.com/prexel/prexel/internal/auth"
	"github.com/prexel/prexel/internal/crypto"
	"github.com/prexel/prexel/internal/setup"

	_ "modernc.org/sqlite"
)

// twoFactorTestSetup wires an in-memory DB with the 2FA columns plus a fully
// initialised TwoFactorService and Auth handler that knows about it. Returns
// the auth + 2fa handlers and the DB so individual tests can poke around.
func twoFactorTestSetup(t *testing.T) (*Auth, *TwoFactor, *auth.TwoFactorService, *sql.DB) {
	t.Helper()
	// Per-test private DB — avoids cross-test bleed via the shared cache.
	dsn := "file:twofactor_" + uuid.NewString() + "?mode=memory&cache=shared"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	stmts := []string{
		`CREATE TABLE settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at INTEGER NOT NULL DEFAULT (unixepoch())
		)`,
		`CREATE TABLE users (
			id            TEXT PRIMARY KEY,
			email         TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			role          TEXT NOT NULL DEFAULT 'admin',
			created_at    INTEGER NOT NULL DEFAULT (unixepoch()),
			two_factor_enabled BOOLEAN NOT NULL DEFAULT 0,
			two_factor_secret TEXT,
			two_factor_pending_secret TEXT,
			two_factor_confirmed_at DATETIME,
			two_factor_recovery_codes TEXT
		)`,
		`CREATE TABLE refresh_tokens (
			id         TEXT PRIMARY KEY,
			user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			family_id  TEXT NOT NULL,
			token_hash TEXT NOT NULL UNIQUE,
			revoked_at INTEGER,
			expires_at INTEGER NOT NULL,
			created_at INTEGER NOT NULL DEFAULT (unixepoch())
		)`,
		// Replay guard table (migration 014). Required for any flow
		// that calls ValidateAndConsumeTOTP, which is now every
		// challenge/disable path.
		`CREATE TABLE totp_used (
			user_id   TEXT    NOT NULL,
			code      TEXT    NOT NULL,
			window_ts INTEGER NOT NULL,
			PRIMARY KEY (user_id, code, window_ts)
		)`,
	}
	for _, q := range stmts {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("schema: %v", err)
		}
	}

	setupSvc, err := setup.NewService(db)
	if err != nil {
		t.Fatalf("setup svc: %v", err)
	}
	if err := setupSvc.MarkComplete(); err != nil {
		t.Fatalf("mark complete: %v", err)
	}

	cipher, err := crypto.New("test-secret-key-32-chars-minimum-aaaaaa")
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	store := auth.NewChallengeStore()
	svc := auth.NewTwoFactorService(db, cipher, store, "Prexel")

	authH := NewAuthHandler(AuthDeps{
		DB:        db,
		Setup:     setupSvc,
		JWTSecret: jwtTestSecret,
		Secure:    false,
		TwoFactor: svc,
	})
	twoH := NewTwoFactorHandler(TwoFactorDeps{
		DB:        db,
		Service:   svc,
		JWTSecret: jwtTestSecret,
		Secure:    false,
	})
	return authH, twoH, svc, db
}

// callAuthed mints a JWT for userID, builds a request with it, and dispatches
// the request through the real Auth middleware so the handler observes the
// user_id via the package-private context key — same path as production.
func callAuthed(t *testing.T, handler http.HandlerFunc, method, path string, body []byte, userID string) *httptest.ResponseRecorder {
	t.Helper()
	tok, _, err := auth.IssueAccessToken(jwtTestSecret, userID)
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, path, bytes.NewReader(body))
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.Header.Set("Authorization", "Bearer "+tok)

	rec := httptest.NewRecorder()
	apimiddleware.Auth(jwtTestSecret, nil)(handler).ServeHTTP(rec, req)
	return rec
}

// seedUser2FA inserts a user with a known bcrypt password. Returns user_id.
func seedUser2FA(t *testing.T, db *sql.DB, email, plain string) string {
	t.Helper()
	hash, err := auth.HashPassword(plain)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	id := uuid.NewString()
	if _, err := db.Exec(`INSERT INTO users(id, email, password_hash) VALUES (?, ?, ?)`, id, email, hash); err != nil {
		t.Fatalf("insert: %v", err)
	}
	return id
}

func TestInitiateSetup_ReturnsSecretAndQR(t *testing.T) {
	_, twoH, _, db := twoFactorTestSetup(t)
	uid := seedUser2FA(t, db, "admin@example.com", "Strong#Pass1ord")

	body, _ := json.Marshal(map[string]string{"password": "Strong#Pass1ord"})
	rec := callAuthed(t, twoH.InitiateSetup, http.MethodPost, "/api/v1/auth/2fa/setup-initiate", body, uid)
	if rec.Code != http.StatusOK {
		t.Fatalf("initiate: %d body=%s", rec.Code, rec.Body.String())
	}
	resp := decode(t, rec.Body.Bytes())
	if resp["secret"] == "" || resp["otpauth_url"] == "" || resp["qr_code_png_b64"] == "" {
		t.Errorf("missing fields: %+v", resp)
	}
	if !strings.HasPrefix(resp["otpauth_url"].(string), "otpauth://totp/") {
		t.Errorf("bad otpauth url: %v", resp["otpauth_url"])
	}

	// Pending secret persisted (not enabled yet).
	var pending sql.NullString
	var enabled int
	if err := db.QueryRow(`SELECT two_factor_pending_secret, two_factor_enabled FROM users WHERE id = ?`, uid).Scan(&pending, &enabled); err != nil {
		t.Fatalf("query: %v", err)
	}
	if !pending.Valid || pending.String == "" {
		t.Error("pending secret not stored")
	}
	if enabled != 0 {
		t.Error("2FA flipped on before confirm")
	}
}

func TestInitiateSetup_RequiresPassword(t *testing.T) {
	_, twoH, _, db := twoFactorTestSetup(t)
	uid := seedUser2FA(t, db, "admin@example.com", "Strong#Pass1ord")

	body, _ := json.Marshal(map[string]string{"password": "wrong"})
	rec := callAuthed(t, twoH.InitiateSetup, http.MethodPost, "/api/v1/auth/2fa/setup-initiate", body, uid)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid_credentials") {
		t.Fatalf("expected invalid_credentials, got %s", rec.Body.String())
	}

	var pending sql.NullString
	if err := db.QueryRow(`SELECT two_factor_pending_secret FROM users WHERE id = ?`, uid).Scan(&pending); err != nil {
		t.Fatalf("query: %v", err)
	}
	if pending.Valid {
		t.Fatal("pending secret was created with a wrong password")
	}
}

func TestConfirmSetup_WithCorrectCodeActivates(t *testing.T) {
	_, twoH, svc, db := twoFactorTestSetup(t)
	uid := seedUser2FA(t, db, "admin@example.com", "Strong#Pass1ord")

	secret, _, _, err := svc.InitiateSetup(context.Background(), uid, "admin@example.com")
	if err != nil {
		t.Fatalf("initiate: %v", err)
	}
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("code: %v", err)
	}

	body, _ := json.Marshal(map[string]string{"code": code})
	rec := callAuthed(t, twoH.ConfirmSetup, http.MethodPost, "/api/v1/auth/2fa/setup-confirm", body, uid)
	if rec.Code != http.StatusOK {
		t.Fatalf("confirm: %d body=%s", rec.Code, rec.Body.String())
	}
	resp := decode(t, rec.Body.Bytes())
	codes, _ := resp["recovery_codes"].([]any)
	if len(codes) != auth.RecoveryCodeCount {
		t.Errorf("expected %d recovery codes, got %d", auth.RecoveryCodeCount, len(codes))
	}

	var enabled int
	if err := db.QueryRow(`SELECT two_factor_enabled FROM users WHERE id = ?`, uid).Scan(&enabled); err != nil {
		t.Fatalf("query: %v", err)
	}
	if enabled != 1 {
		t.Error("2FA still off after successful confirm")
	}
}

func TestConfirmSetup_WithWrongCodeRejects(t *testing.T) {
	_, twoH, svc, db := twoFactorTestSetup(t)
	uid := seedUser2FA(t, db, "admin@example.com", "Strong#Pass1ord")
	if _, _, _, err := svc.InitiateSetup(context.Background(), uid, "admin@example.com"); err != nil {
		t.Fatalf("initiate: %v", err)
	}

	body, _ := json.Marshal(map[string]string{"code": "000000"})
	rec := callAuthed(t, twoH.ConfirmSetup, http.MethodPost, "/api/v1/auth/2fa/setup-confirm", body, uid)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestLogin_With2FAEnabledReturnsChallenge(t *testing.T) {
	authH, _, svc, db := twoFactorTestSetup(t)
	uid := seedUser2FA(t, db, "admin@example.com", "Strong#Pass1ord")
	// Force-enable 2FA without the wizard.
	enable2FA(t, db, svc, uid, "admin@example.com")

	body, _ := json.Marshal(map[string]string{"email": "admin@example.com", "password": "Strong#Pass1ord"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	authH.Login(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login: %d body=%s", rec.Code, rec.Body.String())
	}
	resp := decode(t, rec.Body.Bytes())
	if resp["requires_2fa"] != true {
		t.Errorf("expected requires_2fa=true, got %+v", resp)
	}
	if resp["challenge_id"] == "" {
		t.Error("no challenge_id")
	}
	if resp["access_token"] != nil {
		t.Error("access_token leaked before challenge")
	}
	// No refresh cookie either.
	for _, c := range rec.Result().Cookies() {
		if c.Name == "refresh_token" && c.Value != "" {
			t.Error("refresh cookie set before challenge")
		}
	}
}

func TestChallenge_WithCorrectCodeIssuesJWT(t *testing.T) {
	authH, twoH, svc, db := twoFactorTestSetup(t)
	uid := seedUser2FA(t, db, "admin@example.com", "Strong#Pass1ord")
	secret := enable2FA(t, db, svc, uid, "admin@example.com")

	challengeID := loginAndExtractChallenge(t, authH)

	code, _ := totp.GenerateCode(secret, time.Now())
	body, _ := json.Marshal(map[string]string{
		"challenge_id": challengeID,
		"code":         code,
		"method":       "app",
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/2fa/challenge", bytes.NewReader(body))
	twoH.Challenge(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("challenge: %d body=%s", rec.Code, rec.Body.String())
	}
	resp := decode(t, rec.Body.Bytes())
	if resp["access_token"] == nil || resp["access_token"] == "" {
		t.Error("no access_token after challenge")
	}
	found := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == "refresh_token" && c.Value != "" {
			found = true
		}
	}
	if !found {
		t.Error("refresh cookie not set after challenge")
	}
}

func TestChallenge_WithWrongCodeFails(t *testing.T) {
	authH, twoH, svc, db := twoFactorTestSetup(t)
	uid := seedUser2FA(t, db, "admin@example.com", "Strong#Pass1ord")
	_ = enable2FA(t, db, svc, uid, "admin@example.com")

	challengeID := loginAndExtractChallenge(t, authH)

	body, _ := json.Marshal(map[string]string{
		"challenge_id": challengeID,
		"code":         "000000",
		"method":       "app",
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/2fa/challenge", bytes.NewReader(body))
	twoH.Challenge(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestChallenge_FiveAttemptsExhausts(t *testing.T) {
	authH, twoH, svc, db := twoFactorTestSetup(t)
	uid := seedUser2FA(t, db, "admin@example.com", "Strong#Pass1ord")
	_ = enable2FA(t, db, svc, uid, "admin@example.com")

	challengeID := loginAndExtractChallenge(t, authH)

	body, _ := json.Marshal(map[string]string{
		"challenge_id": challengeID,
		"code":         "000000",
		"method":       "app",
	})
	var lastBody string
	var lastCode int
	for i := 0; i < auth.MaxChallengeAttempts; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/2fa/challenge", bytes.NewReader(body))
		twoH.Challenge(rec, req)
		lastBody = rec.Body.String()
		lastCode = rec.Code
	}
	if lastCode != http.StatusUnauthorized {
		t.Errorf("expected 401 on exhaustion, got %d body=%s", lastCode, lastBody)
	}
	if !strings.Contains(lastBody, "too_many_attempts") {
		t.Errorf("expected too_many_attempts, got %s", lastBody)
	}

	// Subsequent attempts must complain that the challenge is gone.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/2fa/challenge", bytes.NewReader(body))
	twoH.Challenge(rec, req)
	if !strings.Contains(rec.Body.String(), "invalid_challenge") {
		t.Errorf("expected invalid_challenge after exhaustion, got %s", rec.Body.String())
	}
}

func TestChallenge_RecoveryCodeIsOneShot(t *testing.T) {
	authH, twoH, svc, db := twoFactorTestSetup(t)
	uid := seedUser2FA(t, db, "admin@example.com", "Strong#Pass1ord")
	_, recoveryCodes := enable2FAWithCodes(t, db, svc, uid, "admin@example.com")

	challengeID := loginAndExtractChallenge(t, authH)

	body, _ := json.Marshal(map[string]string{
		"challenge_id": challengeID,
		"code":         recoveryCodes[0],
		"method":       "recovery",
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/2fa/challenge", bytes.NewReader(body))
	twoH.Challenge(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("recovery challenge: %d body=%s", rec.Code, rec.Body.String())
	}

	// Same recovery code on a fresh login should now fail.
	challengeID = loginAndExtractChallenge(t, authH)
	body, _ = json.Marshal(map[string]string{
		"challenge_id": challengeID,
		"code":         recoveryCodes[0],
		"method":       "recovery",
	})
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/2fa/challenge", bytes.NewReader(body))
	twoH.Challenge(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("re-used recovery code accepted: %d", rec.Code)
	}

	// Confirm DB has one less code.
	var raw sql.NullString
	if err := db.QueryRow(`SELECT two_factor_recovery_codes FROM users WHERE id = ?`, uid).Scan(&raw); err != nil {
		t.Fatalf("query: %v", err)
	}
	var hashes []string
	if err := json.Unmarshal([]byte(raw.String), &hashes); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(hashes) != auth.RecoveryCodeCount-1 {
		t.Errorf("expected %d remaining, got %d", auth.RecoveryCodeCount-1, len(hashes))
	}
}

func TestDisable_ClearsAllColumns(t *testing.T) {
	_, twoH, svc, db := twoFactorTestSetup(t)
	uid := seedUser2FA(t, db, "admin@example.com", "Strong#Pass1ord")
	secret := enable2FA(t, db, svc, uid, "admin@example.com")

	code, _ := totp.GenerateCode(secret, time.Now())
	body, _ := json.Marshal(map[string]string{
		"password": "Strong#Pass1ord",
		"code":     code,
	})
	rec := callAuthed(t, twoH.Disable, http.MethodPost, "/api/v1/auth/2fa/disable", body, uid)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("disable: %d body=%s", rec.Code, rec.Body.String())
	}

	var (
		enabled                      int
		secretCol, pending, recovery sql.NullString
		confirmedAt                  sql.NullInt64
	)
	if err := db.QueryRow(`SELECT two_factor_enabled, two_factor_secret, two_factor_pending_secret, two_factor_confirmed_at, two_factor_recovery_codes FROM users WHERE id = ?`, uid).Scan(&enabled, &secretCol, &pending, &confirmedAt, &recovery); err != nil {
		t.Fatalf("query: %v", err)
	}
	if enabled != 0 || secretCol.Valid || pending.Valid || confirmedAt.Valid || recovery.Valid {
		t.Errorf("disable did not clear everything: enabled=%d secret=%v pending=%v confirmed=%v recovery=%v",
			enabled, secretCol, pending, confirmedAt, recovery)
	}
}

func TestStatus_ReportsRecoveryRemaining(t *testing.T) {
	_, twoH, svc, db := twoFactorTestSetup(t)
	uid := seedUser2FA(t, db, "admin@example.com", "Strong#Pass1ord")
	_ = enable2FA(t, db, svc, uid, "admin@example.com")

	rec := callAuthed(t, twoH.Status, http.MethodGet, "/api/v1/auth/2fa/status", nil, uid)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d", rec.Code)
	}
	resp := decode(t, rec.Body.Bytes())
	if resp["enabled"] != true {
		t.Errorf("expected enabled=true, got %+v", resp)
	}
	if int(resp["recovery_codes_remaining"].(float64)) != auth.RecoveryCodeCount {
		t.Errorf("recovery_codes_remaining wrong: %v", resp["recovery_codes_remaining"])
	}
}

func TestChangePassword_With2FAEnabledRequiresCode(t *testing.T) {
	authH, _, svc, db := twoFactorTestSetup(t)
	uid := seedUser2FA(t, db, "admin@example.com", "Strong#Pass1ord")
	_ = enable2FA(t, db, svc, uid, "admin@example.com")

	body, _ := json.Marshal(map[string]string{
		"current_password": "Strong#Pass1ord",
		"new_password":     "NewStrong#Pass2ord",
	})
	rec := callAuthed(t, authH.ChangePassword, http.MethodPost, "/api/v1/auth/password", body, uid)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "twofactor_required") {
		t.Fatalf("expected twofactor_required, got %s", rec.Body.String())
	}
}

func TestChangePassword_With2FACodeSucceeds(t *testing.T) {
	authH, _, svc, db := twoFactorTestSetup(t)
	uid := seedUser2FA(t, db, "admin@example.com", "Strong#Pass1ord")
	secret := enable2FA(t, db, svc, uid, "admin@example.com")
	code, _ := totp.GenerateCode(secret, time.Now())

	body, _ := json.Marshal(map[string]string{
		"current_password":  "Strong#Pass1ord",
		"new_password":      "NewStrong#Pass2ord",
		"two_factor_code":   code,
		"two_factor_method": "app",
	})
	rec := callAuthed(t, authH.ChangePassword, http.MethodPost, "/api/v1/auth/password", body, uid)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("change password: %d body=%s", rec.Code, rec.Body.String())
	}

	var hash string
	if err := db.QueryRow(`SELECT password_hash FROM users WHERE id = ?`, uid).Scan(&hash); err != nil {
		t.Fatalf("query: %v", err)
	}
	if !auth.VerifyPassword(hash, "NewStrong#Pass2ord") {
		t.Fatal("password hash was not updated")
	}
}

// --- helpers ---

// enable2FA force-flips 2FA on by running InitiateSetup + ConfirmSetup with a
// freshly generated TOTP code. Returns the secret so the test can mint codes.
func enable2FA(t *testing.T, db *sql.DB, svc *auth.TwoFactorService, userID, email string) string {
	t.Helper()
	secret, _, _, err := svc.InitiateSetup(context.Background(), userID, email)
	if err != nil {
		t.Fatalf("initiate: %v", err)
	}
	code, _ := totp.GenerateCode(secret, time.Now())
	if _, err := svc.ConfirmSetup(context.Background(), userID, code); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	return secret
}

// enable2FAWithCodes is enable2FA but returns the plaintext recovery codes too.
func enable2FAWithCodes(t *testing.T, db *sql.DB, svc *auth.TwoFactorService, userID, email string) (string, []string) {
	t.Helper()
	secret, _, _, err := svc.InitiateSetup(context.Background(), userID, email)
	if err != nil {
		t.Fatalf("initiate: %v", err)
	}
	code, _ := totp.GenerateCode(secret, time.Now())
	codes, err := svc.ConfirmSetup(context.Background(), userID, code)
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	return secret, codes
}

func loginAndExtractChallenge(t *testing.T, authH *Auth) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"email": "admin@example.com", "password": "Strong#Pass1ord"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	authH.Login(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login: %d body=%s", rec.Code, rec.Body.String())
	}
	resp := decode(t, rec.Body.Bytes())
	id, _ := resp["challenge_id"].(string)
	if id == "" {
		t.Fatalf("no challenge_id in: %+v", resp)
	}
	return id
}
