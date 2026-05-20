package gitsrc

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/prexel/prexel/internal/crypto"
)

// NewWebhookSecret returns a fresh 32-byte hex string (64 chars) suitable
// for use as the GitHub App webhook secret. The output is what the
// operator pastes into the App's webhook settings on GitHub.
func NewWebhookSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// Source mirrors a row in the `git_sources` table. Encrypted fields are
// kept as raw ciphertext blobs; callers go through the Repo to decrypt
// on demand.
//
// For type=github_app rows the App credentials (AppID/AppSlug/
// AppPrivateKey) live on the source row itself — there is no global
// singleton. That lets a single Prexel instance carry multiple GitHub
// Apps (operator's personal account + organisation, say) without
// either one clobbering the other.
type Source struct {
	ID             string
	Type           string // github_app | ssh_key | token
	Name           string
	InstallationID *string
	PrivateKey     []byte  // encrypted ciphertext (BLOB) — ssh_key only
	PublicKey      *string
	Token          []byte  // encrypted ciphertext (BLOB) — token only
	CreatedAt      time.Time

	// GitHub App credentials — populated when Type == "github_app".
	AppID         string // GitHub's app_id (integer-as-string)
	AppSlug       string // URL slug under github.com/apps/
	AppPrivateKey []byte // encrypted ciphertext (BLOB), RSA PEM after decrypt
	AccountLogin  string // installation's account: "octocat" / "acme-corp"
	AccountType   string // "User" | "Organization"

	// WebhookSecret is the HMAC-SHA256 shared secret pasted into the
	// GitHub App's webhook config. Plaintext on purpose — the secret
	// only authenticates incoming push events, it doesn't grant access
	// to anything else. Empty for non-github_app sources (they don't
	// support webhooks).
	WebhookSecret string
}

// GitHubAppConfig is the output of the manifest conversion flow — the
// raw values GitHub returns when an App is created from a manifest.
// We persist these onto a new Source row during ManifestCallback.
type GitHubAppConfig struct {
	AppID      string
	Slug       string
	PrivateKey string
}

// ErrNotFound is returned when a lookup by id misses.
var ErrNotFound = errors.New("git source: not found")

// ErrInUse is returned by Delete when at least one app references this source.
var ErrInUse = errors.New("git source: in use by one or more apps")

// Repo is a thin CRUD layer around the git_sources table. It owns the
// cipher so handlers don't have to thread it through manually.
type Repo struct {
	db     *sql.DB
	cipher *crypto.Cipher
}

// NewRepo builds a Repo bound to db and cipher.
func NewRepo(db *sql.DB, cipher *crypto.Cipher) *Repo {
	return &Repo{db: db, cipher: cipher}
}

// Create inserts a new git_source. PrivateKey/Token/AppPrivateKey must
// already be encrypted by the caller via EncryptString.
func (r *Repo) Create(s *Source) error {
	_, err := r.db.Exec(
		`INSERT INTO git_sources(
			id, type, name,
			installation_id, private_key, public_key, token,
			app_id, app_slug, app_private_key, account_login, account_type,
			webhook_secret
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.Type, s.Name,
		s.InstallationID, nullBytes(s.PrivateKey), s.PublicKey, nullBytes(s.Token),
		nullString(s.AppID), nullString(s.AppSlug), nullBytes(s.AppPrivateKey),
		nullString(s.AccountLogin), nullString(s.AccountType),
		nullString(s.WebhookSecret),
	)
	if err != nil {
		return fmt.Errorf("insert git_source: %w", err)
	}
	return nil
}

// SetWebhookSecret rotates the webhook secret for a github_app source.
// Empty `secret` clears it (useful for non-github_app sources that
// accidentally inherited a value). Returns ErrNotFound when id misses.
func (r *Repo) SetWebhookSecret(id, secret string) error {
	res, err := r.db.Exec(
		`UPDATE git_sources SET webhook_secret = ? WHERE id = ?`,
		nullString(secret), id,
	)
	if err != nil {
		return fmt.Errorf("update webhook_secret: %w", err)
	}
	aff, _ := res.RowsAffected()
	if aff == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateInstallation finalises a github_app source after the operator
// completes the install flow on GitHub. Atomically writes
// installation_id + the account info we resolved via the GitHub API.
func (r *Repo) UpdateInstallation(id, installationID, accountLogin, accountType string) error {
	res, err := r.db.Exec(
		`UPDATE git_sources
		    SET installation_id = ?, account_login = ?, account_type = ?
		  WHERE id = ?`,
		installationID, accountLogin, accountType, id,
	)
	if err != nil {
		return fmt.Errorf("update installation: %w", err)
	}
	aff, _ := res.RowsAffected()
	if aff == 0 {
		return ErrNotFound
	}
	return nil
}

// Get returns a single source by id.
func (r *Repo) Get(id string) (*Source, error) {
	row := r.db.QueryRow(sourceSelect+` WHERE id = ?`, id)
	return scanSource(row)
}

// List returns all sources ordered by creation time (newest first).
func (r *Repo) List() ([]*Source, error) {
	rows, err := r.db.Query(sourceSelect + ` ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("query git_sources: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []*Source
	for rows.Next() {
		s, err := scanSource(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// CountGitHubSources returns how many rows already exist with
// type='github_app'. Used by the manifest flow to suffix the App's
// human name on GitHub ("Prexel · <instance>" for the first, then
// "... · 2", "... · 3" ...) so each registration is uniquely named.
func (r *Repo) CountGitHubSources() (int, error) {
	var n int
	if err := r.db.QueryRow(
		`SELECT COUNT(*) FROM git_sources WHERE type = 'github_app'`,
	).Scan(&n); err != nil {
		return 0, fmt.Errorf("count github sources: %w", err)
	}
	return n, nil
}

// Delete removes a source. It returns ErrInUse if any app references this id.
func (r *Repo) Delete(id string) error {
	var n int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM apps WHERE git_source_id = ?`, id).Scan(&n); err != nil {
		return fmt.Errorf("count apps: %w", err)
	}
	if n > 0 {
		return ErrInUse
	}
	res, err := r.db.Exec(`DELETE FROM git_sources WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete git_source: %w", err)
	}
	aff, _ := res.RowsAffected()
	if aff == 0 {
		return ErrNotFound
	}
	return nil
}

// DecryptPrivateKey returns the plaintext SSH private key (if any).
func (r *Repo) DecryptPrivateKey(s *Source) (string, error) {
	if len(s.PrivateKey) == 0 {
		return "", nil
	}
	b, err := r.cipher.Decrypt(s.PrivateKey)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// DecryptToken returns the plaintext PAT (if any).
func (r *Repo) DecryptToken(s *Source) (string, error) {
	if len(s.Token) == 0 {
		return "", nil
	}
	b, err := r.cipher.Decrypt(s.Token)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// DecryptAppPrivateKey returns the plaintext RSA private key (PEM)
// belonging to this source's GitHub App registration. Empty when the
// source isn't a github_app or wasn't fully initialised.
func (r *Repo) DecryptAppPrivateKey(s *Source) (string, error) {
	if len(s.AppPrivateKey) == 0 {
		return "", nil
	}
	b, err := r.cipher.Decrypt(s.AppPrivateKey)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// AppAuthFor decrypts the App credentials carried by `s` and stamps
// them into the AppAuth bundle the GitHubApp helper expects. Used by
// handlers and the cloner so they don't repeat the same 3 lines of
// "decrypt then build struct" boilerplate.
//
// Returns an AppAuth with empty InstallationID when the source's
// installation hasn't been finalised yet — callers that need the
// install (token, repo listing, clone) must check `auth.InstallationID`
// themselves and surface a friendly error.
func (r *Repo) AppAuthFor(s *Source) (AppAuth, error) {
	if s == nil {
		return AppAuth{}, errors.New("git source: nil")
	}
	pem, err := r.DecryptAppPrivateKey(s)
	if err != nil {
		return AppAuth{}, fmt.Errorf("decrypt app private key: %w", err)
	}
	auth := AppAuth{
		AppID:         s.AppID,
		PrivateKeyPEM: pem,
	}
	if s.InstallationID != nil {
		auth.InstallationID = *s.InstallationID
	}
	return auth, nil
}

// EncryptString helper used by handlers building Source structs.
func (r *Repo) EncryptString(s string) ([]byte, error) {
	if s == "" {
		return nil, nil
	}
	return r.cipher.Encrypt([]byte(s))
}

// Setting returns a plain setting value from the generic settings table.
func (r *Repo) Setting(key string) (string, error) {
	var value string
	if err := r.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&value); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", err
	}
	return value, nil
}

// SaveManifestState stores a one-time state token used by the GitHub
// manifest callback to validate the browser round-trip.
func (r *Repo) SaveManifestState(state, baseURL string) error {
	_, err := r.db.Exec(
		`INSERT INTO settings(key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = unixepoch()`,
		"github_manifest_state:"+state, baseURL,
	)
	return err
}

// ConsumeManifestState returns the state payload and deletes it.
func (r *Repo) ConsumeManifestState(state string) (string, error) {
	key := "github_manifest_state:" + state
	tx, err := r.db.Begin()
	if err != nil {
		return "", err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	var baseURL string
	if err := tx.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&baseURL); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", err
	}
	if _, err := tx.Exec(`DELETE FROM settings WHERE key = ?`, key); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	committed = true
	return baseURL, nil
}

// BackfillSingletonAppConfig migrates pre-009 instances onto the per-row
// model. It reads the deprecated singleton from `settings`, copies the
// credentials onto every existing github_app row, then deletes the
// singleton keys. Idempotent: no-op if the singleton is gone.
//
// Must be called from main.go right after db.Migrate so the runtime
// never sees the half-migrated state.
func (r *Repo) BackfillSingletonAppConfig() error {
	values := map[string]string{}
	rows, err := r.db.Query(
		`SELECT key, value FROM settings
		  WHERE key IN ('github_app_id','github_app_slug','github_app_private_key')`,
	)
	if err != nil {
		return fmt.Errorf("read singleton: %w", err)
	}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan singleton: %w", err)
		}
		values[k] = v
	}
	_ = rows.Close()

	// Nothing to migrate — either fresh install or already finished.
	if values["github_app_id"] == "" || values["github_app_private_key"] == "" {
		return nil
	}

	// Legacy stored the cipher as base64(ciphertext). Decode it back
	// to the raw BLOB shape the new column expects.
	rawKey, err := base64.StdEncoding.DecodeString(values["github_app_private_key"])
	if err != nil {
		return fmt.Errorf("decode legacy private_key: %w", err)
	}

	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// Apply the credentials to every existing github_app row that
	// doesn't already have them. New rows (post-009) populate the
	// columns at INSERT time, so this UPDATE only ever touches
	// legacy ones during the one-shot upgrade.
	if _, err := tx.Exec(
		`UPDATE git_sources
		    SET app_id = ?, app_slug = ?, app_private_key = ?
		  WHERE type = 'github_app' AND app_id IS NULL`,
		values["github_app_id"], values["github_app_slug"], rawKey,
	); err != nil {
		return fmt.Errorf("apply singleton to rows: %w", err)
	}

	// Drop the singleton so no future code path accidentally reads it.
	if _, err := tx.Exec(
		`DELETE FROM settings
		  WHERE key IN ('github_app_id','github_app_slug','github_app_private_key')`,
	); err != nil {
		return fmt.Errorf("delete singleton: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

// ── scanning ──────────────────────────────────────────────────────────

const sourceSelect = `SELECT
    id, type, name,
    installation_id, private_key, public_key, token, created_at,
    app_id, app_slug, app_private_key, account_login, account_type,
    webhook_secret
  FROM git_sources`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanSource(r rowScanner) (*Source, error) {
	var (
		s             Source
		instID        sql.NullString
		pub           sql.NullString
		createdUnix   int64
		appID         sql.NullString
		appSlug       sql.NullString
		accountLogin  sql.NullString
		accountType   sql.NullString
		webhookSecret sql.NullString
	)
	err := r.Scan(
		&s.ID, &s.Type, &s.Name,
		&instID, &s.PrivateKey, &pub, &s.Token, &createdUnix,
		&appID, &appSlug, &s.AppPrivateKey, &accountLogin, &accountType,
		&webhookSecret,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if instID.Valid {
		v := instID.String
		s.InstallationID = &v
	}
	if pub.Valid {
		v := pub.String
		s.PublicKey = &v
	}
	if appID.Valid {
		s.AppID = appID.String
	}
	if appSlug.Valid {
		s.AppSlug = appSlug.String
	}
	if accountLogin.Valid {
		s.AccountLogin = accountLogin.String
	}
	if accountType.Valid {
		s.AccountType = accountType.String
	}
	if webhookSecret.Valid {
		s.WebhookSecret = webhookSecret.String
	}
	s.CreatedAt = time.Unix(createdUnix, 0).UTC()
	return &s, nil
}

func nullBytes(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
