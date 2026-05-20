// Package domain owns the `domains` table: app/instance domains, DNS
// verification status, and SSL provisioning state. The background loops
// (dnscheck.Loop, sslmonitor.Loop) live alongside the service so they can
// share the repo without going through HTTP.
//
// Status state machine (Spec: Domains, Tech Review §15):
//
//	pending  -> dns_verified=false; DNS loop running until match or 48h
//	issuing  -> DNS matched; Caddy.EnableTLS called; waiting for cert
//	active   -> certificate observed live via TLS handshake (or simulated
//	            in dev when tls_mode=self-signed)
//	failed   -> dns_check_count >= 96 (48h) or repeated TLS-handshake
//	            failures after >10min in `issuing`
//
// `Retry` zeros the counter and resets to `pending`. `Verify` forces an
// immediate DNS lookup for a single domain.
package domains

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrNotFound is returned when a domain lookup misses.
var ErrNotFound = errors.New("domain: not found")

// ErrDuplicate is returned by Create when the name is already used.
var ErrDuplicate = errors.New("domain: name already exists")

// ErrAppNotFound is returned by Create when AppID points at a non-existent app.
var ErrAppNotFound = errors.New("domain: app not found")

// Domain is the API-facing representation of a row in the `domains` table.
type Domain struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	AppID         *string `json:"app_id,omitempty"`
	IsPrimary     bool    `json:"is_primary"`
	SSLStatus     string  `json:"ssl_status"`
	SSLExpiresAt  *int64  `json:"ssl_expires_at,omitempty"`
	DNSVerified   bool    `json:"dns_verified"`
	DNSVerifiedAt *int64  `json:"dns_verified_at,omitempty"`
	DNSLastCheck  *int64  `json:"dns_last_check,omitempty"`
	DNSCheckCount int     `json:"dns_check_count"`
	// ZoneID is the dns_zones row this domain belongs to (apex match).
	// Migration 007 added this column; auto-populated by the service when
	// the FQDN has a public apex extractable via publicsuffix.
	ZoneID *string `json:"zone_id,omitempty"`
	// CoveredByWildcard reflects the parent zone's wildcard_verified flag
	// at the time of the last DNS check. Denormalized so the UI can show
	// a "wildcard-covered" badge without a join. Refreshed by the DNS
	// check loop.
	CoveredByWildcard bool  `json:"covered_by_wildcard"`
	// Service / Port are set when this domain routes traffic to a
	// specific service inside a Compose app. When NULL (single-
	// container apps), Caddy falls back to `prexel-<app>:<app.port>`.
	// When set, Caddy targets `prexel-<app>-<service>:<port>`.
	Service *string `json:"service,omitempty"`
	Port    *int    `json:"port,omitempty"`
	// ForceHTTPS gates the HTTP→HTTPS 308 redirect Caddy installs by
	// default. True (the schema default) keeps the redirect; false makes
	// the host reachable over plain HTTP on :80 in addition to :443.
	// Persisted as INTEGER (0/1) in the `domains` table; migration 011
	// added the column with DEFAULT 1 so existing rows keep redirecting.
	ForceHTTPS bool  `json:"force_https"`
	CreatedAt  int64 `json:"created_at"`
	UpdatedAt  int64 `json:"updated_at"`
}

type repo struct{ db *sql.DB }

func newRepo(db *sql.DB) *repo { return &repo{db: db} }

func (r *repo) insertTx(ctx context.Context, tx *sql.Tx, d *Domain) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO domains(id, name, app_id, is_primary, ssl_status,
		                     dns_verified, dns_check_count, zone_id, covered_by_wildcard,
		                     force_https)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.Name, d.AppID, boolToInt(d.IsPrimary), d.SSLStatus,
		boolToInt(d.DNSVerified), d.DNSCheckCount,
		d.ZoneID, boolToInt(d.CoveredByWildcard),
		boolToInt(d.ForceHTTPS),
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return ErrDuplicate
		}
		return err
	}
	return nil
}

func (r *repo) list(ctx context.Context, appID *string) ([]Domain, error) {
	var rows *sql.Rows
	var err error
	// `force_https` MUST be included — scanDomain expects 17 columns
	// and silently fails with a misleading scan error if it's missing.
	// The two list/get/listForDNSCheck siblings already include it;
	// list() was the outlier and caused every `GET /domains` to 500.
	if appID != nil {
		rows, err = r.db.QueryContext(ctx,
			`SELECT id, name, app_id, is_primary, ssl_status, ssl_expires_at, zone_id, covered_by_wildcard,
			        dns_verified, dns_verified_at, dns_last_check, dns_check_count, service, port,
			        force_https, created_at, updated_at
			 FROM domains WHERE app_id = ? ORDER BY created_at ASC`, *appID)
	} else {
		rows, err = r.db.QueryContext(ctx,
			`SELECT id, name, app_id, is_primary, ssl_status, ssl_expires_at, zone_id, covered_by_wildcard,
			        dns_verified, dns_verified_at, dns_last_check, dns_check_count, service, port,
			        force_https, created_at, updated_at
			 FROM domains ORDER BY created_at ASC`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Domain
	for rows.Next() {
		d, err := scanDomain(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

func (r *repo) get(ctx context.Context, id string) (*Domain, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, name, app_id, is_primary, ssl_status, ssl_expires_at, zone_id, covered_by_wildcard,
		        dns_verified, dns_verified_at, dns_last_check, dns_check_count, service, port,
		        force_https, created_at, updated_at
		 FROM domains WHERE id = ?`, id)
	d, err := scanDomain(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return d, err
}

func (r *repo) getByName(ctx context.Context, name string) (*Domain, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, name, app_id, is_primary, ssl_status, ssl_expires_at, zone_id, covered_by_wildcard,
		        dns_verified, dns_verified_at, dns_last_check, dns_check_count, service, port,
		        force_https, created_at, updated_at
		 FROM domains WHERE name = ?`, name)
	d, err := scanDomain(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return d, err
}

func (r *repo) delete(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM domains WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// demotePrimaryTx flips every existing primary domain of `appID` to is_primary=0.
// Called inside the Create transaction when a new primary is being inserted.
func (r *repo) demotePrimaryTx(ctx context.Context, tx *sql.Tx, appID string) error {
	_, err := tx.ExecContext(ctx,
		`UPDATE domains SET is_primary = 0, updated_at = unixepoch()
		 WHERE app_id = ? AND is_primary = 1`, appID)
	return err
}

// appExistsTx verifies the apps row exists. Apps live in a separate package
// (A6); we touch the table directly here to avoid a package-level dependency
// cycle.
func (r *repo) appExistsTx(ctx context.Context, tx *sql.Tx, appID string) (bool, error) {
	var n int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM apps WHERE id = ?`, appID).Scan(&n)
	return n > 0, err
}

// appNameTx returns the apps.name used as the Caddy upstream hostname
// (containers run with --name prexel-<app.name>). Called outside the create
// tx because the upstream is only consulted after commit succeeds.
func (r *repo) appName(ctx context.Context, appID string) (string, error) {
	var name string
	err := r.db.QueryRowContext(ctx, `SELECT name FROM apps WHERE id = ?`, appID).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrAppNotFound
	}
	return name, err
}

// appPort returns the configured container port for `appID`. NULL/0 maps to
// 80 (the documented default for dockerfile_inline / no-port apps).
func (r *repo) appPort(ctx context.Context, appID string) (int, error) {
	var port sql.NullInt64
	err := r.db.QueryRowContext(ctx, `SELECT port FROM apps WHERE id = ?`, appID).Scan(&port)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrAppNotFound
	}
	if err != nil {
		return 0, err
	}
	if !port.Valid || port.Int64 == 0 {
		return 80, nil
	}
	return int(port.Int64), nil
}

// listForDNSCheck returns every domain still in `pending` whose counter has
// not exceeded the 48h ceiling.
func (r *repo) listForDNSCheck(ctx context.Context, maxCount int) ([]Domain, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, name, app_id, is_primary, ssl_status, ssl_expires_at, zone_id, covered_by_wildcard,
		        dns_verified, dns_verified_at, dns_last_check, dns_check_count, service, port,
		        force_https, created_at, updated_at
		 FROM domains
		 WHERE ssl_status = 'pending' AND dns_check_count < ?`, maxCount)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Domain
	for rows.Next() {
		d, err := scanDomain(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// listForSSLMonitor returns every domain in `issuing` or `active` — these are
// the ones we keep polling for cert state.
func (r *repo) listForSSLMonitor(ctx context.Context) ([]Domain, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, name, app_id, is_primary, ssl_status, ssl_expires_at, zone_id, covered_by_wildcard,
		        dns_verified, dns_verified_at, dns_last_check, dns_check_count, service, port,
		        force_https, created_at, updated_at
		 FROM domains
		 WHERE ssl_status IN ('issuing', 'active')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Domain
	for rows.Next() {
		d, err := scanDomain(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// markDNSVerified flips dns_verified=true + ssl_status=issuing on a single
// domain. Called by dnscheck.Loop after a successful match.
func (r *repo) markDNSVerified(ctx context.Context, id string) error {
	now := time.Now().Unix()
	_, err := r.db.ExecContext(ctx,
		`UPDATE domains
		 SET dns_verified = 1, dns_verified_at = ?, dns_last_check = ?,
		     ssl_status = 'issuing', updated_at = unixepoch()
		 WHERE id = ?`, now, now, id)
	return err
}

// updateAppPrimaryTx mutates the app_id and is_primary columns inside an
// existing transaction. Used by Service.Update — kept inside the tx so the
// "demote old primary + promote new one" pair is atomic.
func (r *repo) updateAppPrimaryTx(ctx context.Context, tx *sql.Tx, id string, appID *string, primary bool) error {
	_, err := tx.ExecContext(ctx,
		`UPDATE domains SET app_id = ?, is_primary = ?, updated_at = unixepoch()
		 WHERE id = ?`,
		appID, boolToInt(primary), id)
	return err
}

// updateForceHTTPSTx flips the per-domain force_https flag inside an
// existing transaction. Caddy reconciliation happens at the service
// layer after commit because UpsertRoute must run unlocked.
func (r *repo) updateForceHTTPSTx(ctx context.Context, tx *sql.Tx, id string, force bool) error {
	_, err := tx.ExecContext(ctx,
		`UPDATE domains SET force_https = ?, updated_at = unixepoch()
		 WHERE id = ?`, boolToInt(force), id)
	return err
}

// updateServiceRouteTx writes the per-service routing fields. NULL
// in either column means "no service binding"; both set means
// "Caddy targets prexel-<app>-<service>:<port>".
func (r *repo) updateServiceRouteTx(ctx context.Context, tx *sql.Tx, id string, service *string, port *int) error {
	var portArg any
	if port != nil {
		portArg = *port
	}
	_, err := tx.ExecContext(ctx,
		`UPDATE domains SET service = ?, port = ?, updated_at = unixepoch()
		 WHERE id = ?`,
		service, portArg, id)
	return err
}

// setZoneID attaches a domain to its dns_zones row. Used by the service
// when a domain is first inserted (and also as a backfill when an admin
// pre-registers a zone after subdomains already exist).
func (r *repo) setZoneID(ctx context.Context, id string, zoneID string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE domains SET zone_id = ?, updated_at = unixepoch() WHERE id = ?`,
		zoneID, id)
	return err
}

// setCoveredByWildcard updates the denormalized "this domain is covered
// by the parent zone's wildcard" flag. Called by the DNS check loop when
// it observes that the zone's wildcard probe succeeded; the per-domain
// A check itself runs separately and is not skipped.
func (r *repo) setCoveredByWildcard(ctx context.Context, id string, covered bool) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE domains SET covered_by_wildcard = ?, updated_at = unixepoch() WHERE id = ?`,
		boolToInt(covered), id)
	return err
}

// bumpDNSFailure increments dns_check_count and updates dns_last_check.
func (r *repo) bumpDNSFailure(ctx context.Context, id string) error {
	now := time.Now().Unix()
	_, err := r.db.ExecContext(ctx,
		`UPDATE domains
		 SET dns_check_count = dns_check_count + 1, dns_last_check = ?,
		     updated_at = unixepoch()
		 WHERE id = ?`, now, id)
	return err
}

// markSSLStatus updates ssl_status and ssl_expires_at. Passing exp=nil leaves
// the existing value (used when we want to flag failure without losing the
// previously-observed expiration).
func (r *repo) markSSLStatus(ctx context.Context, id, status string, exp *int64) error {
	if exp == nil {
		_, err := r.db.ExecContext(ctx,
			`UPDATE domains SET ssl_status = ?, updated_at = unixepoch()
			 WHERE id = ?`, status, id)
		return err
	}
	_, err := r.db.ExecContext(ctx,
		`UPDATE domains SET ssl_status = ?, ssl_expires_at = ?, updated_at = unixepoch()
		 WHERE id = ?`, status, *exp, id)
	return err
}

// resetForRetry zeros the failure counter and moves the row back to `pending`.
func (r *repo) resetForRetry(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE domains
		 SET dns_check_count = 0, dns_verified = 0, dns_verified_at = NULL,
		     dns_last_check = NULL, ssl_status = 'pending',
		     ssl_expires_at = NULL, updated_at = unixepoch()
		 WHERE id = ?`, id)
	return err
}

// tlsMode reads the settings.tls_mode value. Returns "" when unset.
func (r *repo) tlsMode(ctx context.Context) (string, error) {
	var v string
	err := r.db.QueryRowContext(ctx,
		`SELECT value FROM settings WHERE key = 'tls_mode'`).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

type scanner interface {
	Scan(dest ...any) error
}

func scanDomain(s scanner) (*Domain, error) {
	// SELECT order (kept in sync with every query that calls this scanner):
	//   id, name, app_id, is_primary, ssl_status, ssl_expires_at,
	//   zone_id, covered_by_wildcard,
	//   dns_verified, dns_verified_at, dns_last_check, dns_check_count, service, port,
	//   force_https, created_at, updated_at
	var d Domain
	var appID, zoneID, service sql.NullString
	var sslExp, dnsVerifiedAt, dnsLastCheck sql.NullInt64
	var port sql.NullInt64
	var isPrimary, dnsVerified, coveredWildcard, forceHTTPS int
	if err := s.Scan(&d.ID, &d.Name, &appID, &isPrimary, &d.SSLStatus,
		&sslExp, &zoneID, &coveredWildcard,
		&dnsVerified, &dnsVerifiedAt, &dnsLastCheck, &d.DNSCheckCount,
		&service, &port,
		&forceHTTPS, &d.CreatedAt, &d.UpdatedAt); err != nil {
		return nil, err
	}
	if appID.Valid {
		d.AppID = &appID.String
	}
	if zoneID.Valid {
		d.ZoneID = &zoneID.String
	}
	if service.Valid {
		d.Service = &service.String
	}
	if port.Valid {
		p := int(port.Int64)
		d.Port = &p
	}
	d.IsPrimary = isPrimary != 0
	d.DNSVerified = dnsVerified != 0
	d.CoveredByWildcard = coveredWildcard != 0
	d.ForceHTTPS = forceHTTPS != 0
	if sslExp.Valid {
		d.SSLExpiresAt = &sslExp.Int64
	}
	if dnsVerifiedAt.Valid {
		d.DNSVerifiedAt = &dnsVerifiedAt.Int64
	}
	if dnsLastCheck.Valid {
		d.DNSLastCheck = &dnsLastCheck.Int64
	}
	return &d, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// describe formats a Domain compactly for log lines.
func describe(d *Domain) string {
	return fmt.Sprintf("%s(%s)", d.Name, d.ID[:min(8, len(d.ID))])
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
