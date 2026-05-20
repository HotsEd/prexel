// Package caddy is an HTTP client for the Caddy v2 admin API
// (https://caddyserver.com/docs/api). It deliberately does NOT import any
// caddyserver/caddy code — we treat Caddy as a black box reached over HTTP so
// we can manage it the same way regardless of whether it runs as a sidecar
// container (dev/prod) or as a systemd service.
package caddy

import "errors"

// Sentinel errors. Match with errors.Is.
var (
	// ErrRouteNotFound is returned when a route lookup/delete by host slug misses.
	ErrRouteNotFound = errors.New("caddy: route not found")

	// ErrAdminUnreachable is returned when the admin API does not respond
	// (typically network-level errors against the configured base URL).
	ErrAdminUnreachable = errors.New("caddy: admin API unreachable")

	// ErrBadResponse is returned when the admin API returns an unexpected
	// payload that we cannot decode.
	ErrBadResponse = errors.New("caddy: bad response from admin API")
)
