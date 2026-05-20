package domains

import (
	"context"
	"crypto/tls"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// SSL monitor cadence and failure thresholds.
const (
	sslDefaultInterval     = 120 * time.Second
	sslHandshakeTimeout    = 5 * time.Second
	sslIssuingMaxAttempts  = 3
	sslIssuingFailDuration = 10 * time.Minute
	sslSelfSignedTTL       = 90 * 24 * time.Hour // dev: simulate Let's Encrypt cert lifetime
)

// failureTracker holds per-domain failure counters used to decide when an
// `issuing` row should flip to `failed`. We keep it in memory because the
// information is fundamentally ephemeral (handshake-based) and we'd rather
// not write to the DB every 120s.
type failureTracker struct {
	mu       sync.Mutex
	attempts map[string]int
	firstSeen map[string]time.Time
}

func newFailureTracker() *failureTracker {
	return &failureTracker{
		attempts:  map[string]int{},
		firstSeen: map[string]time.Time{},
	}
}

func (t *failureTracker) record(id string) (int, time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.attempts[id]++
	if _, ok := t.firstSeen[id]; !ok {
		t.firstSeen[id] = time.Now()
	}
	return t.attempts[id], time.Since(t.firstSeen[id])
}

func (t *failureTracker) clear(id string) {
	t.mu.Lock()
	delete(t.attempts, id)
	delete(t.firstSeen, id)
	t.mu.Unlock()
}

// SSLLoop polls every domain in `issuing` or `active` and updates ssl_status
// + ssl_expires_at based on a live TLS handshake. In dev mode (settings
// tls_mode=self-signed) the loop simulates issuance: any `issuing` row is
// promoted to `active` immediately with a 90-day fake expiration. This avoids
// requiring real Let's Encrypt access from development environments.
//
// Pass <=0 for the default 120s.
func SSLLoop(ctx context.Context, svc *Service, interval time.Duration) {
	if interval <= 0 {
		interval = sslDefaultInterval
	}
	logger := slog.With("loop", "sslmonitor", "interval", interval.String())
	logger.Info("starting")
	failures := newFailureTracker()

	t := time.NewTimer(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			logger.Info("stopping")
			return
		case <-t.C:
			runSSLPass(ctx, svc, failures, logger)
			t.Reset(interval)
		}
	}
}

func runSSLPass(ctx context.Context, svc *Service, failures *failureTracker, logger *slog.Logger) {
	domains, err := svc.repo.listForSSLMonitor(ctx)
	if err != nil {
		logger.Warn("list failed", "err", err)
		return
	}
	if len(domains) == 0 {
		return
	}
	mode, err := svc.repo.tlsMode(ctx)
	if err != nil {
		logger.Warn("tls_mode lookup failed", "err", err)
	}
	devMode := mode == "self-signed"

	for i := range domains {
		d := &domains[i]
		if devMode {
			handleDevMode(ctx, svc, d, failures, logger)
			continue
		}
		handleProdMode(ctx, svc, d, failures, logger)
	}
}

// handleDevMode short-circuits any `issuing` row to `active` with a 90-day
// simulated expiration. `active` rows are left alone — operators can clear by
// deleting/recreating the domain.
func handleDevMode(ctx context.Context, svc *Service, d *Domain, failures *failureTracker, logger *slog.Logger) {
	if d.SSLStatus != "issuing" {
		return
	}
	exp := time.Now().Add(sslSelfSignedTTL).Unix()
	if err := svc.repo.markSSLStatus(ctx, d.ID, "active", &exp); err != nil {
		logger.Warn("dev: mark active failed", "domain", describe(d), "err", err)
		return
	}
	failures.clear(d.ID)
	svc.publish("domain.ssl.changed", d.ID)
	logger.Info("dev: simulated active cert", "domain", d.Name, "expires_at", exp)
}

// handleProdMode performs an outbound TLS handshake against the domain's
// public 443 endpoint. We trust the cert presented (we're not authenticating
// the server, we just want to read the cert metadata). A cert signed by a
// Let's Encrypt-like issuer flips status to `active`.
func handleProdMode(ctx context.Context, svc *Service, d *Domain, failures *failureTracker, logger *slog.Logger) {
	dialer := &tls.Dialer{
		Config: &tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: true, // we read the cert, we don't verify identity
		},
	}
	dialCtx, cancel := context.WithTimeout(ctx, sslHandshakeTimeout)
	defer cancel()
	conn, err := dialer.DialContext(dialCtx, "tcp", d.Name+":443")
	if err != nil {
		recordSSLFailure(ctx, svc, d, failures, logger, "dial: "+err.Error())
		return
	}
	tlsConn := conn.(*tls.Conn)
	state := tlsConn.ConnectionState()
	_ = tlsConn.Close()
	if len(state.PeerCertificates) == 0 {
		recordSSLFailure(ctx, svc, d, failures, logger, "no peer certs")
		return
	}
	cert := state.PeerCertificates[0]

	// Heuristic: consider the cert "real" when its issuer mentions Let's
	// Encrypt OR when it is clearly not self-signed (issuer != subject) and
	// has a populated NotAfter. This intentionally accepts other CAs (ZeroSSL,
	// Cloudflare) — operators may legitimately front Prexel with their own.
	issuer := cert.Issuer.CommonName
	if !strings.Contains(strings.ToLower(issuer), "let's encrypt") &&
		cert.Issuer.String() == cert.Subject.String() {
		recordSSLFailure(ctx, svc, d, failures, logger, "self-signed cert seen")
		return
	}
	exp := cert.NotAfter.Unix()
	if d.SSLStatus != "active" {
		svc.publish("domain.ssl.changed", d.ID)
	}
	if err := svc.repo.markSSLStatus(ctx, d.ID, "active", &exp); err != nil {
		logger.Warn("mark active failed", "domain", describe(d), "err", err)
		return
	}
	failures.clear(d.ID)
}

// recordSSLFailure increments the in-memory failure counter. If the row has
// been in `issuing` for >10min AND we've seen >=3 failures, flip to `failed`.
// `active` rows are left alone (transient handshake failures shouldn't
// invalidate a previously-confirmed cert — they are usually network glitches).
func recordSSLFailure(ctx context.Context, svc *Service, d *Domain, failures *failureTracker, logger *slog.Logger, reason string) {
	attempts, elapsed := failures.record(d.ID)
	logger.Debug("ssl handshake failed", "domain", d.Name, "reason", reason, "attempts", attempts, "elapsed", elapsed.String())
	if d.SSLStatus != "issuing" {
		return
	}
	if attempts >= sslIssuingMaxAttempts && elapsed >= sslIssuingFailDuration {
		if err := svc.repo.markSSLStatus(ctx, d.ID, "failed", nil); err != nil {
			logger.Warn("mark failed failed", "domain", describe(d), "err", err)
			return
		}
		failures.clear(d.ID)
		svc.publish("domain.ssl.failed", d.ID)
		logger.Warn("ssl issuance abandoned", "domain", d.Name, "attempts", attempts, "elapsed", elapsed.String())
	}
}
