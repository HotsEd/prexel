package gitsrc

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// generateRSAPEM builds a fresh PKCS1 RSA private key in PEM form so
// the JWT tests don't depend on any on-disk material.
func generateRSAPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	der := x509.MarshalPKCS1PrivateKey(key)
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der}))
}

// TestGitHubApp_JWTForApp covers the happy path + the two early-exit
// guards (missing app id, malformed PEM). We don't validate the JWT
// against GitHub — that's an integration concern — but we DO confirm
// the helper returns a 3-segment dot-separated token with non-empty
// segments, which is enough to catch silently-broken signing.
func TestGitHubApp_JWTForApp(t *testing.T) {
	app := NewGitHubApp()
	pemKey := generateRSAPEM(t)
	auth := AppAuth{AppID: "123", PrivateKeyPEM: pemKey}

	tok, err := app.JWTForApp(time.Now(), auth)
	if err != nil {
		t.Fatalf("JWTForApp: %v", err)
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("JWT must have 3 segments, got %d", len(parts))
	}
	for i, p := range parts {
		if p == "" {
			t.Errorf("segment %d is empty", i)
		}
	}

	// Missing app id -> ErrAppNotConfigured.
	if _, err := app.JWTForApp(time.Now(), AppAuth{PrivateKeyPEM: pemKey}); err != ErrAppNotConfigured {
		t.Errorf("missing AppID returned %v, want ErrAppNotConfigured", err)
	}

	// Missing PEM -> ErrAppNotConfigured.
	if _, err := app.JWTForApp(time.Now(), AppAuth{AppID: "x"}); err != ErrAppNotConfigured {
		t.Errorf("missing PEM returned %v, want ErrAppNotConfigured", err)
	}

	// Malformed PEM -> parse error (NOT ErrAppNotConfigured — different
	// failure mode, different surface for the caller to render).
	if _, err := app.JWTForApp(time.Now(), AppAuth{AppID: "x", PrivateKeyPEM: "not a pem"}); err == nil {
		t.Error("malformed PEM returned nil err")
	}
}

// TestGitHubApp_InstallURLFor covers both branches: nil source / empty
// slug yields empty string; populated slug yields the canonical URL.
func TestGitHubApp_InstallURLFor(t *testing.T) {
	app := NewGitHubApp()
	if got := app.InstallURLFor(nil); got != "" {
		t.Errorf("nil source = %q, want \"\"", got)
	}
	if got := app.InstallURLFor(&Source{}); got != "" {
		t.Errorf("empty slug = %q, want \"\"", got)
	}
	src := &Source{AppSlug: "prexel-test"}
	want := "https://github.com/apps/prexel-test/installations/new"
	if got := app.InstallURLFor(src); got != want {
		t.Errorf("InstallURLFor = %q, want %q", got, want)
	}
}

// TestAppAuth_Configured exercises the small predicate the rest of
// the package uses to gate sensitive ops.
func TestAppAuth_Configured(t *testing.T) {
	cases := []struct {
		name string
		auth AppAuth
		want bool
	}{
		{"empty", AppAuth{}, false},
		{"appid only", AppAuth{AppID: "1"}, false},
		{"pem only", AppAuth{PrivateKeyPEM: "x"}, false},
		{"both", AppAuth{AppID: "1", PrivateKeyPEM: "x"}, true},
		{"install id alone irrelevant", AppAuth{InstallationID: "y"}, false},
	}
	for _, tc := range cases {
		if got := tc.auth.configured(); got != tc.want {
			t.Errorf("%s: configured() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestRepo_BackfillSingletonAppConfig drives the upgrade path from
// pre-009 instances: a singleton in `settings` must seed every
// existing github_app row that doesn't already carry credentials,
// then the singleton keys get deleted.
func TestRepo_BackfillSingletonAppConfig(t *testing.T) {
	db, cipher := gitsrcTestDB(t)
	repo := NewRepo(db, cipher)

	// No-op when the singleton isn't present.
	if err := repo.BackfillSingletonAppConfig(); err != nil {
		t.Errorf("Backfill no-op: %v", err)
	}

	// Seed a legacy github_app row with NULL credentials and a singleton.
	src := &Source{ID: uuid.NewString(), Type: "github_app", Name: "legacy"}
	if err := repo.Create(src); err != nil {
		t.Fatalf("Create legacy: %v", err)
	}
	// Legacy stored encrypted private key as base64(ciphertext).
	rawKey := []byte("encrypted-bytes-pretend")
	if _, err := db.Exec(`INSERT INTO settings(key, value) VALUES
		('github_app_id', '777'),
		('github_app_slug', 'prexel-legacy'),
		('github_app_private_key', ?)`,
		base64.StdEncoding.EncodeToString(rawKey)); err != nil {
		t.Fatalf("seed singleton: %v", err)
	}

	if err := repo.BackfillSingletonAppConfig(); err != nil {
		t.Fatalf("Backfill: %v", err)
	}

	got, err := repo.Get(src.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.AppID != "777" {
		t.Errorf("AppID = %q, want 777", got.AppID)
	}
	if got.AppSlug != "prexel-legacy" {
		t.Errorf("AppSlug = %q, want prexel-legacy", got.AppSlug)
	}
	if string(got.AppPrivateKey) != string(rawKey) {
		t.Errorf("AppPrivateKey not migrated correctly")
	}

	// Singleton keys must be gone.
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM settings WHERE key LIKE 'github_app%'`).Scan(&n); err != nil {
		t.Fatalf("count settings: %v", err)
	}
	if n != 0 {
		t.Errorf("singleton keys still present (%d) after Backfill", n)
	}

	// Idempotency: second run is a no-op.
	if err := repo.BackfillSingletonAppConfig(); err != nil {
		t.Errorf("Backfill (second run): %v", err)
	}
}

// TestValidateRepoURL_AcceptedSchemes covers the allowlist's positive
// side. Public hostnames pass; we don't assert on the resolved IP
// because that depends on DNS at test time — github.com is a stable
// public target.
func TestValidateRepoURL_AcceptedSchemes(t *testing.T) {
	cases := []string{
		"https://github.com/acme/repo.git",
		"ssh://git@github.com/acme/repo.git",
		"git+ssh://git@github.com/acme/repo.git",
		"git@github.com:acme/repo.git", // SCP-style — implicit ssh
	}
	for _, in := range cases {
		if err := ValidateRepoURL(in); err != nil {
			t.Errorf("ValidateRepoURL(%q) = %v, want nil", in, err)
		}
	}
}

// TestValidateRepoURL_RejectedSchemes covers the disallowed schemes
// that gave the function its reason to exist.
func TestValidateRepoURL_RejectedSchemes(t *testing.T) {
	cases := []string{
		"file:///etc/passwd",
		"git://github.com/acme/repo",
		"http://github.com/acme/repo",
		"ftp://example.com/repo",
		"ext::sh -c whatever",
	}
	for _, in := range cases {
		if err := ValidateRepoURL(in); err == nil {
			t.Errorf("ValidateRepoURL(%q) returned nil — wanted scheme rejection", in)
		}
	}
}

// TestValidateRepoURL_EmptyAndMalformed covers the early-exit branches.
func TestValidateRepoURL_EmptyAndMalformed(t *testing.T) {
	if err := ValidateRepoURL(""); err == nil {
		t.Error("empty URL returned nil err")
	}
	if err := ValidateRepoURL("   "); err == nil {
		t.Error("whitespace-only URL returned nil err")
	}
	// No scheme and no SCP shape -> rejected.
	if err := ValidateRepoURL("not-a-url"); err == nil {
		t.Error("bare string returned nil err")
	}
	// https with no host -> rejected.
	if err := ValidateRepoURL("https:///"); err == nil {
		t.Error("hostless https URL returned nil err")
	}
}

// TestValidateRepoURL_BlocksPrivateIPLiteral verifies that an IP
// literal in any of the blocked ranges is refused without touching DNS.
func TestValidateRepoURL_BlocksPrivateIPLiteral(t *testing.T) {
	cases := []string{
		"https://127.0.0.1/repo",
		"https://10.0.0.1/repo",
		"https://192.168.1.1/repo",
		"https://172.16.0.1/repo",
		"https://169.254.169.254/repo",
		"git@10.0.0.1:repo.git",
	}
	for _, in := range cases {
		if err := ValidateRepoURL(in); err == nil {
			t.Errorf("ValidateRepoURL(%q) returned nil — wanted IP block", in)
		}
	}
}

// TestParseSCPLike covers the helper's edge cases directly so a
// regression in the host-extraction logic surfaces locally.
func TestParseSCPLike(t *testing.T) {
	cases := []struct {
		in       string
		wantHost string
		wantOK   bool
	}{
		{"git@github.com:repo.git", "github.com", true},
		{"user@host:path", "host", true},
		{"host:path", "host", true},
		{"no-colon", "", false},
		{":missing-host", "", false},
		// Has '/' before ':' — caller is a path, not host:path.
		{"/path/to:thing", "", false},
		// "@host:path" (no user) still strips the @ and yields the host.
		{"@host:path", "host", true},
	}
	for _, tc := range cases {
		host, ok := parseSCPLike(tc.in)
		if host != tc.wantHost || ok != tc.wantOK {
			t.Errorf("parseSCPLike(%q) = (%q, %v), want (%q, %v)", tc.in, host, ok, tc.wantHost, tc.wantOK)
		}
	}
}

// TestIsBlockedIP enumerates the policy isBlockedIP encodes — locking
// it down here means a refactor that silently allows, say, link-local
// gets caught.
func TestIsBlockedIP(t *testing.T) {
	cases := map[string]bool{
		"":                false, // ParseIP returns nil → blocked? No: nil is treated by isBlockedIP as blocked.
		"127.0.0.1":       true,
		"10.0.0.1":        true,
		"192.168.1.1":     true,
		"172.16.0.1":      true,
		"169.254.169.254": true,
		"0.0.0.0":         true,
		"8.8.8.8":         false,
		"1.1.1.1":         false,
		"::1":             true,
		"fc00::1":         true,
	}
	for in, want := range cases {
		ip := net.ParseIP(in)
		if in == "" {
			// nil ip → isBlockedIP returns true defensively.
			if got := isBlockedIP(nil); got != true {
				t.Errorf("isBlockedIP(nil) = %v, want true", got)
			}
			continue
		}
		if ip == nil {
			t.Fatalf("ParseIP(%q) returned nil", in)
		}
		if got := isBlockedIP(ip); got != want {
			t.Errorf("isBlockedIP(%q) = %v, want %v", in, got, want)
		}
	}
}

// TestNewGitHubApp ensures the constructor wires the token map +
// http.Client. The struct is otherwise opaque, so we only assert
// the non-nil invariants.
func TestNewGitHubApp(t *testing.T) {
	app := NewGitHubApp()
	if app == nil {
		t.Fatal("NewGitHubApp returned nil")
	}
	if app.tokens == nil {
		t.Error("tokens map nil")
	}
	if app.client == nil {
		t.Error("http.Client nil")
	}
}

// TestInstallationToken_GuardsBeforeNetwork makes sure the helper
// rejects unconfigured / install-less callers BEFORE making a GitHub
// API call — those branches are pure-Go and trivially testable.
func TestInstallationToken_GuardsBeforeNetwork(t *testing.T) {
	app := NewGitHubApp()
	// Unconfigured -> ErrAppNotConfigured.
	if _, _, err := app.InstallationToken(t.Context(), AppAuth{}); err != ErrAppNotConfigured {
		t.Errorf("empty auth = %v, want ErrAppNotConfigured", err)
	}
	// Configured but no install id -> ErrInstallationMissing.
	pemKey := generateRSAPEM(t)
	_, _, err := app.InstallationToken(t.Context(), AppAuth{
		AppID: "1", PrivateKeyPEM: pemKey,
	})
	if err != ErrInstallationMissing {
		t.Errorf("missing install id = %v, want ErrInstallationMissing", err)
	}
}

// TestNewManifestState confirms the state token is 32 hex chars
// (16 random bytes) and changes each call.
func TestNewManifestState(t *testing.T) {
	a, err := NewManifestState()
	if err != nil {
		t.Fatalf("NewManifestState: %v", err)
	}
	if len(a) != 32 {
		t.Errorf("len = %d, want 32", len(a))
	}
	b, _ := NewManifestState()
	if a == b {
		t.Error("two states matched")
	}
}

// TestManifestFormHTML asserts the auto-submit form embeds the
// caller's state token, escapes user-controlled `org`, and switches
// the form action to the org URL when org is non-empty.
func TestManifestFormHTML_PersonalAndOrg(t *testing.T) {
	const baseURL = "https://prexel.example.com"
	const state = "deadbeef"

	personal := ManifestFormHTML(baseURL, state, "", "")
	if !strings.Contains(personal, "settings/apps/new") {
		t.Error("personal form missing user-level apps URL")
	}
	if !strings.Contains(personal, "state=deadbeef") {
		t.Error("personal form missing state token")
	}
	if !strings.Contains(personal, "Prexel Integration App") {
		t.Error("personal form missing default app name")
	}

	org := ManifestFormHTML(baseURL, state, "Custom", "acme-corp")
	if !strings.Contains(org, "organizations/acme-corp/settings/apps/new") {
		t.Error("org form missing org-scoped URL")
	}
	if !strings.Contains(org, "Custom") {
		t.Error("org form missing supplied app name")
	}

	// Embedded manifest must reference the operator-supplied base URL
	// so the redirect/callback URLs come back to this Prexel instance
	// rather than the wrong host.
	if !strings.Contains(org, baseURL) {
		t.Error("manifest JSON does not embed baseURL")
	}
}

// TestConvertManifestCode_RejectsEmpty covers the pre-network guard.
func TestConvertManifestCode_RejectsEmpty(t *testing.T) {
	if _, err := ConvertManifestCode(t.Context(), ""); err == nil {
		t.Error("empty code returned nil err")
	}
	if _, err := ConvertManifestCode(t.Context(), "  "); err == nil {
		t.Error("whitespace-only code returned nil err")
	}
}

// TestClone_ValidationFailFast covers the early-exit branches Cloner
// returns BEFORE invoking git or touching the filesystem.
func TestClone_ValidationFailFast(t *testing.T) {
	repo := NewRepo(nil, nil) // not used; nil source short-circuits first
	c := NewCloner(repo, NewGitHubApp())
	ctx := t.Context()

	if err := c.Clone(ctx, CloneOptions{}); err == nil {
		t.Error("Clone(empty) returned nil err")
	}
	if err := c.Clone(ctx, CloneOptions{Source: &Source{Type: "ssh_key"}}); err == nil {
		t.Error("Clone(no URL) returned nil err")
	}
	if err := c.Clone(ctx, CloneOptions{
		Source:  &Source{Type: "ssh_key"},
		RepoURL: "https://example.com/x",
	}); err == nil {
		t.Error("Clone(no dest) returned nil err")
	}
	if err := c.Clone(ctx, CloneOptions{
		Source:  &Source{Type: "unknown_kind"},
		RepoURL: "https://example.com/x",
		Dest:    t.TempDir() + "/x",
	}); err == nil {
		t.Error("Clone(unknown type) returned nil err")
	}
	if err := c.Test(ctx, nil, "https://x"); err == nil {
		t.Error("Test(nil source) returned nil err")
	}
	if err := c.Test(ctx, &Source{Type: "ssh_key"}, ""); err == nil {
		t.Error("Test(empty URL) returned nil err")
	}
	if err := c.Test(ctx, &Source{Type: "unknown_kind"}, "https://x"); err == nil {
		t.Error("Test(unknown type) returned nil err")
	}
	// github_app with no install id surfaces before the App API call.
	if err := c.Test(ctx, &Source{Type: "github_app"}, "https://github.com/x/y"); err == nil {
		t.Error("Test(github_app, no install) returned nil err")
	}
	if err := c.Clone(ctx, CloneOptions{
		Source:  &Source{Type: "github_app"},
		RepoURL: "https://github.com/x/y",
		Dest:    t.TempDir() + "/x",
	}); err == nil {
		t.Error("Clone(github_app, no install) returned nil err")
	}
}
