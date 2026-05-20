package caddy

import (
	"crypto/sha1"
	"encoding/hex"
	"strings"
	"time"
)

// Route is a single host -> upstream mapping inside Caddy's `apps.http.servers.srv0`.
// We use map[string]any for the actual JSON sent over the wire so we don't
// have to model every Caddy directive — but we keep this struct for callers
// who want a typed view.
type Route struct {
	Host         string
	Upstream     string
	UpstreamPort int
	// ForceHTTPS, when true (the default), lets Caddy's auto-HTTPS feature
	// install a 308 redirect on :80 for this host. When false, we emit an
	// extra sibling route that explicitly matches `protocol: http` for
	// this host so Caddy's redirect-injection logic considers the HTTP
	// side already handled and leaves it alone — the result is "served on
	// both :80 and :443 without redirect".
	ForceHTTPS bool
}

// CertInfo is a simplified TLS certificate status for a host. See
// Client.CertStatus for current limitations.
type CertInfo struct {
	Host      string     // the host we asked about
	Issued    bool       // true if a usable certificate exists
	ExpiresAt *time.Time // nil if unknown / not yet issued
}

// routeID returns a deterministic Caddy `@id` for the route handling `host`.
// We slug the host (replace dots/colons with underscores) and prefix
// "prexel_route_" so prexel-managed routes are easy to spot in the live
// config (which is also how RemoveRoute knows what to delete).
//
// Strategy is documented at the top of client.go.
func routeID(host string) string {
	slug := strings.ReplaceAll(host, ".", "_")
	slug = strings.ReplaceAll(slug, ":", "_")
	slug = strings.ReplaceAll(slug, "*", "wildcard")
	// Truncate aggressive: hash long hosts to keep IDs short and predictable.
	if len(slug) > 60 {
		sum := sha1.Sum([]byte(host))
		slug = slug[:60] + "_" + hex.EncodeToString(sum[:4])
	}
	return "prexel_route_" + slug
}

// baseConfig is the boot config we POST to /load when Prexel starts. It sets
// the admin API listener and creates two servers:
//
//   - `srv0` on :80 and :443 — auto-HTTPS enabled, ready to receive
//     UpsertRoute calls for app domains.
//   - `srv_admin` on :3000 — only used in dev to expose the prexel API on
//     localhost via Caddy. Reverse-proxies to prexel:3000 inside prexel-net.
//
// Production callers may want to omit srv_admin; we leave it in for now since
// it is harmless when no client hits it.
func baseConfig() map[string]any {
	return map[string]any{
		"admin": map[string]any{
			"listen": "0.0.0.0:2019",
		},
		"logging": map[string]any{
			"logs": map[string]any{
				"default": map[string]any{
					"level": "INFO",
				},
			},
		},
		"apps": map[string]any{
			"http": map[string]any{
				"servers": map[string]any{
					"srv0": map[string]any{
						"listen": []string{":80", ":443"},
						// Auto-HTTPS handles redirect & cert issuance. We do NOT
						// disable it. EnableTLS/DisableTLS adjust which hosts
						// are exempted via automatic_https.skip / disable.
						"routes": []any{},
						// Initialize empty automatic_https — managed dynamically.
						"automatic_https": map[string]any{
							"disable": false,
						},
					},
				},
			},
		},
	}
}

// makeRoute builds the JSON object representing a host->upstream route.
// The host-only matcher catches both HTTP and HTTPS by default because
// srv0 listens on :80 and :443. Caddy's auto-HTTPS layer normally
// rewrites this so that HTTP requests for hosts with managed certs are
// 308-redirected to HTTPS; that is the desired behaviour when the
// operator wants `force_https=true`.
func makeRoute(host, upstream string, port int) map[string]any {
	return map[string]any{
		"@id": routeID(host),
		"match": []any{
			map[string]any{
				"host": []string{host},
			},
		},
		"handle": []any{
			map[string]any{
				"handler": "reverse_proxy",
				"upstreams": []any{
					map[string]any{
						"dial": joinHostPort(upstream, port),
					},
				},
			},
		},
		"terminal": true,
	}
}

// httpPassthroughRouteID returns the deterministic @id used for the
// extra HTTP-only sibling route emitted when force_https=false.
// Mirrors routeID but uses a distinct prefix so UpsertRoute/RemoveRoute
// can address the two routes independently.
func httpPassthroughRouteID(host string) string {
	base := routeID(host) // "prexel_route_<slug>"
	return "prexel_http_" + base[len("prexel_route_"):]
}

// makeHTTPPassthroughRoute builds the sibling route that explicitly
// matches `protocol: http` for `host`. Caddy's auto-HTTPS layer skips
// the HTTP→HTTPS redirect insertion when an explicit user route already
// claims the HTTP side for that host, so emitting this route is the
// minimal way to opt out of the auto-redirect without disabling the
// TLS-managed sibling on :443.
//
// The handler is the same reverse-proxy block as the canonical route;
// requests on :80 land directly on the upstream while :443 is still
// covered by the standard route returned by makeRoute.
func makeHTTPPassthroughRoute(host, upstream string, port int) map[string]any {
	return map[string]any{
		"@id": httpPassthroughRouteID(host),
		"match": []any{
			map[string]any{
				"host":     []string{host},
				"protocol": "http",
			},
		},
		"handle": []any{
			map[string]any{
				"handler": "reverse_proxy",
				"upstreams": []any{
					map[string]any{
						"dial": joinHostPort(upstream, port),
					},
				},
			},
		},
		"terminal": true,
	}
}

func joinHostPort(host string, port int) string {
	return host + ":" + itoa(port)
}

// itoa avoids importing strconv just for this hot path.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
