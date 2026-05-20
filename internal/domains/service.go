package domains

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/prexel/prexel/internal/caddy"
)

// CaddyClient is the subset of *caddy.Client the domain service depends on.
// Defined as an interface so tests (and future Caddy-less unit smoke) can
// substitute a no-op.
//
// `forceHTTPS` mirrors the domain-level toggle: when true (the historical
// default) Caddy installs the HTTP→HTTPS 308 redirect; when false the
// admin client also publishes an HTTP-passthrough sibling route so :80
// continues to serve the upstream directly. Reconciliation when the flag
// changes is the caller's responsibility — UpsertRoute is idempotent.
type CaddyClient interface {
	UpsertRoute(ctx context.Context, host, upstream string, port int, forceHTTPS bool) error
	RemoveRoute(ctx context.Context, host string) error
	EnableTLS(ctx context.Context, host string) error
	DisableTLS(ctx context.Context, host string) error
}

// PublicIPProvider returns the externally-reachable IP of the Prexel host.
// The DNS check loop compares LookupHost results against this value.
type PublicIPProvider interface {
	Get(ctx context.Context) (string, error)
}

// EventBus is the optional pub/sub plumbing. The A8 milestone wires a real
// implementation; A7 uses a nil-safe wrapper so the service compiles without
// the eventbus package present.
type EventBus interface {
	Publish(topic string, payload any)
}

// ZoneLinker is the tiny surface internal/domains needs from
// internal/dnszone to attach new domains to their apex AND to read the
// parent zone's wildcard verification state on every DNS check pass.
// The interface keeps this package free of a direct dnszone import —
// the production binding lives in wire.go.
type ZoneLinker interface {
	// EnsureForFQDN resolves the apex of fqdn and returns the matching
	// dns_zones row, creating it if missing. Returns an error for
	// non-public names (single-label, IP literals) so the caller can
	// surface a useful validation message.
	EnsureForFQDN(ctx context.Context, fqdn string) (*Zone, error)
	// Get returns the zone row by id, so the DNS check loop can read
	// the live wildcard_verified flag and stamp covered_by_wildcard
	// on the domain row.
	Get(ctx context.Context, id string) (*Zone, error)
}

// Zone is the minimal projection internal/domains needs from a
// dns_zones row. The full struct lives in internal/dnszone — we copy
// only what we touch here to dodge the import cycle.
type Zone struct {
	ID               string
	Apex             string
	WildcardVerified bool
}

// Service is the entry point for the domains domain.
type Service struct {
	repo     *repo
	caddy    CaddyClient
	publicIP PublicIPProvider
	events   EventBus
	zones    ZoneLinker
	instance InstanceURLReader // optional; gates Delete against the panel URL
}

// Deps bundles the (mostly optional) collaborators of Service.
type Deps struct {
	DB       *sql.DB
	Caddy    CaddyClient
	PublicIP PublicIPProvider
	Events   EventBus
	// Zones is optional. When nil, the service skips apex-zone
	// linkage entirely — useful for tests that don't care about zones.
	Zones ZoneLinker
}

// NewService constructs a Service. PublicIP/Events/Zones may be nil —
// the service degrades gracefully (the DNS loop skips iterations until a
// public IP is known; events are silently dropped; zone linkage is
// best-effort).
func NewService(d Deps) *Service {
	return &Service{
		repo:     newRepo(d.DB),
		caddy:    d.Caddy,
		publicIP: d.PublicIP,
		events:   d.Events,
		zones:    d.Zones,
	}
}

// CreateInput is the payload accepted by Create.
type CreateInput struct {
	Name      string  // user-supplied hostname; will be normalised
	AppID     *string // nil for instance-level domains
	IsPrimary bool
	// ForceHTTPS gates whether Caddy installs the HTTP→HTTPS 308
	// redirect for this host. nil means "use the default" — true. The
	// schema column defaults to 1 as well so the two paths agree.
	ForceHTTPS *bool
}

// hostnameRe matches single-segment-or-multi labels separated by dots.
// Labels are 1-63 chars, alnum + hyphen, no leading/trailing hyphen.
var hostnameRe = regexp.MustCompile(`^([a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)+[a-zA-Z]{2,63}$`)

// normalizeName strips protocol, leading www, trailing slashes/whitespace, and
// lowercases. It does NOT validate — callers must pair it with validateName.
func normalizeName(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ToLower(s)
	for _, prefix := range []string{"https://", "http://"} {
		if strings.HasPrefix(s, prefix) {
			s = strings.TrimPrefix(s, prefix)
			break
		}
	}
	s = strings.TrimPrefix(s, "www.")
	s = strings.TrimRight(s, "/")
	return s
}

func validateName(s string) error {
	if s == "" {
		return errors.New("invalid_domain: empty")
	}
	if strings.Contains(s, "/") {
		return errors.New("invalid_domain: paths are not allowed")
	}
	if strings.Contains(s, "*") {
		return errors.New("invalid_domain: wildcards are not supported (v0.1)")
	}
	if strings.EqualFold(s, "localhost") {
		return errors.New("invalid_domain: localhost not allowed")
	}
	if net.ParseIP(s) != nil {
		return errors.New("invalid_domain: IP addresses not allowed")
	}
	if !strings.Contains(s, ".") {
		return errors.New("invalid_domain: must contain a dot")
	}
	if !hostnameRe.MatchString(s) {
		return errors.New("invalid_domain: malformed hostname")
	}
	return nil
}

// Create persists a new domain and (best-effort) installs the Caddy route.
// Validation order matches the Spec: format -> uniqueness -> app existence ->
// primary-conflict resolution -> insert -> route registration.
//
// The Caddy upstream depends on whether the domain is bound to an app:
//
//   - app domain: upstream = "prexel-<app.name>" on the app's container port.
//     The container may not exist yet (A8 builds it); Caddy will return 502
//     until then. That is acceptable: we want the route in place so DNS
//     verification can proceed against the correct host.
//   - instance domain: upstream = "prexel:3000" — points at the prexel HTTPS
//     server. We only register the route in Caddy; users are responsible for
//     ensuring DNS points the hostname at this host.
func (s *Service) Create(ctx context.Context, in CreateInput) (*Domain, error) {
	name := normalizeName(in.Name)
	if err := validateName(name); err != nil {
		return nil, err
	}

	// Auto-link to a DNS zone. The user explicitly asked for "auto-create
	// the zone on first subdomain", so the zone row lands BEFORE the
	// domain row to keep the FK valid even on a race. EnsureForFQDN is
	// idempotent — a second subdomain under the same apex reuses the
	// existing zone.
	//
	// Failure to extract an apex (single-label, IP literal) returns a
	// validation error to the caller. zones may be nil in tests; in that
	// case we silently skip linkage and the row gets created with
	// zone_id = NULL.
	var zoneID *string
	if s.zones != nil {
		z, err := s.zones.EnsureForFQDN(ctx, name)
		if err != nil {
			return nil, fmt.Errorf("domain: zone linkage: %w", err)
		}
		if z != nil {
			id := z.ID
			zoneID = &id
		}
	}

	// Resolve target upstream + port BEFORE opening the transaction so we
	// don't hold a write lock during a slow DB read. The values are only
	// consumed after commit.
	upstream, upstreamPort, err := s.resolveUpstream(ctx, in.AppID)
	if err != nil {
		return nil, err
	}

	tx, err := s.repo.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("domain: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if in.AppID != nil {
		ok, err := s.repo.appExistsTx(ctx, tx, *in.AppID)
		if err != nil {
			return nil, fmt.Errorf("domain: app lookup: %w", err)
		}
		if !ok {
			return nil, ErrAppNotFound
		}
		if in.IsPrimary {
			if err := s.repo.demotePrimaryTx(ctx, tx, *in.AppID); err != nil {
				return nil, fmt.Errorf("domain: demote primary: %w", err)
			}
		}
	}

	// ForceHTTPS defaults to true on Create when the caller didn't
	// specify — matches the schema default and the pre-feature
	// behaviour where every domain redirected HTTP→HTTPS.
	forceHTTPS := true
	if in.ForceHTTPS != nil {
		forceHTTPS = *in.ForceHTTPS
	}

	d := &Domain{
		ID:            uuid.NewString(),
		Name:          name,
		AppID:         in.AppID,
		IsPrimary:     in.IsPrimary,
		SSLStatus:     "pending",
		DNSVerified:   false,
		DNSCheckCount: 0,
		ZoneID:        zoneID,
		ForceHTTPS:    forceHTTPS,
		// covered_by_wildcard is set by the DNS check loop the first time
		// it sees the zone's wildcard probe succeed; never set here.
	}
	if err := s.repo.insertTx(ctx, tx, d); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("domain: commit: %w", err)
	}
	committed = true

	// Re-read so created_at/updated_at (set by SQL DEFAULT) come back
	// populated. The in-memory `d` carries the zero values otherwise.
	persisted, err := s.repo.get(ctx, d.ID)
	if err == nil {
		d = persisted
	}

	// Best-effort Caddy registration. We never SSL-enable here — that happens
	// only after the DNS loop sees the right A record (so we don't burn LE
	// quota on misconfigured hosts).
	if s.caddy != nil {
		if err := s.caddy.UpsertRoute(ctx, d.Name, upstream, upstreamPort, d.ForceHTTPS); err != nil {
			// Don't roll back the DB row — Caddy can be reconciled later by a
			// retry. Surface a warning via the returned error path? We choose
			// to log and continue: the row is authoritative.
			return d, fmt.Errorf("domain: caddy upsert: %w", err)
		}
		// Make sure the host is in the skip list so Caddy does NOT try to
		// auto-issue a cert before DNS is verified.
		if err := s.caddy.DisableTLS(ctx, d.Name); err != nil && !errors.Is(err, caddy.ErrRouteNotFound) {
			return d, fmt.Errorf("domain: caddy disable tls: %w", err)
		}
	}

	return d, nil
}

// resolveUpstream returns (upstreamHost, upstreamPort) for a new domain.
// Kept for back-compat (single-container apps); for service-aware
// callers use resolveUpstreamFor instead. App domains target the
// container name on the prexel-net bridge; instance domains target
// the prexel container itself.
func (s *Service) resolveUpstream(ctx context.Context, appID *string) (string, int, error) {
	return s.resolveUpstreamFor(ctx, appID, nil, nil)
}

// resolveUpstreamFor extends resolveUpstream with per-service routing.
// When `service` and `port` are both set, the upstream becomes
// `prexel-<app>-<service>:<port>` (Compose runtime convention).
// When either is nil, falls back to the app-level upstream.
func (s *Service) resolveUpstreamFor(ctx context.Context, appID *string, service *string, port *int) (string, int, error) {
	if appID == nil {
		// Instance domain: point at the prexel HTTPS server. In dev
		// compose the container is named "prexel" inside prexel-net;
		// in prod the same holds (Tech Review §6). Port 3000 is the
		// API port.
		return "prexel", 3000, nil
	}
	name, err := s.repo.appName(ctx, *appID)
	if err != nil {
		return "", 0, err
	}
	if service != nil && port != nil && strings.TrimSpace(*service) != "" && *port > 0 {
		return "prexel-" + name + "-" + strings.TrimSpace(*service), *port, nil
	}
	appPort, err := s.repo.appPort(ctx, *appID)
	if err != nil {
		return "", 0, err
	}
	return "prexel-" + name, appPort, nil
}

// List returns every domain, optionally filtered by appID.
func (s *Service) List(ctx context.Context, appID *string) ([]Domain, error) {
	return s.repo.list(ctx, appID)
}

// Get returns a domain by id.
func (s *Service) Get(ctx context.Context, id string) (*Domain, error) {
	return s.repo.get(ctx, id)
}

// ErrInUseByApp is returned by Delete when an app is still bound to the
// domain. Callers (HTTP handler) should map this to 409 with a hint to
// unbind the app first.
var ErrInUseByApp = errors.New("domain: in use by an app")

// ErrInUseByInstance is returned by Delete when the domain matches the
// configured panel instance_url. The operator must first switch the panel
// to another domain (or to IP-only mode) in Settings → Instance.
var ErrInUseByInstance = errors.New("domain: in use by the panel instance URL")

// InstanceURLReader is the small surface Delete needs to know what the
// panel's current public URL is, so it can refuse to drop the row out
// from under the running panel. Production wiring (wire.go) passes the
// instance.Service which already caches this.
type InstanceURLReader interface {
	CurrentInstanceURL() string
}

// SetInstanceURLReader plugs the live instance URL provider used by
// Delete. Chainable so wire.go can call it after constructing both
// services. nil is allowed — the instance gate is then skipped, matching
// pre-feature behaviour useful in tests.
func (s *Service) SetInstanceURLReader(r InstanceURLReader) *Service {
	s.instance = r
	return s
}

// Delete removes the Caddy route and the DB row.
//
// Refuses with ErrInUseByApp / ErrInUseByInstance when something downstream
// still depends on the FQDN — the user explicitly chose this fail-loud
// behaviour over silently breaking a running app or panel.
func (s *Service) Delete(ctx context.Context, id string) error {
	d, err := s.repo.get(ctx, id)
	if err != nil {
		return err
	}
	if d.AppID != nil && *d.AppID != "" {
		return ErrInUseByApp
	}
	if s.instance != nil {
		if cur := s.instance.CurrentInstanceURL(); cur != "" && strings.EqualFold(cur, d.Name) {
			return ErrInUseByInstance
		}
	}
	if s.caddy != nil {
		if err := s.caddy.RemoveRoute(ctx, d.Name); err != nil && !errors.Is(err, caddy.ErrRouteNotFound) {
			// Continue: the DB row should still be deleted to avoid orphaned
			// entries blocking re-creation.
		}
	}
	return s.repo.delete(ctx, id)
}

// UpdateInput is the payload accepted by Update. Every field is optional —
// fields left nil are not touched. ClearAppID exists so the caller can
// explicitly disassociate the domain from an app (sending AppID=nil is
// ambiguous in JSON because "absent" and "explicit null" look the same in
// the wire encoding).
type UpdateInput struct {
	AppID      *string
	ClearAppID bool
	IsPrimary  *bool
	// Service / Port route this domain to a specific service inside
	// a Compose app. ClearService=true wipes both fields back to NULL
	// (the domain reverts to "default route to app's primary
	// container"). Setting Service without Port (or vice versa) is
	// rejected — both must be provided together.
	Service      *string
	Port         *int
	ClearService bool
	// ForceHTTPS flips the HTTP→HTTPS redirect toggle. nil means
	// "don't touch". When changed, the service re-runs UpsertRoute
	// so Caddy publishes/retracts the HTTP-passthrough sibling.
	ForceHTTPS *bool
}

// Update applies a partial mutation. Currently supports re-binding the
// domain to a different app (or detaching it) and flipping the
// is_primary flag. Re-uses Create's primary-demotion rule: setting
// is_primary=true demotes every other primary domain on the target app.
func (s *Service) Update(ctx context.Context, id string, in UpdateInput) (*Domain, error) {
	d, err := s.repo.get(ctx, id)
	if err != nil {
		return nil, err
	}

	tx, err := s.repo.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("domain: begin update: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// Resolve the next app linkage.
	newAppID := d.AppID
	if in.ClearAppID {
		newAppID = nil
	} else if in.AppID != nil {
		v := strings.TrimSpace(*in.AppID)
		if v == "" {
			newAppID = nil
		} else {
			ok, err := s.repo.appExistsTx(ctx, tx, v)
			if err != nil {
				return nil, fmt.Errorf("domain: app lookup: %w", err)
			}
			if !ok {
				return nil, ErrAppNotFound
			}
			newAppID = &v
		}
	}

	// Resolve the next is_primary value.
	newPrimary := d.IsPrimary
	if in.IsPrimary != nil {
		newPrimary = *in.IsPrimary
	}
	// is_primary only makes sense when the domain is bound to an app —
	// instance domains can't be "primary of" anything.
	if newAppID == nil {
		newPrimary = false
	}

	// If we're promoting this domain to primary, demote whatever's currently
	// primary on the target app.
	if newPrimary && newAppID != nil {
		if err := s.repo.demotePrimaryTx(ctx, tx, *newAppID); err != nil {
			return nil, fmt.Errorf("domain: demote primary: %w", err)
		}
	}

	if err := s.repo.updateAppPrimaryTx(ctx, tx, id, newAppID, newPrimary); err != nil {
		return nil, err
	}

	// Service-route bookkeeping. Three valid intents:
	//   1. ClearService → both nullify (revert to app-level route)
	//   2. Service + Port both set → bind to service+port (Compose)
	//   3. neither set → leave unchanged
	// Setting just one of the two is rejected at the handler level;
	// here we treat "either was provided" as an explicit write.
	newService := d.Service
	newPort := d.Port
	serviceChanged := false
	if in.ClearService {
		newService, newPort = nil, nil
		serviceChanged = true
	} else if in.Service != nil || in.Port != nil {
		newService, newPort = in.Service, in.Port
		serviceChanged = true
	}
	if serviceChanged {
		if err := s.repo.updateServiceRouteTx(ctx, tx, id, newService, newPort); err != nil {
			return nil, fmt.Errorf("domain: update service route: %w", err)
		}
	}

	// force_https bookkeeping. Nil = don't touch; a non-nil pointer
	// always commits the new value even when it equals the existing
	// flag, so the trailing Caddy reconcile gets to converge live
	// state with the DB (idempotent — cheap to over-run).
	newForceHTTPS := d.ForceHTTPS
	forceHTTPSChanged := false
	if in.ForceHTTPS != nil {
		newForceHTTPS = *in.ForceHTTPS
		if newForceHTTPS != d.ForceHTTPS {
			forceHTTPSChanged = true
		}
		if err := s.repo.updateForceHTTPSTx(ctx, tx, id, newForceHTTPS); err != nil {
			return nil, fmt.Errorf("domain: update force_https: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("domain: commit: %w", err)
	}
	committed = true

	// If the app OR the service-route changed, the Caddy upstream
	// changed too — re-upsert so requests start hitting the new
	// container. When force_https flipped we also reconcile so the
	// HTTP-passthrough sibling is added/removed in line with the new
	// flag. Best-effort; the row is authoritative either way.
	if s.caddy != nil && (!sameAppID(d.AppID, newAppID) || serviceChanged || forceHTTPSChanged) {
		upstream, port, err := s.resolveUpstreamFor(ctx, newAppID, newService, newPort)
		if err == nil {
			_ = s.caddy.UpsertRoute(ctx, d.Name, upstream, port, newForceHTTPS)
		}
	}

	return s.repo.get(ctx, id)
}

func sameAppID(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

// Retry zeros the failure counter and resets the domain to `pending`. The
// DNS loop will pick it up on its next iteration.
func (s *Service) Retry(ctx context.Context, id string) error {
	if _, err := s.repo.get(ctx, id); err != nil {
		return err
	}
	return s.repo.resetForRetry(ctx, id)
}

// Verify forces an immediate DNS check for the given domain (outside the
// background loop's cadence). Mostly useful from the UI's "Verify now" button.
func (s *Service) Verify(ctx context.Context, id string) error {
	d, err := s.repo.get(ctx, id)
	if err != nil {
		return err
	}
	if s.publicIP == nil {
		return errors.New("domain: public IP unknown — try again in a moment")
	}
	ip, err := s.publicIP.Get(ctx)
	if err != nil {
		return fmt.Errorf("domain: public IP lookup failed: %w", err)
	}
	return s.runDNSCheckOnce(ctx, d, ip)
}

// runDNSCheckOnce executes a single DNS lookup + state transition for a
// domain. Shared between Verify and dnscheck.Loop so behaviour is uniform.
//
// In addition to the per-domain A check, we refresh the domain's
// `covered_by_wildcard` flag based on the parent zone's live
// wildcard_verified state. We never skip the per-domain check even
// when covered — the user explicitly asked "always verify DNS of
// everything" — but the UI can surface the coverage badge so the
// operator knows a wildcard-backed domain is reachable even if the
// per-domain probe transiently flakes.
func (s *Service) runDNSCheckOnce(ctx context.Context, d *Domain, expectedIP string) error {
	// Refresh the wildcard-coverage flag first (best-effort; failure
	// doesn't block the actual A check below).
	s.refreshWildcardCoverage(ctx, d)

	resolveCtx, cancel := context.WithTimeout(ctx, dnsLookupTimeout)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupHost(resolveCtx, d.Name)
	if err != nil || !containsAddr(addrs, expectedIP) {
		if bumpErr := s.repo.bumpDNSFailure(ctx, d.ID); bumpErr != nil {
			return bumpErr
		}
		// After incrementing, check whether we have crossed the 48h ceiling.
		// The repo doesn't return the new count, so re-read.
		updated, getErr := s.repo.get(ctx, d.ID)
		if getErr == nil && updated.DNSCheckCount >= maxDNSCheckCount {
			_ = s.repo.markSSLStatus(ctx, d.ID, "failed", nil)
			s.publish("domain.dns.failed", d.ID)
		}
		return nil
	}
	// Match!
	if err := s.repo.markDNSVerified(ctx, d.ID); err != nil {
		return err
	}
	if s.caddy != nil {
		if enableErr := s.caddy.EnableTLS(ctx, d.Name); enableErr != nil {
			// Don't fail the verification — Caddy may catch up later.
			s.publish("domain.caddy.enable_tls_failed", d.ID)
		}
	}
	s.publish("domain.dns.verified", d.ID)
	return nil
}

// refreshWildcardCoverage reads the parent zone (if any) and stamps the
// denormalized covered_by_wildcard flag when it disagrees with the live
// state. Errors are swallowed — coverage is a UX hint, not a
// correctness signal.
func (s *Service) refreshWildcardCoverage(ctx context.Context, d *Domain) {
	if s.zones == nil || d.ZoneID == nil {
		return
	}
	z, err := s.zones.Get(ctx, *d.ZoneID)
	if err != nil || z == nil {
		return
	}
	if z.WildcardVerified != d.CoveredByWildcard {
		_ = s.repo.setCoveredByWildcard(ctx, d.ID, z.WildcardVerified)
	}
}

func containsAddr(addrs []string, want string) bool {
	for _, a := range addrs {
		if a == want {
			return true
		}
	}
	return false
}

func (s *Service) publish(topic string, payload any) {
	if s.events != nil {
		s.events.Publish(topic, payload)
	}
}
