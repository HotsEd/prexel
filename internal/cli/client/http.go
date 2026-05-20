// Package client is the HTTP client used by the CLI subcommands to talk to
// the Prexel HTTP API. It handles:
//
//   - cert verification: Let's Encrypt validated normally; self-signed certs
//     pinned via ~/.prexel/config.yaml known_hosts (TOFU + interactive prompt).
//   - access-token refresh: if TokenExpiresAt < now+60s the client transparently
//     calls POST /auth/refresh and persists the new tokens.
//   - JSON encoding/decoding, error responses, basic SSE consumption.
package client

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/prexel/prexel/internal/cli/config"
)

// PromptFn asks the user a yes/no question and returns true on yes.
type PromptFn func(question string) bool

// Client is the HTTP client.
type Client struct {
	cfg     *config.Config
	http    *http.Client
	baseURL string
	host    string

	prompt PromptFn // optional; if nil, unknown hosts are rejected.

	mu sync.Mutex
}

// HTTPError is returned when the server replies with a non-2xx status.
type HTTPError struct {
	Status int
	Code   string
	Body   string
}

func (e *HTTPError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("http %d: %s", e.Status, e.Code)
	}
	if e.Body != "" && len(e.Body) < 200 {
		return fmt.Sprintf("http %d: %s", e.Status, strings.TrimSpace(e.Body))
	}
	return fmt.Sprintf("http %d", e.Status)
}

// IsAuthError reports whether the error is a 401.
func IsAuthError(err error) bool {
	var he *HTTPError
	if errors.As(err, &he) {
		return he.Status == http.StatusUnauthorized
	}
	return false
}

// New constructs a Client. If baseURL is empty, cfg.InstanceURL is used. The
// returned Client can be safely reused.
func New(cfg *config.Config, baseURL string, prompt PromptFn) (*Client, error) {
	if baseURL == "" {
		baseURL = cfg.InstanceURL
	}
	if baseURL == "" {
		return nil, errors.New("no instance URL configured: pass --url or run `prexel login --url <url>`")
	}
	if !strings.Contains(baseURL, "://") {
		baseURL = "https://" + baseURL
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid base URL: %w", err)
	}
	host := u.Hostname()

	c := &Client{
		cfg:     cfg,
		baseURL: strings.TrimRight(baseURL, "/"),
		host:    host,
		prompt:  prompt,
	}

	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			// We do our own verification — see verifyPeer below.
			InsecureSkipVerify: true, //nolint:gosec // explicit pin via known_hosts
			VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
				return c.verifyPeerRaw(rawCerts)
			},
			MinVersion: tls.VersionTLS12,
		},
	}
	c.http = &http.Client{
		Timeout:   60 * time.Second,
		Transport: transport,
	}
	return c, nil
}

// verifyPeerRaw is the cert pinning callback.
//
// Strategy:
//  1. Try standard verification against the system roots (Let's Encrypt and
//     friends).  On success, we're done — also bust any old pin.
//  2. On failure (self-signed/unknown CA), compute the SHA-256 of the leaf
//     cert's raw DER. Compare to known_hosts:
//     - hit + matches → ok
//     - hit + mismatch → error (possible MITM)
//     - miss → if interactive (prompt != nil) ask the user; otherwise reject.
func (c *Client) verifyPeerRaw(rawCerts [][]byte) error {
	if len(rawCerts) == 0 {
		return errors.New("tls: no peer cert")
	}
	leaf, err := parseCert(rawCerts[0])
	if err != nil {
		return fmt.Errorf("tls: parse cert: %w", err)
	}
	if verifyAgainstSystem(c.host, leaf, rawCerts[1:]) {
		return nil
	}

	fp := fingerprintSHA256(rawCerts[0])
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing, ok := c.cfg.FindKnownHost(c.host); ok {
		if existing == fp {
			return nil
		}
		return fmt.Errorf("tls: cert fingerprint changed for %s\n  known:   %s\n  current: %s\nDelete the known_hosts entry in %s if intentional.",
			c.host, existing, fp, configPathOrEmpty())
	}
	if c.prompt == nil {
		return fmt.Errorf("tls: self-signed cert for %s (fp %s) not pinned; run `prexel login --url …` interactively first", c.host, fp)
	}
	q := fmt.Sprintf("Server %s presented a self-signed certificate.\n  Fingerprint (SHA256): %s\nTrust this fingerprint?", c.host, fp)
	if !c.prompt(q) {
		return errors.New("tls: user declined to trust certificate")
	}
	c.cfg.AddKnownHost(c.host, fp)
	if err := config.Save(c.cfg); err != nil {
		return fmt.Errorf("tls: save known_hosts: %w", err)
	}
	return nil
}

func configPathOrEmpty() string {
	p, err := config.Path()
	if err != nil {
		return ""
	}
	return p
}

func fingerprintSHA256(der []byte) string {
	sum := sha256.Sum256(der)
	return "sha256-" + base64.RawStdEncoding.EncodeToString(sum[:])
}

// ensureToken refreshes the access token if it's about to expire.
func (c *Client) ensureToken(ctx context.Context) error {
	if c.cfg.AccessToken == "" {
		return nil // anonymous endpoints (login, /setup, /healthz)
	}
	if !c.cfg.TokenExpiresAt.IsZero() && time.Until(c.cfg.TokenExpiresAt) > 60*time.Second {
		return nil
	}
	// Attempt refresh via cookie. Some endpoints (login) don't need this.
	if c.cfg.RefreshToken == "" {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/auth/refresh", nil)
	if err != nil {
		return err
	}
	req.AddCookie(&http.Cookie{Name: "refresh_token", Value: c.cfg.RefreshToken})
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("refresh: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		// Don't error — let the actual request fail with 401 and the caller
		// surfaces "run prexel login".
		return nil
	}
	var body struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil
	}
	c.mu.Lock()
	c.cfg.AccessToken = body.AccessToken
	c.cfg.TokenExpiresAt = time.Now().Add(time.Duration(body.ExpiresIn) * time.Second)
	// Update refresh token from new Set-Cookie.
	for _, ck := range resp.Cookies() {
		if ck.Name == "refresh_token" && ck.Value != "" {
			c.cfg.RefreshToken = ck.Value
		}
	}
	c.mu.Unlock()
	_ = config.Save(c.cfg)
	return nil
}

// Do builds + sends a request, decoding body into `out` if 2xx. method/path are
// joined to the base URL. body, if non-nil, is JSON-encoded.
func (c *Client) Do(ctx context.Context, method, path string, body, out any) error {
	if err := c.ensureToken(ctx); err != nil {
		return err
	}
	var reqBody io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal body: %w", err)
		}
		reqBody = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
	if err != nil {
		return err
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.cfg.AccessToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.AccessToken)
	}
	if c.cfg.RefreshToken != "" {
		req.AddCookie(&http.Cookie{Name: "refresh_token", Value: c.cfg.RefreshToken})
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	// Capture refresh-token rotations even on non-refresh endpoints.
	for _, ck := range resp.Cookies() {
		if ck.Name == "refresh_token" && ck.Value != "" {
			c.mu.Lock()
			c.cfg.RefreshToken = ck.Value
			c.mu.Unlock()
			_ = config.Save(c.cfg)
		}
	}

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		he := &HTTPError{Status: resp.StatusCode, Body: string(respBody)}
		// Try to surface the API's error code.
		var errEnv map[string]any
		if json.Unmarshal(respBody, &errEnv) == nil {
			if v, ok := errEnv["error"].(string); ok {
				he.Code = v
			}
			if v, ok := errEnv["message"].(string); ok && v != "" {
				he.Body = v
			}
		}
		return he
	}
	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

// Get is a convenience wrapper for GET.
func (c *Client) Get(ctx context.Context, path string, out any) error {
	return c.Do(ctx, http.MethodGet, path, nil, out)
}

// GetRaw performs a GET and returns the raw response body bytes (no JSON
// decoding). Useful for endpoints that return text/plain (e.g. build logs).
func (c *Client) GetRaw(ctx context.Context, path string) ([]byte, error) {
	if err := c.ensureToken(ctx); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	if c.cfg.AccessToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.AccessToken)
	}
	if c.cfg.RefreshToken != "" {
		req.AddCookie(&http.Cookie{Name: "refresh_token", Value: c.cfg.RefreshToken})
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		he := &HTTPError{Status: resp.StatusCode, Body: string(body)}
		var errEnv map[string]any
		if json.Unmarshal(body, &errEnv) == nil {
			if v, ok := errEnv["error"].(string); ok {
				he.Code = v
			}
			if v, ok := errEnv["message"].(string); ok && v != "" {
				he.Body = v
			}
		}
		return nil, he
	}
	return body, nil
}

// Post is a convenience wrapper for POST.
func (c *Client) Post(ctx context.Context, path string, body, out any) error {
	return c.Do(ctx, http.MethodPost, path, body, out)
}

// Put is a convenience wrapper for PUT.
func (c *Client) Put(ctx context.Context, path string, body, out any) error {
	return c.Do(ctx, http.MethodPut, path, body, out)
}

// Patch is a convenience wrapper for PATCH.
func (c *Client) Patch(ctx context.Context, path string, body, out any) error {
	return c.Do(ctx, http.MethodPatch, path, body, out)
}

// Delete is a convenience wrapper for DELETE.
func (c *Client) Delete(ctx context.Context, path string) error {
	return c.Do(ctx, http.MethodDelete, path, nil, nil)
}

// BaseURL returns the configured base URL (no trailing slash).
func (c *Client) BaseURL() string { return c.baseURL }

// HTTPClient returns the underlying *http.Client. Exported so callers
// that need to perform non-standard requests (e.g. WebSocket dial via
// coder/websocket, which takes an *http.Client) can reuse the same TLS
// configuration (our cert-pinning callback).
func (c *Client) HTTPClient() *http.Client { return c.http }

// Config returns the underlying config pointer. The caller may mutate it but
// must call config.Save when persisting.
func (c *Client) Config() *config.Config { return c.cfg }

// LoginResponse mirrors the server's POST /auth/login JSON.
type LoginResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	TokenType   string `json:"token_type"`
}

// Login performs POST /auth/login and persists tokens to the config. The
// returned LoginResponse may be ignored.
func (c *Client) Login(ctx context.Context, email, password string) (*LoginResponse, error) {
	body := map[string]string{"email": email, "password": password}
	buf, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/auth/login", bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		he := &HTTPError{Status: resp.StatusCode, Body: string(respBody)}
		var env map[string]any
		if json.Unmarshal(respBody, &env) == nil {
			if v, ok := env["error"].(string); ok {
				he.Code = v
			}
		}
		return nil, he
	}
	var out LoginResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("decode login: %w", err)
	}
	c.mu.Lock()
	c.cfg.AccessToken = out.AccessToken
	c.cfg.TokenExpiresAt = time.Now().Add(time.Duration(out.ExpiresIn) * time.Second)
	for _, ck := range resp.Cookies() {
		if ck.Name == "refresh_token" && ck.Value != "" {
			c.cfg.RefreshToken = ck.Value
		}
	}
	c.cfg.InstanceURL = c.baseURL
	c.mu.Unlock()
	if err := config.Save(c.cfg); err != nil {
		return nil, fmt.Errorf("save config: %w", err)
	}
	return &out, nil
}

// Logout calls POST /auth/logout and clears local tokens. Failures are silent
// (we still wipe local state).
func (c *Client) Logout(ctx context.Context) error {
	_ = c.Do(ctx, http.MethodPost, "/api/v1/auth/logout", nil, nil)
	c.mu.Lock()
	c.cfg.ClearTokens()
	c.mu.Unlock()
	return config.Save(c.cfg)
}

// SSEEvent is a single Server-Sent Event.
type SSEEvent struct {
	ID    string
	Event string
	Data  string
}

// SSE opens an SSE stream. Returns a channel that closes when the stream ends
// and a cancel function the caller MUST eventually invoke.
//
// lastEventID is sent via Last-Event-ID for resume after reconnect.
func (c *Client) SSE(ctx context.Context, path, lastEventID string) (<-chan SSEEvent, func(), error) {
	if err := c.ensureToken(ctx); err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	if c.cfg.AccessToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.AccessToken)
	}

	// Use a dedicated client with no timeout for SSE.
	httpc := &http.Client{
		Transport: c.http.Transport,
	}
	resp, err := httpc.Do(req)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		cancel()
		return nil, nil, &HTTPError{Status: resp.StatusCode, Body: string(body)}
	}

	out := make(chan SSEEvent, 16)
	go func() {
		defer close(out)
		defer func() { _ = resp.Body.Close() }()
		reader := bufio.NewReader(resp.Body)
		var ev SSEEvent
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				// Dispatch on blank line.
				if ev.Data != "" || ev.Event != "" {
					select {
					case out <- ev:
					case <-ctx.Done():
						return
					}
				}
				ev = SSEEvent{}
				continue
			}
			if strings.HasPrefix(line, ":") {
				// comment / keepalive
				continue
			}
			if idx := strings.IndexByte(line, ':'); idx > 0 {
				field := line[:idx]
				value := strings.TrimPrefix(line[idx+1:], " ")
				switch field {
				case "id":
					ev.ID = value
				case "event":
					ev.Event = value
				case "data":
					if ev.Data != "" {
						ev.Data += "\n"
					}
					ev.Data += value
				}
			}
		}
	}()

	return out, cancel, nil
}
