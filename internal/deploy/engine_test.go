package deploy

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/prexel/prexel/internal/app"
	"github.com/prexel/prexel/internal/crypto"
	"github.com/prexel/prexel/internal/server"

	_ "modernc.org/sqlite"
)

// TODO(deploy smoke): the full happy-path Deploy and Rollback flows pull on
// e.Servers.Provider(...).Client(ctx), which returns the live Moby SDK client
// and immediately performs network/SSH operations. We cannot mock that
// surface without restructuring the Engine to take an interface (the same
// limitation noted in build/engine_test.go). The tests below therefore cover:
//   - The per-app lock invariant (concurrent Deploy attempts).
//   - Reconcile's pending/building/deploying → failed sweep (the core
//     crash-recovery contract from Tech Review §14).
//   - Validation paths in Deploy/Rollback/Stop that fail BEFORE any provider
//     work (missing app, missing server, no successful deployment yet).
//   - Repo round-trips (insert / listByApp / lastSuccess / failPending) since
//     they encode the deployment table contract.

const deployTestSecretKey = "test-secret-key-32-chars-minimum-aaaaaa"

// deployTestSetup wires the in-memory schema (apps + servers + deployments)
// and returns an Engine with no Build/Caddy/Bus deps. Tests that exercise
// runDeploy paths can stage app rows directly; tests that only need the lock
// or Reconcile don't need to touch anything beyond what's seeded here.
func deployTestSetup(t *testing.T) (*Engine, *sql.DB, *app.Service, string) {
	t.Helper()

	db, err := sql.Open("sqlite", "file::memory:?cache=shared&_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	stmts := []string{
		`CREATE TABLE servers (
			id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE,
			type TEXT NOT NULL CHECK (type IN ('local','remote')),
			host TEXT, port INTEGER NOT NULL DEFAULT 22, user TEXT,
			private_key BLOB, host_key_fingerprint TEXT,
			status TEXT NOT NULL DEFAULT 'unknown',
			docker_version TEXT, last_checked_at INTEGER,
			created_at INTEGER NOT NULL DEFAULT (unixepoch()),
			updated_at INTEGER NOT NULL DEFAULT (unixepoch())
		)`,
		`CREATE TABLE apps (
			id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, description TEXT,
			team_id TEXT,
			server_id TEXT REFERENCES servers(id), git_source_id TEXT,
			repo_url TEXT, branch TEXT NOT NULL DEFAULT 'main', git_commit_sha TEXT,
			build_type TEXT NOT NULL DEFAULT 'dockerfile',
			dockerfile_path TEXT NOT NULL DEFAULT 'Dockerfile',
			build_context TEXT NOT NULL DEFAULT '.',
			dockerfile_inline TEXT, compose_file TEXT, compose_inline TEXT, image_name TEXT,
			image_tag TEXT NOT NULL DEFAULT 'latest',
			install_command TEXT, build_command TEXT, start_command TEXT,
			pre_deploy_command TEXT, post_deploy_command TEXT,
			port INTEGER, host_port INTEGER, container_name TEXT,
			health_check_enabled INTEGER NOT NULL DEFAULT 1,
			health_check_path TEXT NOT NULL DEFAULT '/',
			health_check_method TEXT NOT NULL DEFAULT 'GET',
			health_check_port INTEGER, health_check_return_code INTEGER NOT NULL DEFAULT 200,
			health_check_interval INTEGER NOT NULL DEFAULT 30,
			health_check_timeout INTEGER NOT NULL DEFAULT 60,
			health_check_retries INTEGER NOT NULL DEFAULT 3,
			health_check_start_period INTEGER NOT NULL DEFAULT 10,
			limits_memory TEXT, limits_cpus TEXT,
			limits_memory_swap TEXT, limits_memory_swappiness INTEGER,
			limits_memory_reservation TEXT, limits_cpuset TEXT, limits_cpu_shares INTEGER,
			restart_policy TEXT NOT NULL DEFAULT 'unless-stopped',
			auto_deploy_branch TEXT,
			build_args_inject INTEGER NOT NULL DEFAULT 1,
			build_args_source_commit INTEGER NOT NULL DEFAULT 0,
			docker_labels TEXT NOT NULL DEFAULT '{}',
			env_vars TEXT NOT NULL DEFAULT '{}',
			status TEXT NOT NULL DEFAULT 'idle',
			created_at INTEGER NOT NULL DEFAULT (unixepoch()),
			updated_at INTEGER NOT NULL DEFAULT (unixepoch())
		)`,
		`CREATE TABLE deployments (
			id TEXT PRIMARY KEY,
			app_id TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
			commit_sha TEXT, commit_msg TEXT, branch TEXT, image_tag TEXT,
			rollback_of TEXT REFERENCES deployments(id),
			status TEXT NOT NULL DEFAULT 'pending',
			log_path TEXT, error_message TEXT,
			started_at INTEGER, finished_at INTEGER,
			created_at INTEGER NOT NULL DEFAULT (unixepoch())
		)`,
	}
	for _, q := range stmts {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("schema: %v", err)
		}
	}

	cipher, err := crypto.New(deployTestSecretKey)
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	serverSvc := server.NewService(db, cipher)
	appSvc := app.NewService(db, serverSvc, nil)

	// Seed a connected local server. We need the row so app.Service.Create
	// (called by tests that go further) sees a valid server_id.
	serverID := uuid.NewString()
	if _, err := db.Exec(`INSERT INTO servers(id, name, type, status) VALUES (?, 'local', 'local', 'connected')`,
		serverID); err != nil {
		t.Fatalf("seed server: %v", err)
	}

	e := New(Deps{
		DB:      db,
		Apps:    appSvc,
		Servers: serverSvc,
		LogDir:  t.TempDir(),
	})
	return e, db, appSvc, serverID
}

func TestEngine_Deploy_AppNotFound(t *testing.T) {
	// Deploy resolves the app id before doing anything else. A missing app
	// surfaces the underlying app.ErrNotFound — never reaches the docker
	// provider.
	e, _, _, _ := deployTestSetup(t)
	_, err := e.Deploy(context.Background(), uuid.NewString(), DeployOptions{})
	if err == nil {
		t.Fatal("expected error for unknown app")
	}
	if !errors.Is(err, app.ErrNotFound) {
		t.Errorf("err = %v, want app.ErrNotFound", err)
	}
}

func TestEngine_Deploy_ConcurrentRejectsSecond(t *testing.T) {
	// Per-app lock invariant: while one Deploy is in flight, subsequent
	// Deploy calls for the same app must return ErrDeployInProgress without
	// touching the docker provider.
	//
	// We simulate "in flight" by manually populating the engine's locks map
	// with an already-locked mutex. The second Deploy call hits acquireLock,
	// fails TryLock, and returns the sentinel error — same code path as a
	// real concurrent request.
	e, _, appSvc, serverID := deployTestSetup(t)

	created, err := appSvc.Create(context.Background(), app.CreateInput{
		Name:      "web",
		ServerID:  serverID,
		BuildType: "dockerfile",
		RepoURL:   strPtr("https://github.com/example/repo"),
	})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}

	// Pre-occupy the lock for this app id.
	mu := &sync.Mutex{}
	mu.Lock()
	defer mu.Unlock()
	e.locks.Store(created.ID, mu)

	_, err = e.Deploy(context.Background(), created.ID, DeployOptions{})
	if !errors.Is(err, ErrDeployInProgress) {
		t.Errorf("want ErrDeployInProgress, got %v", err)
	}
}

func TestEngine_Rollback_AppNotFound(t *testing.T) {
	e, _, _, _ := deployTestSetup(t)
	_, err := e.Rollback(context.Background(), uuid.NewString(), "", DeployOptions{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestEngine_Rollback_NoSuccessfulDeployment(t *testing.T) {
	// Rollback with an empty target id and no prior successful deployment
	// returns a clear error rather than panicking or pulling on docker.
	e, _, appSvc, serverID := deployTestSetup(t)
	a, err := appSvc.Create(context.Background(), app.CreateInput{
		Name: "web", ServerID: serverID, BuildType: "dockerfile",
		RepoURL: strPtr("https://github.com/example/repo"),
	})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	_, err = e.Rollback(context.Background(), a.ID, "", DeployOptions{})
	if err == nil {
		t.Fatal("expected error when no successful deployment exists")
	}
}

func TestEngine_Stop_NoContainerName_SetsStatus(t *testing.T) {
	// Stop short-circuits the docker call when the app has no container_name
	// (e.g. never deployed). It still flips the app status to "stopped".
	e, db, appSvc, serverID := deployTestSetup(t)
	a, err := appSvc.Create(context.Background(), app.CreateInput{
		Name: "web", ServerID: serverID, BuildType: "dockerfile",
		RepoURL: strPtr("https://github.com/example/repo"),
	})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	// Clear container_name so Stop skips the docker path entirely. App.Create
	// always sets one; we override directly via SQL to isolate the test from
	// the rest of the lifecycle.
	if _, err := db.Exec(`UPDATE apps SET container_name = NULL WHERE id = ?`, a.ID); err != nil {
		t.Fatalf("clear container_name: %v", err)
	}
	if err := e.Stop(context.Background(), a.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	got, err := appSvc.Get(context.Background(), a.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != "stopped" {
		t.Errorf("status = %q, want stopped", got.Status)
	}
}

func TestEngine_Reconcile_FailsOrphanedPendingDeployments(t *testing.T) {
	// Reconcile is the crash-recovery routine: every deployment in
	// pending/building/deploying (from a crashed Prexel) becomes "failed".
	// Apps not in building/deploying states are left alone so the test runs
	// without ever touching the docker provider.
	e, db, appSvc, serverID := deployTestSetup(t)
	a, err := appSvc.Create(context.Background(), app.CreateInput{
		Name: "web", ServerID: serverID, BuildType: "dockerfile",
		RepoURL: strPtr("https://github.com/example/repo"),
	})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}

	// Insert three orphaned deployment rows: one each of pending, building,
	// deploying. All three should be marked failed by Reconcile.
	statuses := []string{"pending", "building", "deploying"}
	ids := make([]string, 0, len(statuses))
	for _, st := range statuses {
		id := uuid.NewString()
		ids = append(ids, id)
		if _, err := db.Exec(
			`INSERT INTO deployments(id, app_id, status) VALUES (?, ?, ?)`,
			id, a.ID, st); err != nil {
			t.Fatalf("insert deployment: %v", err)
		}
	}

	if err := e.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	for _, id := range ids {
		d, err := e.GetDeployment(context.Background(), id)
		if err != nil {
			t.Fatalf("get deployment %s: %v", id, err)
		}
		if d.Status != "failed" {
			t.Errorf("deployment %s status = %q, want failed", id, d.Status)
		}
		if d.FinishedAt == nil {
			t.Errorf("deployment %s missing finished_at after reconcile", id)
		}
	}
}

func TestEngine_Reconcile_LeavesSuccessfulDeploymentsAlone(t *testing.T) {
	// Reconcile's failPending only touches in-flight statuses. Successful
	// deployments must survive untouched (otherwise rollback history is lost).
	e, db, appSvc, serverID := deployTestSetup(t)
	a, err := appSvc.Create(context.Background(), app.CreateInput{
		Name: "web", ServerID: serverID, BuildType: "dockerfile",
		RepoURL: strPtr("https://github.com/example/repo"),
	})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	successID := uuid.NewString()
	if _, err := db.Exec(
		`INSERT INTO deployments(id, app_id, status, image_tag) VALUES (?, ?, 'success', 'img:1')`,
		successID, a.ID); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := e.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	d, err := e.GetDeployment(context.Background(), successID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if d.Status != "success" {
		t.Errorf("status = %q, want success (unchanged)", d.Status)
	}
}

func TestRepo_RoundTrip(t *testing.T) {
	// Cover the repo contract: insert -> get -> listByApp ordering ->
	// lastSuccess excludes the given id.
	e, _, appSvc, serverID := deployTestSetup(t)
	a, err := appSvc.Create(context.Background(), app.CreateInput{
		Name: "web", ServerID: serverID, BuildType: "dockerfile",
		RepoURL: strPtr("https://github.com/example/repo"),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	ctx := context.Background()
	r := e.Repo()

	d1 := &Deployment{ID: uuid.NewString(), AppID: a.ID, Status: "success", ImageTag: strPtr("img:1")}
	d2 := &Deployment{ID: uuid.NewString(), AppID: a.ID, Status: "success", ImageTag: strPtr("img:2")}
	if err := r.insert(ctx, d1); err != nil {
		t.Fatalf("insert d1: %v", err)
	}
	if err := r.insert(ctx, d2); err != nil {
		t.Fatalf("insert d2: %v", err)
	}

	got, err := r.get(ctx, d1.ID)
	if err != nil {
		t.Fatalf("get d1: %v", err)
	}
	if got.ImageTag == nil || *got.ImageTag != "img:1" {
		t.Errorf("got.ImageTag = %v, want img:1", got.ImageTag)
	}

	list, err := r.listByApp(ctx, a.ID, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("listByApp len = %d, want 2", len(list))
	}

	// lastSuccess excludes d2 → returns d1.
	last, err := r.lastSuccess(ctx, a.ID, d2.ID)
	if err != nil {
		t.Fatalf("lastSuccess: %v", err)
	}
	if last.ID != d1.ID {
		t.Errorf("lastSuccess = %s, want %s", last.ID, d1.ID)
	}
}

func TestSuffixForType(t *testing.T) {
	// Lock the topic-suffix mapping down — SSE subscribers in the API layer
	// rely on these strings.
	cases := map[string]string{
		"deploy.started":     "started",
		"deploy.log":         "log",
		"deploy.success":     "success",
		"deploy.failed":      "failed",
		"app.status_changed": "status_changed",
		"custom.foo":         "custom_foo",
	}
	for in, want := range cases {
		if got := suffixForType(in); got != want {
			t.Errorf("suffixForType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNamedVolumeName(t *testing.T) {
	// Deterministic slugification of (app, mount_path) into a docker-legal
	// volume name. The contract is documented at the namedVolumeName decl;
	// these cases lock it down so a future refactor doesn't silently
	// orphan named volumes by changing the naming scheme.
	cases := []struct {
		app, mount, want string
	}{
		// Canonical case from the spec.
		{"myapp", "/var/lib/postgres", "prexel-vol-myapp-var-lib-postgres"},
		// Single-segment path.
		{"web", "/data", "prexel-vol-web-data"},
		// Trailing slashes and dots collapse to single dashes and trim.
		{"web", "/var/data/", "prexel-vol-web-var-data"},
		// Mixed-case input is folded to lowercase.
		{"MyApp", "/Var/LIB/Postgres", "prexel-vol-myapp-var-lib-postgres"},
		// Underscores are preserved (they're docker-legal and operators
		// often use them in path names like /var/lib/my_db).
		{"web", "/var/lib/my_db", "prexel-vol-web-var-lib-my_db"},
		// Funky characters fold to dashes and collapse.
		{"web", "/var//lib///etc", "prexel-vol-web-var-lib-etc"},
		// Dots in the path get slugified to dashes (no risk of looking
		// like a hostname).
		{"web", "/etc/foo.conf", "prexel-vol-web-etc-foo-conf"},
	}
	for _, tc := range cases {
		got := namedVolumeName(tc.app, tc.mount)
		if got != tc.want {
			t.Errorf("namedVolumeName(%q, %q) = %q, want %q", tc.app, tc.mount, got, tc.want)
		}
	}
}

func TestSlugify_StripsAndCollapses(t *testing.T) {
	// Direct slugify coverage so failures localise to the helper rather
	// than to the namedVolumeName tests above.
	cases := map[string]string{
		"":                "",
		"   ":             "",
		"/":               "",
		"-foo-":           "foo",
		"FOO_BAR":         "foo_bar",
		"a b c":           "a-b-c",
		"a/b/c":           "a-b-c",
		"a---b":           "a-b",
		"héllo wörld":     "h-llo-w-rld", // non-ascii folds to dash
		"123.456":         "123-456",
		"x_y_z":           "x_y_z",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestImageShortSHA(t *testing.T) {
	// Canonical: prexel-<app>-<sha>:<ts> → extracts <sha>.
	if got := imageShortSHA("prexel-web-abc1234:1700000000", "web"); got != "abc1234" {
		t.Errorf("imageShortSHA(canonical) = %q, want abc1234", got)
	}
	// Mismatched prefix → falls back to a generated short id (random uuid
	// prefix). We only assert it's non-empty.
	if got := imageShortSHA("nginx:latest", "web"); got == "" {
		t.Error("imageShortSHA(non-canonical) returned empty")
	}
}
