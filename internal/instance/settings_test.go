package instance

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// setupTestDB applies the migration-006 shape inline. Keeping the schema
// duplicated here (instead of running the file via golang-migrate) keeps
// the test free of file-IO and matches the convention used by other
// handler-level tests in the repo (app_test.go inlines the schema too).
func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?cache=shared&_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`
		CREATE TABLE instance_settings (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			instance_url TEXT NOT NULL DEFAULT '',
			tls_mode TEXT NOT NULL DEFAULT 'self-signed' CHECK (tls_mode IN ('self-signed','letsencrypt')),
			default_memory_limit TEXT,
			default_cpu_limit TEXT,
			cleanup_enabled INTEGER NOT NULL DEFAULT 0,
			cleanup_schedule TEXT NOT NULL DEFAULT '0 3 * * *',
			cleanup_disk_threshold INTEGER NOT NULL DEFAULT 80,
			cleanup_image_retention INTEGER NOT NULL DEFAULT 5,
			max_concurrent_deploys INTEGER NOT NULL DEFAULT 3,
			maintenance_mode INTEGER NOT NULL DEFAULT 0,
			maintenance_message TEXT NOT NULL DEFAULT 'Prexel is currently under maintenance.',
			updated_at INTEGER NOT NULL DEFAULT (unixepoch())
		);
		INSERT INTO instance_settings (id) VALUES (1);
	`)
	if err != nil {
		t.Fatalf("schema: %v", err)
	}
	return db
}

func TestService_GetDefaults(t *testing.T) {
	db := setupTestDB(t)
	svc, err := NewService(context.Background(), db)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	got, err := svc.Get(context.Background())
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.TLSMode != TLSModeSelfSigned {
		t.Errorf("tls_mode default: want self-signed, got %s", got.TLSMode)
	}
	if got.MaxConcurrentDeploys != 3 {
		t.Errorf("max_concurrent_deploys default: want 3, got %d", got.MaxConcurrentDeploys)
	}
	if got.CleanupImageRetention != 5 {
		t.Errorf("retention default: want 5, got %d", got.CleanupImageRetention)
	}
}

func TestService_Update_LetsEncryptRequiresURL(t *testing.T) {
	db := setupTestDB(t)
	svc, _ := NewService(context.Background(), db)
	mode := TLSModeLetsEncrypt
	_, err := svc.Update(context.Background(), UpdateInput{TLSMode: &mode})
	if !errors.Is(err, ErrLetsEncryptNeedsURL) {
		t.Fatalf("want ErrLetsEncryptNeedsURL, got %v", err)
	}
}

func TestService_Update_LetsEncryptOKWithURL(t *testing.T) {
	db := setupTestDB(t)
	svc, _ := NewService(context.Background(), db)
	url := "prexel.example.com"
	mode := TLSModeLetsEncrypt
	out, err := svc.Update(context.Background(), UpdateInput{InstanceURL: &url, TLSMode: &mode})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if out.InstanceURL != "prexel.example.com" || out.TLSMode != TLSModeLetsEncrypt {
		t.Errorf("unexpected state: %+v", out)
	}
}

func TestService_Update_NormalisesInstanceURL(t *testing.T) {
	db := setupTestDB(t)
	svc, _ := NewService(context.Background(), db)
	url := "https://www.prexel.example.com/"
	out, err := svc.Update(context.Background(), UpdateInput{InstanceURL: &url})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if out.InstanceURL != "prexel.example.com" {
		t.Errorf("want normalised url, got %q", out.InstanceURL)
	}
}

func TestService_Update_ValidatesMemoryFormat(t *testing.T) {
	db := setupTestDB(t)
	svc, _ := NewService(context.Background(), db)
	bad := "garbage"
	_, err := svc.Update(context.Background(), UpdateInput{DefaultMemoryLimit: &bad})
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("want ErrInvalidInput, got %v", err)
	}
}

func TestService_Update_ValidatesConcurrencyMinimum(t *testing.T) {
	db := setupTestDB(t)
	svc, _ := NewService(context.Background(), db)
	zero := 0
	_, err := svc.Update(context.Background(), UpdateInput{MaxConcurrentDeploys: &zero})
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("want ErrInvalidInput on zero, got %v", err)
	}
}

func TestService_Cached_HotPath(t *testing.T) {
	db := setupTestDB(t)
	svc, _ := NewService(context.Background(), db)
	on := true
	if _, err := svc.Update(context.Background(), UpdateInput{MaintenanceMode: &on}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if !svc.Cached().MaintenanceMode {
		t.Error("cache did not refresh after Update")
	}
}

func TestScheduleMatches(t *testing.T) {
	// 03:00 UTC on a Thursday — verifies hour + minute + wildcards.
	// 2026-05-14 happens to be a Thursday (weekday=4).
	when := time.Date(2026, 5, 14, 3, 0, 0, 0, time.UTC)
	if when.Weekday() != time.Thursday {
		t.Fatalf("fixture date is not a Thursday; weekday=%d", when.Weekday())
	}
	cases := []struct {
		expr string
		want bool
	}{
		{"0 3 * * *", true},
		{"0 3 * * 4", true}, // Thursday = 4
		{"0 3 * * 0", false},
		{"5 3 * * *", false},
		{"* * * * *", true},
		{"*/15 * * * *", false}, // step not supported, declines
		{"bogus", false},
	}
	for _, c := range cases {
		if got := scheduleMatches(c.expr, when); got != c.want {
			t.Errorf("scheduleMatches(%q) = %v, want %v", c.expr, got, c.want)
		}
	}
}

// fakeZoneRegistry lets the instance-settings tests drive the gate without
// pulling in the full dnszone package.
type fakeZoneRegistry struct {
	registered map[string]bool
}

func (f *fakeZoneRegistry) ApexOf(fqdn string) (string, error) {
	// Same naive rule the production extractor uses for the cases we care
	// about: strip everything up to the last two labels. Good enough for
	// tests on `.com` / `.net`; multi-label TLDs are exercised in dnszone.
	if fqdn == "" {
		return "", errors.New("empty")
	}
	parts := strings.Split(fqdn, ".")
	if len(parts) < 2 {
		return "", errors.New("not an apex")
	}
	return strings.Join(parts[len(parts)-2:], "."), nil
}

func (f *fakeZoneRegistry) ApexRegistered(_ context.Context, apex string) (bool, error) {
	return f.registered[apex], nil
}

func TestService_Update_RejectsInstanceURLForUnregisteredZone(t *testing.T) {
	db := setupTestDB(t)
	svc, _ := NewService(context.Background(), db)
	svc.WithZoneRegistry(&fakeZoneRegistry{registered: map[string]bool{"other.com": true}})
	url := "panel.unregistered.com"
	_, err := svc.Update(context.Background(), UpdateInput{InstanceURL: &url})
	if !errors.Is(err, ErrZoneNotRegistered) {
		t.Fatalf("want ErrZoneNotRegistered, got %v", err)
	}
}

func TestService_Update_AcceptsInstanceURLForRegisteredZone(t *testing.T) {
	db := setupTestDB(t)
	svc, _ := NewService(context.Background(), db)
	svc.WithZoneRegistry(&fakeZoneRegistry{registered: map[string]bool{"example.com": true}})
	url := "panel.example.com"
	out, err := svc.Update(context.Background(), UpdateInput{InstanceURL: &url})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if out.InstanceURL != "panel.example.com" {
		t.Errorf("instance_url not persisted: %q", out.InstanceURL)
	}
}

func TestService_Update_EmptyInstanceURLSkipsGate(t *testing.T) {
	// Empty instance_url means "no domain, use IP". The gate must not fire.
	db := setupTestDB(t)
	svc, _ := NewService(context.Background(), db)
	svc.WithZoneRegistry(&fakeZoneRegistry{}) // nothing registered
	empty := ""
	_, err := svc.Update(context.Background(), UpdateInput{InstanceURL: &empty})
	if err != nil {
		t.Errorf("want nil err for empty url, got %v", err)
	}
}

func TestDefaultLimits_ImplementsApp(t *testing.T) {
	// Compile-time guarantee that *instance.Service satisfies the small
	// interface app/.go expects. Catches accidental method-set drift.
	var _ interface {
		DefaultLimits() (string, string)
	} = (*Service)(nil)
	var _ interface {
		MaxConcurrentDeploys() int
	} = (*Service)(nil)
}
