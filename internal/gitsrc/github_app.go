package gitsrc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

/*
   GitHubApp is a stateless GitHub API helper.

   Pre-009 design: this struct held a SINGLE App's credentials in
   memory (appID, privateKey, slug) and Configure() swapped them at
   runtime. That made >1 installed App impossible — registering a
   second one would clobber the first's JWT signing key and silently
   break token issuance for any installation_id created under the
   first App.

   Post-009 design: credentials live on the git_sources row (per-Source
   AppID / AppPrivateKey / AppSlug). This struct keeps no App state —
   every API call reads the credentials it needs from the *Source
   argument the caller passes in. The token cache is still keyed by
   installation_id (those are globally unique on GitHub's side), so
   we still avoid hammering the access_tokens endpoint.
*/

// GitHubApp is safe for concurrent use.
type GitHubApp struct {
	mu     sync.Mutex
	tokens map[string]cachedToken // keyed by installation_id
	client *http.Client
}

// AppAuth bundles every credential needed to act AS one specific
// GitHub App installation. Built by the handler layer from a *Source
// (after decrypting AppPrivateKey via the Repo's cipher). Kept as a
// value type so callers can stamp it together easily and pass by
// value — no aliasing surprises.
type AppAuth struct {
	AppID          string // GitHub's numeric app id, as string
	PrivateKeyPEM  string // RSA private key, PEM-encoded (plaintext)
	InstallationID string // optional — empty during the pre-install window
}

// configured returns true when AppID + private key are present.
// InstallationID is checked separately by the callers that need it.
func (c AppAuth) configured() bool {
	return c.AppID != "" && c.PrivateKeyPEM != ""
}

// ErrAppNotConfigured is returned when callers try to use a Source
// that isn't a fully-configured github_app (missing app_id, missing
// private key, etc).
var ErrAppNotConfigured = errors.New("github app not configured for this source")

// ErrInstallationMissing is returned when a Source doesn't carry an
// installation_id yet — i.e. the operator created the App via
// manifest but never finished the install flow.
var ErrInstallationMissing = errors.New("github app source has no installation_id")

type cachedToken struct {
	Token     string
	ExpiresAt time.Time
}

// NewGitHubApp builds a fresh client. No credentials are taken at
// construction time — the helper is per-process, but the App identity
// is resolved per call from the Source argument.
func NewGitHubApp() *GitHubApp {
	return &GitHubApp{
		tokens: make(map[string]cachedToken),
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

// InstallURLFor returns the public install URL for the App backing
// this source. Empty when the source doesn't have an app_slug (very
// early in the manifest flow — handler should never expose this case
// to the operator).
func (a *GitHubApp) InstallURLFor(s *Source) string {
	if s == nil || s.AppSlug == "" {
		return ""
	}
	return "https://github.com/apps/" + s.AppSlug + "/installations/new"
}

// JWTForApp signs an RS256 JWT identifying the App for /app/* calls.
// `now` exists for testability; production passes time.Now().
func (a *GitHubApp) JWTForApp(now time.Time, auth AppAuth) (string, error) {
	if !auth.configured() {
		return "", ErrAppNotConfigured
	}
	key, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(auth.PrivateKeyPEM))
	if err != nil {
		return "", fmt.Errorf("parse private key: %w", err)
	}
	// Issued 60s in the past to cushion against clock drift; max
	// lifetime allowed by GitHub is 10 minutes.
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iat": now.Add(-60 * time.Second).Unix(),
		"exp": now.Add(10 * time.Minute).Unix(),
		"iss": auth.AppID,
	})
	signed, err := tok.SignedString(key)
	if err != nil {
		return "", fmt.Errorf("sign jwt: %w", err)
	}
	return signed, nil
}

// InstallationToken returns a short-lived installation access token,
// reusing a cached one until it is within 5 minutes of expiry.
func (a *GitHubApp) InstallationToken(ctx context.Context, auth AppAuth) (string, time.Time, error) {
	if !auth.configured() {
		return "", time.Time{}, ErrAppNotConfigured
	}
	if auth.InstallationID == "" {
		return "", time.Time{}, ErrInstallationMissing
	}

	a.mu.Lock()
	if c, ok := a.tokens[auth.InstallationID]; ok && time.Until(c.ExpiresAt) > 5*time.Minute {
		a.mu.Unlock()
		return c.Token, c.ExpiresAt, nil
	}
	a.mu.Unlock()

	appJWT, err := a.JWTForApp(time.Now(), auth)
	if err != nil {
		return "", time.Time{}, err
	}

	url := "https://api.github.com/app/installations/" + auth.InstallationID + "/access_tokens"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(nil))
	if err != nil {
		return "", time.Time{}, err
	}
	req.Header.Set("Authorization", "Bearer "+appJWT)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := a.client.Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("github request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", time.Time{}, fmt.Errorf("github installation_token: status %d: %s", resp.StatusCode, string(body))
	}
	var payload struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", time.Time{}, fmt.Errorf("decode token: %w", err)
	}

	a.mu.Lock()
	a.tokens[auth.InstallationID] = cachedToken{Token: payload.Token, ExpiresAt: payload.ExpiresAt}
	a.mu.Unlock()

	return payload.Token, payload.ExpiresAt, nil
}

// InstallationAccount is the subset of GitHub's /app/installations/{id}
// response we care about — used right after the install callback to
// fill account_login / account_type on the source row.
type InstallationAccount struct {
	Login string `json:"login"`
	Type  string `json:"type"`
}

// GetInstallationAccount queries GitHub for the account this
// installation belongs to. Called once by the install-finalize
// handler so we can show "octocat" / "Organization · acme-corp" in
// the UI instead of an opaque installation_id.
func (a *GitHubApp) GetInstallationAccount(ctx context.Context, auth AppAuth) (*InstallationAccount, error) {
	if !auth.configured() {
		return nil, ErrAppNotConfigured
	}
	if auth.InstallationID == "" {
		return nil, ErrInstallationMissing
	}
	appJWT, err := a.JWTForApp(time.Now(), auth)
	if err != nil {
		return nil, err
	}
	url := "https://api.github.com/app/installations/" + auth.InstallationID
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+appJWT)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("github get installation: status %d: %s", resp.StatusCode, string(body))
	}
	var payload struct {
		Account InstallationAccount `json:"account"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode installation: %w", err)
	}
	if payload.Account.Login == "" {
		return nil, errors.New("github: installation has empty account.login")
	}
	return &payload.Account, nil
}
