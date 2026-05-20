package apimiddleware

import (
	"context"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// RateLimitConfig collects the knobs the rate-limit middleware needs.
// Today there's exactly one: which upstream IPs to trust when reading
// X-Forwarded-For / X-Real-IP. Anything not on this list is treated as
// an attacker spoofing headers — we fall back to r.RemoteAddr.
//
// TrustedProxies is a slice of CIDRs (e.g. "10.0.0.0/8", "::1/128").
// Empty == trust nobody == always use RemoteAddr. The empty default is
// load-bearing: a careless operator who terminates TLS in front of
// Prexel and then leaves PREXEL_TRUSTED_PROXIES unset gets the
// safe behaviour (rate limit per real connection, even if the headers
// claim a different client) instead of the dangerous one.
type RateLimitConfig struct {
	TrustedProxies []string
}

// LoginRateLimit returns a middleware that limits failed login attempts to
// 5 / 15 minutes per client IP. The middleware itself only checks whether the
// caller has budget left; the handler is expected to call CountLoginFailure on
// a 401 so successful logins don't consume the bucket.
//
// Implementation: token bucket per IP with lazy cleanup of stale entries.
// Bucket starts full and refills at loginBurst tokens / loginWindow.
func LoginRateLimit(cfg RateLimitConfig) func(http.Handler) http.Handler {
	limiter := newLoginLimiter()
	go limiter.cleanupLoop()
	trusted := parseTrusted(cfg.TrustedProxies)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := clientIP(r, trusted)
			if !limiter.allow(ip) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"error":"rate_limited"}`))
				return
			}
			ctx := context.WithValue(r.Context(), limiterCtxKey{}, &limiterHandle{limiter: limiter, ip: ip})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RefreshRateLimit caps refresh-token endpoint usage at refreshBurst /
// refreshWindow per client IP. Same trust-proxies behaviour as
// LoginRateLimit. We separate the limiter instance so refreshes can't
// starve logins (and vice versa).
//
// Unlike LoginRateLimit there's no "spend on failure" pattern here —
// every call decrements the bucket. Refresh tokens are short-lived
// and used at most every 14 minutes by a well-behaved client, so a
// 30/min cap leaves plenty of headroom while still throttling a
// stolen refresh token in a brute-force loop.
func RefreshRateLimit(cfg RateLimitConfig) func(http.Handler) http.Handler {
	limiter := newRefreshLimiter()
	go limiter.cleanupLoop()
	trusted := parseTrusted(cfg.TrustedProxies)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := clientIP(r, trusted)
			if !limiter.allow(ip) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"error":"rate_limited"}`))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

const (
	loginBurst      = 5
	loginWindow     = 15 * time.Minute
	loginCleanupTTL = 30 * time.Minute

	// 30 requests / minute / IP for /auth/refresh. Well above any
	// legitimate client (the SPA refreshes every ~14min) but enough
	// to throttle a stolen refresh token used in a tight loop.
	refreshBurst      = 30
	refreshWindow     = 1 * time.Minute
	refreshCleanupTTL = 5 * time.Minute
)

type loginBucket struct {
	tokens   float64
	lastSeen time.Time
}

type loginLimiter struct {
	mu      sync.Mutex
	buckets map[string]*loginBucket
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{buckets: map[string]*loginBucket{}}
}

// allow reports whether the IP has at least one token available. It does not
// spend the token — countFailure does that on real failure.
func (l *loginLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.refill(ip).tokens >= 1
}

func (l *loginLimiter) countFailure(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.refill(ip)
	b.tokens--
	if b.tokens < 0 {
		b.tokens = 0
	}
}

func (l *loginLimiter) refill(ip string) *loginBucket {
	now := time.Now()
	b, ok := l.buckets[ip]
	if !ok {
		b = &loginBucket{tokens: loginBurst, lastSeen: now}
		l.buckets[ip] = b
		return b
	}
	elapsed := now.Sub(b.lastSeen)
	if elapsed > 0 {
		b.tokens += float64(loginBurst) * (float64(elapsed) / float64(loginWindow))
		if b.tokens > float64(loginBurst) {
			b.tokens = float64(loginBurst)
		}
	}
	b.lastSeen = now
	return b
}

func (l *loginLimiter) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		l.mu.Lock()
		cutoff := time.Now().Add(-loginCleanupTTL)
		for ip, b := range l.buckets {
			if b.lastSeen.Before(cutoff) {
				delete(l.buckets, ip)
			}
		}
		l.mu.Unlock()
	}
}

// refreshLimiter is structurally identical to loginLimiter but with its
// own bucket map and refresh-specific tunables. Kept separate so a
// burst of refresh attempts can't share or interfere with the
// login-failure bucket on the same IP.
type refreshLimiter struct {
	mu      sync.Mutex
	buckets map[string]*loginBucket
}

func newRefreshLimiter() *refreshLimiter {
	return &refreshLimiter{buckets: map[string]*loginBucket{}}
}

// allow spends a token on every call. Unlike login, refresh is metered
// per-request, not per-failure — see the package comment in
// RefreshRateLimit for why.
func (l *refreshLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.refill(ip)
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func (l *refreshLimiter) refill(ip string) *loginBucket {
	now := time.Now()
	b, ok := l.buckets[ip]
	if !ok {
		b = &loginBucket{tokens: refreshBurst, lastSeen: now}
		l.buckets[ip] = b
		return b
	}
	elapsed := now.Sub(b.lastSeen)
	if elapsed > 0 {
		b.tokens += float64(refreshBurst) * (float64(elapsed) / float64(refreshWindow))
		if b.tokens > float64(refreshBurst) {
			b.tokens = float64(refreshBurst)
		}
	}
	b.lastSeen = now
	return b
}

func (l *refreshLimiter) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		l.mu.Lock()
		cutoff := time.Now().Add(-refreshCleanupTTL)
		for ip, b := range l.buckets {
			if b.lastSeen.Before(cutoff) {
				delete(l.buckets, ip)
			}
		}
		l.mu.Unlock()
	}
}

// parseTrusted converts the operator-supplied CIDR list into the form
// clientIP wants: a slice of *net.IPNet ready for IP membership checks.
// Invalid entries are silently dropped — startup logs them upstream
// (config validation); inside the hot path we want the lookup to keep
// running even if one entry is malformed.
func parseTrusted(cidrs []string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, s := range cidrs {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		// Allow bare IPs as a /32 (or /128) shortcut.
		if !strings.Contains(s, "/") {
			if ip := net.ParseIP(s); ip != nil {
				bits := 32
				if ip.To4() == nil {
					bits = 128
				}
				s = s + "/" + itoa(bits)
			}
		}
		_, ipNet, err := net.ParseCIDR(s)
		if err != nil {
			continue
		}
		out = append(out, ipNet)
	}
	return out
}

// itoa is a stdlib-free int-to-string for the small numeric range
// parseTrusted needs. Pulled out so parseTrusted doesn't need
// strconv just for two values.
func itoa(n int) string {
	switch n {
	case 32:
		return "32"
	case 128:
		return "128"
	default:
		// Fall back via fmt only when called with an unexpected value.
		return ""
	}
}

// clientIP returns the IP we'll meter against. Policy:
//
//   - We always start with r.RemoteAddr (the actual TCP peer). This is
//     the only value we can trust unconditionally.
//   - If r.RemoteAddr falls inside `trusted`, we accept the upstream's
//     claim about who the original client was: first
//     X-Forwarded-For entry, falling back to X-Real-IP, falling back to
//     RemoteAddr itself.
//   - If r.RemoteAddr is NOT in `trusted`, we ignore the headers
//     entirely. A user with no proxy in front of Prexel cannot
//     bypass the limiter by setting X-Forwarded-For themselves.
//
// chi's middleware.RealIP rewrites r.RemoteAddr before we even get
// here, but only when it sees X-Forwarded-For — which is exactly the
// header we're guarding against. So we must NOT rely on whatever
// RealIP did; we re-do the trust check on the raw remote address.
func clientIP(r *http.Request, trusted []*net.IPNet) string {
	remote := remoteHost(r.RemoteAddr)
	if len(trusted) == 0 {
		return remote
	}
	ip := net.ParseIP(remote)
	if ip == nil || !inTrusted(ip, trusted) {
		return remote
	}
	// Trusted upstream — honour its header. XFF can be a comma-separated
	// chain of proxies; the leftmost entry is the original client.
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		first := strings.TrimSpace(strings.SplitN(xff, ",", 2)[0])
		if first != "" {
			return first
		}
	}
	if xr := strings.TrimSpace(r.Header.Get("X-Real-IP")); xr != "" {
		return xr
	}
	return remote
}

func remoteHost(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

func inTrusted(ip net.IP, trusted []*net.IPNet) bool {
	for _, n := range trusted {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// --- request-scoped counter handle, exported for handler/auth.go --- //

type limiterCtxKey struct{}

type limiterHandle struct {
	limiter *loginLimiter
	ip      string
}

// CountLoginFailure decrements the caller's IP token bucket after a failed
// login. No-op if the limiter middleware is not active for the request.
func CountLoginFailure(ctx context.Context) {
	h, _ := ctx.Value(limiterCtxKey{}).(*limiterHandle)
	if h == nil {
		return
	}
	h.limiter.countFailure(h.ip)
}
