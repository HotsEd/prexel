package instance

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

/*
   Update awareness — passive "is there a newer release?" check.

   Surfaces the latest tagged release of github.com/prexel/prexel so the
   Settings → Instance card can show a tiny "Update available" hint next
   to the running version. There is no auto-update path here: the operator
   still runs the installer / package manager themselves. We only remove
   the "am I out of date?" friction.

   Design constraints:
     - Outbound network is best-effort. If GitHub is unreachable, slow, or
       rate-limited, the panel must NOT break — every failure leaves the
       previously cached answer in place (or empty on cold start) and the
       UI silently degrades to showing only the running version.
     - In-memory cache only. The signal is not security-sensitive and
       persisting it would force a DB migration; a fresh boot re-querying
       GitHub once is cheaper than a schema bump.
     - Polite caching window: 24h hard TTL on success, 5-minute back-off
       on failure so we don't hammer api.github.com during outages.
*/

// VersionInfo is what we surface to the API. An empty Version means the
// checker hasn't successfully fetched yet (cold start, or last fetch
// failed). Callers are expected to treat "empty" as "I don't know".
type VersionInfo struct {
	Version     string    `json:"version"`
	URL         string    `json:"url"`
	PublishedAt time.Time `json:"published_at"`
}

// LatestVersionChecker is safe for concurrent use.
type LatestVersionChecker struct {
	mu        sync.Mutex
	cached    VersionInfo
	fetchedAt time.Time
	ttl       time.Duration
	failTTL   time.Duration
	repo      string
	httpc     *http.Client
}

// NewLatestVersionChecker returns a checker configured for the official
// repo with a 24h success TTL and a 5-minute failure back-off. The HTTP
// client has an 8-second timeout — generous enough for slow networks,
// strict enough to not hold the Settings page hostage.
func NewLatestVersionChecker() *LatestVersionChecker {
	return &LatestVersionChecker{
		ttl:     24 * time.Hour,
		failTTL: 5 * time.Minute,
		repo:    "prexel/prexel",
		httpc:   &http.Client{Timeout: 8 * time.Second},
	}
}

// Get returns the cached version when fresh, otherwise tries to fetch.
// On failure it returns whatever was previously cached (possibly empty)
// and shifts `fetchedAt` so the next attempt is ~failTTL away — that
// keeps GitHub rate limits happy without leaving the UI starving for an
// answer for 24h after a single hiccup.
func (c *LatestVersionChecker) Get(ctx context.Context) VersionInfo {
	c.mu.Lock()
	if c.cached.Version != "" && time.Since(c.fetchedAt) < c.ttl {
		v := c.cached
		c.mu.Unlock()
		return v
	}
	c.mu.Unlock()

	fresh, err := c.fetch(ctx)
	if err != nil {
		// Back off: pretend the last successful fetch happened
		// (ttl - failTTL) ago, so the next call will retry in ~failTTL.
		c.mu.Lock()
		c.fetchedAt = time.Now().Add(-(c.ttl - c.failTTL))
		v := c.cached
		c.mu.Unlock()
		return v
	}

	c.mu.Lock()
	c.cached = fresh
	c.fetchedAt = time.Now()
	c.mu.Unlock()
	return fresh
}

// ghRelease matches just the fields we care about from the GitHub
// "releases/latest" payload. Anything else is ignored.
type ghRelease struct {
	TagName     string    `json:"tag_name"`
	HTMLURL     string    `json:"html_url"`
	PublishedAt time.Time `json:"published_at"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
}

func (c *LatestVersionChecker) fetch(ctx context.Context) (VersionInfo, error) {
	url := "https://api.github.com/repos/" + c.repo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return VersionInfo{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "prexel-instance")

	resp, err := c.httpc.Do(req)
	if err != nil {
		return VersionInfo{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Drain a bit of the body for a meaningful error in logs without
		// holding the full response in memory.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return VersionInfo{}, errors.New("github releases: " + resp.Status + ": " + string(body))
	}

	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return VersionInfo{}, err
	}
	// The "releases/latest" endpoint already excludes drafts/prereleases,
	// but the defensive check is cheap and protects us if GitHub ever
	// loosens that contract.
	if rel.Draft || rel.Prerelease {
		return VersionInfo{}, errors.New("latest release is draft or prerelease")
	}
	return VersionInfo{
		Version:     strings.TrimPrefix(rel.TagName, "v"),
		URL:         rel.HTMLURL,
		PublishedAt: rel.PublishedAt,
	}, nil
}
