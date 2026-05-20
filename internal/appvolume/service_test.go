package appvolume

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/prexel/prexel/internal/app"
	"github.com/prexel/prexel/internal/crypto"
	"github.com/prexel/prexel/internal/server"

	_ "modernc.org/sqlite"
)

const testCipherKey = "test-secret-key-32-chars-minimum-aaaaaa"

// volumeTestSetup wires the in-memory schema needed by the app.Service FK
// preflight (servers + apps) and the app_volumes table from migration 011.
// Returns the appvolume service plus a seeded app id so tests can call
// Create without re-staging rows themselves.
func volumeTestSetup(t *testing.T) (*Service, *sql.DB, string) {
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
		// Minimal apps table — only the columns the app.Service scan touches
		// during Get, plus the FK target column. The deploy engine test does
		// the same trick.
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
		`CREATE TABLE app_volumes (
			id          TEXT PRIMARY KEY,
			app_id      TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
			service     TEXT,
			mount_path  TEXT NOT NULL,
			host_path   TEXT,
			is_named    INTEGER NOT NULL DEFAULT 1,
			read_only   INTEGER NOT NULL DEFAULT 0,
			created_at  INTEGER NOT NULL DEFAULT (unixepoch()),
			updated_at  INTEGER NOT NULL DEFAULT (unixepoch()),
			UNIQUE (app_id, service, mount_path)
		)`,
		`CREATE TRIGGER trg_app_volumes_updated_at
		 AFTER UPDATE ON app_volumes FOR EACH ROW
		 BEGIN
		     UPDATE app_volumes SET updated_at = unixepoch() WHERE id = OLD.id;
		 END`,
	}
	for _, q := range stmts {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("schema: %v", err)
		}
	}

	cipher, err := crypto.New(testCipherKey)
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	serverSvc := server.NewService(db, cipher)
	appSvc := app.NewService(db, serverSvc, nil)

	appID := uuid.NewString()
	if _, err := db.Exec(
		`INSERT INTO apps(id, name, server_id) VALUES (?, 'demo', NULL)`, appID,
	); err != nil {
		t.Fatalf("seed app: %v", err)
	}

	return NewService(db, appSvc), db, appID
}

func TestCreate_RejectsRelativeMountPath(t *testing.T) {
	svc, _, appID := volumeTestSetup(t)
	_, err := svc.Create(context.Background(), CreateInput{
		AppID:     appID,
		MountPath: "var/data",
		IsNamed:   true,
	})
	if err == nil || !contains(err.Error(), "invalid_mount_path") {
		t.Errorf("want invalid_mount_path error, got %v", err)
	}
}

func TestCreate_RejectsParentSegmentInMountPath(t *testing.T) {
	svc, _, appID := volumeTestSetup(t)
	_, err := svc.Create(context.Background(), CreateInput{
		AppID:     appID,
		MountPath: "/var/data/../etc",
		IsNamed:   true,
	})
	if err == nil || !contains(err.Error(), "invalid_mount_path") {
		t.Errorf("want invalid_mount_path error, got %v", err)
	}
}

func TestCreate_RejectsBindMountWithoutHostPath(t *testing.T) {
	svc, _, appID := volumeTestSetup(t)
	_, err := svc.Create(context.Background(), CreateInput{
		AppID:     appID,
		MountPath: "/data",
		IsNamed:   false,
	})
	if err == nil || !contains(err.Error(), "invalid_host_path") {
		t.Errorf("want invalid_host_path error, got %v", err)
	}
}

func TestCreate_RejectsNamedVolumeWithHostPath(t *testing.T) {
	svc, _, appID := volumeTestSetup(t)
	host := "/srv/data"
	_, err := svc.Create(context.Background(), CreateInput{
		AppID:     appID,
		MountPath: "/data",
		HostPath:  &host,
		IsNamed:   true,
	})
	if err == nil || !contains(err.Error(), "invalid_host_path") {
		t.Errorf("want invalid_host_path error, got %v", err)
	}
}

func TestCreate_RejectsRelativeHostPath(t *testing.T) {
	svc, _, appID := volumeTestSetup(t)
	host := "relative/host"
	_, err := svc.Create(context.Background(), CreateInput{
		AppID:     appID,
		MountPath: "/data",
		HostPath:  &host,
		IsNamed:   false,
	})
	if err == nil || !contains(err.Error(), "invalid_host_path") {
		t.Errorf("want invalid_host_path error, got %v", err)
	}
}

func TestCreate_AcceptsValidNamedVolume(t *testing.T) {
	svc, _, appID := volumeTestSetup(t)
	v, err := svc.Create(context.Background(), CreateInput{
		AppID:     appID,
		MountPath: "/var/lib/postgresql/data",
		IsNamed:   true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if v.HostPath != nil {
		t.Errorf("expected nil HostPath for named volume, got %v", *v.HostPath)
	}
	if !v.IsNamed {
		t.Errorf("expected IsNamed=true, got false")
	}
	if v.AppID != appID {
		t.Errorf("AppID mismatch: %q vs %q", v.AppID, appID)
	}
}

func TestCreate_AcceptsValidBindMount(t *testing.T) {
	svc, _, appID := volumeTestSetup(t)
	host := "/srv/uploads"
	v, err := svc.Create(context.Background(), CreateInput{
		AppID:     appID,
		MountPath: "/app/uploads",
		HostPath:  &host,
		IsNamed:   false,
		ReadOnly:  true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if v.HostPath == nil || *v.HostPath != host {
		t.Errorf("HostPath round-trip failed: %+v", v.HostPath)
	}
	if v.IsNamed {
		t.Errorf("expected IsNamed=false")
	}
	if !v.ReadOnly {
		t.Errorf("expected ReadOnly=true")
	}
}

func TestCreate_AcceptsServiceScopedVolume(t *testing.T) {
	svc, _, appID := volumeTestSetup(t)
	service := "db"
	v, err := svc.Create(context.Background(), CreateInput{
		AppID:     appID,
		Service:   &service,
		MountPath: "/var/lib/postgresql/data",
		IsNamed:   true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if v.Service == nil || *v.Service != "db" {
		t.Errorf("Service round-trip failed: %+v", v.Service)
	}
}

func TestCreate_RejectsInvalidService(t *testing.T) {
	svc, _, appID := volumeTestSetup(t)
	bad := "has space"
	_, err := svc.Create(context.Background(), CreateInput{
		AppID:     appID,
		Service:   &bad,
		MountPath: "/data",
		IsNamed:   true,
	})
	if err == nil || !contains(err.Error(), "invalid_service") {
		t.Errorf("want invalid_service error, got %v", err)
	}
}

func TestCreate_RejectsDuplicateTupleNullService(t *testing.T) {
	// SQLite treats NULL as distinct in UNIQUE constraints, so the schema
	// alone wouldn't reject a duplicate (app_id, NULL, mount_path) row.
	// The service layer backfills the check explicitly.
	svc, _, appID := volumeTestSetup(t)
	in := CreateInput{
		AppID:     appID,
		MountPath: "/data",
		IsNamed:   true,
	}
	if _, err := svc.Create(context.Background(), in); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := svc.Create(context.Background(), in)
	if !errors.Is(err, ErrDuplicate) {
		t.Errorf("want ErrDuplicate, got %v", err)
	}
}

func TestCreate_RejectsDuplicateTupleWithService(t *testing.T) {
	// Same case but with a non-NULL service column, which is enforced by
	// the DB-level UNIQUE constraint directly.
	svc, _, appID := volumeTestSetup(t)
	service := "db"
	in := CreateInput{
		AppID:     appID,
		Service:   &service,
		MountPath: "/data",
		IsNamed:   true,
	}
	if _, err := svc.Create(context.Background(), in); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := svc.Create(context.Background(), in)
	if !errors.Is(err, ErrDuplicate) {
		t.Errorf("want ErrDuplicate, got %v", err)
	}
}

func TestCreate_RejectsMissingApp(t *testing.T) {
	svc, _, _ := volumeTestSetup(t)
	_, err := svc.Create(context.Background(), CreateInput{
		AppID:     uuid.NewString(),
		MountPath: "/data",
		IsNamed:   true,
	})
	if err == nil || !contains(err.Error(), "invalid_app_id") {
		t.Errorf("want invalid_app_id error, got %v", err)
	}
}

func TestList_ReturnsEmptySliceNotNil(t *testing.T) {
	svc, _, appID := volumeTestSetup(t)
	out, err := svc.List(context.Background(), appID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if out == nil {
		t.Error("List returned nil slice, expected []")
	}
	if len(out) != 0 {
		t.Errorf("want 0 items, got %d", len(out))
	}
}

func TestUpdate_RejectsFlippingToBindWithoutHostPath(t *testing.T) {
	svc, _, appID := volumeTestSetup(t)
	v, err := svc.Create(context.Background(), CreateInput{
		AppID:     appID,
		MountPath: "/data",
		IsNamed:   true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	flip := false
	_, err = svc.Update(context.Background(), v.ID, UpdatePatch{IsNamed: &flip})
	if err == nil || !contains(err.Error(), "invalid_host_path") {
		t.Errorf("want invalid_host_path on flip, got %v", err)
	}
}

func TestUpdate_AllowsCoordinatedFlipWithHostPath(t *testing.T) {
	svc, _, appID := volumeTestSetup(t)
	v, err := svc.Create(context.Background(), CreateInput{
		AppID:     appID,
		MountPath: "/data",
		IsNamed:   true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	flip := false
	host := "/srv/data"
	out, err := svc.Update(context.Background(), v.ID, UpdatePatch{
		IsNamed:  &flip,
		HostPath: &host,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if out.IsNamed {
		t.Errorf("want IsNamed=false after patch")
	}
	if out.HostPath == nil || *out.HostPath != host {
		t.Errorf("HostPath not applied: %+v", out.HostPath)
	}
}

func TestDelete_RemovesRow(t *testing.T) {
	svc, _, appID := volumeTestSetup(t)
	v, err := svc.Create(context.Background(), CreateInput{
		AppID:     appID,
		MountPath: "/data",
		IsNamed:   true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.Delete(context.Background(), v.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := svc.Delete(context.Background(), v.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound on second delete, got %v", err)
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
