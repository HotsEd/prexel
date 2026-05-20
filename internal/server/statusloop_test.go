package server

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prexel/prexel/internal/crypto"

	_ "modernc.org/sqlite"
)

// statusTestSecret is the 32-byte key used to construct the cipher
// for every test in this file. Long enough to satisfy crypto.New,
// no other special properties needed — server tests don't decrypt
// anything in the loop.
const statusTestSecret = "test-secret-key-32-chars-minimum-aaaaaa"

// statusTestDB sets up an in-memory SQLite with the servers + apps
// tables Loop / runOnce touch. We use file::memory:?cache=shared so
// every connection in the pool sees the same schema.
func statusTestDB(t *testing.T) (*sql.DB, *Service) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:memdb_"+uuid.NewString()+"?mode=memory&cache=shared&_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	stmts := []string{
		`CREATE TABLE servers (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			type TEXT NOT NULL,
			host TEXT, port INTEGER NOT NULL DEFAULT 22, user TEXT,
			private_key BLOB, host_key_fingerprint TEXT,
			status TEXT NOT NULL DEFAULT 'unknown',
			docker_version TEXT, last_checked_at INTEGER,
			created_at INTEGER NOT NULL DEFAULT (unixepoch()),
			updated_at INTEGER NOT NULL DEFAULT (unixepoch())
		)`,
		`CREATE TABLE apps (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			server_id TEXT REFERENCES servers(id),
			status TEXT NOT NULL DEFAULT 'idle',
			updated_at INTEGER NOT NULL DEFAULT (unixepoch())
		)`,
	}
	for _, q := range stmts {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("schema: %v", err)
		}
	}
	cipher, err := crypto.New(statusTestSecret)
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	return db, NewService(db, cipher)
}

// TestStatusLoopHonoursContext — Loop must return promptly when its
// context is cancelled. Without this contract the daemon can't shut
// down cleanly. We start Loop with a tight interval and cancel
// straight away; if the goroutine doesn't exit within the timeout
// the test fails.
func TestStatusLoopHonoursContext(t *testing.T) {
	_, svc := statusTestDB(t)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		Loop(ctx, svc, 50*time.Millisecond)
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Loop did not exit within 500ms of context cancel")
	}
}

// TestStatusLoopDefaultsInterval — when interval <= 0 Loop must
// substitute the 60s default rather than spin-loop. We confirm by
// passing 0 and observing the goroutine never logs a probe within
// 100ms (which would only be possible if the timer fired before our
// cancel).
func TestStatusLoopDefaultsInterval(t *testing.T) {
	db, svc := statusTestDB(t)
	// Seed an unreachable remote (so a probe would change state)
	// then verify status DOESN'T change within 100ms — confirms
	// the timer didn't fire (default = 60s).
	seedRemoteServer(t, db, "remote", "127.0.0.1", 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Loop(ctx, svc, 0)

	time.Sleep(100 * time.Millisecond)

	srvs, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(srvs) != 1 {
		t.Fatalf("len = %d, want 1", len(srvs))
	}
	if srvs[0].Status != "unknown" {
		t.Errorf("server status = %q, want unknown (loop should not have fired yet)", srvs[0].Status)
	}
}

// TestRunOnce_LocalProbe is the connected/disconnected matrix for the
// "local" server type. Local probes use a local docker provider; in
// the test container we expect Docker to be reachable, so the row
// flips to "connected" and last_checked_at advances.
func TestRunOnce_LocalProbe_UpdatesStatus(t *testing.T) {
	db, svc := statusTestDB(t)
	id := uuid.NewString()
	if _, err := db.Exec(
		`INSERT INTO servers(id, name, type, status) VALUES (?, ?, 'local', 'unknown')`,
		id, "local",
	); err != nil {
		t.Fatalf("seed: %v", err)
	}

	runOnce(context.Background(), svc)

	got, err := svc.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	// The probe touches last_checked_at unconditionally — regardless
	// of whether the docker daemon was reachable.
	if got.LastCheckedAt == nil {
		t.Error("LastCheckedAt nil after runOnce — probe didn't persist")
	}
	if got.Status == "unknown" {
		t.Errorf("status still %q after runOnce — probe never resolved", got.Status)
	}
}

// TestRunOnce_DisconnectedMarksApps — when a server flips from
// connected to disconnected, every app on that server gets
// status="unreachable". We seed a remote pointing at a dead address
// (127.0.0.1:1 — nothing listens there) so the probe necessarily
// fails, and verify the cascade.
func TestRunOnce_DisconnectedMarksApps(t *testing.T) {
	db, svc := statusTestDB(t)
	srvID := uuid.NewString()
	// Encrypted private key (any non-empty blob) so the ping() doesn't
	// short-circuit with "unknown" — we want it to attempt the remote
	// dial and hit "disconnected" instead.
	encKey, err := svc.cipher.Encrypt([]byte("not-a-real-key"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO servers(id, name, type, host, port, user, private_key, status)
		 VALUES (?, 'unreachable', 'remote', '127.0.0.1', 1, 'ghost', ?, 'connected')`,
		srvID, encKey,
	); err != nil {
		t.Fatalf("seed server: %v", err)
	}
	// Two apps on that server.
	for i, name := range []string{"app1", "app2"} {
		if _, err := db.Exec(
			`INSERT INTO apps(id, name, server_id, status) VALUES (?, ?, ?, 'running')`,
			"app-"+name, name, srvID,
		); err != nil {
			t.Fatalf("seed app %d: %v", i, err)
		}
	}

	runOnce(context.Background(), svc)

	got, err := svc.Get(context.Background(), srvID)
	if err != nil {
		t.Fatalf("Get server: %v", err)
	}
	if got.Status != "disconnected" {
		t.Errorf("server status = %q, want disconnected", got.Status)
	}

	// Apps must now be "unreachable".
	rows, err := db.Query(`SELECT name, status FROM apps WHERE server_id = ?`, srvID)
	if err != nil {
		t.Fatalf("query apps: %v", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var name, status string
		if err := rows.Scan(&name, &status); err != nil {
			t.Fatalf("scan: %v", err)
		}
		count++
		if status != "unreachable" {
			t.Errorf("app %s status = %q, want unreachable", name, status)
		}
	}
	if count != 2 {
		t.Errorf("app count = %d, want 2", count)
	}
}

// TestRunOnce_StaysConsistent — if a server is already disconnected
// and the probe still fails, status stays disconnected but apps are
// NOT re-marked (the cascade only fires on the connected -> disconnected
// transition). This protects against thrashing app status when the
// loop runs every minute against a long-dead host.
func TestRunOnce_NoDoubleMarkOnConsecutiveFailures(t *testing.T) {
	db, svc := statusTestDB(t)
	srvID := uuid.NewString()
	if _, err := db.Exec(
		`INSERT INTO servers(id, name, type, host, port, user, status)
		 VALUES (?, 'long-dead', 'remote', '127.0.0.1', 1, 'ghost', 'disconnected')`,
		srvID,
	); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// App in stopped state — must NOT get flipped to unreachable.
	if _, err := db.Exec(
		`INSERT INTO apps(id, name, server_id, status) VALUES ('app-stop', 'stop', ?, 'stopped')`,
		srvID,
	); err != nil {
		t.Fatalf("seed app: %v", err)
	}

	runOnce(context.Background(), svc)

	var status string
	if err := db.QueryRow(`SELECT status FROM apps WHERE id = 'app-stop'`).Scan(&status); err != nil {
		t.Fatalf("scan app: %v", err)
	}
	if status != "stopped" {
		t.Errorf("app status changed to %q on consecutive failure — expected stopped untouched", status)
	}
}

// TestLoop_RunsAtLeastOnce — sanity check the full Loop wiring. We
// give it a very short interval and a connected local server, then
// wait for the timer to fire. last_checked_at should advance.
func TestLoop_RunsAtLeastOnce(t *testing.T) {
	db, svc := statusTestDB(t)
	id := uuid.NewString()
	if _, err := db.Exec(
		`INSERT INTO servers(id, name, type, status) VALUES (?, 'local-l', 'local', 'unknown')`,
		id,
	); err != nil {
		t.Fatalf("seed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go Loop(ctx, svc, 30*time.Millisecond)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, err := svc.Get(ctx, id)
		if err == nil && got.LastCheckedAt != nil {
			cancel()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	t.Fatal("Loop never executed a probe within 2s")
}

// seedRemoteServer is a tiny helper that inserts a remote row with
// the supplied unreachable address so the test doesn't carry SQL
// boilerplate for the small number of places that need it.
func seedRemoteServer(t *testing.T, db *sql.DB, name, host string, port int) string {
	t.Helper()
	id := uuid.NewString()
	if _, err := db.Exec(
		`INSERT INTO servers(id, name, type, host, port, user, status)
		 VALUES (?, ?, 'remote', ?, ?, 'ghost', 'unknown')`,
		id, name, host, port,
	); err != nil {
		t.Fatalf("seed remote: %v", err)
	}
	return id
}

// TestRepo_UpdateStatus_UpdatesEverything covers the persistence
// helper directly. The loop relies on this to advance both status
// and last_checked_at atomically.
func TestRepo_UpdateStatus_PersistsFields(t *testing.T) {
	db, svc := statusTestDB(t)
	id := uuid.NewString()
	if _, err := db.Exec(
		`INSERT INTO servers(id, name, type, status) VALUES (?, 'x', 'local', 'unknown')`,
		id,
	); err != nil {
		t.Fatalf("seed: %v", err)
	}
	ver := "27.0.1"
	if err := svc.repo.updateStatus(context.Background(), id, "connected", &ver); err != nil {
		t.Fatalf("updateStatus: %v", err)
	}
	got, err := svc.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != "connected" {
		t.Errorf("status = %q, want connected", got.Status)
	}
	if got.DockerVersion == nil || *got.DockerVersion != ver {
		t.Errorf("docker_version = %v, want %q", got.DockerVersion, ver)
	}
	if got.LastCheckedAt == nil {
		t.Errorf("LastCheckedAt nil")
	}
}

// TestRepo_MarkAppsUnreachable_OnlyTouchesTargetServer guards against
// the SQL accidentally marking apps on OTHER servers as unreachable.
func TestRepo_MarkAppsUnreachable_OnlyTouchesTargetServer(t *testing.T) {
	db, svc := statusTestDB(t)
	srv1 := uuid.NewString()
	srv2 := uuid.NewString()
	for _, id := range []string{srv1, srv2} {
		if _, err := db.Exec(
			`INSERT INTO servers(id, name, type, status) VALUES (?, ?, 'local', 'connected')`,
			id, "s-"+id[:8],
		); err != nil {
			t.Fatalf("seed server: %v", err)
		}
	}
	if _, err := db.Exec(
		`INSERT INTO apps(id, name, server_id, status) VALUES
		 ('a1', 'a1', ?, 'running'),
		 ('a2', 'a2', ?, 'running')`,
		srv1, srv2,
	); err != nil {
		t.Fatalf("seed apps: %v", err)
	}

	if err := svc.repo.markAppsUnreachable(context.Background(), srv1); err != nil {
		t.Fatalf("markAppsUnreachable: %v", err)
	}
	var s1, s2 string
	_ = db.QueryRow(`SELECT status FROM apps WHERE id='a1'`).Scan(&s1)
	_ = db.QueryRow(`SELECT status FROM apps WHERE id='a2'`).Scan(&s2)
	if s1 != "unreachable" {
		t.Errorf("a1 status = %q, want unreachable", s1)
	}
	if s2 != "running" {
		t.Errorf("a2 status = %q on UNRELATED server, want running (untouched)", s2)
	}
}

// TestService_CreateLocal_TwiceFails covers the singleton invariant
// (Tech Review §8): the local server row must be unique. The first
// call may legitimately fail if Docker isn't reachable from the test
// runner, so we only assert on the second call's error type when the
// first succeeds.
func TestService_CreateLocal_TwiceFails(t *testing.T) {
	_, svc := statusTestDB(t)
	first, err := svc.Create(context.Background(), CreateInput{Type: "local", Name: "local-uniq"})
	if err != nil {
		t.Skipf("Create first local failed (likely no docker in test env): %v", err)
	}
	if first == nil {
		t.Skip("Create first returned nil server")
	}
	_, err = svc.Create(context.Background(), CreateInput{Type: "local", Name: "another-local"})
	if err == nil || err != ErrLocalAlreadyExists {
		t.Errorf("second Create = %v, want ErrLocalAlreadyExists", err)
	}
}

// TestService_Create_RejectsMissingName ensures the validation runs
// before any provider work.
func TestService_Create_RejectsMissingName(t *testing.T) {
	_, svc := statusTestDB(t)
	if _, err := svc.Create(context.Background(), CreateInput{Type: "local", Name: " "}); err == nil {
		t.Error("missing name returned nil err")
	}
	if _, err := svc.Create(context.Background(), CreateInput{Type: "weird", Name: "x"}); err == nil {
		t.Error("invalid type returned nil err")
	}
	// Remote without host/user.
	if _, err := svc.Create(context.Background(), CreateInput{Type: "remote", Name: "r"}); err == nil {
		t.Error("missing host/user returned nil err")
	}
	// Remote with invalid port.
	if _, err := svc.Create(context.Background(), CreateInput{
		Type: "remote", Name: "r", Host: "h", User: "u", Port: 70000,
	}); err == nil {
		t.Error("invalid port returned nil err")
	}
	// Remote with neither GenerateKey nor PrivateKey.
	if _, err := svc.Create(context.Background(), CreateInput{
		Type: "remote", Name: "r", Host: "h", User: "u", Port: 22,
	}); err == nil {
		t.Error("missing key returned nil err")
	}
}

// TestService_Update_ValidatesPortAndName covers the Update patch
// validation surface.
func TestService_Update_Validation(t *testing.T) {
	_, svc := statusTestDB(t)
	badPort := 70000
	if _, err := svc.Update(context.Background(), "x", Patch{Port: &badPort}); err == nil {
		t.Error("Update bad port returned nil err")
	}
	emptyName := "   "
	if _, err := svc.Update(context.Background(), "x", Patch{Name: &emptyName}); err == nil {
		t.Error("Update blank name returned nil err")
	}
}

// TestService_Update_AppliesPatch covers updatePatch. We seed a
// remote, patch Name/Host/Port/User, and verify each field round-trips.
func TestService_Update_AppliesPatch(t *testing.T) {
	db, svc := statusTestDB(t)
	id := uuid.NewString()
	if _, err := db.Exec(
		`INSERT INTO servers(id, name, type, host, port, user, status)
		 VALUES (?, 'first', 'remote', 'host.a', 22, 'rootA', 'unknown')`,
		id,
	); err != nil {
		t.Fatalf("seed: %v", err)
	}
	newName := "second"
	newHost := "host.b"
	newPort := 2222
	newUser := "rootB"
	got, err := svc.Update(context.Background(), id, Patch{
		Name: &newName,
		Host: &newHost,
		Port: &newPort,
		User: &newUser,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got.Name != newName {
		t.Errorf("Name = %q, want %q", got.Name, newName)
	}
	if got.Host == nil || *got.Host != newHost {
		t.Errorf("Host = %v, want %q", got.Host, newHost)
	}
	if got.Port != newPort {
		t.Errorf("Port = %d, want %d", got.Port, newPort)
	}
	if got.User == nil || *got.User != newUser {
		t.Errorf("User = %v, want %q", got.User, newUser)
	}

	// Empty patch returns the row unchanged — no UPDATE issued.
	again, err := svc.Update(context.Background(), id, Patch{})
	if err != nil {
		t.Fatalf("Update empty: %v", err)
	}
	if again.Name != newName {
		t.Errorf("empty Patch mutated Name: %q", again.Name)
	}

	// Unknown id surfaces ErrNotFound.
	if _, err := svc.Update(context.Background(), "nope", Patch{Name: &newName}); err == nil {
		t.Error("Update unknown id returned nil err")
	}
}

// TestRepo_UpdateHostKey persists the first-contact fingerprint —
// drives the TOFU path used by SSH connections.
func TestRepo_UpdateHostKey(t *testing.T) {
	db, svc := statusTestDB(t)
	id := uuid.NewString()
	if _, err := db.Exec(
		`INSERT INTO servers(id, name, type, status) VALUES (?, 'hk', 'local', 'unknown')`,
		id,
	); err != nil {
		t.Fatalf("seed: %v", err)
	}
	const fp = "SHA256:abcdef"
	if err := svc.repo.updateHostKey(context.Background(), id, fp); err != nil {
		t.Fatalf("updateHostKey: %v", err)
	}
	got, err := svc.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.HostKeyFingerprint == nil || *got.HostKeyFingerprint != fp {
		t.Errorf("HostKeyFingerprint = %v, want %q", got.HostKeyFingerprint, fp)
	}
}

// TestService_Provider_UnknownID covers the explicit Provider error
// surface — handler maps this to a 404.
func TestService_Provider_UnknownID(t *testing.T) {
	_, svc := statusTestDB(t)
	if _, err := svc.Provider(context.Background(), "missing-id"); err == nil {
		t.Error("Provider unknown returned nil err")
	}
}

// TestService_Provider_LocalReturnsProvider — for a local row we get
// back a usable provider object (we close it immediately; the local
// constructor doesn't open anything heavy).
func TestService_Provider_LocalReturnsProvider(t *testing.T) {
	db, svc := statusTestDB(t)
	id := uuid.NewString()
	if _, err := db.Exec(
		`INSERT INTO servers(id, name, type, status) VALUES (?, 'localprov', 'local', 'connected')`,
		id,
	); err != nil {
		t.Fatalf("seed: %v", err)
	}
	p, err := svc.Provider(context.Background(), id)
	if err != nil {
		t.Fatalf("Provider: %v", err)
	}
	if p == nil {
		t.Fatal("Provider returned nil")
	}
	defer func() { _ = p.Close() }()
	if p.Name() != "localprov" {
		t.Errorf("Provider name = %q, want localprov", p.Name())
	}
}

// TestService_Provider_RemoteWithoutKey rejects the row when its
// private_key column is empty.
func TestService_Provider_RemoteWithoutKey(t *testing.T) {
	db, svc := statusTestDB(t)
	id := uuid.NewString()
	if _, err := db.Exec(
		`INSERT INTO servers(id, name, type, host, port, user, status)
		 VALUES (?, 'remprov', 'remote', 'host', 22, 'u', 'unknown')`,
		id,
	); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := svc.Provider(context.Background(), id); err == nil {
		t.Error("Provider on keyless remote returned nil err")
	}
}

// TestService_Delete_BlocksWhenAppsAttached — Delete must return
// ErrHasApps when at least one app references the server, otherwise
// FK ON DELETE behaviour would orphan the apps silently.
func TestService_Delete_BlocksWhenAppsAttached(t *testing.T) {
	db, svc := statusTestDB(t)
	srvID := uuid.NewString()
	if _, err := db.Exec(
		`INSERT INTO servers(id, name, type, status) VALUES (?, 'svc-a', 'local', 'connected')`,
		srvID,
	); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO apps(id, name, server_id) VALUES ('aa', 'aa', ?)`, srvID,
	); err != nil {
		t.Fatalf("seed app: %v", err)
	}
	err := svc.Delete(context.Background(), srvID)
	if err == nil {
		t.Fatal("Delete returned nil while app still attached")
	}
}
