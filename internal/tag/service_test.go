package tag

import (
	"context"
	"database/sql"
	"sort"
	"testing"

	_ "modernc.org/sqlite"
)

// newTestSvc spins up an in-memory SQLite database with the minimum schema
// the tag service touches: an apps table (so the FK on app_tags is valid),
// the tags dictionary, and the app_tags join table with ON DELETE CASCADE
// so the cascade test below has something to observe.
func newTestSvc(t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?cache=shared&_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	stmts := []string{
		`CREATE TABLE apps (id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE)`,
		`CREATE TABLE tags (
			id         TEXT PRIMARY KEY,
			name       TEXT NOT NULL UNIQUE,
			color      TEXT,
			created_at INTEGER NOT NULL DEFAULT (unixepoch())
		)`,
		`CREATE TABLE app_tags (
			app_id TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
			tag_id TEXT NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
			PRIMARY KEY (app_id, tag_id)
		)`,
		`CREATE INDEX idx_app_tags_tag ON app_tags(tag_id)`,
		`INSERT INTO apps(id, name) VALUES ('app-1', 'app-1')`,
	}
	for _, q := range stmts {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("schema: %v", err)
		}
	}
	return NewService(db), db
}

func TestNormalizeName(t *testing.T) {
	// Each case captures one normalisation rule the service guarantees;
	// table-driven so a new rule lands as one row instead of a new fn.
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "prod", want: "prod"},
		{in: "  Prod  ", want: "prod"},                  // trim + lowercase
		{in: "QA Stage", want: "qa-stage"},              // whitespace → dash
		{in: "feature  branch", want: "feature-branch"}, // collapsed whitespace
		{in: "Hello!@#World", want: "helloworld"},       // strip junk
		{in: "v1.0", want: "v10"},                       // dots not allowed
		{in: "tag-name", want: "tag-name"},              // dashes kept
		{in: "TAG_NAME", want: "tagname"},               // underscore not allowed
		{in: "", wantErr: true},                         // empty
		{in: "   ", wantErr: true},                      // whitespace-only
		{in: "!!!", wantErr: true},                      // strips to empty
		{in: "-leading", wantErr: true},                 // must start with [a-z0-9]
		{in: "ok-" + repeat("a", 60), wantErr: true},    // length cap (40)
	}
	for _, c := range cases {
		got, err := normalizeName(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("normalizeName(%q): expected error, got %q", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("normalizeName(%q): unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("normalizeName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNormalizeColor(t *testing.T) {
	// Valid hex passes through untouched.
	ok := "#3b82f6"
	out, err := normalizeColor(&ok)
	if err != nil || out == nil || *out != "#3b82f6" {
		t.Fatalf("normalizeColor(valid): out=%v err=%v", out, err)
	}
	// Uppercase digits accepted — operator may paste from a design tool.
	upper := "#ABCDEF"
	if _, err := normalizeColor(&upper); err != nil {
		t.Errorf("normalizeColor(uppercase): %v", err)
	}
	// "red" is a CSS keyword, not a hex code — must reject.
	bad := "red"
	if _, err := normalizeColor(&bad); err == nil {
		t.Error("normalizeColor(red): expected error")
	}
	// Empty/nil → no color (caller stores NULL).
	out, err = normalizeColor(nil)
	if err != nil || out != nil {
		t.Errorf("normalizeColor(nil) = %v, %v", out, err)
	}
	empty := "   "
	out, err = normalizeColor(&empty)
	if err != nil || out != nil {
		t.Errorf("normalizeColor(\"   \") = %v, %v", out, err)
	}
}

func TestCreate_UpsertByName(t *testing.T) {
	svc, _ := newTestSvc(t)
	ctx := context.Background()

	first, err := svc.Create(ctx, "Prod", nil)
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	if first.Name != "prod" {
		t.Errorf("normalised name not applied: %q", first.Name)
	}

	// Re-creating with a different casing should hit the upsert path and
	// return the existing row (same id), not a fresh one.
	second, err := svc.Create(ctx, "  PROD  ", nil)
	if err != nil {
		t.Fatalf("second create: %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("upsert returned new id: first=%s second=%s", first.ID, second.ID)
	}
}

func TestCreate_PreservesColorOnFirstWriter(t *testing.T) {
	svc, _ := newTestSvc(t)
	ctx := context.Background()
	c1 := "#111111"
	first, err := svc.Create(ctx, "qa", &c1)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	c2 := "#222222"
	second, err := svc.Create(ctx, "qa", &c2)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	// First-writer-wins: the color from the upsert is ignored.
	if second.Color == nil || *second.Color != "#111111" {
		t.Errorf("color mutated on upsert: %+v (first=%+v)", second.Color, first.Color)
	}
}

func TestSetAppTags_AddsRemovesCreates(t *testing.T) {
	svc, db := newTestSvc(t)
	ctx := context.Background()

	// Seed one pre-existing tag so SetAppTags exercises both branches
	// (existing-by-name + create-on-the-fly).
	if _, err := svc.Create(ctx, "prod", nil); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// First pass: attach "prod" + "api". Expect both rows in app_tags.
	out, err := svc.SetAppTags(ctx, "app-1", []string{"prod", "API"})
	if err != nil {
		t.Fatalf("first set: %v", err)
	}
	if names := namesOf(out); !equalSet(names, []string{"api", "prod"}) {
		t.Errorf("first set: got %v, want [api prod]", names)
	}

	// Second pass: drop "api", add "staging". Expect detach + attach.
	out, err = svc.SetAppTags(ctx, "app-1", []string{"prod", "staging"})
	if err != nil {
		t.Fatalf("second set: %v", err)
	}
	if names := namesOf(out); !equalSet(names, []string{"prod", "staging"}) {
		t.Errorf("second set: got %v, want [prod staging]", names)
	}

	// "api" tag still exists in the dictionary (only the link was
	// detached) — the unused-tag GC is a future feature.
	var apiCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM tags WHERE name = 'api'`).Scan(&apiCount); err != nil {
		t.Fatalf("count api: %v", err)
	}
	if apiCount != 1 {
		t.Errorf("api tag should still be in dictionary, count=%d", apiCount)
	}

	// Direct DB check: the app currently has 2 attachments.
	var linkCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM app_tags WHERE app_id = 'app-1'`).Scan(&linkCount); err != nil {
		t.Fatalf("count links: %v", err)
	}
	if linkCount != 2 {
		t.Errorf("expected 2 attachments, got %d", linkCount)
	}
}

func TestSetAppTags_EmptyClearsAll(t *testing.T) {
	svc, _ := newTestSvc(t)
	ctx := context.Background()
	if _, err := svc.SetAppTags(ctx, "app-1", []string{"prod", "api"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	out, err := svc.SetAppTags(ctx, "app-1", nil)
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("expected empty after clear, got %v", namesOf(out))
	}
}

func TestSetAppTags_RejectsInvalidName(t *testing.T) {
	svc, _ := newTestSvc(t)
	if _, err := svc.SetAppTags(context.Background(), "app-1", []string{"!!!"}); err == nil {
		t.Error("expected error for invalid name")
	}
}

func TestDelete_CascadesToAppTags(t *testing.T) {
	svc, db := newTestSvc(t)
	ctx := context.Background()

	tag, err := svc.Create(ctx, "prod", nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.Attach(ctx, "app-1", tag.ID); err != nil {
		t.Fatalf("attach: %v", err)
	}

	var before int
	if err := db.QueryRow(`SELECT COUNT(*) FROM app_tags WHERE tag_id = ?`, tag.ID).Scan(&before); err != nil {
		t.Fatalf("count before: %v", err)
	}
	if before != 1 {
		t.Fatalf("expected 1 link before delete, got %d", before)
	}

	if err := svc.Delete(ctx, tag.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// FK ON DELETE CASCADE should have cleared the join rows.
	var after int
	if err := db.QueryRow(`SELECT COUNT(*) FROM app_tags WHERE tag_id = ?`, tag.ID).Scan(&after); err != nil {
		t.Fatalf("count after: %v", err)
	}
	if after != 0 {
		t.Errorf("cascade did not fire: %d join rows remain", after)
	}

	// And the dictionary row itself is gone.
	if _, err := svc.Get(ctx, tag.ID); err == nil {
		t.Error("expected ErrNotFound for deleted tag")
	}
}

func TestForApp_ReturnsNames(t *testing.T) {
	svc, _ := newTestSvc(t)
	ctx := context.Background()
	if _, err := svc.SetAppTags(ctx, "app-1", []string{"prod", "api"}); err != nil {
		t.Fatalf("set: %v", err)
	}
	names, err := svc.ForApp(ctx, "app-1")
	if err != nil {
		t.Fatalf("ForApp: %v", err)
	}
	if !equalSet(names, []string{"api", "prod"}) {
		t.Errorf("ForApp = %v, want [api prod]", names)
	}
}

func TestAttach_Idempotent(t *testing.T) {
	svc, db := newTestSvc(t)
	ctx := context.Background()
	tag, _ := svc.Create(ctx, "prod", nil)
	for i := 0; i < 3; i++ {
		if err := svc.Attach(ctx, "app-1", tag.ID); err != nil {
			t.Fatalf("attach iter %d: %v", i, err)
		}
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM app_tags WHERE app_id = 'app-1'`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 link after triple-attach, got %d", n)
	}
}

// equalSet compares two slices as sets — SetAppTags returns rows in
// name-sorted order today but the test isn't pinning to that contract.
func equalSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x := append([]string(nil), a...)
	y := append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

func namesOf(ts []Tag) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Name
	}
	return out
}

func repeat(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}
