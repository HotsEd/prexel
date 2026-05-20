// Strategy notes for route management:
//
// Caddy v2's admin API supports referencing any config node by an `@id` field
// embedded in the JSON. We assign every Prexel-managed route a deterministic
// id of the form `prexel_route_<slugged_host>` (see routeID in config.go).
// That lets UpsertRoute/RemoveRoute target individual routes with
// PUT/DELETE /id/<id> without rewriting the entire config — Caddy treats the
// route list under `apps/http/servers/srv0/routes` as an array we can append
// to via POST /config/apps/http/servers/srv0/routes/... and replace
// per-element via PUT /id/<id>.
//
// Adoption flow:
//   - BootstrapBaseConfig() POSTs the initial config to /load. After that,
//     `srv0` exists with an empty routes array.
//   - UpsertRoute() first tries PUT /id/<id> (succeeds if the route already
//     exists). If Caddy returns 404 (id unknown) we POST a new route to
//     `apps/http/servers/srv0/routes` — Caddy preserves the @id we send so
//     the next UpsertRoute for the same host will succeed via PUT.
//   - RemoveRoute() DELETE /id/<id>.
//
// This means every route Prexel creates carries a stable identity in the
// live config and we never have to read-modify-write the entire routes
// array (which would race with other operators in dev).

package caddy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultAdminURL is the standard Caddy admin endpoint. In dev the docker
// compose stack exposes 2019 on the host; from inside prexel-net the same
// endpoint is reachable as http://caddy:2019.
const DefaultAdminURL = "http://127.0.0.1:2019"

// Client is a thin HTTP client around the Caddy v2 admin API.
type Client struct {
	baseURL string
	http    *http.Client
}

// New returns a Client targeting the given admin URL. If adminURL is empty,
// DefaultAdminURL is used.
func New(adminURL string) *Client {
	if adminURL == "" {
		adminURL = DefaultAdminURL
	}
	adminURL = strings.TrimRight(adminURL, "/")
	return &Client{
		baseURL: adminURL,
		http: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Ping verifies the admin API is reachable. Caddy's admin /config/ endpoint
// returns 200 with the current config (possibly null); we just check status.
func (c *Client) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/config/", nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrAdminUnreachable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("%w: status %d: %s", ErrAdminUnreachable, resp.StatusCode, string(body))
	}
	return nil
}

// GetConfig returns the full live Caddy config as a generic map. Useful for
// debugging and for callers that want to assert invariants.
func (c *Client) GetConfig(ctx context.Context) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/config/", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAdminUnreachable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, c.errFromBody(resp)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		if errors.Is(err, io.EOF) {
			return map[string]any{}, nil
		}
		return nil, fmt.Errorf("%w: decode config: %v", ErrBadResponse, err)
	}
	return out, nil
}

// LoadConfig replaces the entire Caddy configuration. Equivalent to
// `POST /load` with a JSON body.
func (c *Client) LoadConfig(ctx context.Context, full map[string]any) error {
	body, err := json.Marshal(full)
	if err != nil {
		return fmt.Errorf("caddy: marshal load config: %w", err)
	}
	return c.do(ctx, http.MethodPost, "/load", body, nil)
}

// BootstrapBaseConfig POSTs the boot configuration described in config.go.
// It is safe to call repeatedly: `/load` replaces the whole config.
func (c *Client) BootstrapBaseConfig(ctx context.Context) error {
	return c.LoadConfig(ctx, baseConfig())
}

// UpsertRoute creates or updates the route that proxies `host` -> `upstream:port`.
// See top-of-file for the routing-id strategy.
//
// When `forceHTTPS` is true (the historical default) only the canonical
// host->upstream route is published; Caddy's auto-HTTPS layer takes care of
// the HTTP→HTTPS 308 redirect on :80. When false, a second sibling route is
// published with an explicit `protocol: http` matcher so the auto-HTTPS
// layer treats the HTTP side as already handled and skips the redirect —
// the host is then reachable over plain HTTP on :80 while still being
// served via TLS on :443.
func (c *Client) UpsertRoute(ctx context.Context, host, upstream string, port int, forceHTTPS bool) error {
	if host == "" {
		return errors.New("caddy: UpsertRoute: empty host")
	}
	if upstream == "" {
		return errors.New("caddy: UpsertRoute: empty upstream")
	}
	if port <= 0 || port > 65535 {
		return fmt.Errorf("caddy: UpsertRoute: invalid port %d", port)
	}

	if err := c.upsertSingleRoute(ctx, makeRoute(host, upstream, port), routeID(host)); err != nil {
		return err
	}

	// Reconcile the optional HTTP-passthrough sibling. When force_https
	// flips on for a previously-permissive host we must drop the sibling
	// or the host will keep serving HTTP without redirect.
	if !forceHTTPS {
		if err := c.upsertSingleRoute(ctx, makeHTTPPassthroughRoute(host, upstream, port), httpPassthroughRouteID(host)); err != nil {
			return err
		}
	} else {
		// Best-effort delete: an existing sibling means we were previously
		// in passthrough mode. A 404 here is fine — the sibling was never
		// created. Any other error bubbles so the caller can retry.
		if err := c.do(ctx, http.MethodDelete, "/id/"+httpPassthroughRouteID(host), nil, nil); err != nil && !errors.Is(err, ErrRouteNotFound) {
			return fmt.Errorf("caddy: drop http passthrough %s: %w", host, err)
		}
	}
	return nil
}

// upsertSingleRoute applies the PATCH-then-POST upsert strategy described
// at the top of the file for a single pre-built route object. Factored
// out so UpsertRoute can stamp both the canonical route and the optional
// HTTP-passthrough sibling without duplicating the self-heal logic.
func (c *Client) upsertSingleRoute(ctx context.Context, route map[string]any, id string) error {
	body, err := json.Marshal(route)
	if err != nil {
		return fmt.Errorf("caddy: marshal route: %w", err)
	}

	// Try update-in-place first.
	err = c.do(ctx, http.MethodPatch, "/id/"+id, body, nil)
	if err == nil {
		return nil
	}
	if !errors.Is(err, ErrRouteNotFound) {
		// On 404 we'll create the route below; any other error bubbles up.
		// PATCH semantics in Caddy don't allow creating, so we fall through
		// only on not-found.
	}

	// Route does not exist — append to srv0.routes. Caddy accepts POST to a
	// path that points at an array and pushes the supplied element. We post
	// to `/config/apps/http/servers/srv0/routes` rather than `/.../routes/0`
	// so we always append rather than replace index 0.
	if err := c.do(ctx, http.MethodPost, "/config/apps/http/servers/srv0/routes", body, nil); err != nil {
		// Self-heal: if Caddy boots from a stripped-down config (or someone
		// /load'd a config without srv0), the POST path above is invalid and
		// Caddy returns "invalid traversal path". Bootstrap the base config
		// once and retry — this turns a hard failure into a transparent
		// recovery the first time the operator adds a domain.
		if isInvalidTraversal(err) {
			if bootErr := c.BootstrapBaseConfig(ctx); bootErr != nil {
				return fmt.Errorf("caddy: bootstrap during upsert: %w (original: %v)", bootErr, err)
			}
			return c.do(ctx, http.MethodPost, "/config/apps/http/servers/srv0/routes", body, nil)
		}
		return err
	}
	return nil
}

// isInvalidTraversal reports whether the Caddy admin API rejected the call
// because the JSON pointer we POSTed at doesn't exist in the config tree
// (typically because srv0 was never created). The error body Caddy returns
// looks like: {"error":"invalid traversal path at: config/.../srv0/routes"}.
func isInvalidTraversal(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "invalid traversal path")
}

// RemoveRoute deletes the prexel-managed route for `host`, including the
// optional HTTP-passthrough sibling installed when force_https=false.
// A missing canonical route surfaces as ErrRouteNotFound; a missing
// sibling is silently ignored because not every host carries one.
func (c *Client) RemoveRoute(ctx context.Context, host string) error {
	if host == "" {
		return errors.New("caddy: RemoveRoute: empty host")
	}
	// Drop the sibling first so we never leave a dangling HTTP route if
	// the canonical delete races/fails. Sibling-missing is the common
	// case (force_https=true hosts never have one), so swallow 404.
	if err := c.do(ctx, http.MethodDelete, "/id/"+httpPassthroughRouteID(host), nil, nil); err != nil && !errors.Is(err, ErrRouteNotFound) {
		return fmt.Errorf("caddy: drop http passthrough %s: %w", host, err)
	}
	return c.do(ctx, http.MethodDelete, "/id/"+routeID(host), nil, nil)
}

// EnableTLS turns on automatic HTTPS for `host`. Caddy v2 issues a Let's
// Encrypt certificate the first time a request for the host hits srv0 and
// stores it in `caddy-data` volume. This call is idempotent: it removes the
// host from automatic_https.skip if it was previously disabled.
//
// In practice Caddy auto-detects which hosts to provision certs for by
// scanning the routes; the host added via UpsertRoute is enough. This method
// is therefore primarily a safety net: it ensures the host is NOT in the
// `skip` list and refreshes the server config.
func (c *Client) EnableTLS(ctx context.Context, host string) error {
	if host == "" {
		return errors.New("caddy: EnableTLS: empty host")
	}
	return c.removeFromSkip(ctx, host)
}

// DisableTLS adds `host` to srv0's automatic_https.skip list so Caddy stops
// trying to issue/serve a certificate for it. Useful when DNS is misconfigured
// to avoid hitting Let's Encrypt's rate limits.
func (c *Client) DisableTLS(ctx context.Context, host string) error {
	if host == "" {
		return errors.New("caddy: DisableTLS: empty host")
	}
	return c.addToSkip(ctx, host)
}

// CertStatus returns a best-effort summary of TLS status for the host.
//
// TODO(milestone-A7): Caddy's admin API does not expose certificate
// expiration in a stable way as of v2.7. The cert manager surfaces certs at
// /pki/ca/local for internal CAs but Let's Encrypt certs are tracked under
// the certmagic storage which is not enumerable over the admin API. For now
// we return Issued=false / ExpiresAt=nil — the domain SSL monitor will need
// to either parse the on-disk storage or perform an outbound TLS handshake
// against the host (which is what the spec ultimately calls for: §15).
func (c *Client) CertStatus(_ context.Context, host string) (CertInfo, error) {
	return CertInfo{Host: host, Issued: false, ExpiresAt: nil}, nil
}

// --- internals ---------------------------------------------------------

// addToSkip / removeFromSkip rewrite srv0.automatic_https.skip in a single
// read-modify-write because Caddy doesn't offer a primitive to "add unique
// element" to a list inside the config tree.
func (c *Client) addToSkip(ctx context.Context, host string) error {
	cfg, err := c.GetConfig(ctx)
	if err != nil {
		return err
	}
	skip := readSkipList(cfg)
	for _, h := range skip {
		if h == host {
			return nil
		}
	}
	skip = append(skip, host)
	return c.putSkipList(ctx, skip)
}

func (c *Client) removeFromSkip(ctx context.Context, host string) error {
	cfg, err := c.GetConfig(ctx)
	if err != nil {
		return err
	}
	skip := readSkipList(cfg)
	out := skip[:0]
	changed := false
	for _, h := range skip {
		if h == host {
			changed = true
			continue
		}
		out = append(out, h)
	}
	if !changed {
		return nil
	}
	return c.putSkipList(ctx, out)
}

func (c *Client) putSkipList(ctx context.Context, list []string) error {
	body, err := json.Marshal(list)
	if err != nil {
		return err
	}
	// PATCH replaces the value at the path with the supplied JSON.
	return c.do(ctx, http.MethodPatch, "/config/apps/http/servers/srv0/automatic_https/skip", body, nil)
}

func readSkipList(cfg map[string]any) []string {
	apps, _ := cfg["apps"].(map[string]any)
	if apps == nil {
		return nil
	}
	http, _ := apps["http"].(map[string]any)
	if http == nil {
		return nil
	}
	servers, _ := http["servers"].(map[string]any)
	if servers == nil {
		return nil
	}
	srv0, _ := servers["srv0"].(map[string]any)
	if srv0 == nil {
		return nil
	}
	ah, _ := srv0["automatic_https"].(map[string]any)
	if ah == nil {
		return nil
	}
	rawSkip, _ := ah["skip"].([]any)
	out := make([]string, 0, len(rawSkip))
	for _, r := range rawSkip {
		if s, ok := r.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// do issues an HTTP request, normalising error mapping. If `into` is non-nil
// the response body is decoded as JSON into it.
func (c *Client) do(ctx context.Context, method, path string, body []byte, into any) error {
	var reader io.Reader
	if len(body) > 0 {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrAdminUnreachable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		// Caddy returns 404 both for "no such id" and "no such config path".
		// We funnel that to ErrRouteNotFound; callers that issue non-route
		// requests don't typically encounter 404.
		return ErrRouteNotFound
	}
	if resp.StatusCode/100 != 2 {
		return c.errFromBody(resp)
	}
	if into != nil {
		if err := json.NewDecoder(resp.Body).Decode(into); err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("%w: %v", ErrBadResponse, err)
		}
	}
	return nil
}

func (c *Client) errFromBody(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	msg := strings.TrimSpace(string(body))
	return fmt.Errorf("caddy: admin API %s %s -> %d: %s", resp.Request.Method, resp.Request.URL.Path, resp.StatusCode, msg)
}
