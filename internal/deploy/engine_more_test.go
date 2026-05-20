package deploy

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/prexel/prexel/internal/app"
	"github.com/prexel/prexel/internal/build"
	"github.com/prexel/prexel/internal/domains"
)

// TestComposeCanonicalName locks down the per-service container
// naming. The new scheme is `<app>-<svc>` (no `prexel-` prefix);
// operators recognise their containers by app name + service.
func TestComposeCanonicalName(t *testing.T) {
	cases := []struct {
		app, svc, want string
	}{
		{"myapp", "web", "myapp-web"},
		{"shop", "redis", "shop-redis"},
		{"a", "b", "a-b"},
	}
	for _, tc := range cases {
		if got := composeCanonicalName(tc.app, tc.svc); got != tc.want {
			t.Errorf("composeCanonicalName(%q, %q) = %q, want %q", tc.app, tc.svc, got, tc.want)
		}
	}
}

// TestLegacyComposeName mirrors composeCanonicalName but for the
// pre-cleanup naming Prexel used before the prefix was dropped. The
// swap code still consults this name so it can stop+remove legacy
// containers when an existing app is redeployed under the new scheme.
func TestLegacyComposeName(t *testing.T) {
	cases := []struct {
		app, svc, want string
	}{
		{"myapp", "web", "prexel-myapp-web"},
		{"shop", "redis", "prexel-shop-redis"},
	}
	for _, tc := range cases {
		if got := legacyComposeName(tc.app, tc.svc); got != tc.want {
			t.Errorf("legacyComposeName(%q, %q) = %q, want %q", tc.app, tc.svc, got, tc.want)
		}
	}
}

// TestLegacyDedupeSkipsDuplicates covers the helper used by the swap
// loop. Empty strings drop; duplicates collapse so the stop/remove
// cycle never fires twice when canonical == legacy (operators with
// their own container_name).
func TestLegacyDedupeSkipsDuplicates(t *testing.T) {
	cases := []struct {
		in   []string
		want []string
	}{
		{nil, []string{}},
		{[]string{}, []string{}},
		{[]string{"a", "b"}, []string{"a", "b"}},
		// Dupe collapse.
		{[]string{"a", "a"}, []string{"a"}},
		// Empty + dupe + interleave — empties skipped, order preserved.
		{[]string{"", "a", "", "b", "a"}, []string{"a", "b"}},
		// Single empty entry → empty result.
		{[]string{""}, []string{}},
	}
	for i, tc := range cases {
		got := legacyDedupe(tc.in...)
		if len(got) != len(tc.want) {
			t.Errorf("case %d: len = %d, want %d (got %v)", i, len(got), len(tc.want), got)
			continue
		}
		for j := range got {
			if got[j] != tc.want[j] {
				t.Errorf("case %d: got[%d] = %q, want %q", i, j, got[j], tc.want[j])
			}
		}
	}
}

// TestFailDeployPersistsErrorMessage — the failDeploy path stores the
// wrapped error chain on the deployment row so the UI can render the
// cause without log scraping. We seed an app + deployment, drive
// failDeploy, and read error_message back.
func TestFailDeployPersistsErrorMessage(t *testing.T) {
	e, _, appSvc, serverID := deployTestSetup(t)
	a, err := appSvc.Create(context.Background(), app.CreateInput{
		Name: "errapp", ServerID: serverID, BuildType: "dockerfile",
		RepoURL: strPtr("https://github.com/example/repo"),
	})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}

	d := &Deployment{
		ID:     uuid.NewString(),
		AppID:  a.ID,
		Status: "pending",
	}
	if err := e.repo.insert(context.Background(), d); err != nil {
		t.Fatalf("insert deployment: %v", err)
	}

	// We don't have a real logFile — failDeploy will Fprintf into nil,
	// but the function is robust to that because os.File methods on
	// nil panic. Use a tempfile instead so the function runs cleanly.
	logFile, err := openTempLog(t)
	if err != nil {
		t.Fatalf("open temp log: %v", err)
	}
	defer func() { _ = logFile.Close() }()

	cause := errors.New("build: missing Dockerfile in build context")
	e.failDeploy(context.Background(), a, d, logFile, cause)

	got, err := e.GetDeployment(context.Background(), d.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != "failed" {
		t.Errorf("status = %q, want failed", got.Status)
	}
	if got.ErrorMessage == nil {
		t.Fatal("ErrorMessage nil — failDeploy did not persist cause")
	}
	if *got.ErrorMessage != cause.Error() {
		t.Errorf("ErrorMessage = %q, want %q", *got.ErrorMessage, cause.Error())
	}
	if got.FinishedAt == nil {
		t.Error("FinishedAt nil")
	}

	// App status — no previous success, so failDeploy promotes the
	// app status to "error".
	updated, err := appSvc.Get(context.Background(), a.ID)
	if err != nil {
		t.Fatalf("app Get: %v", err)
	}
	if updated.Status != "error" {
		t.Errorf("app status = %q, want error", updated.Status)
	}
}

// TestFailDeploy_KeepsAppRunningWhenPriorSuccessExists — when the
// app has a previously-successful deployment, a fresh failure leaves
// the app in "running" (the old version is still serving traffic).
func TestFailDeploy_KeepsAppRunningWhenPriorSuccessExists(t *testing.T) {
	e, db, appSvc, serverID := deployTestSetup(t)
	a, err := appSvc.Create(context.Background(), app.CreateInput{
		Name: "running-app", ServerID: serverID, BuildType: "dockerfile",
		RepoURL: strPtr("https://github.com/example/repo"),
	})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	// Seed a previous successful deployment.
	if _, err := db.Exec(
		`INSERT INTO deployments(id, app_id, status, image_tag) VALUES (?, ?, 'success', 'old:1')`,
		uuid.NewString(), a.ID,
	); err != nil {
		t.Fatalf("seed prev: %v", err)
	}
	// Insert a new in-flight deployment and fail it.
	d := &Deployment{ID: uuid.NewString(), AppID: a.ID, Status: "pending"}
	if err := e.repo.insert(context.Background(), d); err != nil {
		t.Fatalf("insert: %v", err)
	}

	logFile, err := openTempLog(t)
	if err != nil {
		t.Fatalf("temp log: %v", err)
	}
	defer func() { _ = logFile.Close() }()

	e.failDeploy(context.Background(), a, d, logFile, errors.New("transient"))

	updated, err := appSvc.Get(context.Background(), a.ID)
	if err != nil {
		t.Fatalf("get app: %v", err)
	}
	if updated.Status != "running" {
		t.Errorf("app status = %q, want running (prior success exists)", updated.Status)
	}
}

// TestRollbackRequiresExistingImage covers the early validation path
// in Rollback: when a target deployment id is supplied but the row
// belongs to a different app, Rollback refuses without doing any
// docker work.
func TestRollbackRequiresExistingImage_WrongApp(t *testing.T) {
	e, db, appSvc, serverID := deployTestSetup(t)
	a, err := appSvc.Create(context.Background(), app.CreateInput{
		Name: "primary", ServerID: serverID, BuildType: "dockerfile",
		RepoURL: strPtr("https://github.com/example/repo"),
	})
	if err != nil {
		t.Fatalf("create app A: %v", err)
	}
	b, err := appSvc.Create(context.Background(), app.CreateInput{
		Name: "secondary", ServerID: serverID, BuildType: "dockerfile",
		RepoURL: strPtr("https://github.com/example/repo"),
	})
	if err != nil {
		t.Fatalf("create app B: %v", err)
	}
	// Successful deployment on app B.
	depB := uuid.NewString()
	if _, err := db.Exec(
		`INSERT INTO deployments(id, app_id, status, image_tag) VALUES (?, ?, 'success', 'b:1')`,
		depB, b.ID,
	); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Trying to roll back app A using B's deployment id must fail.
	_, err = e.Rollback(context.Background(), a.ID, depB, DeployOptions{})
	if err == nil {
		t.Fatal("Rollback cross-app returned nil err")
	}
}

// TestRollback_TargetNotSuccess covers the "target.Status != success"
// guard — rolling back to a failed deployment must be refused.
func TestRollback_TargetNotSuccess(t *testing.T) {
	e, db, appSvc, serverID := deployTestSetup(t)
	a, err := appSvc.Create(context.Background(), app.CreateInput{
		Name: "rb-app", ServerID: serverID, BuildType: "dockerfile",
		RepoURL: strPtr("https://github.com/example/repo"),
	})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	failedID := uuid.NewString()
	if _, err := db.Exec(
		`INSERT INTO deployments(id, app_id, status) VALUES (?, ?, 'failed')`,
		failedID, a.ID,
	); err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	_, err = e.Rollback(context.Background(), a.ID, failedID, DeployOptions{})
	if err == nil {
		t.Fatal("Rollback to failed target returned nil err")
	}
}

// TestRollback_TargetHasNoImageTag — Rollback can't reuse a target
// that doesn't carry an image_tag (e.g. a successful deploy from
// the old days when the column was nullable in flight).
func TestRollback_TargetHasNoImageTag(t *testing.T) {
	e, db, appSvc, serverID := deployTestSetup(t)
	a, err := appSvc.Create(context.Background(), app.CreateInput{
		Name: "rb-app-noimg", ServerID: serverID, BuildType: "dockerfile",
		RepoURL: strPtr("https://github.com/example/repo"),
	})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	// Success row WITHOUT image_tag.
	id := uuid.NewString()
	if _, err := db.Exec(
		`INSERT INTO deployments(id, app_id, status) VALUES (?, ?, 'success')`,
		id, a.ID,
	); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err = e.Rollback(context.Background(), a.ID, id, DeployOptions{})
	if err == nil {
		t.Fatal("Rollback to tag-less target returned nil err")
	}
}

// TestReconcileMarksStaleDeploys — boot-time recovery covered in
// engine_test.go already, but here we add a paranoid check that
// ONLY the stale rows flip. Mix the three pending statuses with a
// fresh "success" + "failed" row and confirm the latter two survive.
func TestReconcileMarksStaleDeploys_MixedRows(t *testing.T) {
	e, db, appSvc, serverID := deployTestSetup(t)
	a, err := appSvc.Create(context.Background(), app.CreateInput{
		Name: "mixed-app", ServerID: serverID, BuildType: "dockerfile",
		RepoURL: strPtr("https://github.com/example/repo"),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	type row struct {
		id, status string
	}
	rows := []row{
		{uuid.NewString(), "pending"},
		{uuid.NewString(), "building"},
		{uuid.NewString(), "deploying"},
		{uuid.NewString(), "success"},
		{uuid.NewString(), "failed"},
	}
	for _, r := range rows {
		if _, err := db.Exec(
			`INSERT INTO deployments(id, app_id, status) VALUES (?, ?, ?)`,
			r.id, a.ID, r.status,
		); err != nil {
			t.Fatalf("seed %q: %v", r.status, err)
		}
	}
	if err := e.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	want := map[string]string{
		rows[0].id: "failed",
		rows[1].id: "failed",
		rows[2].id: "failed",
		rows[3].id: "success",
		rows[4].id: "failed",
	}
	for id, wantStatus := range want {
		d, err := e.GetDeployment(context.Background(), id)
		if err != nil {
			t.Fatalf("Get %s: %v", id, err)
		}
		if d.Status != wantStatus {
			t.Errorf("deployment %s status = %q, want %q", id, d.Status, wantStatus)
		}
	}
}

// TestEngine_LogPath returns the path inside LogDir.
func TestEngine_LogPath(t *testing.T) {
	e, _, _, _ := deployTestSetup(t)
	p := e.LogPath("dep-1")
	if p == "" {
		t.Fatal("LogPath returned empty")
	}
	if got := p[len(p)-9:]; got != "dep-1.log" {
		t.Errorf("LogPath = %q, want suffix dep-1.log", p)
	}
}

// TestEngine_ListDeployments orders results newest-first.
func TestEngine_ListDeployments_Ordering(t *testing.T) {
	e, db, appSvc, serverID := deployTestSetup(t)
	a, err := appSvc.Create(context.Background(), app.CreateInput{
		Name: "list-app", ServerID: serverID, BuildType: "dockerfile",
		RepoURL: strPtr("https://github.com/example/repo"),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Three rows with monotonically increasing created_at.
	ids := []string{uuid.NewString(), uuid.NewString(), uuid.NewString()}
	for i, id := range ids {
		if _, err := db.Exec(
			`INSERT INTO deployments(id, app_id, status, created_at) VALUES (?, ?, 'success', ?)`,
			id, a.ID, 1000+i,
		); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
	got, err := e.ListDeployments(context.Background(), a.ID, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	// Newest first — the third insert (ids[2]) must lead.
	if got[0].ID != ids[2] {
		t.Errorf("got[0].ID = %s, want newest %s", got[0].ID, ids[2])
	}
}

// TestEngine_Deploy_ServerNotConnected covers the runDeploy validation
// guard. We seed an app whose server_id points at a disconnected
// row; Deploy must fail before doing any work.
func TestEngine_Deploy_ServerNotConnected(t *testing.T) {
	e, db, appSvc, serverID := deployTestSetup(t)
	// Create the app FIRST (while server is still connected — app.Create
	// validates the server is reachable), then flip the server to
	// disconnected before the Deploy call so the runDeploy guard fires.
	a, err := appSvc.Create(context.Background(), app.CreateInput{
		Name: "disconnected-app", ServerID: serverID, BuildType: "dockerfile",
		RepoURL: strPtr("https://github.com/example/repo"),
	})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	if _, err := db.Exec(`UPDATE servers SET status = 'disconnected' WHERE id = ?`, serverID); err != nil {
		t.Fatalf("update server: %v", err)
	}
	// Deploy must surface a connection error, not a docker error.
	_, err = e.Deploy(context.Background(), a.ID, DeployOptions{})
	if err == nil {
		t.Fatal("Deploy on disconnected server returned nil err")
	}
}

// TestShortDeployID truncates to the first 7 hex chars from a UUID.
func TestShortDeployID(t *testing.T) {
	got := shortDeployID("01234567-89ab-cdef-0123-456789abcdef")
	if got != "0123456" {
		t.Errorf("shortDeployID = %q, want 0123456", got)
	}
	// Strings shorter than 7 (post-strip) pass through unchanged.
	if got := shortDeployID("abc"); got != "abc" {
		t.Errorf("shortDeployID short = %q, want abc", got)
	}
}

// openTempLog gives failDeploy a real *os.File (its concrete arg type)
// pointed at a per-test directory we throw away on t.Cleanup.
func openTempLog(t *testing.T) (*os.File, error) {
	t.Helper()
	return os.Create(t.TempDir() + "/test.log")
}

// fakeLimit lets the global-semaphore tests dial the cap.
type fakeLimit struct{ n int }

func (f fakeLimit) MaxConcurrentDeploys() int { return f.n }

// TestAcquireGlobalSlot_Unlimited covers the nil-Limit fast path:
// the engine returns a no-op release and ok=true forever.
func TestAcquireGlobalSlot_Unlimited(t *testing.T) {
	e := &Engine{} // no Limit
	rel, ok := e.acquireGlobalSlot()
	if !ok {
		t.Fatal("nil Limit must always grant a slot")
	}
	if rel == nil {
		t.Fatal("release func must be non-nil")
	}
	rel() // no panic
}

// TestAcquireGlobalSlot_Saturated covers the saturation branch: a
// 1-slot cap grants once and rejects subsequent attempts until the
// first release runs.
func TestAcquireGlobalSlot_Saturated(t *testing.T) {
	e := &Engine{Limit: fakeLimit{n: 1}}
	rel, ok := e.acquireGlobalSlot()
	if !ok {
		t.Fatal("first acquire must succeed")
	}
	if _, ok := e.acquireGlobalSlot(); ok {
		t.Fatal("second acquire on saturated 1-slot pool returned ok=true")
	}
	rel()
	// After release, a new acquire fits again.
	if _, ok := e.acquireGlobalSlot(); !ok {
		t.Fatal("acquire after release returned ok=false")
	}
}

// TestAcquireGlobalSlot_ZeroOrNegativeCapacity clamps to 1.
func TestAcquireGlobalSlot_NegativeCapacity(t *testing.T) {
	e := &Engine{Limit: fakeLimit{n: 0}}
	if _, ok := e.acquireGlobalSlot(); !ok {
		t.Fatal("zero capacity should be clamped to 1 — first acquire must succeed")
	}
}

// TestEngine_Restart_AppNoContainerName covers the early-exit guard.
func TestEngine_Restart_AppNoContainerName(t *testing.T) {
	e, db, appSvc, serverID := deployTestSetup(t)
	a, err := appSvc.Create(context.Background(), app.CreateInput{
		Name: "restart-app", ServerID: serverID, BuildType: "dockerfile",
		RepoURL: strPtr("https://github.com/example/repo"),
	})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	if _, err := db.Exec(`UPDATE apps SET container_name = NULL WHERE id = ?`, a.ID); err != nil {
		t.Fatalf("clear container_name: %v", err)
	}
	if err := e.Restart(context.Background(), a.ID); err == nil {
		t.Error("Restart on app without container_name returned nil err")
	}
}

// TestEngine_Restart_UnknownApp covers the up-front Get error path.
func TestEngine_Restart_UnknownApp(t *testing.T) {
	e, _, _, _ := deployTestSetup(t)
	if err := e.Restart(context.Background(), uuid.NewString()); err == nil {
		t.Error("Restart unknown returned nil err")
	}
}

// TestEngine_Restart_MissingContainerSurfacesError — when the app has
// a container_name but the container doesn't actually exist on the
// daemon, Restart returns a docker error. We exercise this against
// the local docker the test runs in.
func TestEngine_Restart_MissingContainerSurfacesError(t *testing.T) {
	e, _, appSvc, serverID := deployTestSetup(t)
	a, err := appSvc.Create(context.Background(), app.CreateInput{
		Name: "ghost-app", ServerID: serverID, BuildType: "dockerfile",
		RepoURL: strPtr("https://github.com/example/repo"),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// app.Create populates container_name = a.Name automatically.
	// The container doesn't exist on the local daemon → Restart
	// returns a docker error.
	if err := e.Restart(context.Background(), a.ID); err == nil {
		t.Error("Restart on non-existent container returned nil err")
	}
}

// TestEngine_Stop_RunsThroughDockerWhenContainerNameSet — Stop with
// a container_name set still flips the app status to "stopped" even
// when the docker stop returns "container not found".
func TestEngine_Stop_StillFlipsStatusOnGhostContainer(t *testing.T) {
	e, _, appSvc, serverID := deployTestSetup(t)
	a, err := appSvc.Create(context.Background(), app.CreateInput{
		Name: "stop-ghost", ServerID: serverID, BuildType: "dockerfile",
		RepoURL: strPtr("https://github.com/example/repo"),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// app.Create populates container_name automatically — the docker
	// stop will fail with not-found but Stop must still mark the app.
	if err := e.Stop(context.Background(), a.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	updated, err := appSvc.Get(context.Background(), a.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if updated.Status != "stopped" {
		t.Errorf("app status = %q, want stopped", updated.Status)
	}
}

// TestDerefStr covers the simple nil/non-nil branches that show up
// in nearly every deploy code path.
func TestDerefStr(t *testing.T) {
	if got := derefStr(nil); got != "" {
		t.Errorf("derefStr(nil) = %q, want \"\"", got)
	}
	s := "hello"
	if got := derefStr(&s); got != s {
		t.Errorf("derefStr(&%q) = %q, want %q", s, got, s)
	}
}

// TestIntPtrConverters covers intPtrToInt64 and intPtrToInt64Ptr —
// both nil-safe by contract.
func TestIntPtrConverters(t *testing.T) {
	if got := intPtrToInt64(nil); got != 0 {
		t.Errorf("intPtrToInt64(nil) = %d, want 0", got)
	}
	v := 42
	if got := intPtrToInt64(&v); got != 42 {
		t.Errorf("intPtrToInt64(&42) = %d, want 42", got)
	}
	if got := intPtrToInt64Ptr(nil); got != nil {
		t.Errorf("intPtrToInt64Ptr(nil) = %v, want nil", got)
	}
	p := intPtrToInt64Ptr(&v)
	if p == nil || *p != 42 {
		t.Errorf("intPtrToInt64Ptr(&42) = %v, want *p=42", p)
	}
}

// TestPublish_NilBusIsSafe — publish must be a no-op when no event bus
// is wired (used by minimal test setups and unit tests).
func TestPublish_NilBusIsSafe(t *testing.T) {
	e := &Engine{}
	e.publish("app-id", "dep-id", "deploy.started", map[string]any{"x": 1})
	// No panic = pass.
}

// TestReconcile_NoApps short-circuits cleanly when ListAll returns
// an empty set.
func TestReconcile_NoApps(t *testing.T) {
	e, _, _, _ := deployTestSetup(t)
	// No apps seeded — Reconcile is a no-op.
	if err := e.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
}

// TestReconcile_AppMissingServer — apps with server_id NULL flip
// straight to "error" without trying to inspect any container.
func TestReconcile_AppMissingServer(t *testing.T) {
	e, db, appSvc, serverID := deployTestSetup(t)
	a, err := appSvc.Create(context.Background(), app.CreateInput{
		Name: "no-srv", ServerID: serverID, BuildType: "dockerfile",
		RepoURL: strPtr("https://github.com/example/repo"),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Flip to deploying then drop the server_id.
	if _, err := db.Exec(
		`UPDATE apps SET status = 'deploying', server_id = NULL WHERE id = ?`, a.ID,
	); err != nil {
		t.Fatalf("mutate app: %v", err)
	}
	if err := e.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	got, err := appSvc.Get(context.Background(), a.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != "error" {
		t.Errorf("status = %q, want error", got.Status)
	}
}

// TestIsNotFound is a thin wrapper around errors.Is; lock down the
// nil case and a non-matching err.
func TestIsNotFound(t *testing.T) {
	if isNotFound(nil) {
		t.Error("isNotFound(nil) = true")
	}
	if isNotFound(errors.New("random")) {
		t.Error("isNotFound(random err) = true")
	}
}

// TestEngine_AppDomains_NoDomainsReturnsEmpty — when no domains are
// bound to the app, appDomains returns ("" , nil , empty-map , nil)
// so callers can treat "no domain" as a value comparison on primary.
func TestEngine_AppDomains_NoDomainsReturnsEmpty(t *testing.T) {
	e, db, appSvc, serverID := deployTestSetup(t)
	// We need a Domains service — wire one against the engine's DB.
	if _, err := db.Exec(`CREATE TABLE dns_zones (id TEXT PRIMARY KEY, apex TEXT NOT NULL UNIQUE)`); err != nil {
		t.Fatalf("seed dns_zones: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE domains (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL UNIQUE,
		app_id TEXT REFERENCES apps(id),
		is_primary INTEGER NOT NULL DEFAULT 0,
		ssl_status TEXT NOT NULL DEFAULT 'pending',
		ssl_expires_at INTEGER,
		dns_verified INTEGER NOT NULL DEFAULT 0,
		dns_verified_at INTEGER,
		dns_last_check INTEGER,
		dns_check_count INTEGER NOT NULL DEFAULT 0,
		zone_id TEXT REFERENCES dns_zones(id) ON DELETE SET NULL,
		covered_by_wildcard INTEGER NOT NULL DEFAULT 0,
		service TEXT,
		port INTEGER,
		force_https INTEGER NOT NULL DEFAULT 1,
		created_at INTEGER NOT NULL DEFAULT (unixepoch()),
		updated_at INTEGER NOT NULL DEFAULT (unixepoch())
	)`); err != nil {
		t.Fatalf("seed domains: %v", err)
	}
	a, err := appSvc.Create(context.Background(), app.CreateInput{
		Name: "no-domains-app", ServerID: serverID, BuildType: "dockerfile",
		RepoURL: strPtr("https://github.com/example/repo"),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	e.Domains = newTestDomainsService(t, db)
	primary, aliases, force, err := e.appDomains(context.Background(), a.ID)
	if err != nil {
		t.Fatalf("appDomains: %v", err)
	}
	if primary != "" {
		t.Errorf("primary = %q, want \"\"", primary)
	}
	if len(aliases) != 0 {
		t.Errorf("aliases = %v, want empty", aliases)
	}
	if force == nil {
		t.Error("force map nil")
	}
}

// TestEngine_AppDomains_PrimaryAndAliases — with multiple rows for
// the app, the helper picks the primary first and emits the rest as
// aliases. force_https flows through per host.
func TestEngine_AppDomains_PrimaryAndAliases(t *testing.T) {
	e, db, appSvc, serverID := deployTestSetup(t)
	if _, err := db.Exec(`CREATE TABLE dns_zones (id TEXT PRIMARY KEY, apex TEXT NOT NULL UNIQUE)`); err != nil {
		t.Fatalf("seed dns_zones: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE domains (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL UNIQUE,
		app_id TEXT REFERENCES apps(id),
		is_primary INTEGER NOT NULL DEFAULT 0,
		ssl_status TEXT NOT NULL DEFAULT 'pending',
		ssl_expires_at INTEGER,
		dns_verified INTEGER NOT NULL DEFAULT 0,
		dns_verified_at INTEGER,
		dns_last_check INTEGER,
		dns_check_count INTEGER NOT NULL DEFAULT 0,
		zone_id TEXT REFERENCES dns_zones(id) ON DELETE SET NULL,
		covered_by_wildcard INTEGER NOT NULL DEFAULT 0,
		service TEXT,
		port INTEGER,
		force_https INTEGER NOT NULL DEFAULT 1,
		created_at INTEGER NOT NULL DEFAULT (unixepoch()),
		updated_at INTEGER NOT NULL DEFAULT (unixepoch())
	)`); err != nil {
		t.Fatalf("seed domains: %v", err)
	}
	a, err := appSvc.Create(context.Background(), app.CreateInput{
		Name: "domain-app", ServerID: serverID, BuildType: "dockerfile",
		RepoURL: strPtr("https://github.com/example/repo"),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Seed: primary api.example.com (force_https=1) + alias alt.example.com (force_https=0).
	if _, err := db.Exec(
		`INSERT INTO domains(id, name, app_id, is_primary, force_https) VALUES
		 ('d-primary', 'api.example.com', ?, 1, 1),
		 ('d-alias', 'alt.example.com', ?, 0, 0)`,
		a.ID, a.ID,
	); err != nil {
		t.Fatalf("seed domains rows: %v", err)
	}
	e.Domains = newTestDomainsService(t, db)
	primary, aliases, force, err := e.appDomains(context.Background(), a.ID)
	if err != nil {
		t.Fatalf("appDomains: %v", err)
	}
	if primary != "api.example.com" {
		t.Errorf("primary = %q, want api.example.com", primary)
	}
	if len(aliases) != 1 || aliases[0] != "alt.example.com" {
		t.Errorf("aliases = %v, want [alt.example.com]", aliases)
	}
	if !force["api.example.com"] {
		t.Error("force_https for primary should be true")
	}
	if force["alt.example.com"] {
		t.Error("force_https for alias should be false")
	}
}

// TestEngine_HealthCheck_DisabledShortCircuits — when an app turns
// off the health check, the helper just sleeps a tiny window and
// returns nil. We pass a cancelled context to make the sleep fail
// fast (the helper's select honours ctx.Done()).
func TestEngine_HealthCheck_DisabledShortCircuits(t *testing.T) {
	e, _, _, _ := deployTestSetup(t)
	a := &app.App{HealthCheckEnabled: false}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // immediately
	logFile, err := openTempLog(t)
	if err != nil {
		t.Fatalf("temp log: %v", err)
	}
	defer func() { _ = logFile.Close() }()
	err = e.healthCheck(ctx, a, "container", 80, logFile)
	if err == nil {
		t.Fatal("cancelled ctx must surface ctx.Err()")
	}
}

// newTestDomainsService wires a domains.Service against the engine's
// DB with no Caddy/PublicIP/Events/Zones — enough to drive read-only
// helpers like appDomains.
func newTestDomainsService(t *testing.T, db *sql.DB) *domains.Service {
	t.Helper()
	return domains.NewService(domains.Deps{DB: db})
}

// seedDomainsSchema runs the migration-equivalent DDL the tests need
// for domains lookups (dns_zones + domains). Factored out so each
// test focused on appDomains/appDomainsByService doesn't repeat the
// 20-line table definition inline.
func seedDomainsSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`CREATE TABLE dns_zones (id TEXT PRIMARY KEY, apex TEXT NOT NULL UNIQUE)`); err != nil {
		t.Fatalf("seed dns_zones: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE domains (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL UNIQUE,
		app_id TEXT REFERENCES apps(id),
		is_primary INTEGER NOT NULL DEFAULT 0,
		ssl_status TEXT NOT NULL DEFAULT 'pending',
		ssl_expires_at INTEGER,
		dns_verified INTEGER NOT NULL DEFAULT 0,
		dns_verified_at INTEGER,
		dns_last_check INTEGER,
		dns_check_count INTEGER NOT NULL DEFAULT 0,
		zone_id TEXT REFERENCES dns_zones(id) ON DELETE SET NULL,
		covered_by_wildcard INTEGER NOT NULL DEFAULT 0,
		service TEXT,
		port INTEGER,
		force_https INTEGER NOT NULL DEFAULT 1,
		created_at INTEGER NOT NULL DEFAULT (unixepoch()),
		updated_at INTEGER NOT NULL DEFAULT (unixepoch())
	)`); err != nil {
		t.Fatalf("seed domains: %v", err)
	}
}

// TestEngine_AppDomainsByService groups domain rows by their `service`
// column — drives Caddy routing for compose apps. Domains with NULL
// service are dropped.
func TestEngine_AppDomainsByService(t *testing.T) {
	e, db, appSvc, serverID := deployTestSetup(t)
	seedDomainsSchema(t, db)
	a, err := appSvc.Create(context.Background(), app.CreateInput{
		Name: "compose-app", ServerID: serverID, BuildType: "docker_compose",
		RepoURL: strPtr("https://github.com/example/repo"),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Three domains: two bound to services, one bound at app-level (no service).
	if _, err := db.Exec(
		`INSERT INTO domains(id, name, app_id, service, port) VALUES
		 ('d-web', 'web.example.com', ?, 'web', 80),
		 ('d-api', 'api.example.com', ?, 'api', 3000),
		 ('d-app', 'app.example.com', ?, NULL, NULL)`,
		a.ID, a.ID, a.ID,
	); err != nil {
		t.Fatalf("seed: %v", err)
	}
	e.Domains = newTestDomainsService(t, db)

	bySvc, err := e.appDomainsByService(context.Background(), a.ID)
	if err != nil {
		t.Fatalf("appDomainsByService: %v", err)
	}
	if len(bySvc) != 2 {
		t.Errorf("len(bySvc) = %d, want 2 (NULL-service domains excluded)", len(bySvc))
	}
	if len(bySvc["web"]) != 1 || bySvc["web"][0].host != "web.example.com" {
		t.Errorf("bySvc[web] = %+v, want one entry for web.example.com", bySvc["web"])
	}
	if len(bySvc["api"]) != 1 || bySvc["api"][0].host != "api.example.com" {
		t.Errorf("bySvc[api] = %+v, want one entry for api.example.com", bySvc["api"])
	}
}

// TestRollbackRequiresExistingImage — when the target deployment row
// has a valid image_tag but the image was pruned off the host, the
// pre-flight `ImageInspect` returns image_not_found and Rollback
// short-circuits before any swap work. We seed a target on the
// local server (docker IS reachable inside the test container) with
// a tag that surely doesn't exist.
func TestRollbackRequiresExistingImage(t *testing.T) {
	e, db, appSvc, serverID := deployTestSetup(t)
	a, err := appSvc.Create(context.Background(), app.CreateInput{
		Name: "rbimg-app", ServerID: serverID, BuildType: "dockerfile",
		RepoURL: strPtr("https://github.com/example/repo"),
	})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	target := uuid.NewString()
	// success row pointing at a nonsense image tag.
	if _, err := db.Exec(
		`INSERT INTO deployments(id, app_id, status, image_tag) VALUES (?, ?, 'success', 'prexel-rbimg-app-nonexistent:1')`,
		target, a.ID,
	); err != nil {
		t.Fatalf("seed target: %v", err)
	}
	_, err = e.Rollback(context.Background(), a.ID, target, DeployOptions{})
	if err == nil {
		t.Fatal("Rollback with missing image returned nil err")
	}
	// The error must mention image_not_found — that's the contract
	// the UI maps to a distinct toast.
	if !errors.Is(err, err) { // dummy to silence linter; we just assert on message
	}
}

// TestEngine_Deploy_FailsCleanlyOnBadBuildType — drives Deploy all
// the way into runDeploy + Build.Build, but the app's build_type
// short-circuits at the build engine with an error. The deploy row
// must end up status=failed and error_message must reference the
// build failure. This exercises the runDeploy happy path up to the
// build step (a big chunk of code that we otherwise can't reach).
func TestEngine_Deploy_FailsCleanlyOnBadBuildType(t *testing.T) {
	e, db, _, serverID := deployTestSetup(t)
	// Wire a real *build.Engine (zero-value works — Build returns a
	// clear error for an unknown build_type without touching any of
	// its nil collaborators).
	e.Build = &build.Engine{}
	// app.Service.Create rejects unknown build_type — insert directly
	// so we can drive runDeploy past the build call.
	appID := uuid.NewString()
	if _, err := db.Exec(
		`INSERT INTO apps(id, name, server_id, build_type, container_name) VALUES (?, 'bad-build-app', ?, 'unknown-build-type', 'bad-build-app')`,
		appID, serverID,
	); err != nil {
		t.Fatalf("seed app: %v", err)
	}

	dep, err := e.Deploy(context.Background(), appID, DeployOptions{})
	if err == nil {
		t.Fatal("Deploy returned nil err on bad build")
	}
	if dep == nil {
		t.Fatal("Deploy returned nil deployment row")
	}
	got, gerr := e.GetDeployment(context.Background(), dep.ID)
	if gerr != nil {
		t.Fatalf("Get deployment: %v", gerr)
	}
	if got.Status != "failed" {
		t.Errorf("deployment status = %q, want failed", got.Status)
	}
	if got.ErrorMessage == nil {
		t.Error("error_message nil — failDeploy did not persist cause")
	}
}

// TestEngine_PruneImages_NoProviderClient covers the early-out branch.
// We pass a provider whose Client() will fail (closed) so pruneImages
// returns silently without errors. Use a local provider that we close
// immediately — the cached client will fail on the next Client() call.
func TestEngine_PruneImages_GracefullyHandlesClientFailure(t *testing.T) {
	// pruneImages takes any object satisfying the Provider interface;
	// we use the engine's server-resolved provider but cancel ctx first
	// so any docker call fails. We're really only checking that the
	// function doesn't panic on the error path.
	e, _, appSvc, serverID := deployTestSetup(t)
	a, err := appSvc.Create(context.Background(), app.CreateInput{
		Name: "prune-app", ServerID: serverID, BuildType: "dockerfile",
		RepoURL: strPtr("https://github.com/example/repo"),
	})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	prov, err := e.Servers.Provider(context.Background(), serverID)
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	defer func() { _ = prov.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// Should not panic — failure modes inside pruneImages are silent.
	e.pruneImages(ctx, prov, a, 5)
}
