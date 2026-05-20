package auth

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/prexel/prexel/internal/crypto"
)

// TwoFactorService is the application-level facade for the 2FA flow. It owns
// the in-memory challenge store, the AES-256-GCM cipher used to seal TOTP
// secrets at rest, and the SHA-256 hashing of one-shot recovery codes.
//
// Recovery codes are *hashes*, not ciphertexts: we never need to recover the
// original code (only verify presented ones) and a leaked DB shouldn't allow
// bypassing 2FA the way a leaked symmetric key would.
type TwoFactorService struct {
	db         *sql.DB
	cipher     *crypto.Cipher
	challenges *ChallengeStore
	issuer     string
}

// 2FA service errors. These map 1:1 to handler responses; see twofactor.go.
var (
	ErrTwoFactorNotPending       = errors.New("auth: no pending 2FA setup")
	ErrTwoFactorNotEnabled       = errors.New("auth: 2FA not enabled")
	ErrInvalidCode               = errors.New("auth: invalid 2FA code")
	ErrInvalidPassword           = errors.New("auth: invalid password")
	ErrChallengeUnknownMethod    = errors.New("auth: unknown 2FA method")
	ErrChallengeInvalidUserState = errors.New("auth: 2FA not configured for user")
)

// NewTwoFactorService wires the service. issuer is the label shown in the
// authenticator app (e.g. "Prexel") and comes from PREXEL_2FA_ISSUER.
func NewTwoFactorService(db *sql.DB, cipher *crypto.Cipher, challenges *ChallengeStore, issuer string) *TwoFactorService {
	if issuer == "" {
		issuer = "Prexel"
	}
	return &TwoFactorService{db: db, cipher: cipher, challenges: challenges, issuer: issuer}
}

// Challenges exposes the underlying store. The HTTP layer needs access to
// create challenges from the login handler (which doesn't have an authenticated
// user context until the second step).
func (s *TwoFactorService) Challenges() *ChallengeStore { return s.challenges }

// ---------- Setup wizard ----------

// InitiateSetup creates a fresh TOTP secret, seals it into the pending column,
// and returns the secret + otpauth URL + a PNG QR code (raw bytes). The user
// scans the QR with their authenticator app and proves possession by hitting
// ConfirmSetup with the code shown on screen.
//
// Re-running InitiateSetup before confirm simply overwrites the pending
// secret — nice when the user closes the wizard and re-opens it.
func (s *TwoFactorService) InitiateSetup(ctx context.Context, userID, email string) (secret, otpauthURL string, qrPNG []byte, err error) {
	secret, otpauthURL, err = GenerateTOTPSecret(email, s.issuer)
	if err != nil {
		return "", "", nil, err
	}
	qrPNG, err = TOTPQRCodePNG(otpauthURL)
	if err != nil {
		return "", "", nil, err
	}

	enc, err := s.cipher.Encrypt([]byte(secret))
	if err != nil {
		return "", "", nil, fmt.Errorf("encrypt secret: %w", err)
	}
	encB64 := base64.StdEncoding.EncodeToString(enc)

	if _, err := s.db.ExecContext(ctx,
		`UPDATE users SET two_factor_pending_secret = ? WHERE id = ?`,
		encB64, userID,
	); err != nil {
		return "", "", nil, fmt.Errorf("save pending secret: %w", err)
	}
	return secret, otpauthURL, qrPNG, nil
}

// ConfirmSetup validates that the user actually configured their authenticator
// app by checking code against the *pending* secret. On success it promotes
// the secret to the live column, generates 8 recovery codes (storing only
// hashes), and returns the plaintext codes — the caller must show these to
// the user UMA ÚNICA VEZ.
func (s *TwoFactorService) ConfirmSetup(ctx context.Context, userID, code string) ([]string, error) {
	var pendingB64 sql.NullString
	if err := s.db.QueryRowContext(ctx,
		`SELECT two_factor_pending_secret FROM users WHERE id = ?`, userID,
	).Scan(&pendingB64); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrTwoFactorNotPending
		}
		return nil, fmt.Errorf("load pending: %w", err)
	}
	if !pendingB64.Valid || pendingB64.String == "" {
		return nil, ErrTwoFactorNotPending
	}
	secret, err := s.decryptStored(pendingB64.String)
	if err != nil {
		return nil, err
	}
	if !ValidateTOTP(secret, code) {
		return nil, ErrInvalidCode
	}

	plain, hashes, err := GenerateRecoveryCodes()
	if err != nil {
		return nil, err
	}
	hashesJSON, err := json.Marshal(hashes)
	if err != nil {
		return nil, fmt.Errorf("marshal hashes: %w", err)
	}

	// Promote: move pending → live, clear pending, set confirmed_at, store
	// hashes, flip the enabled flag. Single statement so we don't half-enable.
	now := time.Now().Unix()
	if _, err := s.db.ExecContext(ctx, `
		UPDATE users SET
			two_factor_secret = two_factor_pending_secret,
			two_factor_pending_secret = NULL,
			two_factor_enabled = 1,
			two_factor_confirmed_at = ?,
			two_factor_recovery_codes = ?
		WHERE id = ?`,
		now, string(hashesJSON), userID,
	); err != nil {
		return nil, fmt.Errorf("activate 2fa: %w", err)
	}
	return plain, nil
}

// ---------- Login flow ----------

// RequiresChallenge reports whether the user has 2FA fully enabled. Called by
// the login handler right after the bcrypt check.
func (s *TwoFactorService) RequiresChallenge(ctx context.Context, userID string) (bool, error) {
	var enabled int
	err := s.db.QueryRowContext(ctx, `SELECT two_factor_enabled FROM users WHERE id = ?`, userID).Scan(&enabled)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return enabled == 1, nil
}

// CreateChallenge stashes a pending 2FA challenge in memory and returns the
// challenge_id + supported methods. For v0.1 we only support "app" (TOTP) and
// "recovery"; email-OTP is deferred to v0.2.
func (s *TwoFactorService) CreateChallenge(userID string) (challengeID string, methods []string) {
	c := s.challenges.Create(userID)
	return c.ID, []string{"app", "recovery"}
}

// VerifyChallenge validates a code for the challenge identified by id. On
// success it consumes the challenge and returns the user_id so the caller can
// issue a JWT/refresh pair just like a password-only login.
//
// Wrong codes increment the attempts counter; the 5th wrong attempt nukes the
// challenge entirely (ErrChallengeExhausted).
func (s *TwoFactorService) VerifyChallenge(ctx context.Context, challengeID, code, method string) (string, error) {
	c, err := s.challenges.Get(challengeID)
	if err != nil {
		return "", err
	}

	if err := s.VerifyUserCode(ctx, c.UserID, code, method); err != nil {
		if errors.Is(err, ErrInvalidCode) {
			if _, incErr := s.challenges.IncrementAttempts(challengeID); incErr != nil {
				return "", incErr
			}
		}
		return "", err
	}

	if _, err := s.challenges.Consume(challengeID); err != nil {
		return "", err
	}
	return c.UserID, nil
}

// VerifyUserCode validates the user's configured second factor for sensitive
// authenticated actions such as changing a password. Recovery codes are
// one-shot here too, matching the login challenge behavior.
func (s *TwoFactorService) VerifyUserCode(ctx context.Context, userID, code, method string) error {
	var (
		secretEnc    sql.NullString
		recoveryJSON sql.NullString
		enabled      int
	)
	if err := s.db.QueryRowContext(ctx,
		`SELECT two_factor_enabled, two_factor_secret, two_factor_recovery_codes FROM users WHERE id = ?`,
		userID,
	).Scan(&enabled, &secretEnc, &recoveryJSON); err != nil {
		return fmt.Errorf("load user 2fa: %w", err)
	}
	if enabled != 1 || !secretEnc.Valid {
		return ErrChallengeInvalidUserState
	}

	var ok bool
	switch method {
	case "app", "":
		secret, err := s.decryptStored(secretEnc.String)
		if err != nil {
			return err
		}
		// Replay guard: once a TOTP is accepted for this user inside
		// its 30s window, refuse it again. ErrCodeReused maps to the
		// same ErrInvalidCode the caller would surface for a wrong
		// code — we deliberately don't tell the client "this exact
		// code already worked", as that would confirm a stolen code
		// is correct.
		accepted, err := ValidateAndConsumeTOTP(ctx, s.db, userID, secret, code)
		if err != nil {
			if errors.Is(err, ErrCodeReused) {
				return ErrInvalidCode
			}
			return err
		}
		ok = accepted
	case "recovery":
		var hashes []string
		if recoveryJSON.Valid && recoveryJSON.String != "" {
			if err := json.Unmarshal([]byte(recoveryJSON.String), &hashes); err != nil {
				return fmt.Errorf("decode recovery: %w", err)
			}
		}
		remaining, valid := VerifyAndConsumeRecoveryCode(hashes, code)
		ok = valid
		if valid {
			// Persist the trimmed list before we treat the challenge as solved,
			// so a code can never be replayed even if the JWT issuance below
			// races with a parallel request.
			newJSON, err := json.Marshal(remaining)
			if err != nil {
				return fmt.Errorf("encode recovery: %w", err)
			}
			if _, err := s.db.ExecContext(ctx,
				`UPDATE users SET two_factor_recovery_codes = ? WHERE id = ?`,
				string(newJSON), userID,
			); err != nil {
				return fmt.Errorf("update recovery: %w", err)
			}
		}
	default:
		return ErrChallengeUnknownMethod
	}

	if !ok {
		return ErrInvalidCode
	}
	return nil
}

// ---------- Disable / regenerate ----------

// Disable requires both the user's password AND a fresh TOTP code, then
// zeroes every 2FA column. Returning the account to "no 2FA" is a higher-risk
// operation than enabling, hence the double check.
func (s *TwoFactorService) Disable(ctx context.Context, userID, password, code string) error {
	var (
		hash      string
		secretEnc sql.NullString
		enabled   int
	)
	if err := s.db.QueryRowContext(ctx,
		`SELECT password_hash, two_factor_enabled, two_factor_secret FROM users WHERE id = ?`,
		userID,
	).Scan(&hash, &enabled, &secretEnc); err != nil {
		return fmt.Errorf("load user: %w", err)
	}
	if !VerifyPassword(hash, password) {
		return ErrInvalidPassword
	}
	if enabled != 1 || !secretEnc.Valid {
		return ErrTwoFactorNotEnabled
	}
	secret, err := s.decryptStored(secretEnc.String)
	if err != nil {
		return err
	}
	// Replay guard — same shape as VerifyUserCode.
	accepted, err := ValidateAndConsumeTOTP(ctx, s.db, userID, secret, code)
	if err != nil {
		if errors.Is(err, ErrCodeReused) {
			return ErrInvalidCode
		}
		return err
	}
	if !accepted {
		return ErrInvalidCode
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE users SET
			two_factor_enabled = 0,
			two_factor_secret = NULL,
			two_factor_pending_secret = NULL,
			two_factor_confirmed_at = NULL,
			two_factor_recovery_codes = NULL
		WHERE id = ?`, userID,
	); err != nil {
		return fmt.Errorf("disable: %w", err)
	}
	return nil
}

// RegenerateRecoveryCodes issues 8 fresh codes (invalidating the old ones)
// after confirming the user's password. Returns plaintext codes once.
func (s *TwoFactorService) RegenerateRecoveryCodes(ctx context.Context, userID, password string) ([]string, error) {
	var (
		hash    string
		enabled int
	)
	if err := s.db.QueryRowContext(ctx,
		`SELECT password_hash, two_factor_enabled FROM users WHERE id = ?`, userID,
	).Scan(&hash, &enabled); err != nil {
		return nil, fmt.Errorf("load user: %w", err)
	}
	if !VerifyPassword(hash, password) {
		return nil, ErrInvalidPassword
	}
	if enabled != 1 {
		return nil, ErrTwoFactorNotEnabled
	}
	plain, hashes, err := GenerateRecoveryCodes()
	if err != nil {
		return nil, err
	}
	hashesJSON, err := json.Marshal(hashes)
	if err != nil {
		return nil, fmt.Errorf("marshal hashes: %w", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE users SET two_factor_recovery_codes = ? WHERE id = ?`,
		string(hashesJSON), userID,
	); err != nil {
		return nil, fmt.Errorf("save hashes: %w", err)
	}
	return plain, nil
}

// ---------- Status ----------

// Status returns the public-facing view of a user's 2FA state. recoveryRemaining
// reflects how many one-shot codes are still unused — useful for warning the
// user to regenerate when they're running low.
func (s *TwoFactorService) Status(ctx context.Context, userID string) (enabled bool, confirmedAt *time.Time, recoveryRemaining int, err error) {
	var (
		enabledInt   int
		confirmedTs  sql.NullInt64
		recoveryJSON sql.NullString
	)
	if e := s.db.QueryRowContext(ctx,
		`SELECT two_factor_enabled, two_factor_confirmed_at, two_factor_recovery_codes FROM users WHERE id = ?`,
		userID,
	).Scan(&enabledInt, &confirmedTs, &recoveryJSON); e != nil {
		err = e
		return
	}
	enabled = enabledInt == 1
	if confirmedTs.Valid {
		t := time.Unix(confirmedTs.Int64, 0)
		confirmedAt = &t
	}
	if recoveryJSON.Valid && recoveryJSON.String != "" {
		var list []string
		if e := json.Unmarshal([]byte(recoveryJSON.String), &list); e != nil {
			err = e
			return
		}
		recoveryRemaining = len(list)
	}
	return
}

// decryptStored undoes the base64 + AES-256-GCM that InitiateSetup applied.
func (s *TwoFactorService) decryptStored(b64 string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return "", fmt.Errorf("base64 decode: %w", err)
	}
	pt, err := s.cipher.Decrypt(raw)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}
	return string(pt), nil
}
