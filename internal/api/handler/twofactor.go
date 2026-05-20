package handler

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/prexel/prexel/internal/api/apimiddleware"
	"github.com/prexel/prexel/internal/auth"
)

// TwoFactorDeps bundles the dependencies for the 2FA handler. The 2FA flow
// reuses the JWT secret + Secure cookie attribute from the standard auth flow
// because a successful challenge issues the same access/refresh pair.
type TwoFactorDeps struct {
	DB        *sql.DB
	Service   *auth.TwoFactorService
	JWTSecret string
	Secure    bool
}

// TwoFactor groups the 2FA endpoints. The first five are authenticated; only
// /auth/2fa/challenge is public (it's the second step of login, before the
// JWT is issued).
type TwoFactor struct {
	d TwoFactorDeps
}

// NewTwoFactorHandler builds the handler.
func NewTwoFactorHandler(d TwoFactorDeps) *TwoFactor {
	return &TwoFactor{d: d}
}

// ---------- Setup wizard ----------

type setupInitiateResp struct {
	Secret       string `json:"secret"`
	OtpauthURL   string `json:"otpauth_url"`
	QRCodePNGB64 string `json:"qr_code_png_b64"`
}

type setupInitiateReq struct {
	Password string `json:"password"`
}

// InitiateSetup handles POST /api/v1/auth/2fa/setup-initiate.
// Returns a freshly generated secret + QR. The secret is stashed in the
// pending column server-side; nothing is "live" until ConfirmSetup succeeds.
func (h *TwoFactor) InitiateSetup(w http.ResponseWriter, r *http.Request) {
	uid, ok := apimiddleware.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req setupInitiateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}

	var email, hash string
	if err := h.d.DB.QueryRowContext(r.Context(), `SELECT email, password_hash FROM users WHERE id = ?`, uid).Scan(&email, &hash); err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if !auth.VerifyPassword(hash, req.Password) {
		writeError(w, http.StatusUnauthorized, "invalid_credentials")
		return
	}
	secret, otpauthURL, png, err := h.d.Service.InitiateSetup(r.Context(), uid, email)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "twofactor_error")
		return
	}
	writeJSON(w, http.StatusOK, setupInitiateResp{
		Secret:       secret,
		OtpauthURL:   otpauthURL,
		QRCodePNGB64: base64.StdEncoding.EncodeToString(png),
	})
}

type setupConfirmReq struct {
	Code string `json:"code"`
}

// ConfirmSetup handles POST /api/v1/auth/2fa/setup-confirm.
// Validates the user's authenticator app is paired with the pending secret,
// flips 2FA on, and returns the freshly-minted recovery codes (plaintext —
// the only time the server ever ships them).
func (h *TwoFactor) ConfirmSetup(w http.ResponseWriter, r *http.Request) {
	uid, ok := apimiddleware.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req setupConfirmReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	codes, err := h.d.Service.ConfirmSetup(r.Context(), uid, req.Code)
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrTwoFactorNotPending):
			writeError(w, http.StatusBadRequest, "no_pending_setup")
		case errors.Is(err, auth.ErrInvalidCode):
			writeError(w, http.StatusUnauthorized, "invalid_code")
		default:
			writeError(w, http.StatusInternalServerError, "twofactor_error")
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"recovery_codes": codes})
}

// ---------- Disable / regenerate ----------

type disableReq struct {
	Password string `json:"password"`
	Code     string `json:"code"`
}

// Disable handles POST /api/v1/auth/2fa/disable. 204 on success.
func (h *TwoFactor) Disable(w http.ResponseWriter, r *http.Request) {
	uid, ok := apimiddleware.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req disableReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	if err := h.d.Service.Disable(r.Context(), uid, req.Password, req.Code); err != nil {
		switch {
		case errors.Is(err, auth.ErrInvalidPassword):
			writeError(w, http.StatusUnauthorized, "invalid_credentials")
		case errors.Is(err, auth.ErrInvalidCode):
			writeError(w, http.StatusUnauthorized, "invalid_code")
		case errors.Is(err, auth.ErrTwoFactorNotEnabled):
			writeError(w, http.StatusBadRequest, "not_enabled")
		default:
			writeError(w, http.StatusInternalServerError, "twofactor_error")
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type regenerateReq struct {
	Password string `json:"password"`
}

// RegenerateRecoveryCodes handles POST /api/v1/auth/2fa/recovery-codes.
// Returns 8 new plaintext codes; the previous list is invalidated.
func (h *TwoFactor) RegenerateRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	uid, ok := apimiddleware.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req regenerateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	codes, err := h.d.Service.RegenerateRecoveryCodes(r.Context(), uid, req.Password)
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrInvalidPassword):
			writeError(w, http.StatusUnauthorized, "invalid_credentials")
		case errors.Is(err, auth.ErrTwoFactorNotEnabled):
			writeError(w, http.StatusBadRequest, "not_enabled")
		default:
			writeError(w, http.StatusInternalServerError, "twofactor_error")
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"recovery_codes": codes})
}

// ---------- Status ----------

// Status handles GET /api/v1/auth/2fa/status.
func (h *TwoFactor) Status(w http.ResponseWriter, r *http.Request) {
	uid, ok := apimiddleware.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	enabled, confirmedAt, remaining, err := h.d.Service.Status(r.Context(), uid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "twofactor_error")
		return
	}
	out := map[string]any{
		"enabled":                  enabled,
		"recovery_codes_remaining": remaining,
	}
	if confirmedAt != nil {
		out["confirmed_at"] = confirmedAt.Unix()
	} else {
		out["confirmed_at"] = nil
	}
	writeJSON(w, http.StatusOK, out)
}

// ---------- Challenge (public, second step of login) ----------

type challengeReq struct {
	ChallengeID string `json:"challenge_id"`
	Code        string `json:"code"`
	Method      string `json:"method"`
}

// Challenge handles POST /api/v1/auth/2fa/challenge. Public endpoint — the
// client doesn't have a JWT yet. Success emits an access token + refresh
// cookie identical to a password-only login.
func (h *TwoFactor) Challenge(w http.ResponseWriter, r *http.Request) {
	var req challengeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	if req.ChallengeID == "" || req.Code == "" {
		writeError(w, http.StatusBadRequest, "missing_fields")
		return
	}

	userID, err := h.d.Service.VerifyChallenge(r.Context(), req.ChallengeID, req.Code, req.Method)
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrChallengeNotFound),
			errors.Is(err, auth.ErrChallengeExpired):
			writeError(w, http.StatusUnauthorized, "invalid_challenge")
		case errors.Is(err, auth.ErrChallengeExhausted):
			writeError(w, http.StatusUnauthorized, "too_many_attempts")
		case errors.Is(err, auth.ErrInvalidCode):
			writeError(w, http.StatusUnauthorized, "invalid_code")
		case errors.Is(err, auth.ErrChallengeUnknownMethod):
			writeError(w, http.StatusBadRequest, "unknown_method")
		case errors.Is(err, auth.ErrChallengeInvalidUserState):
			writeError(w, http.StatusUnauthorized, "twofactor_not_configured")
		default:
			writeError(w, http.StatusInternalServerError, "twofactor_error")
		}
		return
	}

	access, _, err := auth.IssueAccessToken(h.d.JWTSecret, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "token_error")
		return
	}
	rawRefresh, _, _, err := auth.IssueRefreshTokenInFamily(h.d.DB, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "token_error")
		return
	}
	setRefreshCookie(w, rawRefresh, h.d.Secure)
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": access,
		"expires_in":   int(auth.AccessTokenTTL.Seconds()),
		"token_type":   "Bearer",
	})
}
