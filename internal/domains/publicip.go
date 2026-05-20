package domains

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// publicIPProvider implements PublicIPProvider with an in-memory 1h cache and
// a primary (ipify) + fallback (interface scan) strategy. Cache miss path is
// safe to call concurrently; the first goroutine performs the lookup and
// subsequent callers receive the cached value.
type publicIPProvider struct {
	url    string
	timeout time.Duration
	ttl    time.Duration

	mu        sync.Mutex
	cached    string
	expiresAt time.Time
}

// PublicIPDefaultURL is the canonical lookup endpoint. Override for tests.
const PublicIPDefaultURL = "https://api.ipify.org"

// NewPublicIPProvider returns the default provider. Pass an empty url to use
// PublicIPDefaultURL.
func NewPublicIPProvider(url string) PublicIPProvider {
	if url == "" {
		url = PublicIPDefaultURL
	}
	return &publicIPProvider{
		url:     url,
		timeout: 5 * time.Second,
		ttl:     1 * time.Hour,
	}
}

// Get returns the host's public IPv4. Cached for 1h after the first
// successful lookup. Lookups fall back to a local interface scan when the
// HTTP endpoint is unreachable — useful in air-gapped/dev environments where
// the host actually has a routable IP via NAT but cannot reach ipify.
func (p *publicIPProvider) Get(ctx context.Context) (string, error) {
	p.mu.Lock()
	if p.cached != "" && time.Now().Before(p.expiresAt) {
		ip := p.cached
		p.mu.Unlock()
		return ip, nil
	}
	p.mu.Unlock()

	ip, err := p.lookupHTTP(ctx)
	if err != nil {
		ip2, err2 := p.lookupInterfaces()
		if err2 != nil {
			return "", fmt.Errorf("public ip: ipify=%v interfaces=%v", err, err2)
		}
		ip = ip2
	}

	p.mu.Lock()
	p.cached = ip
	p.expiresAt = time.Now().Add(p.ttl)
	p.mu.Unlock()
	return ip, nil
}

func (p *publicIPProvider) lookupHTTP(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.url, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		return "", err
	}
	ip := strings.TrimSpace(string(body))
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return "", fmt.Errorf("invalid IP from %s: %q", p.url, ip)
	}
	return parsed.String(), nil
}

// lookupInterfaces walks the host's network interfaces and returns the first
// IPv4 that is not loopback/link-local/private. Note: in dev compose the
// container's own interfaces are RFC1918 — fallback will return an error, and
// that's fine; callers handle missing public IP gracefully.
func (p *publicIPProvider) lookupInterfaces() (string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil {
				continue
			}
			ip = ip.To4()
			if ip == nil {
				continue
			}
			if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsPrivate() {
				continue
			}
			return ip.String(), nil
		}
	}
	return "", errors.New("no public IPv4 interface found")
}
