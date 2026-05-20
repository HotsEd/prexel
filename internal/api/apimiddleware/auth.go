package apimiddleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"

	"github.com/prexel/prexel/internal/apitoken"
)

// ctxKey is unexported to prevent collision with other packages' context keys.
type ctxKey int

const (
	ctxUserID ctxKey = iota
)

// TokenAuthenticator narrows the apitoken.Service surface we need.
// Exists mostly so tests can pass in a fake without dragging the full
// service struct in.
type TokenAuthenticator interface {
	Authenticate(ctx context.Context, raw string) (userID string, err error)
	TouchLastUsed(ctx context.Context, raw string)
}

// Auth validates an Authorization: Bearer header, accepting two flavours:
//
//   1. JWT HS256, signed with `secret`, carrying a "sub" claim — what
//      the web UI uses after /auth/login. This is the original behaviour
//      and the only mode when `tokens` is nil.
//
//   2. Personal API token (`prx_pat_…`) — looked up by SHA-256 in the
//      api_tokens table via `tokens`. Used by CLI / CI / scripts so
//      they don't need a session cookie.
//
// Detection is by prefix on the raw token. We deliberately try PAT
// FIRST when the prefix matches so a stray JWT that happens to start
// with `prx_pat_` (it won't — JWTs are base64url'd JSON) can't bypass
// the PAT path. Conversely, anything without the PAT prefix goes
// straight to JWT, no extra DB hit on the hot web path.
//
// `tokens` is optional. Wiring it in production enables PAT auth;
// leaving it nil is the legacy behaviour and is what test fixtures
// use to keep their setup tight.
func Auth(secret string, tokens TokenAuthenticator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authz := r.Header.Get("Authorization")
			if !strings.HasPrefix(authz, "Bearer ") {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			raw := strings.TrimPrefix(authz, "Bearer ")

			// PAT path — only attempt if both the prefix matches and a
			// token service is wired. Any failure here is a hard 401:
			// we don't fall through to JWT, because a malformed PAT
			// shouldn't accidentally validate as something else.
			if tokens != nil && strings.HasPrefix(raw, apitoken.TokenPrefix) {
				userID, err := tokens.Authenticate(r.Context(), raw)
				if err != nil {
					http.Error(w, "unauthorized", http.StatusUnauthorized)
					return
				}
				// Update last_used_at after the handler returns so the
				// audit write never gates the actual request. We use
				// r.Context() so a long-running stream still gets an
				// updated timestamp when it eventually finishes.
				defer tokens.TouchLastUsed(r.Context(), raw)

				ctx := context.WithValue(r.Context(), ctxUserID, userID)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			tok, err := jwt.Parse(raw, func(t *jwt.Token) (interface{}, error) {
				if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
					return nil, jwt.ErrSignatureInvalid
				}
				return []byte(secret), nil
			})
			if err != nil || !tok.Valid {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}

			claims, ok := tok.Claims.(jwt.MapClaims)
			if !ok {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			sub, _ := claims["sub"].(string)
			if sub == "" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}

			ctx := context.WithValue(r.Context(), ctxUserID, sub)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// UserIDFromContext returns the authenticated user_id, if any.
func UserIDFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(ctxUserID).(string)
	return v, ok
}

// WithUserID stamps the authenticated user_id on a context. Intended for
// tests and a few internal flows (e.g. /setup → autologin) that bypass
// the Auth middleware but still need handlers to find a user identity.
func WithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, ctxUserID, userID)
}
