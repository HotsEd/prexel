package apimiddleware

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prexel/prexel/internal/auth"
)

const testJWTSecret = "test-jwt-secret-32-chars-minimum-aaaaaa"

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}

// --- Auth ---------------------------------------------------------------

func TestAuth_RejectsNoBearer(t *testing.T) {
	h := Auth(testJWTSecret, nil)(okHandler())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestAuth_RejectsBadToken(t *testing.T) {
	h := Auth(testJWTSecret, nil)(okHandler())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps", nil)
	req.Header.Set("Authorization", "Bearer not-a-jwt")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestAuth_AcceptsValidToken(t *testing.T) {
	tok, _, err := auth.IssueAccessToken(testJWTSecret, "user-42")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	captured := ""
	h := Auth(testJWTSecret, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid, _ := UserIDFromContext(r.Context())
		captured = uid
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if captured != "user-42" {
		t.Errorf("user_id not propagated: got %q", captured)
	}
}

func TestAuth_RejectsWrongSecret(t *testing.T) {
	tok, _, _ := auth.IssueAccessToken(testJWTSecret, "u1")
	h := Auth("different-secret-32-chars-minimum-bbb", nil)(okHandler())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 with wrong secret, got %d", rec.Code)
	}
}

// --- SetupGate ----------------------------------------------------------

type stubSetup struct{ done bool }

func (s *stubSetup) Completed() bool { return s.done }

func TestSetupGate_RedirectsSPAPreSetup(t *testing.T) {
	h := SetupGate(&stubSetup{done: false})(okHandler())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/apps/some-app", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Errorf("expected 302, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/setup" {
		t.Errorf("expected redirect to /setup, got %q", loc)
	}
}

func TestSetupGate_PassesSetupRoutePreSetup(t *testing.T) {
	// The SPA route /setup itself must pass through pre-setup so the wizard
	// can render (otherwise we end up in a 302 loop).
	h := SetupGate(&stubSetup{done: false})(okHandler())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/setup", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected /setup to pass, got %d", rec.Code)
	}
}

func TestSetupGate_PassesAssetsPreSetup(t *testing.T) {
	h := SetupGate(&stubSetup{done: false})(okHandler())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/assets/index-abc.js", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected asset to pass, got %d", rec.Code)
	}
}

func TestSetupGate_AllowsSetupPathsPreSetup(t *testing.T) {
	h := SetupGate(&stubSetup{done: false})(okHandler())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup/admin", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestSetupGate_403APIPreSetup(t *testing.T) {
	h := SetupGate(&stubSetup{done: false})(okHandler())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

func TestSetupGate_SealsWizardAfterCompletion(t *testing.T) {
	h := SetupGate(&stubSetup{done: true})(okHandler())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup/admin", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 sealed, got %d", rec.Code)
	}
	// /status survives so SPA can poll.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/setup/status", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected /setup/status to pass: %d", rec.Code)
	}
}

func TestSetupGate_HealthzAlwaysPasses(t *testing.T) {
	for _, done := range []bool{false, true} {
		h := SetupGate(&stubSetup{done: done})(okHandler())
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/healthz", nil)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("healthz blocked (done=%v): %d", done, rec.Code)
		}
	}
}

// --- LoginRateLimit -----------------------------------------------------

func TestLoginRateLimit_AllowsBurst_ThenBlocks(t *testing.T) {
	h := LoginRateLimit(RateLimitConfig{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate a failed login.
		CountLoginFailure(r.Context())
		w.WriteHeader(http.StatusUnauthorized)
	}))

	codes := []int{}
	for i := 0; i < 7; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader("{}"))
		req.RemoteAddr = "10.0.0.1:1234"
		h.ServeHTTP(rec, req)
		codes = append(codes, rec.Code)
	}
	// Expect first 5 to be 401 (failures within budget), 6th to be 429.
	for i := 0; i < 5; i++ {
		if codes[i] != http.StatusUnauthorized {
			t.Errorf("attempt %d: expected 401, got %d", i+1, codes[i])
		}
	}
	if codes[5] != http.StatusTooManyRequests {
		t.Errorf("6th attempt: expected 429, got %d", codes[5])
	}
}

func TestLoginRateLimit_DifferentIPsIndependent(t *testing.T) {
	h := LoginRateLimit(RateLimitConfig{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		CountLoginFailure(r.Context())
		w.WriteHeader(http.StatusUnauthorized)
	}))

	// Burn IP A.
	for i := 0; i < 6; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		h.ServeHTTP(rec, req)
	}
	// IP B unaffected.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.RemoteAddr = "10.0.0.2:5678"
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("IP B should not be rate-limited: %d", rec.Code)
	}
}

func TestLoginRateLimit_IgnoresXFFWithoutTrustedProxy(t *testing.T) {
	// Without TrustedProxies, the middleware must NEVER honour
	// X-Forwarded-For — otherwise any attacker can bypass the rate
	// limit by rotating the header per request.
	h := LoginRateLimit(RateLimitConfig{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		CountLoginFailure(r.Context())
		w.WriteHeader(http.StatusUnauthorized)
	}))
	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		req.Header.Set("X-Forwarded-For", "203.0.113."+itoaTest(i))
		h.ServeHTTP(rec, req)
	}
	// 6th attempt from the same RemoteAddr must now be 429 regardless of
	// what XFF claims — the limiter is bucketed on the real IP.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.99")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("untrusted XFF bypassed rate limit: got %d", rec.Code)
	}
}

func TestLoginRateLimit_HonoursXFFFromTrustedProxy(t *testing.T) {
	// With TrustedProxies set, the middleware should bucket on the
	// upstream-claimed client IP, so distinct XFF values restart from
	// a fresh budget.
	h := LoginRateLimit(RateLimitConfig{TrustedProxies: []string{"10.0.0.0/8"}})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		CountLoginFailure(r.Context())
		w.WriteHeader(http.StatusUnauthorized)
	}))
	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		req.Header.Set("X-Forwarded-For", "203.0.113.7")
		h.ServeHTTP(rec, req)
	}
	// Same proxy, NEW client IP — should still have budget.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.8")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("fresh client behind trusted proxy was rate-limited: %d", rec.Code)
	}
}

// --- RefreshRateLimit ---------------------------------------------------

func TestRefreshRateLimit_BlocksAfterBurst(t *testing.T) {
	h := RefreshRateLimit(RateLimitConfig{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	codes := []int{}
	for i := 0; i < 32; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil)
		req.RemoteAddr = "192.0.2.1:5555"
		h.ServeHTTP(rec, req)
		codes = append(codes, rec.Code)
	}
	// First 30 within budget, then 429.
	for i := 0; i < 30; i++ {
		if codes[i] != http.StatusOK {
			t.Errorf("attempt %d: expected 200, got %d", i+1, codes[i])
		}
	}
	for i := 30; i < 32; i++ {
		if codes[i] != http.StatusTooManyRequests {
			t.Errorf("attempt %d: expected 429, got %d", i+1, codes[i])
		}
	}
}

// itoaTest is a tiny stringifier so the XFF tests don't pull in strconv
// for a single value. Limited to 0..9 which is all we need.
func itoaTest(n int) string {
	if n < 0 || n > 9 {
		return ""
	}
	return string(rune('0' + n))
}

// --- RequestSize --------------------------------------------------------

func TestRequestSize_RejectsLarge(t *testing.T) {
	called := false
	h := RequestSize(100)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		_, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "too big", http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	body := bytes.Repeat([]byte("a"), 200)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	h.ServeHTTP(rec, req)
	if !called {
		t.Fatal("handler not called")
	}
	if rec.Code == http.StatusOK {
		t.Errorf("expected non-200 for oversized body, got %d", rec.Code)
	}
}

func TestRequestSize_AcceptsSmall(t *testing.T) {
	h := RequestSize(100)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "fail", http.StatusBadRequest)
			return
		}
		_, _ = w.Write(b)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("hello"))
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if rec.Body.String() != "hello" {
		t.Errorf("body: %q", rec.Body.String())
	}
}

// Avoid unused import on time during test runs.
var _ = time.Second
