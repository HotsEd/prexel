package gitsrc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

// ValidateRepoURL gates every clone/test/inspect path against the two
// classes of attack the git surface enables when the operator can pick
// any URL they want:
//
//  1. Scheme abuse — git supports `file://`, `git://`, and (depending on
//     the libgit2 build) `ext::`, `gopher://`, etc. `file://` would let
//     a logged-in operator read arbitrary files off the Prexel host
//     ("clone this repo, then look at the working tree"). Plain `git://`
//     and `http://` happen in cleartext and are trivially MITM'd. We
//     restrict the allowlist to https + ssh + git+ssh.
//
//  2. SSRF / metadata-endpoint pivots — even with https, a hostname can
//     resolve to a private IP (RFC1918, loopback, link-local, cloud
//     metadata 169.254.169.254). Cloning then runs a TLS handshake
//     against the internal target, which can be enough on its own to
//     enumerate services on the host network or trigger a SSRF on a
//     misconfigured internal service. We resolve the host and refuse if
//     ANY resolved IP lands in a private range — this also defeats DNS
//     rebinding, where a host first returns a public IP for our
//     validation and then a private IP for the actual git fetch.
//
// SSH URLs are passed through DNS resolution too; we don't whitelist
// "github.com only" because operators legitimately host on internal
// providers, but we still refuse private-ip resolution so an SSH
// shortcut like `git@10.0.0.1:repo.git` can't sneak past either.
//
// The function is fail-closed: any parse error, scheme outside the
// allowlist, or unresolvable / private host yields an error. It returns
// nil only when every check passes.
func ValidateRepoURL(rawURL string) error {
	raw := strings.TrimSpace(rawURL)
	if raw == "" {
		return errors.New("git url: empty")
	}

	// SCP-style "git@host:path" form (no scheme) — common for ssh.
	if !strings.Contains(raw, "://") {
		host, ok := parseSCPLike(raw)
		if !ok {
			return fmt.Errorf("git url: unsupported form %q", raw)
		}
		return validateHost(host)
	}

	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("git url: parse: %w", err)
	}

	switch strings.ToLower(u.Scheme) {
	case "https", "ssh", "git+ssh":
		// allowed
	default:
		return fmt.Errorf("git url: scheme %q not allowed (use https, ssh, or git+ssh)", u.Scheme)
	}

	host := u.Hostname()
	if host == "" {
		return errors.New("git url: missing host")
	}
	return validateHost(host)
}

// parseSCPLike extracts the host from `user@host:path` style SSH URLs
// that git accepts without an explicit scheme. Returns ok=false for
// anything that doesn't match the shape.
func parseSCPLike(raw string) (string, bool) {
	// Must contain ':' (separating host and path) and must NOT contain
	// "/" before that colon — otherwise it's a path, not a host.
	colon := strings.Index(raw, ":")
	if colon <= 0 {
		return "", false
	}
	if strings.IndexByte(raw[:colon], '/') >= 0 {
		return "", false
	}
	hostPart := raw[:colon]
	// Strip leading "user@".
	if at := strings.LastIndex(hostPart, "@"); at >= 0 {
		hostPart = hostPart[at+1:]
	}
	if hostPart == "" {
		return "", false
	}
	return hostPart, true
}

// validateHost rejects hosts that resolve to (or already are) a private
// or otherwise-special IP. It performs DNS resolution and inspects every
// returned address — the goal is to fail closed against DNS rebinding,
// where a quick TTL flip turns a public-looking validation into a
// private-pointing fetch.
func validateHost(host string) error {
	// Strip brackets from IPv6 literals.
	host = strings.Trim(host, "[]")

	// Literal IP — check directly without touching DNS.
	if ip := net.ParseIP(host); ip != nil {
		if isBlockedIP(ip) {
			return fmt.Errorf("git url: host %q resolves to a blocked address", host)
		}
		return nil
	}

	// Hostname — resolve and check every answer. We use a short timeout
	// so a stalled resolver can't pin a clone goroutine indefinitely;
	// 5s is plenty for any reasonable DNS path.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return fmt.Errorf("git url: resolve %q: %w", host, err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("git url: host %q resolves to no addresses", host)
	}
	for _, ip := range ips {
		if isBlockedIP(ip) {
			return fmt.Errorf("git url: host %q resolves to a blocked address (%s)", host, ip)
		}
	}
	return nil
}

// awsMetadataIP is the well-known link-local address every cloud
// (AWS, GCP, Azure, Hetzner Cloud, DO, …) uses for instance metadata.
// IsLinkLocalUnicast() already catches it, but we keep the explicit
// check so the failure mode is "blocked because metadata" and not
// "blocked because link-local" in logs.
var awsMetadataIP = net.ParseIP("169.254.169.254")

// isBlockedIP reports whether ip falls into a range Prexel must never
// reach during a git operation: loopback, link-local, cloud metadata,
// or any of the RFC1918 private-network ranges (10/8, 172.16/12,
// 192.168/16) plus the IPv6 ULA range (fc00::/7).
func isBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() {
		return true
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}
	if ip.IsMulticast() {
		return true
	}
	if ip.IsUnspecified() {
		return true
	}
	if ip.Equal(awsMetadataIP) {
		return true
	}
	// RFC1918 + CGNAT + IPv6 ULA. net.IP.IsPrivate covers both family
	// trees in modern Go (1.17+).
	if ip.IsPrivate() {
		return true
	}
	return false
}
