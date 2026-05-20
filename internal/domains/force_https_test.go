package domains

import (
	"context"
	"sync"
	"testing"

	_ "modernc.org/sqlite"
)

// fakeCaddy is the minimal CaddyClient stub used by the force_https tests.
// We only assert on UpsertRoute calls — the other surfaces (RemoveRoute,
// EnableTLS, DisableTLS) are no-ops here because Create+Update happen
// before DNS verification.
type fakeCaddy struct {
	mu          sync.Mutex
	upsertCalls []upsertCall
}

type upsertCall struct {
	host       string
	upstream   string
	port       int
	forceHTTPS bool
}

func (f *fakeCaddy) UpsertRoute(_ context.Context, host, upstream string, port int, forceHTTPS bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upsertCalls = append(f.upsertCalls, upsertCall{host, upstream, port, forceHTTPS})
	return nil
}

func (f *fakeCaddy) RemoveRoute(_ context.Context, _ string) error { return nil }
func (f *fakeCaddy) EnableTLS(_ context.Context, _ string) error   { return nil }
func (f *fakeCaddy) DisableTLS(_ context.Context, _ string) error  { return nil }

func (f *fakeCaddy) calls() []upsertCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]upsertCall, len(f.upsertCalls))
	copy(out, f.upsertCalls)
	return out
}

func TestCreate_DefaultForceHTTPSIsTrue(t *testing.T) {
	db := setupTestDB(t)
	if _, err := db.Exec(`INSERT INTO apps(id, name) VALUES ('app-1', 'web')`); err != nil {
		t.Fatalf("seed app: %v", err)
	}
	caddy := &fakeCaddy{}
	svc := NewService(Deps{DB: db, Caddy: caddy})

	appID := "app-1"
	d, err := svc.Create(context.Background(), CreateInput{
		Name:  "api.example.com",
		AppID: &appID,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !d.ForceHTTPS {
		t.Errorf("ForceHTTPS default = false, want true")
	}
	calls := caddy.calls()
	if len(calls) != 1 {
		t.Fatalf("want 1 UpsertRoute call, got %d", len(calls))
	}
	if !calls[0].forceHTTPS {
		t.Errorf("UpsertRoute forceHTTPS = false, want true")
	}

	// Persisted value must round-trip via the repo too.
	got, err := svc.Get(context.Background(), d.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.ForceHTTPS {
		t.Errorf("persisted ForceHTTPS = false, want true")
	}
}

func TestCreate_ExplicitForceHTTPSFalse(t *testing.T) {
	db := setupTestDB(t)
	if _, err := db.Exec(`INSERT INTO apps(id, name) VALUES ('app-1', 'web')`); err != nil {
		t.Fatalf("seed app: %v", err)
	}
	caddy := &fakeCaddy{}
	svc := NewService(Deps{DB: db, Caddy: caddy})

	appID := "app-1"
	off := false
	d, err := svc.Create(context.Background(), CreateInput{
		Name:       "plain.example.com",
		AppID:      &appID,
		ForceHTTPS: &off,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if d.ForceHTTPS {
		t.Errorf("ForceHTTPS = true, want false")
	}

	// Caddy must have been told force_https=false so it publishes the
	// HTTP-passthrough sibling instead of letting auto-HTTPS redirect.
	calls := caddy.calls()
	if len(calls) != 1 {
		t.Fatalf("want 1 UpsertRoute call, got %d", len(calls))
	}
	if calls[0].forceHTTPS {
		t.Errorf("UpsertRoute forceHTTPS = true, want false")
	}

	got, err := svc.Get(context.Background(), d.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ForceHTTPS {
		t.Errorf("persisted ForceHTTPS = true, want false")
	}
}

func TestUpdate_ForceHTTPSFlipReconcilesCaddy(t *testing.T) {
	db := setupTestDB(t)
	if _, err := db.Exec(`INSERT INTO apps(id, name) VALUES ('app-1','web')`); err != nil {
		t.Fatalf("seed apps: %v", err)
	}
	caddy := &fakeCaddy{}
	svc := NewService(Deps{DB: db, Caddy: caddy})

	// Seed a domain with the default force_https=true via Create so the
	// initial Caddy state is exercised end-to-end.
	appID := "app-1"
	d, err := svc.Create(context.Background(), CreateInput{
		Name:  "toggle.example.com",
		AppID: &appID,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(caddy.calls()) != 1 {
		t.Fatalf("expected 1 upsert from create, got %d", len(caddy.calls()))
	}

	// Flip force_https=false. The service must persist the change AND
	// re-run UpsertRoute with the new flag so Caddy publishes the
	// HTTP-passthrough sibling.
	off := false
	out, err := svc.Update(context.Background(), d.ID, UpdateInput{ForceHTTPS: &off})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if out.ForceHTTPS {
		t.Errorf("ForceHTTPS = true after flip, want false")
	}
	calls := caddy.calls()
	if len(calls) != 2 {
		t.Fatalf("want 2 UpsertRoute calls (create + update), got %d", len(calls))
	}
	last := calls[len(calls)-1]
	if last.host != "toggle.example.com" {
		t.Errorf("reconcile host = %q, want toggle.example.com", last.host)
	}
	if last.forceHTTPS {
		t.Errorf("reconcile forceHTTPS = true, want false")
	}

	// Flip back to true and make sure we reconcile again.
	on := true
	if _, err := svc.Update(context.Background(), d.ID, UpdateInput{ForceHTTPS: &on}); err != nil {
		t.Fatalf("update back: %v", err)
	}
	calls = caddy.calls()
	if len(calls) != 3 {
		t.Fatalf("want 3 UpsertRoute calls after re-flip, got %d", len(calls))
	}
	if !calls[2].forceHTTPS {
		t.Errorf("third upsert forceHTTPS = false, want true")
	}
}

func TestUpdate_ForceHTTPSUnchangedDoesNotReconcile(t *testing.T) {
	// Calling Update without changing force_https (and without changing
	// app/service) must not re-issue an UpsertRoute — the existing
	// reconcile gating depends on a real change to avoid hammering
	// Caddy on every PATCH.
	db := setupTestDB(t)
	if _, err := db.Exec(`INSERT INTO apps(id, name) VALUES ('app-1','web')`); err != nil {
		t.Fatalf("seed apps: %v", err)
	}
	caddy := &fakeCaddy{}
	svc := NewService(Deps{DB: db, Caddy: caddy})
	appID := "app-1"
	d, err := svc.Create(context.Background(), CreateInput{
		Name:  "noop.example.com",
		AppID: &appID,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	before := len(caddy.calls())

	// Send the same value the row already has.
	same := true
	if _, err := svc.Update(context.Background(), d.ID, UpdateInput{ForceHTTPS: &same}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if got := len(caddy.calls()); got != before {
		t.Errorf("expected no extra UpsertRoute on same-value update, got %d (was %d)", got, before)
	}
}
