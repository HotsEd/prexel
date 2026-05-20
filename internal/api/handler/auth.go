package handler

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/prexel/prexel/internal/api/apimiddleware"
	"github.com/prexel/prexel/internal/auth"
	"github.com/prexel/prexel/internal/rbac"
	"github.com/prexel/prexel/internal/setup"
)

// AuthDeps bundles dependencies used by the auth handlers.
type AuthDeps struct {
	DB        *sql.DB
	Setup     *setup.Service
	JWTSecret string
	// Secure controls whether refresh cookies carry the Secure attribute. In
	// production this should be true; in dev with self-signed TLS we still want
	// Secure=true because cookies are only set over HTTPS (curl -k still works).
	Secure bool
	// TwoFactor is optional. When set, Login routes 2FA-enabled users through
	// the challenge flow instead of issuing tokens directly. Nil in tests that
	// don't exercise 2FA.
	TwoFactor *auth.TwoFactorService
	RBAC      *rbac.Service
}

// Auth groups the authentication endpoints.
type Auth struct {
	d AuthDeps
}

// NewAuthHandler builds an Auth handler bundle.
func NewAuthHandler(d AuthDeps) *Auth {
	return &Auth{d: d}
}

const refreshCookieName = "refresh_token"

type loginReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// Login handles POST /api/v1/auth/login.
func (h *Auth) Login(w http.ResponseWriter, r *http.Request) {
	if !h.d.Setup.Completed() {
		writeError(w, http.StatusForbidden, "setup_not_completed")
		return
	}
	var req loginReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	email := strings.TrimSpace(req.Email)

	var (
		userID, hash string
	)
	row := h.d.DB.QueryRow(`SELECT id, password_hash FROM users WHERE email = ?`, email)
	if err := row.Scan(&userID, &hash); err != nil {
		// Run bcrypt anyway to keep timing similar on unknown-user / wrong-pw.
		_ = auth.VerifyPassword("$2a$12$abcdefghijklmnopqrstuv", req.Password)
		apimiddleware.CountLoginFailure(r.Context())
		writeError(w, http.StatusUnauthorized, "invalid_credentials")
		return
	}
	if !auth.VerifyPassword(hash, req.Password) {
		apimiddleware.CountLoginFailure(r.Context())
		writeError(w, http.StatusUnauthorized, "invalid_credentials")
		return
	}

	// If 2FA is enabled for this user, stop here: create a challenge in
	// memory and return its ID + the methods we accept. The frontend will
	// drive the user through /auth/2fa/challenge, which is the only place
	// that actually issues tokens for 2FA-protected accounts.
	if h.d.TwoFactor != nil {
		needs, err := h.d.TwoFactor.RequiresChallenge(r.Context(), userID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "twofactor_error")
			return
		}
		if needs {
			challengeID, methods := h.d.TwoFactor.CreateChallenge(userID)
			writeJSON(w, http.StatusOK, map[string]any{
				"requires_2fa": true,
				"challenge_id": challengeID,
				"methods":      methods,
			})
			return
		}
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

// Refresh handles POST /api/v1/auth/refresh.
func (h *Auth) Refresh(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(refreshCookieName)
	if err != nil || cookie.Value == "" {
		writeError(w, http.StatusUnauthorized, "missing_refresh_token")
		return
	}
	newRaw, access, _, err := auth.RotateRefreshToken(h.d.DB, h.d.JWTSecret, cookie.Value)
	if err != nil {
		clearRefreshCookie(w, h.d.Secure)
		if errors.Is(err, auth.ErrTokenReuse) {
			writeError(w, http.StatusUnauthorized, "refresh_token_reuse")
			return
		}
		writeError(w, http.StatusUnauthorized, "invalid_refresh_token")
		return
	}
	setRefreshCookie(w, newRaw, h.d.Secure)
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": access,
		"expires_in":   int(auth.AccessTokenTTL.Seconds()),
		"token_type":   "Bearer",
	})
}

// Logout handles POST /api/v1/auth/logout (authenticated).
func (h *Auth) Logout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(refreshCookieName)
	if err == nil && cookie.Value != "" {
		_ = auth.RevokeRefreshToken(h.d.DB, cookie.Value)
	}
	clearRefreshCookie(w, h.d.Secure)
	w.WriteHeader(http.StatusNoContent)
}

// Me handles GET /api/v1/auth/me (authenticated).
func (h *Auth) Me(w http.ResponseWriter, r *http.Request) {
	uid, ok := apimiddleware.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if h.d.RBAC != nil {
		ctx, err := h.d.RBAC.UserContext(r.Context(), uid)
		if err != nil {
			if errors.Is(err, rbac.ErrNotFound) {
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			writeError(w, http.StatusInternalServerError, "db_error")
			return
		}
		writeJSON(w, http.StatusOK, ctx)
		return
	}

	var out struct {
		ID    string `json:"id"`
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := h.d.DB.QueryRow(`SELECT id, email, role FROM users WHERE id = ?`, uid).Scan(&out.ID, &out.Email, &out.Role); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		writeError(w, http.StatusInternalServerError, "db_error")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type passwordChangeReq struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
	TwoFactorCode   string `json:"two_factor_code"`
	TwoFactorMethod string `json:"two_factor_method"`
}

type profileUpdateReq struct {
	Name *string `json:"name"`
}

// UpdateProfile handles PATCH /api/v1/auth/profile (authenticated).
func (h *Auth) UpdateProfile(w http.ResponseWriter, r *http.Request) {
	uid, ok := apimiddleware.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req profileUpdateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if len(name) > 120 {
			writeError(w, http.StatusBadRequest, "invalid_name")
			return
		}
		res, err := h.d.DB.ExecContext(r.Context(), `UPDATE users SET name = ? WHERE id = ?`, name, uid)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "db_error")
			return
		}
		if n, _ := res.RowsAffected(); n == 0 {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
	}
	h.writeCurrentUser(w, r, uid)
}

type emailChangeReq struct {
	Email           string `json:"email"`
	CurrentPassword string `json:"current_password"`
	TwoFactorCode   string `json:"two_factor_code"`
	TwoFactorMethod string `json:"two_factor_method"`
}

// ChangeEmail handles POST /api/v1/auth/email (authenticated). The operation
// always requires the current password and, when enabled, a fresh 2FA code.
func (h *Auth) ChangeEmail(w http.ResponseWriter, r *http.Request) {
	uid, ok := apimiddleware.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req emailChangeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if !strings.Contains(email, "@") || len(email) > 254 {
		writeError(w, http.StatusBadRequest, "invalid_email")
		return
	}
	var hash string
	if err := h.d.DB.QueryRowContext(r.Context(), `SELECT password_hash FROM users WHERE id = ?`, uid).Scan(&hash); err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if !auth.VerifyPassword(hash, req.CurrentPassword) {
		writeError(w, http.StatusUnauthorized, "invalid_credentials")
		return
	}
	if h.d.TwoFactor != nil {
		needs, err := h.d.TwoFactor.RequiresChallenge(r.Context(), uid)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "twofactor_error")
			return
		}
		if needs {
			if strings.TrimSpace(req.TwoFactorCode) == "" {
				writeError(w, http.StatusUnauthorized, "twofactor_required")
				return
			}
			if err := h.d.TwoFactor.VerifyUserCode(r.Context(), uid, req.TwoFactorCode, req.TwoFactorMethod); err != nil {
				switch {
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
		}
	}
	var exists int
	if err := h.d.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM users WHERE lower(email) = lower(?) AND id <> ?`, email, uid).Scan(&exists); err != nil {
		writeError(w, http.StatusInternalServerError, "db_error")
		return
	}
	if exists > 0 {
		writeError(w, http.StatusConflict, "email_in_use")
		return
	}
	res, err := h.d.DB.ExecContext(r.Context(), `UPDATE users SET email = ? WHERE id = ?`, email, uid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db_error")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	h.writeCurrentUser(w, r, uid)
}

// ChangePassword handles POST /api/v1/auth/password (authenticated).
func (h *Auth) ChangePassword(w http.ResponseWriter, r *http.Request) {
	uid, ok := apimiddleware.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req passwordChangeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	if err := auth.ValidateStrong(req.NewPassword); err != nil {
		writeError(w, http.StatusBadRequest, "weak_password")
		return
	}
	var hash string
	if err := h.d.DB.QueryRow(`SELECT password_hash FROM users WHERE id = ?`, uid).Scan(&hash); err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if !auth.VerifyPassword(hash, req.CurrentPassword) {
		writeError(w, http.StatusUnauthorized, "invalid_credentials")
		return
	}
	if h.d.TwoFactor != nil {
		needs, err := h.d.TwoFactor.RequiresChallenge(r.Context(), uid)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "twofactor_error")
			return
		}
		if needs {
			if strings.TrimSpace(req.TwoFactorCode) == "" {
				writeError(w, http.StatusUnauthorized, "twofactor_required")
				return
			}
			if err := h.d.TwoFactor.VerifyUserCode(r.Context(), uid, req.TwoFactorCode, req.TwoFactorMethod); err != nil {
				switch {
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
		}
	}
	if err := auth.ChangePassword(h.d.DB, uid, req.NewPassword); err != nil {
		writeError(w, http.StatusInternalServerError, "db_error")
		return
	}
	clearRefreshCookie(w, h.d.Secure)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Auth) writeCurrentUser(w http.ResponseWriter, r *http.Request, uid string) {
	if h.d.RBAC != nil {
		ctx, err := h.d.RBAC.UserContext(r.Context(), uid)
		if err != nil {
			if errors.Is(err, rbac.ErrNotFound) {
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			writeError(w, http.StatusInternalServerError, "db_error")
			return
		}
		writeJSON(w, http.StatusOK, ctx)
		return
	}
	var out struct {
		ID    string `json:"id"`
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := h.d.DB.QueryRowContext(r.Context(), `SELECT id, email, role FROM users WHERE id = ?`, uid).Scan(&out.ID, &out.Email, &out.Role); err != nil {
		writeError(w, http.StatusInternalServerError, "db_error")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func setRefreshCookie(w http.ResponseWriter, value string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     refreshCookieName,
		Value:    value,
		Path:     "/api/v1/auth",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(auth.RefreshTokenTTL.Seconds()),
	})
}

func clearRefreshCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     refreshCookieName,
		Value:    "",
		Path:     "/api/v1/auth",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}
