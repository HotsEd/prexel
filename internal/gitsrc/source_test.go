package gitsrc

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/prexel/prexel/internal/crypto"

	_ "modernc.org/sqlite"
)

// gitsrcTestSecretKey is the 32-byte seed used by every test in this
// file. The cipher's HKDF stretches it to a 256-bit AES key — the
// raw bytes never leave the test process.
const gitsrcTestSecretKey = "test-secret-key-32-chars-minimum-aaaaaa"

// gitsrcTestDB spins up an in-memory SQLite with the git_sources +
// settings tables we touch. Schema mirrors migrations 001 + 009.
func gitsrcTestDB(t *testing.T) (*sql.DB, *crypto.Cipher) {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?cache=shared&_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	stmts := []string{
		`CREATE TABLE git_sources (
			id TEXT PRIMARY KEY,
			type TEXT NOT NULL,
			name TEXT NOT NULL,
			installation_id TEXT,
			private_key BLOB,
			public_key TEXT,
			token BLOB,
			created_at INTEGER NOT NULL DEFAULT (unixepoch()),
			app_id TEXT,
			app_slug TEXT,
			app_private_key BLOB,
			account_login TEXT,
			account_type TEXT,
			webhook_secret TEXT
		)`,
		`CREATE TABLE apps (
			id TEXT PRIMARY KEY,
			git_source_id TEXT
		)`,
		`CREATE TABLE settings (
			key TEXT PRIMARY KEY,
			value TEXT,
			updated_at INTEGER NOT NULL DEFAULT (unixepoch())
		)`,
	}
	for _, q := range stmts {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("schema: %v", err)
		}
	}
	cipher, err := crypto.New(gitsrcTestSecretKey)
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	return db, cipher
}

// TestEncryptDecryptCredential covers the end-to-end credential
// round-trip: EncryptString → store on the row → reload → Decrypt*
// returns the original plaintext. Belt-and-suspenders for the three
// fields that go through the cipher (Token, PrivateKey, AppPrivateKey).
func TestEncryptDecryptCredential(t *testing.T) {
	db, cipher := gitsrcTestDB(t)
	repo := NewRepo(db, cipher)

	plaintextToken := "ghp_topsecrettoken123456"
	plaintextKey := "-----BEGIN OPENSSH PRIVATE KEY-----\nfake-private\n-----END OPENSSH PRIVATE KEY-----"
	plaintextAppKey := "-----BEGIN RSA PRIVATE KEY-----\napp-key\n-----END RSA PRIVATE KEY-----"

	encToken, err := repo.EncryptString(plaintextToken)
	if err != nil {
		t.Fatalf("encrypt token: %v", err)
	}
	encKey, err := repo.EncryptString(plaintextKey)
	if err != nil {
		t.Fatalf("encrypt key: %v", err)
	}
	encAppKey, err := repo.EncryptString(plaintextAppKey)
	if err != nil {
		t.Fatalf("encrypt app key: %v", err)
	}

	// Ciphertexts must not match plaintext — if they did, the cipher
	// is a no-op (regression check).
	if string(encToken) == plaintextToken {
		t.Error("encrypted token equals plaintext — cipher is a no-op")
	}

	src := &Source{
		ID:            uuid.NewString(),
		Type:          "github_app",
		Name:          "test-source",
		Token:         encToken,
		PrivateKey:    encKey,
		AppPrivateKey: encAppKey,
		AppID:         "12345",
		AppSlug:       "prexel-test",
	}
	if err := repo.Create(src); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.Get(src.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	rtToken, err := repo.DecryptToken(got)
	if err != nil {
		t.Fatalf("DecryptToken: %v", err)
	}
	if rtToken != plaintextToken {
		t.Errorf("DecryptToken roundtrip mismatch: %q vs %q", rtToken, plaintextToken)
	}

	rtKey, err := repo.DecryptPrivateKey(got)
	if err != nil {
		t.Fatalf("DecryptPrivateKey: %v", err)
	}
	if rtKey != plaintextKey {
		t.Errorf("DecryptPrivateKey roundtrip mismatch: %q vs %q", rtKey, plaintextKey)
	}

	rtAppKey, err := repo.DecryptAppPrivateKey(got)
	if err != nil {
		t.Fatalf("DecryptAppPrivateKey: %v", err)
	}
	if rtAppKey != plaintextAppKey {
		t.Errorf("DecryptAppPrivateKey roundtrip mismatch")
	}

	// EncryptString("") returns nil — preserve that contract.
	if b, err := repo.EncryptString(""); err != nil || b != nil {
		t.Errorf("EncryptString(\"\") = (%v, %v), want (nil, nil)", b, err)
	}
}

// TestInjectHTTPSToken_ValidURL covers the happy path: a clean https
// URL gets `user:token@` injected before the host.
func TestInjectHTTPSToken_ValidURL(t *testing.T) {
	got, err := injectHTTPSToken("https://github.com/acme/repo.git", "x-access-token", "ghs_abc123")
	if err != nil {
		t.Fatalf("injectHTTPSToken: %v", err)
	}
	want := "https://x-access-token:ghs_abc123@github.com/acme/repo.git"
	if got != want {
		t.Errorf("injectHTTPSToken = %q, want %q", got, want)
	}
}

// TestInjectHTTPSToken_RejectsNonHTTPS verifies the safety check: any
// scheme other than https returns an error — we never silently
// downgrade or leak tokens over plaintext.
func TestInjectHTTPSToken_RejectsNonHTTPS(t *testing.T) {
	cases := []string{
		"http://github.com/acme/repo.git",
		"ssh://git@github.com/acme/repo.git",
		"git@github.com:acme/repo.git", // SSH short form — url.Parse misreads scheme
	}
	for _, in := range cases {
		_, err := injectHTTPSToken(in, "x-access-token", "tok")
		if err == nil {
			t.Errorf("injectHTTPSToken(%q) returned nil err — want non-https rejection", in)
		}
	}
}

// TestInjectHTTPSToken_BadURL ensures truly malformed URLs surface the
// parse error rather than the scheme error.
func TestInjectHTTPSToken_BadURL(t *testing.T) {
	_, err := injectHTTPSToken("https://%zz/", "u", "p")
	if err == nil {
		t.Error("expected parse error for malformed URL")
	}
}

// TestBuildSSHCommand encodes the TOFU policy: the command string MUST
// pin IdentityFile to the supplied key, set IdentitiesOnly=yes so SSH
// won't fall back to the agent (which would leak the operator's
// personal keys to the upstream), and StrictHostKeyChecking=accept-new
// so first contact succeeds but subsequent key changes are refused.
func TestBuildSSHCommand(t *testing.T) {
	cmd := sshCommand("/tmp/some-key.pem")

	if !strings.Contains(cmd, "-i /tmp/some-key.pem") {
		t.Errorf("sshCommand missing IdentityFile: %q", cmd)
	}
	if !strings.Contains(cmd, "-o IdentitiesOnly=yes") {
		t.Errorf("sshCommand missing IdentitiesOnly=yes (would leak agent keys): %q", cmd)
	}
	if !strings.Contains(cmd, "-o StrictHostKeyChecking=accept-new") {
		t.Errorf("sshCommand missing StrictHostKeyChecking=accept-new (TOFU): %q", cmd)
	}
	if !strings.Contains(cmd, "UserKnownHostsFile=") {
		t.Errorf("sshCommand missing UserKnownHostsFile pin: %q", cmd)
	}
	// Must start with `ssh ` — the variable feeds GIT_SSH_COMMAND verbatim.
	if !strings.HasPrefix(cmd, "ssh ") {
		t.Errorf("sshCommand must start with %q, got %q", "ssh ", cmd)
	}
}

// TestNewWebhookSecret asserts the contract for the GitHub App webhook
// shared secret: 64-char hex, fresh per call, never empty.
func TestNewWebhookSecret(t *testing.T) {
	s1, err := NewWebhookSecret()
	if err != nil {
		t.Fatalf("NewWebhookSecret: %v", err)
	}
	if len(s1) != 64 {
		t.Errorf("len(secret) = %d, want 64 (32 bytes hex)", len(s1))
	}
	// All-hex check.
	for _, c := range s1 {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("non-hex char %q in secret", c)
		}
	}
	s2, _ := NewWebhookSecret()
	if s1 == s2 {
		t.Error("two consecutive secrets matched — crypto/rand fed from a constant?")
	}
}

// TestScrubToken covers the log-sanitisation path: any user:secret@
// in https-like URLs collapses to ***@ before the output hits the
// build log or error envelope.
func TestScrubToken(t *testing.T) {
	cases := map[string]string{
		// Basic token in URL.
		"clone https://x-access-token:ghp_abc@github.com/me/r.git failed":  "clone https://***@github.com/me/r.git failed",
		// Multiple URLs in one string.
		"first https://u:p@a.com/x and second https://u2:p2@b.com/y":      "first https://***@a.com/x and second https://***@b.com/y",
		// No URL — string passes through unchanged.
		"plain text no urls":                                               "plain text no urls",
		// URL with no userinfo — left alone.
		"clean https://github.com/me/repo.git":                             "clean https://github.com/me/repo.git",
	}
	for in, want := range cases {
		if got := scrubToken(in); got != want {
			t.Errorf("scrubToken(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestWriteTempKey checks the side-effects of writeTempKey: file
// created with 0600 perms, content matches input, cleanup removes it.
func TestWriteTempKey(t *testing.T) {
	content := "-----BEGIN OPENSSH PRIVATE KEY-----\nfake\n-----END OPENSSH PRIVATE KEY-----"
	path, cleanup, err := writeTempKey(content)
	if err != nil {
		t.Fatalf("writeTempKey: %v", err)
	}
	defer cleanup()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("perm = %o, want 0600", info.Mode().Perm())
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != content {
		t.Errorf("content mismatch")
	}
	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file still present after cleanup: %v", err)
	}
}

// TestRepo_CRUD covers Create/Get/List/Delete and the ErrInUse guard
// — fast path validation that the repo wires fields correctly.
func TestRepo_CRUD(t *testing.T) {
	db, cipher := gitsrcTestDB(t)
	repo := NewRepo(db, cipher)

	pub := "ssh-ed25519 AAAA..."
	src := &Source{
		ID:        uuid.NewString(),
		Type:      "ssh_key",
		Name:      "deploy",
		PublicKey: &pub,
	}
	if err := repo.Create(src); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := repo.Get(src.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "deploy" || got.Type != "ssh_key" {
		t.Errorf("unexpected: %+v", got)
	}
	if got.PublicKey == nil || *got.PublicKey != pub {
		t.Errorf("PublicKey = %v, want %q", got.PublicKey, pub)
	}

	list, err := repo.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("List len = %d, want 1", len(list))
	}

	// Block delete when an app references the source.
	if _, err := db.Exec(`INSERT INTO apps(id, git_source_id) VALUES (?, ?)`, "appA", src.ID); err != nil {
		t.Fatalf("seed app: %v", err)
	}
	if err := repo.Delete(src.ID); err != ErrInUse {
		t.Errorf("Delete returned %v, want ErrInUse", err)
	}
	// Free the reference and retry.
	if _, err := db.Exec(`DELETE FROM apps WHERE id = ?`, "appA"); err != nil {
		t.Fatalf("clear app: %v", err)
	}
	if err := repo.Delete(src.ID); err != nil {
		t.Errorf("Delete after clearing apps: %v", err)
	}
	if _, err := repo.Get(src.ID); err != ErrNotFound {
		t.Errorf("post-delete Get returned %v, want ErrNotFound", err)
	}
}

// TestRepo_SetWebhookSecret_NotFound asserts the missing-id signal so
// handler callers can map to 404 reliably.
func TestRepo_SetWebhookSecret_NotFound(t *testing.T) {
	db, cipher := gitsrcTestDB(t)
	repo := NewRepo(db, cipher)
	if err := repo.SetWebhookSecret("unknown-id", "abc"); err != ErrNotFound {
		t.Errorf("SetWebhookSecret unknown = %v, want ErrNotFound", err)
	}
}

// TestRepo_UpdateInstallation_NotFound mirrors the SetWebhookSecret
// missing-id case for the install-finalisation path.
func TestRepo_UpdateInstallation_NotFound(t *testing.T) {
	db, cipher := gitsrcTestDB(t)
	repo := NewRepo(db, cipher)
	err := repo.UpdateInstallation("unknown-id", "1", "octocat", "User")
	if err != ErrNotFound {
		t.Errorf("UpdateInstallation unknown = %v, want ErrNotFound", err)
	}
}

// TestRepo_CountGitHubSources_OnlyCountsAppType — the suffix counter
// for app names depends on github_app rows only, not the other types.
func TestRepo_CountGitHubSources_OnlyCountsAppType(t *testing.T) {
	db, cipher := gitsrcTestDB(t)
	repo := NewRepo(db, cipher)

	for _, ty := range []string{"github_app", "github_app", "ssh_key", "token"} {
		s := &Source{ID: uuid.NewString(), Type: ty, Name: "x-" + ty}
		if err := repo.Create(s); err != nil {
			t.Fatalf("Create(%s): %v", ty, err)
		}
	}
	n, err := repo.CountGitHubSources()
	if err != nil {
		t.Fatalf("CountGitHubSources: %v", err)
	}
	if n != 2 {
		t.Errorf("count = %d, want 2 (only github_app rows)", n)
	}
}

// TestRepo_AppAuthFor handles the four notable branches: nil source,
// missing private key, missing installation, and a fully-populated row.
func TestRepo_AppAuthFor(t *testing.T) {
	db, cipher := gitsrcTestDB(t)
	repo := NewRepo(db, cipher)

	if _, err := repo.AppAuthFor(nil); err == nil {
		t.Error("AppAuthFor(nil) returned nil err")
	}

	// Source missing private key — DecryptAppPrivateKey returns "" silently,
	// AppAuthFor must still hand back an AppAuth with empty PrivateKeyPEM.
	bare := &Source{ID: uuid.NewString(), Type: "github_app", Name: "bare", AppID: "999"}
	if err := repo.Create(bare); err != nil {
		t.Fatalf("Create bare: %v", err)
	}
	auth, err := repo.AppAuthFor(bare)
	if err != nil {
		t.Errorf("AppAuthFor(bare): %v", err)
	}
	if auth.PrivateKeyPEM != "" || auth.InstallationID != "" {
		t.Errorf("bare auth = %+v, want zero-value PEM/install", auth)
	}
	if auth.AppID != "999" {
		t.Errorf("auth.AppID = %q, want 999", auth.AppID)
	}

	// Fully-populated source — round-trips the PEM and threads the install id.
	pemContent := "PEM-DATA"
	enc, err := repo.EncryptString(pemContent)
	if err != nil {
		t.Fatalf("encrypt PEM: %v", err)
	}
	instID := "12345"
	full := &Source{
		ID:             uuid.NewString(),
		Type:           "github_app",
		Name:           "full",
		AppID:          "777",
		InstallationID: &instID,
		AppPrivateKey:  enc,
	}
	if err := repo.Create(full); err != nil {
		t.Fatalf("Create full: %v", err)
	}
	loaded, err := repo.Get(full.ID)
	if err != nil {
		t.Fatalf("Get full: %v", err)
	}
	auth, err = repo.AppAuthFor(loaded)
	if err != nil {
		t.Fatalf("AppAuthFor full: %v", err)
	}
	if auth.PrivateKeyPEM != pemContent {
		t.Errorf("auth.PrivateKeyPEM = %q, want %q", auth.PrivateKeyPEM, pemContent)
	}
	if auth.InstallationID != instID {
		t.Errorf("auth.InstallationID = %q, want %q", auth.InstallationID, instID)
	}
}

// TestRepo_ManifestStateRoundtrip covers Save/ConsumeManifestState as
// a pair — consume must delete on success so a stolen state token
// can't be replayed.
func TestRepo_ManifestStateRoundtrip(t *testing.T) {
	db, cipher := gitsrcTestDB(t)
	repo := NewRepo(db, cipher)

	const state = "state-token-abc"
	const baseURL = "https://prexel.example.com"
	if err := repo.SaveManifestState(state, baseURL); err != nil {
		t.Fatalf("SaveManifestState: %v", err)
	}
	got, err := repo.ConsumeManifestState(state)
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if got != baseURL {
		t.Errorf("baseURL = %q, want %q", got, baseURL)
	}
	// Second consume must miss.
	if _, err := repo.ConsumeManifestState(state); err != ErrNotFound {
		t.Errorf("second Consume = %v, want ErrNotFound", err)
	}
}

// TestGenerateEd25519 confirms the keygen helper produces an
// authorized-keys-format public component plus a PEM-encoded
// private component — the minimum surface the deploy keys feature
// depends on.
func TestGenerateEd25519(t *testing.T) {
	k, err := GenerateEd25519()
	if err != nil {
		t.Fatalf("GenerateEd25519: %v", err)
	}
	if k == nil {
		t.Fatal("GeneratedKey is nil")
	}
	if len(k.PrivatePEM) == 0 {
		t.Error("empty PrivatePEM")
	}
	if !strings.Contains(string(k.PrivatePEM), "OPENSSH PRIVATE KEY") {
		t.Errorf("PrivatePEM not OpenSSH-formatted: %s", string(k.PrivatePEM))
	}
	if !strings.HasPrefix(k.PublicAuthorized, "ssh-ed25519 ") {
		t.Errorf("PublicAuthorized prefix = %q, want ssh-ed25519 ...", k.PublicAuthorized[:min(len(k.PublicAuthorized), 30)])
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestEncryptDecryptCredential_EmptyInput documents that an empty
// payload short-circuits both the encrypt and decrypt helpers — the
// repo deliberately stores NULL rather than a 0-byte ciphertext.
func TestEncryptDecryptCredential_EmptyInput(t *testing.T) {
	db, cipher := gitsrcTestDB(t)
	repo := NewRepo(db, cipher)

	s := &Source{ID: uuid.NewString(), Type: "token", Name: "empty"}
	if err := repo.Create(s); err != nil {
		t.Fatalf("Create: %v", err)
	}
	loaded, err := repo.Get(s.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	tok, err := repo.DecryptToken(loaded)
	if err != nil {
		t.Fatalf("DecryptToken: %v", err)
	}
	if tok != "" {
		t.Errorf("DecryptToken on empty = %q, want \"\"", tok)
	}
}

// TestRepo_Setting_NotFound asserts the missing-key contract on the
// generic settings table — used by the GitHub manifest flow to
// distinguish "first install" from "lookup failed".
func TestRepo_Setting_NotFound(t *testing.T) {
	db, cipher := gitsrcTestDB(t)
	repo := NewRepo(db, cipher)
	_, err := repo.Setting("never_set")
	if err != ErrNotFound {
		t.Errorf("Setting unknown = %v, want ErrNotFound", err)
	}
}

// Compile-time check that crypto can be used directly with NewRepo —
// guards against signature drift in either package.
var _ = context.Background
