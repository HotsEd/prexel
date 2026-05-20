// Package dnszone owns the "apex domain" concept introduced by migration 007.
//
// A zone is the registrable name an operator controls (e.g. `meudominio.com`
// or `meudominio.com.br`). Subdomains the operator adds to apps belong to
// exactly one zone. Knowing the zone lets us:
//
//   - Show the operator one row per apex they manage, with a list of
//     subdomains under it (the "tabela com tudo cadastrado" pedida).
//   - Probe for an upstream `*.apex` wildcard once and reuse the result
//     across every subdomain under that apex.
//   - Auto-create the zone the first time a subdomain under it shows up,
//     so the operator never has to "register the zone before adding the
//     subdomain". Friction-free.
//
// Apex extraction uses publicsuffix so multi-label TLDs work correctly:
//   foo.com           → foo.com
//   api.foo.com       → foo.com
//   api.foo.co.uk     → foo.co.uk     (NOT co.uk)
//   tenant.acme.dev   → acme.dev
//
// Internal/IP-only names (localhost, IP literals, single-label) are
// rejected by ApexOf so the caller can produce a clear validation error.
package dnszone

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/net/publicsuffix"
)

// Zone mirrors the dns_zones row 1:1.
type Zone struct {
	ID                  string  `json:"id"`
	Apex                string  `json:"apex"`
	TargetIP            string  `json:"target_ip,omitempty"`
	ApexVerified        bool    `json:"apex_verified"`
	ApexVerifiedAt      *int64  `json:"apex_verified_at,omitempty"`
	ApexLastCheck       *int64  `json:"apex_last_check,omitempty"`
	WildcardVerified    bool    `json:"wildcard_verified"`
	WildcardVerifiedAt  *int64  `json:"wildcard_verified_at,omitempty"`
	WildcardLastCheck   *int64  `json:"wildcard_last_check,omitempty"`
	Notes               string  `json:"notes,omitempty"`
	CreatedAt           int64   `json:"created_at"`
	UpdatedAt           int64   `json:"updated_at"`
	// Decorated fields. Populated by the service for list views; never
	// stored on the row itself.
	SubdomainCount int `json:"subdomain_count,omitempty"`
}

// Sentinel errors. Handlers map them to wire codes.
var (
	ErrNotFound      = errors.New("dnszone: not found")
	ErrInvalidApex   = errors.New("dnszone: invalid apex")
	ErrInternalName  = errors.New("dnszone: cannot host internal/non-public name")
)

// Resolver is the subset of net.Resolver we need. Tests inject a fake.
type Resolver interface {
	LookupHost(ctx context.Context, host string) ([]string, error)
}

// Service is the read/write surface over dns_zones.
type Service struct {
	db       *sql.DB
	resolver Resolver
	now      func() time.Time
	timeout  time.Duration
}

// NewService wires the service. resolver may be nil to fall back to the
// system resolver; tests inject fakes.
func NewService(db *sql.DB, resolver Resolver) *Service {
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	return &Service{
		db:       db,
		resolver: resolver,
		now:      time.Now,
		timeout:  5 * time.Second,
	}
}

// ----- Apex extraction --------------------------------------------------

// ApexOf returns the registrable apex of fqdn (e.g. "api.foo.co.uk" →
// "foo.co.uk"). Returns ErrInvalidApex / ErrInternalName for bad input.
//
// We rely on publicsuffix so multi-label TLDs (.co.uk, .com.br, .gov.br)
// resolve correctly. Single-label names ("localhost"), IP literals,
// and names whose registrable form equals their own input
// (i.e. they ARE just a TLD/eTLD) are rejected.
func ApexOf(fqdn string) (string, error) {
	clean := strings.TrimSpace(strings.ToLower(fqdn))
	clean = strings.TrimSuffix(clean, ".") // canonical: no trailing dot
	if clean == "" {
		return "", ErrInvalidApex
	}
	if net.ParseIP(clean) != nil {
		return "", ErrInternalName
	}
	if !strings.Contains(clean, ".") {
		return "", ErrInternalName // "localhost", bare hostnames
	}

	registrable, err := publicsuffix.EffectiveTLDPlusOne(clean)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidApex, err)
	}
	return registrable, nil
}

// IsApex reports whether fqdn equals its own registrable apex (no subdomain).
// IsApex("foo.com") → true, IsApex("api.foo.com") → false.
func IsApex(fqdn string) bool {
	apex, err := ApexOf(fqdn)
	if err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSuffix(strings.TrimSpace(fqdn), "."), apex)
}

// ----- Repository surface ----------------------------------------------

// EnsureForFQDN finds or creates the zone matching fqdn's apex. Used by
// domains.Service.Create to auto-register a zone the first time a
// subdomain under it is added.
func (s *Service) EnsureForFQDN(ctx context.Context, fqdn string) (*Zone, error) {
	apex, err := ApexOf(fqdn)
	if err != nil {
		return nil, err
	}
	if z, err := s.GetByApex(ctx, apex); err == nil {
		return z, nil
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	return s.create(ctx, apex)
}

// Create is the explicit "I want to pre-register this zone" entry point.
// Idempotent: returns the existing zone if the apex was already registered.
func (s *Service) Create(ctx context.Context, apex string) (*Zone, error) {
	if _, err := ApexOf(apex); err != nil {
		// Caller may pass a subdomain by mistake — be forgiving by extracting.
		// If the user typed "api.foo.com", they probably wanted "foo.com".
		_, derr := ApexOf("x." + apex)
		if derr != nil {
			return nil, err
		}
	}
	cleaned, err := ApexOf(apex)
	if err != nil {
		// retry through the "treat as subdomain" path above
		cleaned, err = ApexOf("x." + apex)
		if err != nil {
			return nil, err
		}
	}
	if z, err := s.GetByApex(ctx, cleaned); err == nil {
		return z, nil
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	return s.create(ctx, cleaned)
}

func (s *Service) create(ctx context.Context, apex string) (*Zone, error) {
	id := uuid.NewString()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO dns_zones (id, apex) VALUES (?, ?)`,
		id, apex,
	); err != nil {
		return nil, fmt.Errorf("dnszone: insert: %w", err)
	}
	return s.Get(ctx, id)
}

// Get reads a single zone by id.
func (s *Service) Get(ctx context.Context, id string) (*Zone, error) {
	return s.scanOne(ctx, "WHERE id = ?", id)
}

// GetByApex reads a single zone by apex domain.
func (s *Service) GetByApex(ctx context.Context, apex string) (*Zone, error) {
	return s.scanOne(ctx, "WHERE apex = ?", strings.ToLower(apex))
}

// ApexRegistered is the boolean form of GetByApex — exists / not exists,
// without materialising the row. Used by gating callers (e.g. the instance
// settings update) that only need to know whether the zone is on file.
func (s *Service) ApexRegistered(ctx context.Context, apex string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM dns_zones WHERE apex = ?`,
		strings.ToLower(strings.TrimSpace(apex))).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// ApexOf is a thin method wrapper so callers can satisfy small interfaces
// (e.g. instance.ZoneRegistry) with a single concrete dnszone.Service
// without importing the package-level function separately.
func (s *Service) ApexOf(fqdn string) (string, error) { return ApexOf(fqdn) }

// List returns every zone with subdomain counts attached.
func (s *Service) List(ctx context.Context) ([]Zone, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT z.id, z.apex, z.target_ip,
		       z.apex_verified, z.apex_verified_at, z.apex_last_check,
		       z.wildcard_verified, z.wildcard_verified_at, z.wildcard_last_check,
		       z.notes, z.created_at, z.updated_at,
		       (SELECT COUNT(*) FROM domains d WHERE d.zone_id = z.id) AS sub_count
		FROM dns_zones z
		ORDER BY z.apex
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Zone
	for rows.Next() {
		var z Zone
		if err := scan(rows, &z, true); err != nil {
			return nil, err
		}
		out = append(out, z)
	}
	return out, rows.Err()
}

// UpdateNotes sets the operator-supplied notes field.
func (s *Service) UpdateNotes(ctx context.Context, id, notes string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE dns_zones SET notes = ? WHERE id = ?`, notes, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete removes the zone. Domains pointing at it have zone_id set to NULL
// by the FK (ON DELETE SET NULL).
func (s *Service) Delete(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM dns_zones WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Service) scanOne(ctx context.Context, where string, args ...any) (*Zone, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT z.id, z.apex, z.target_ip,
		       z.apex_verified, z.apex_verified_at, z.apex_last_check,
		       z.wildcard_verified, z.wildcard_verified_at, z.wildcard_last_check,
		       z.notes, z.created_at, z.updated_at,
		       (SELECT COUNT(*) FROM domains d WHERE d.zone_id = z.id) AS sub_count
		FROM dns_zones z `+where+` LIMIT 1
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, ErrNotFound
	}
	var z Zone
	if err := scan(rows, &z, true); err != nil {
		return nil, err
	}
	return &z, nil
}

func scan(r interface{ Scan(...any) error }, z *Zone, withCount bool) error {
	var (
		targetIP, notes                                       sql.NullString
		apexVerifiedAt, apexLast, wildVerifiedAt, wildLast    sql.NullInt64
		apexVerified, wildcardVerified                        int
		subCount                                              int
	)
	dst := []any{
		&z.ID, &z.Apex, &targetIP,
		&apexVerified, &apexVerifiedAt, &apexLast,
		&wildcardVerified, &wildVerifiedAt, &wildLast,
		&notes, &z.CreatedAt, &z.UpdatedAt,
	}
	if withCount {
		dst = append(dst, &subCount)
	}
	if err := r.Scan(dst...); err != nil {
		return err
	}
	if targetIP.Valid {
		z.TargetIP = targetIP.String
	}
	if notes.Valid {
		z.Notes = notes.String
	}
	z.ApexVerified = apexVerified == 1
	z.WildcardVerified = wildcardVerified == 1
	if apexVerifiedAt.Valid {
		v := apexVerifiedAt.Int64
		z.ApexVerifiedAt = &v
	}
	if apexLast.Valid {
		v := apexLast.Int64
		z.ApexLastCheck = &v
	}
	if wildVerifiedAt.Valid {
		v := wildVerifiedAt.Int64
		z.WildcardVerifiedAt = &v
	}
	if wildLast.Valid {
		v := wildLast.Int64
		z.WildcardLastCheck = &v
	}
	if withCount {
		z.SubdomainCount = subCount
	}
	return nil
}

// ----- Verification ----------------------------------------------------

// VerificationResult is returned by Verify and exposes the probe outcomes
// the UI needs to drive Step 4 of the add-domain wizard.
//
//   - Apex is always populated (the A record at the zone root).
//   - Wildcard is always populated (resolves a random subdomain to see
//     whether the operator has `*.apex` configured upstream).
//   - Hostname is only populated when the caller passed `hostname` to
//     Verify — the probe that matters when adding a specific subdomain
//     like `api.foo.com`. Lets the UI tell the operator "your subdomain
//     resolves, ignore apex/wildcard" without scaring them with a red
//     apex card they don't care about.
type VerificationResult struct {
	Apex     ProbeResult  `json:"apex"`
	Wildcard ProbeResult  `json:"wildcard"`
	Hostname *ProbeResult `json:"hostname,omitempty"`
}

// ProbeResult captures a single DNS lookup outcome.
type ProbeResult struct {
	OK        bool     `json:"ok"`
	ResolvedTo []string `json:"resolved_to,omitempty"`
	Expected  string   `json:"expected,omitempty"`
	Probed    string   `json:"probed,omitempty"` // the FQDN that was actually queried
	Error     string   `json:"error,omitempty"`
}

// VerifyOptions controls which extra probes Verify performs on top of the
// always-on apex + wildcard pair. Currently just Hostname — when non-empty,
// Verify also resolves that FQDN and includes the result under
// VerificationResult.Hostname.
type VerifyOptions struct {
	// Hostname is the specific FQDN to probe (e.g. "api.example.com").
	// Must be under the zone's apex; mismatches are still probed but the
	// hostname result will obviously fail.
	Hostname string
}

// Verify runs the apex A check, the wildcard probe, and (optionally) a
// hostname probe, then persists the apex/wildcard results to the zone row.
// expectedIP is the IP the operator wants every record under the zone to
// point at (usually the public IP detected on boot).
//
// We always run apex+wildcard regardless of the previous state — the user
// explicitly asked "always verify DNS of everything", so cached results
// are only a UX hint, never an authority. The hostname probe is opt-in
// because most callers (background loop) only care about zone-wide state.
func (s *Service) Verify(ctx context.Context, zoneID, expectedIP string, opts VerifyOptions) (*VerificationResult, error) {
	z, err := s.Get(ctx, zoneID)
	if err != nil {
		return nil, err
	}
	res := &VerificationResult{
		Apex:     s.probeApex(ctx, z.Apex, expectedIP),
		Wildcard: s.probeWildcard(ctx, z.Apex, expectedIP),
	}
	if opts.Hostname != "" {
		host := strings.ToLower(strings.TrimSpace(opts.Hostname))
		host = strings.TrimSuffix(host, ".")
		hp := s.probeHostname(ctx, host, expectedIP)
		res.Hostname = &hp
	}
	now := s.now().Unix()
	target := sql.NullString{String: expectedIP, Valid: expectedIP != ""}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE dns_zones SET
			target_ip            = COALESCE(?, target_ip),
			apex_verified        = ?,
			apex_verified_at     = CASE WHEN ? = 1 THEN ? ELSE apex_verified_at END,
			apex_last_check      = ?,
			wildcard_verified    = ?,
			wildcard_verified_at = CASE WHEN ? = 1 THEN ? ELSE wildcard_verified_at END,
			wildcard_last_check  = ?
		WHERE id = ?
	`,
		target,
		boolToInt(res.Apex.OK), boolToInt(res.Apex.OK), now, now,
		boolToInt(res.Wildcard.OK), boolToInt(res.Wildcard.OK), now, now,
		zoneID,
	); err != nil {
		return nil, fmt.Errorf("dnszone: persist verify: %w", err)
	}
	return res, nil
}

// probeHostname resolves the exact FQDN passed by the caller. Used by the
// add-domain wizard so the operator sees a probe of the thing they actually
// configured (e.g. `api.foo.com`), not just the zone-wide apex/wildcard.
// Matches the apex probe's NXDOMAIN handling: missing record is a clean
// `ok=false` with no Error string.
func (s *Service) probeHostname(ctx context.Context, fqdn, expectedIP string) ProbeResult {
	lookupCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	addrs, err := s.resolver.LookupHost(lookupCtx, fqdn)
	out := ProbeResult{Probed: fqdn, Expected: expectedIP, ResolvedTo: addrs}
	if err != nil {
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
			return out
		}
		out.Error = err.Error()
		return out
	}
	out.OK = expectedIP != "" && containsAddr(addrs, expectedIP)
	return out
}

func (s *Service) probeApex(ctx context.Context, apex, expectedIP string) ProbeResult {
	lookupCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	addrs, err := s.resolver.LookupHost(lookupCtx, apex)
	out := ProbeResult{Probed: apex, Expected: expectedIP, ResolvedTo: addrs}
	if err != nil {
		// NXDOMAIN is the operator's normal "no record yet" — surface as
		// ok=false WITHOUT an error string so the UI renders a neutral
		// "pending" card instead of a scary red one. Matches the
		// hostname/wildcard probes.
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
			return out
		}
		out.Error = err.Error()
		return out
	}
	out.OK = expectedIP != "" && containsAddr(addrs, expectedIP)
	return out
}

// probeWildcard queries a random subdomain under apex. If it resolves
// (and matches expectedIP when supplied), the operator must have a
// wildcard A/CNAME at the DNS provider — there's no other reason a
// random name would answer.
func (s *Service) probeWildcard(ctx context.Context, apex, expectedIP string) ProbeResult {
	probe := randomLabel() + "." + apex
	lookupCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	addrs, err := s.resolver.LookupHost(lookupCtx, probe)
	out := ProbeResult{Probed: probe, Expected: expectedIP, ResolvedTo: addrs}
	if err != nil {
		// NXDOMAIN is the common case (no wildcard) — surface as ok=false,
		// not as an error string the UI would render as a problem.
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
			return out
		}
		out.Error = err.Error()
		return out
	}
	// If the operator configured wildcard but didn't pin target_ip yet,
	// we still consider the probe positive if SOMETHING resolved.
	if expectedIP == "" {
		out.OK = len(addrs) > 0
		return out
	}
	out.OK = containsAddr(addrs, expectedIP)
	return out
}

func randomLabel() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return "prxprobe-" + hex.EncodeToString(b[:])
}

func containsAddr(addrs []string, want string) bool {
	for _, a := range addrs {
		if a == want {
			return true
		}
	}
	return false
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ----- Background loop --------------------------------------------------

// Loop re-verifies every zone on a fixed cadence. Operators can also kick
// off a verify manually via the API.
//
// expectedIPProvider lets callers pass a live function (e.g. the
// publicIP cache from internal/domains) instead of a static string so a
// changed public IP is picked up automatically.
func Loop(ctx context.Context, s *Service, interval time.Duration, expectedIPProvider func(context.Context) string) {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			zones, err := s.List(ctx)
			if err != nil {
				continue
			}
			var ip string
			if expectedIPProvider != nil {
				ip = expectedIPProvider(ctx)
			}
			var wg sync.WaitGroup
			for _, z := range zones {
				wg.Add(1)
				go func(id string) {
					defer wg.Done()
					_, _ = s.Verify(ctx, id, ip, VerifyOptions{})
				}(z.ID)
			}
			wg.Wait()
		}
	}
}
