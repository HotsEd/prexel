package caddy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// recordedRequest captures what the stub admin API received so tests can
// assert method/path/body without coupling to the underlying http.Server.
type recordedRequest struct {
	Method string
	Path   string
	Body   string
}

// stubAdmin spins up an httptest.Server that records every incoming request
// and lets each test plug in a handler for whatever payload it expects. The
// returned slice pointer is shared with the handler; callers should snapshot
// it via reqs() after the operation under test runs.
type stubAdmin struct {
	t      *testing.T
	mu     sync.Mutex
	reqs   []recordedRequest
	server *httptest.Server
}

func newStubAdmin(t *testing.T, handler http.HandlerFunc) *stubAdmin {
	t.Helper()
	s := &stubAdmin{t: t}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.reqs = append(s.reqs, recordedRequest{
			Method: r.Method,
			Path:   r.URL.Path,
			Body:   string(body),
		})
		s.mu.Unlock()
		// Rewind body for handler.
		r.Body = io.NopCloser(strings.NewReader(string(body)))
		handler(w, r)
	}))
	t.Cleanup(s.server.Close)
	return s
}

func (s *stubAdmin) requests() []recordedRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]recordedRequest, len(s.reqs))
	copy(out, s.reqs)
	return out
}

func (s *stubAdmin) url() string { return s.server.URL }

func TestClient_Ping_OK(t *testing.T) {
	stub := newStubAdmin(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})
	c := New(stub.url())
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	reqs := stub.requests()
	if len(reqs) != 1 {
		t.Fatalf("want 1 request, got %d", len(reqs))
	}
	if reqs[0].Method != http.MethodGet || reqs[0].Path != "/config/" {
		t.Errorf("unexpected request: %+v", reqs[0])
	}
}

func TestClient_Ping_Unreachable(t *testing.T) {
	c := New("http://127.0.0.1:1") // closed port
	err := c.Ping(context.Background())
	if err == nil {
		t.Fatal("expected error for unreachable admin API")
	}
	if !errors.Is(err, ErrAdminUnreachable) {
		t.Errorf("want ErrAdminUnreachable, got %v", err)
	}
}

func TestClient_UpsertRoute_UpdateInPlace(t *testing.T) {
	// PATCH /id/<routeID> returns 200 → success without a fallback POST.
	// force_https=true also triggers a best-effort DELETE on the
	// passthrough sibling id; we let that 404 so the call is a no-op.
	stub := newStubAdmin(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPatch:
			w.WriteHeader(http.StatusOK)
		case http.MethodDelete:
			// Sibling never existed — pretend so.
			w.WriteHeader(http.StatusNotFound)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	})
	c := New(stub.url())
	host := "api.example.com"
	if err := c.UpsertRoute(context.Background(), host, "prexel-app", 8080, true); err != nil {
		t.Fatalf("UpsertRoute: %v", err)
	}
	reqs := stub.requests()
	// With force_https=true we push the canonical route (PATCH) and
	// always issue the sibling-cleanup DELETE on the passthrough id.
	if len(reqs) != 2 {
		t.Fatalf("want 2 requests (PATCH canonical + DELETE sibling), got %d", len(reqs))
	}
	wantPath := "/id/" + routeID(host)
	if reqs[0].Method != http.MethodPatch || reqs[0].Path != wantPath {
		t.Errorf("unexpected method/path: %s %s (want PATCH %s)", reqs[0].Method, reqs[0].Path, wantPath)
	}
	wantSibling := "/id/" + httpPassthroughRouteID(host)
	if reqs[1].Method != http.MethodDelete || reqs[1].Path != wantSibling {
		t.Errorf("unexpected sibling cleanup: %+v (want DELETE %s)", reqs[1], wantSibling)
	}
	// Body must be the encoded route JSON carrying the right host, upstream,
	// port, and @id.
	var route map[string]any
	if err := json.Unmarshal([]byte(reqs[0].Body), &route); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if id, _ := route["@id"].(string); id != routeID(host) {
		t.Errorf("body @id = %q, want %q", id, routeID(host))
	}
	// Drill into match[0].host and handle[0].upstreams to confirm shape.
	matches, _ := route["match"].([]any)
	if len(matches) != 1 {
		t.Fatalf("match must have 1 entry, got %d", len(matches))
	}
	matchMap, _ := matches[0].(map[string]any)
	hosts, _ := matchMap["host"].([]any)
	if len(hosts) != 1 || hosts[0] != host {
		t.Errorf("match.host = %v, want [%q]", hosts, host)
	}
	handles, _ := route["handle"].([]any)
	handle0, _ := handles[0].(map[string]any)
	upstreams, _ := handle0["upstreams"].([]any)
	if len(upstreams) != 1 {
		t.Fatalf("upstreams must have 1 entry, got %d", len(upstreams))
	}
	u0, _ := upstreams[0].(map[string]any)
	if dial, _ := u0["dial"].(string); dial != "prexel-app:8080" {
		t.Errorf("upstream.dial = %q, want %q", dial, "prexel-app:8080")
	}
}

func TestClient_UpsertRoute_FallbackPost(t *testing.T) {
	// PATCH returns 404 → client falls back to POST on srv0.routes.
	// DELETE on the passthrough sibling is best-effort and 404s here.
	var sawPatch, sawPost bool
	stub := newStubAdmin(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPatch:
			sawPatch = true
			w.WriteHeader(http.StatusNotFound)
		case http.MethodPost:
			sawPost = true
			w.WriteHeader(http.StatusOK)
		case http.MethodDelete:
			// Best-effort sibling cleanup when force_https=true.
			w.WriteHeader(http.StatusNotFound)
		default:
			t.Errorf("unexpected method %q", r.Method)
		}
	})
	c := New(stub.url())
	if err := c.UpsertRoute(context.Background(), "api.example.com", "prexel-app", 8080, true); err != nil {
		t.Fatalf("UpsertRoute: %v", err)
	}
	if !sawPatch || !sawPost {
		t.Errorf("expected both PATCH (fail) and POST (fallback); patch=%v post=%v", sawPatch, sawPost)
	}
	reqs := stub.requests()
	if len(reqs) < 2 {
		t.Fatalf("want at least 2 requests, got %d", len(reqs))
	}
	// The POST is the fallback insertion of the canonical route — it
	// happens BEFORE the sibling-cleanup DELETE.
	var sawFallback bool
	for _, r := range reqs {
		if r.Method == http.MethodPost && r.Path == "/config/apps/http/servers/srv0/routes" {
			sawFallback = true
			break
		}
	}
	if !sawFallback {
		t.Errorf("expected fallback POST to /config/apps/http/servers/srv0/routes, got %+v", reqs)
	}
}

func TestClient_UpsertRoute_SelfHealsOnInvalidTraversal(t *testing.T) {
	// Reproduces the bug from the field: Caddy boots from a config without
	// srv0, the PATCH 404s as usual, then the fallback POST hits
	// `/config/apps/http/servers/srv0/routes` and Caddy answers 500 with
	// "invalid traversal path at: ...". The client should detect that,
	// bootstrap the base config via /load, and retry the POST.
	var (
		patchCount     int
		postRouteCount int
		loadCount      int
		mu             sync.Mutex
	)
	stub := newStubAdmin(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodPatch:
			patchCount++
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPost && r.URL.Path == "/load":
			loadCount++
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && r.URL.Path == "/config/apps/http/servers/srv0/routes":
			postRouteCount++
			if postRouteCount == 1 {
				// Mirror Caddy's wire shape: 500 with a JSON error body.
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":"invalid traversal path at: config/apps/http/servers/srv0/routes"}`))
				return
			}
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodDelete:
			// Best-effort passthrough cleanup when force_https=true.
			w.WriteHeader(http.StatusNotFound)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})
	c := New(stub.url())
	if err := c.UpsertRoute(context.Background(), "api.example.com", "prexel-app", 8080, true); err != nil {
		t.Fatalf("UpsertRoute should self-heal, got %v", err)
	}
	if patchCount != 1 {
		t.Errorf("expected 1 PATCH attempt, got %d", patchCount)
	}
	if loadCount != 1 {
		t.Errorf("expected exactly one bootstrap /load, got %d", loadCount)
	}
	if postRouteCount != 2 {
		t.Errorf("expected 2 POST attempts (first fails, second succeeds), got %d", postRouteCount)
	}
}

func TestClient_UpsertRoute_Validation(t *testing.T) {
	c := New("http://127.0.0.1:9")
	if err := c.UpsertRoute(context.Background(), "", "x", 8080, true); err == nil {
		t.Error("expected error for empty host")
	}
	if err := c.UpsertRoute(context.Background(), "x.com", "", 8080, true); err == nil {
		t.Error("expected error for empty upstream")
	}
	if err := c.UpsertRoute(context.Background(), "x.com", "y", 0, true); err == nil {
		t.Error("expected error for invalid port")
	}
}

func TestClient_RemoveRoute(t *testing.T) {
	// RemoveRoute drops the sibling first (best-effort, 404 swallowed)
	// then the canonical route. We answer 404 on the sibling id and 200
	// on the canonical id so both code paths exercise.
	host := "api.example.com"
	wantPath := "/id/" + routeID(host)
	wantSibling := "/id/" + httpPassthroughRouteID(host)
	stub := newStubAdmin(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("unexpected method %q", r.Method)
			return
		}
		switch r.URL.Path {
		case wantSibling:
			w.WriteHeader(http.StatusNotFound)
		case wantPath:
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected DELETE path %q", r.URL.Path)
		}
	})
	c := New(stub.url())
	if err := c.RemoveRoute(context.Background(), host); err != nil {
		t.Fatalf("RemoveRoute: %v", err)
	}
	reqs := stub.requests()
	if len(reqs) != 2 {
		t.Fatalf("want 2 requests (sibling + canonical), got %d", len(reqs))
	}
	if reqs[0].Path != wantSibling {
		t.Errorf("first DELETE = %q, want sibling %q", reqs[0].Path, wantSibling)
	}
	if reqs[1].Path != wantPath {
		t.Errorf("second DELETE = %q, want canonical %q", reqs[1].Path, wantPath)
	}
}

func TestClient_RemoveRoute_EmptyHost(t *testing.T) {
	c := New("http://127.0.0.1:9")
	if err := c.RemoveRoute(context.Background(), ""); err == nil {
		t.Error("expected error for empty host")
	}
}

func TestClient_BootstrapBaseConfig(t *testing.T) {
	// /load receives the boot config; we verify the admin listener and the
	// srv0 layout are exactly what config.go produces.
	stub := newStubAdmin(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	c := New(stub.url())
	if err := c.BootstrapBaseConfig(context.Background()); err != nil {
		t.Fatalf("BootstrapBaseConfig: %v", err)
	}
	reqs := stub.requests()
	if len(reqs) != 1 {
		t.Fatalf("want 1 request, got %d", len(reqs))
	}
	r := reqs[0]
	if r.Method != http.MethodPost || r.Path != "/load" {
		t.Errorf("unexpected method/path: %s %s (want POST /load)", r.Method, r.Path)
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(r.Body), &cfg); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	admin, _ := cfg["admin"].(map[string]any)
	if admin == nil {
		t.Fatal("missing admin block in base config")
	}
	if listen, _ := admin["listen"].(string); listen != "0.0.0.0:2019" {
		t.Errorf("admin.listen = %q, want 0.0.0.0:2019", listen)
	}
	apps, _ := cfg["apps"].(map[string]any)
	httpApps, _ := apps["http"].(map[string]any)
	servers, _ := httpApps["servers"].(map[string]any)
	srv0, _ := servers["srv0"].(map[string]any)
	if srv0 == nil {
		t.Fatal("missing srv0 in base config")
	}
	listen, _ := srv0["listen"].([]any)
	if len(listen) != 2 {
		t.Errorf("srv0.listen must have 2 entries, got %d", len(listen))
	}
}

func TestClient_DisableTLS_AddsToSkip(t *testing.T) {
	// GET /config/ returns an empty srv0; PATCH .../skip records the new list.
	var patchedBody string
	stub := newStubAdmin(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			// Return a minimal config that the readSkipList traversal can walk
			// down to automatic_https.skip = [] without panicking.
			cfg := map[string]any{
				"apps": map[string]any{
					"http": map[string]any{
						"servers": map[string]any{
							"srv0": map[string]any{
								"automatic_https": map[string]any{
									"skip": []any{},
								},
							},
						},
					},
				},
			}
			_ = json.NewEncoder(w).Encode(cfg)
		case http.MethodPatch:
			body, _ := io.ReadAll(r.Body)
			patchedBody = string(body)
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected method %q", r.Method)
		}
	})
	c := New(stub.url())
	host := "skip.example.com"
	if err := c.DisableTLS(context.Background(), host); err != nil {
		t.Fatalf("DisableTLS: %v", err)
	}
	if !strings.Contains(patchedBody, host) {
		t.Errorf("PATCH body must contain host %q; got %s", host, patchedBody)
	}
	// Confirm the PATCH was sent to the canonical skip path.
	reqs := stub.requests()
	last := reqs[len(reqs)-1]
	if last.Method != http.MethodPatch || last.Path != "/config/apps/http/servers/srv0/automatic_https/skip" {
		t.Errorf("unexpected PATCH target: %+v", last)
	}
}

func TestClient_EnableTLS_RemovesFromSkip(t *testing.T) {
	host := "tls.example.com"
	var patched bool
	var patchedList []string
	stub := newStubAdmin(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			cfg := map[string]any{
				"apps": map[string]any{
					"http": map[string]any{
						"servers": map[string]any{
							"srv0": map[string]any{
								"automatic_https": map[string]any{
									"skip": []any{host, "other.example.com"},
								},
							},
						},
					},
				},
			}
			_ = json.NewEncoder(w).Encode(cfg)
		case http.MethodPatch:
			patched = true
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &patchedList)
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected method %q", r.Method)
		}
	})
	c := New(stub.url())
	if err := c.EnableTLS(context.Background(), host); err != nil {
		t.Fatalf("EnableTLS: %v", err)
	}
	if !patched {
		t.Fatal("expected PATCH to rewrite skip list")
	}
	for _, h := range patchedList {
		if h == host {
			t.Errorf("host %q still in skip list after EnableTLS: %v", host, patchedList)
		}
	}
}

func TestRouteID_Deterministic(t *testing.T) {
	// Two callers asking for the same host must produce identical IDs; the
	// strategy in client.go counts on this for PATCH-vs-POST routing.
	a := routeID("api.example.com")
	b := routeID("api.example.com")
	if a != b {
		t.Errorf("routeID not deterministic: %q vs %q", a, b)
	}
	if !strings.HasPrefix(a, "prexel_route_") {
		t.Errorf("routeID missing prefix: %q", a)
	}
	if strings.Contains(a, ".") {
		t.Errorf("routeID must not contain dots: %q", a)
	}
}

func TestNew_DefaultAdminURL(t *testing.T) {
	c := New("")
	if c.baseURL != DefaultAdminURL {
		t.Errorf("default baseURL = %q, want %q", c.baseURL, DefaultAdminURL)
	}
	// Trailing slash must be stripped.
	c = New("http://example.com/")
	if strings.HasSuffix(c.baseURL, "/") {
		t.Errorf("trailing slash not stripped: %q", c.baseURL)
	}
}
