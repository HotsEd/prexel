package dnszone

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// setupTestDB inlines the migration 007 shape (zones + the two domains
// columns the FK / wildcard-flag refresh path touches). Keeping schema
// inline mirrors the convention used by the other repo-level tests in
// this codebase.
func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?cache=shared&_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	stmts := []string{
		`CREATE TABLE dns_zones (
			id TEXT PRIMARY KEY,
			apex TEXT NOT NULL UNIQUE,
			target_ip TEXT,
			apex_verified INTEGER NOT NULL DEFAULT 0,
			apex_verified_at INTEGER,
			apex_last_check INTEGER,
			wildcard_verified INTEGER NOT NULL DEFAULT 0,
			wildcard_verified_at INTEGER,
			wildcard_last_check INTEGER,
			notes TEXT,
			created_at INTEGER NOT NULL DEFAULT (unixepoch()),
			updated_at INTEGER NOT NULL DEFAULT (unixepoch())
		)`,
		// Minimal `domains` table — only the columns the COUNT subquery in
		// scanOne needs. The real migration carries way more.
		`CREATE TABLE domains (
			id TEXT PRIMARY KEY,
			zone_id TEXT REFERENCES dns_zones(id) ON DELETE SET NULL
		)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("schema: %v", err)
		}
	}
	return db
}

// --- ApexOf -----------------------------------------------------------------

func TestApexOf(t *testing.T) {
	cases := []struct {
		in   string
		want string
		err  error
	}{
		{"foo.com", "foo.com", nil},
		{"api.foo.com", "foo.com", nil},
		{"deeply.nested.foo.com", "foo.com", nil},
		{"api.foo.co.uk", "foo.co.uk", nil},  // multi-label TLD
		{"app.tenant.example.com.br", "example.com.br", nil},
		{"  Foo.Com  ", "foo.com", nil},      // normalises whitespace+case
		{"foo.com.", "foo.com", nil},         // trailing dot stripped
		{"", "", ErrInvalidApex},
		{"localhost", "", ErrInternalName},   // single-label
		{"127.0.0.1", "", ErrInternalName},   // IP literal
		{"::1", "", ErrInternalName},
	}
	for _, c := range cases {
		got, err := ApexOf(c.in)
		if c.err != nil {
			if !errors.Is(err, c.err) {
				t.Errorf("ApexOf(%q): want err %v, got %v", c.in, c.err, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("ApexOf(%q): unexpected err %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ApexOf(%q): want %q, got %q", c.in, c.want, got)
		}
	}
}

func TestIsApex(t *testing.T) {
	cases := map[string]bool{
		"foo.com":         true,
		"foo.co.uk":       true,
		"api.foo.com":     false,
		"x.y.foo.com":     false,
		"localhost":       false,
		"127.0.0.1":       false,
	}
	for in, want := range cases {
		if got := IsApex(in); got != want {
			t.Errorf("IsApex(%q) = %v, want %v", in, got, want)
		}
	}
}

// --- Service round-trips ----------------------------------------------------

func TestService_EnsureForFQDN_AutoCreatesZone(t *testing.T) {
	db := setupTestDB(t)
	svc := NewService(db, nil)
	z, err := svc.EnsureForFQDN(context.Background(), "api.example.com")
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if z.Apex != "example.com" {
		t.Errorf("apex: want example.com, got %q", z.Apex)
	}
	// Second call returns the same zone, not a duplicate.
	z2, err := svc.EnsureForFQDN(context.Background(), "other.example.com")
	if err != nil {
		t.Fatalf("ensure 2: %v", err)
	}
	if z2.ID != z.ID {
		t.Errorf("expected same zone id for second subdomain, got %s vs %s", z2.ID, z.ID)
	}
}

func TestService_Create_AcceptsSubdomainAsApexInput(t *testing.T) {
	// Forgiveness path: the user typed "api.example.com" thinking they
	// were registering a zone — the service extracts "example.com" and
	// keeps moving.
	db := setupTestDB(t)
	svc := NewService(db, nil)
	z, err := svc.Create(context.Background(), "api.example.com")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if z.Apex != "example.com" {
		t.Errorf("expected forgiven apex example.com, got %q", z.Apex)
	}
}

func TestService_List_IncludesSubdomainCount(t *testing.T) {
	db := setupTestDB(t)
	svc := NewService(db, nil)
	z, _ := svc.EnsureForFQDN(context.Background(), "api.example.com")
	// Seed a couple of domains linked to the zone.
	for _, did := range []string{"d1", "d2", "d3"} {
		if _, err := db.Exec(`INSERT INTO domains(id, zone_id) VALUES (?, ?)`, did, z.ID); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	out, err := svc.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(out) != 1 || out[0].SubdomainCount != 3 {
		t.Errorf("expected one zone with 3 subdomains, got %+v", out)
	}
}

func TestService_Delete_SetsDomainZoneIDNull(t *testing.T) {
	db := setupTestDB(t)
	svc := NewService(db, nil)
	z, _ := svc.EnsureForFQDN(context.Background(), "api.example.com")
	if _, err := db.Exec(`INSERT INTO domains(id, zone_id) VALUES ('d1', ?)`, z.ID); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := svc.Delete(context.Background(), z.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	var zoneID sql.NullString
	_ = db.QueryRow(`SELECT zone_id FROM domains WHERE id = 'd1'`).Scan(&zoneID)
	if zoneID.Valid {
		t.Errorf("expected NULL zone_id after delete, got %v", zoneID.String)
	}
}

// --- Verify (with a fake resolver) ------------------------------------------

type fakeResolver struct {
	lookups map[string][]string
}

func (f fakeResolver) LookupHost(_ context.Context, host string) ([]string, error) {
	if v, ok := f.lookups[host]; ok {
		return v, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
}

func TestService_Verify_ApexAndWildcardBothPositive(t *testing.T) {
	db := setupTestDB(t)
	resolver := fakeResolver{lookups: map[string][]string{
		"example.com": {"1.2.3.4"},
		// Wildcard probe queries a random subdomain — we cheat by
		// returning a hit for ANY probe under the apex via a wildcard
		// lookup hook below. The fakeResolver only matches exact keys,
		// so we install a sentinel that picks up the prxprobe prefix.
	}}
	svc := &Service{db: db, resolver: wildcardEnabled(resolver), now: time.Now, timeout: time.Second}
	z, _ := svc.EnsureForFQDN(context.Background(), "example.com")
	res, err := svc.Verify(context.Background(), z.ID, "1.2.3.4", VerifyOptions{})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !res.Apex.OK {
		t.Errorf("apex probe should succeed: %+v", res.Apex)
	}
	if !res.Wildcard.OK {
		t.Errorf("wildcard probe should succeed: %+v", res.Wildcard)
	}
}

func TestService_Verify_NoWildcard(t *testing.T) {
	db := setupTestDB(t)
	resolver := fakeResolver{lookups: map[string][]string{
		"example.com": {"1.2.3.4"},
	}}
	svc := &Service{db: db, resolver: resolver, now: time.Now, timeout: time.Second}
	z, _ := svc.EnsureForFQDN(context.Background(), "example.com")
	res, err := svc.Verify(context.Background(), z.ID, "1.2.3.4", VerifyOptions{})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if res.Apex.OK != true {
		t.Errorf("apex probe should succeed")
	}
	if res.Wildcard.OK {
		t.Errorf("wildcard probe must fail when no wildcard configured: %+v", res.Wildcard)
	}
	// Wildcard NXDOMAIN should NOT surface as an error string — it's the
	// expected "no wildcard here" signal.
	if res.Wildcard.Error != "" {
		t.Errorf("wildcard NXDOMAIN must not surface as error: %q", res.Wildcard.Error)
	}
}

func TestService_Verify_HostnameProbe(t *testing.T) {
	// User added `api.example.com` only — no apex A record, no wildcard.
	// Apex/wildcard probes must NOT scare the operator into thinking the
	// whole zone is broken; the hostname probe should clearly say "yes,
	// this specific subdomain resolves correctly".
	db := setupTestDB(t)
	resolver := fakeResolver{lookups: map[string][]string{
		"api.example.com": {"1.2.3.4"},
	}}
	svc := &Service{db: db, resolver: resolver, now: time.Now, timeout: time.Second}
	z, _ := svc.EnsureForFQDN(context.Background(), "example.com")

	res, err := svc.Verify(context.Background(), z.ID, "1.2.3.4", VerifyOptions{Hostname: "api.example.com"})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if res.Hostname == nil {
		t.Fatal("hostname probe should be populated when opts.Hostname is set")
	}
	if !res.Hostname.OK {
		t.Errorf("hostname probe must succeed: %+v", res.Hostname)
	}
	if res.Hostname.Probed != "api.example.com" {
		t.Errorf("probed: want api.example.com, got %q", res.Hostname.Probed)
	}
	// Apex + wildcard must remain false (no record configured), but they
	// should be "neutral failures" (no error string), not loud errors.
	if res.Apex.OK || res.Apex.Error != "" {
		t.Errorf("apex must be a clean failure: %+v", res.Apex)
	}
	if res.Wildcard.OK || res.Wildcard.Error != "" {
		t.Errorf("wildcard must be a clean failure: %+v", res.Wildcard)
	}
}

func TestService_Verify_HostnameProbe_OmittedWhenEmpty(t *testing.T) {
	// The background loop calls Verify with VerifyOptions{} — the hostname
	// field on the result must stay nil so old consumers don't see it.
	db := setupTestDB(t)
	svc := &Service{db: db, resolver: fakeResolver{}, now: time.Now, timeout: time.Second}
	z, _ := svc.EnsureForFQDN(context.Background(), "example.com")
	res, _ := svc.Verify(context.Background(), z.ID, "1.2.3.4", VerifyOptions{})
	if res.Hostname != nil {
		t.Errorf("hostname must be nil when not requested, got %+v", res.Hostname)
	}
}

// wildcardEnabled wraps a fakeResolver so any prxprobe-* lookup under
// the apex resolves to 1.2.3.4, simulating a real wildcard A record at
// the DNS provider.
type wildcardResolver struct{ base fakeResolver }

func (w wildcardResolver) LookupHost(_ context.Context, host string) ([]string, error) {
	if v, ok := w.base.lookups[host]; ok {
		return v, nil
	}
	if strings.HasPrefix(host, "prxprobe-") {
		return []string{"1.2.3.4"}, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
}

func wildcardEnabled(r fakeResolver) Resolver { return wildcardResolver{base: r} }
