package handler

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/prexel/prexel/internal/app"
	"github.com/prexel/prexel/internal/crypto"
	"github.com/prexel/prexel/internal/deploy"
	"github.com/prexel/prexel/internal/gitsrc"
	"github.com/prexel/prexel/internal/server"

	_ "modernc.org/sqlite"
)

// fakeDeployer captures the Deploy invocations for assertions and
// returns a deterministic *deploy.Deployment so the webhook handler
// can include a deployment_id in its 202 response.
type fakeDeployer struct {
	mu     sync.Mutex
	calls  []fakeDeployCall
	nextID string // deterministic id for the deployment row
}

type fakeDeployCall struct {
	AppID string
	Opts  deploy.DeployOptions
}

func (f *fakeDeployer) Deploy(_ context.Context, appID string, opts deploy.DeployOptions) (*deploy.Deployment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakeDeployCall{AppID: appID, Opts: opts})
	id := f.nextID
	if id == "" {
		id = uuid.NewString()
	}
	return &deploy.Deployment{ID: id, AppID: appID, Status: "pending"}, nil
}

func (f *fakeDeployer) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// webhookSchema is the in-memory schema the webhook tests need. We
// duplicate the column list (rather than running migrations) so the
// test boots in milliseconds — same pattern as app_test.go. Columns
// must stay in sync with `scanSource` (gitsrc/source.go) and
// `scanApp` (app/repo.go); a missing column would surface as a panic
// when the handler reads back a row.
const webhookSchema = `
CREATE TABLE settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at INTEGER NOT NULL DEFAULT (unixepoch())
);

CREATE TABLE servers (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    type TEXT NOT NULL CHECK (type IN ('local','remote')),
    host TEXT, port INTEGER NOT NULL DEFAULT 22, user TEXT,
    private_key BLOB, host_key_fingerprint TEXT,
    status TEXT NOT NULL DEFAULT 'unknown',
    docker_version TEXT, last_checked_at INTEGER,
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    updated_at INTEGER NOT NULL DEFAULT (unixepoch())
);

CREATE TABLE git_sources (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL,
    name TEXT NOT NULL,
    installation_id TEXT,
    private_key BLOB,
    public_key TEXT,
    token BLOB,
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    app_id TEXT,
    app_slug TEXT,
    app_private_key BLOB,
    account_login TEXT,
    account_type TEXT,
    webhook_secret TEXT
);

CREATE TABLE apps (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    description TEXT,
    team_id TEXT,
    server_id TEXT REFERENCES servers(id),
    git_source_id TEXT REFERENCES git_sources(id),
    repo_url TEXT,
    branch TEXT NOT NULL DEFAULT 'main',
    git_commit_sha TEXT,
    build_type TEXT NOT NULL DEFAULT 'dockerfile'
        CHECK (build_type IN ('dockerfile','docker_image','docker_compose')),
    dockerfile_path TEXT NOT NULL DEFAULT 'Dockerfile',
    build_context TEXT NOT NULL DEFAULT '.',
    dockerfile_inline TEXT,
    compose_file TEXT,
    compose_inline TEXT,
    image_name TEXT,
    image_tag TEXT NOT NULL DEFAULT 'latest',
    install_command TEXT,
    build_command TEXT,
    start_command TEXT,
    pre_deploy_command TEXT,
    post_deploy_command TEXT,
    port INTEGER,
    host_port INTEGER,
    container_name TEXT,
    health_check_enabled INTEGER NOT NULL DEFAULT 1,
    health_check_path TEXT NOT NULL DEFAULT '/',
    health_check_method TEXT NOT NULL DEFAULT 'GET',
    health_check_port INTEGER,
    health_check_return_code INTEGER NOT NULL DEFAULT 200,
    health_check_interval INTEGER NOT NULL DEFAULT 30,
    health_check_timeout INTEGER NOT NULL DEFAULT 60,
    health_check_retries INTEGER NOT NULL DEFAULT 3,
    health_check_start_period INTEGER NOT NULL DEFAULT 10,
    limits_memory TEXT,
    limits_cpus TEXT,
    limits_memory_swap TEXT,
    limits_memory_swappiness INTEGER,
    limits_memory_reservation TEXT,
    limits_cpuset TEXT,
    limits_cpu_shares INTEGER,
    restart_policy TEXT NOT NULL DEFAULT 'unless-stopped',
    auto_deploy_branch TEXT,
    build_args_inject INTEGER NOT NULL DEFAULT 1,
    build_args_source_commit INTEGER NOT NULL DEFAULT 0,
    docker_labels TEXT NOT NULL DEFAULT '{}',
    env_vars TEXT NOT NULL DEFAULT '{}',
    status TEXT NOT NULL DEFAULT 'idle',
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    updated_at INTEGER NOT NULL DEFAULT (unixepoch())
);
`

type webhookFixture struct {
	t        *testing.T
	db       *sql.DB
	apps     *app.Service
	repo     *gitsrc.Repo
	deployer *fakeDeployer
	handler  *WebhookHandler
	source   *gitsrc.Source
	serverID string
}

func setupWebhookTest(t *testing.T) *webhookFixture {
	t.Helper()
	// In-memory DB. Each test gets its own — cache=shared lets multiple
	// sql connections see the same data, which sql.SetMaxOpenConns(1)
	// would otherwise mask if a goroutine reached for a second conn.
	db, err := sql.Open("sqlite", "file:webhook_test_"+uuid.NewString()+"?mode=memory&cache=shared&_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	for _, stmt := range splitSQL(webhookSchema) {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("schema: %v\nstmt: %s", err, stmt)
		}
	}

	// Seed a connected server so app.Service.Create passes its checks.
	serverID := uuid.NewString()
	if _, err := db.Exec(
		`INSERT INTO servers(id, name, type, status) VALUES (?, 'local', 'local', 'connected')`,
		serverID,
	); err != nil {
		t.Fatalf("seed server: %v", err)
	}

	cipher, err := crypto.New(testSecretKey)
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	serverSvc := server.NewService(db, cipher)
	repo := gitsrc.NewRepo(db, cipher)
	appSvc := app.NewService(db, serverSvc, repo)

	// Seed a github_app source with a known secret so the test can
	// produce valid HMAC signatures.
	src := &gitsrc.Source{
		ID:            uuid.NewString(),
		Type:          "github_app",
		Name:          "test-app",
		AppID:         "12345",
		AppSlug:       "test-app",
		WebhookSecret: "secret-shhh",
	}
	if err := repo.Create(src); err != nil {
		t.Fatalf("seed source: %v", err)
	}

	dep := &fakeDeployer{}
	h := NewWebhookHandler(repo, appSvc, dep)

	return &webhookFixture{
		t:        t,
		db:       db,
		apps:     appSvc,
		repo:     repo,
		deployer: dep,
		handler:  h,
		source:   src,
		serverID: serverID,
	}
}

// splitSQL splits a multi-statement SQL blob on `;` boundaries so the
// in-memory driver (which executes one statement per Exec) doesn't trip
// on the multi-statement schema. Stripped statements are skipped.
func splitSQL(blob string) []string {
	var (
		stmts []string
		cur   bytes.Buffer
	)
	for _, line := range bytes.Split([]byte(blob), []byte("\n")) {
		cur.Write(line)
		cur.WriteByte('\n')
		if bytes.HasSuffix(bytes.TrimSpace(line), []byte(";")) {
			s := bytes.TrimSpace(cur.Bytes())
			if len(s) > 0 {
				stmts = append(stmts, string(s))
			}
			cur.Reset()
		}
	}
	if s := bytes.TrimSpace(cur.Bytes()); len(s) > 0 {
		stmts = append(stmts, string(s))
	}
	return stmts
}

// seedApp inserts an app row directly with the columns we care about
// for webhook matching. We skip app.Service.Create because it pulls in
// validation we don't need here; the webhook handler only reads back
// via ListAll.
func (f *webhookFixture) seedApp(name, repoURL string, autoDeployBranch *string) string {
	f.t.Helper()
	id := uuid.NewString()
	var adb any
	if autoDeployBranch != nil {
		adb = *autoDeployBranch
	}
	_, err := f.db.Exec(
		`INSERT INTO apps(
			id, name, server_id, git_source_id, repo_url, branch,
			auto_deploy_branch
		) VALUES (?, ?, ?, ?, ?, 'main', ?)`,
		id, name, f.serverID, f.source.ID, repoURL, adb,
	)
	if err != nil {
		f.t.Fatalf("seed app: %v", err)
	}
	return id
}

// sign produces a valid `sha256=<hex>` header for body with the test source's
// secret. Tests that want a bad signature build the header by hand.
func (f *webhookFixture) sign(body []byte) string {
	mac := hmac.New(sha256.New, []byte(f.source.WebhookSecret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// doRequest builds an HTTP request hitting POST /webhooks/github/{source_id}
// and runs the handler in-process. sourceID overrides the test's seeded
// source when non-empty (used to exercise the 404 path).
func (f *webhookFixture) doRequest(eventType string, body []byte, signature, sourceID string) *httptest.ResponseRecorder {
	f.t.Helper()
	if sourceID == "" {
		sourceID = f.source.ID
	}
	r := httptest.NewRequest(http.MethodPost, "/webhooks/github/"+sourceID, bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if eventType != "" {
		r.Header.Set("X-GitHub-Event", eventType)
	}
	if signature != "" {
		r.Header.Set("X-Hub-Signature-256", signature)
	}
	r = withChiParam(r, "source_id", sourceID)

	rec := httptest.NewRecorder()
	f.handler.Handle(rec, r)
	return rec
}

func pushBody(t *testing.T, ref, after, fullName, cloneURL string, deleted bool) []byte {
	t.Helper()
	payload := map[string]any{
		"ref":     ref,
		"after":   after,
		"deleted": deleted,
		"repository": map[string]any{
			"full_name": fullName,
			"clone_url": cloneURL,
			"ssh_url":   "git@github.com:" + fullName + ".git",
		},
		"head_commit": map[string]any{
			"message": "test commit",
		},
	}
	out, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return out
}

func TestWebhook_BadSignature(t *testing.T) {
	f := setupWebhookTest(t)
	body := pushBody(t, "refs/heads/main", "abc123", "owner/repo",
		"https://github.com/owner/repo.git", false)

	rec := f.doRequest("push", body, "sha256=deadbeef", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d body=%s", rec.Code, rec.Body.String())
	}
	if f.deployer.callCount() != 0 {
		t.Errorf("deploy must NOT fire on bad signature; got %d calls", f.deployer.callCount())
	}
}

func TestWebhook_MissingSignature(t *testing.T) {
	f := setupWebhookTest(t)
	body := pushBody(t, "refs/heads/main", "abc123", "owner/repo",
		"https://github.com/owner/repo.git", false)

	rec := f.doRequest("push", body, "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestWebhook_UnknownSource(t *testing.T) {
	f := setupWebhookTest(t)
	body := pushBody(t, "refs/heads/main", "abc123", "owner/repo",
		"https://github.com/owner/repo.git", false)

	rec := f.doRequest("push", body, f.sign(body), "bogus-source-id")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestWebhook_PingEvent_NoOp(t *testing.T) {
	f := setupWebhookTest(t)
	// ping deliveries carry an arbitrary JSON body (`{"zen":"…"}`). We
	// still expect a valid signature; the handler must early-return 204
	// without parsing it as a push.
	body := []byte(`{"zen":"hello"}`)
	rec := f.doRequest("ping", body, f.sign(body), "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d body=%s", rec.Code, rec.Body.String())
	}
	if f.deployer.callCount() != 0 {
		t.Errorf("ping must NOT trigger deploy; got %d calls", f.deployer.callCount())
	}
}

func TestWebhook_DeletedBranch_NoOp(t *testing.T) {
	f := setupWebhookTest(t)
	branch := "feature/x"
	f.seedApp("web", "https://github.com/owner/repo", &branch)

	body := pushBody(t, "refs/heads/feature/x", "0000000000000000000000000000000000000000",
		"owner/repo", "https://github.com/owner/repo.git", true)

	rec := f.doRequest("push", body, f.sign(body), "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d body=%s", rec.Code, rec.Body.String())
	}
	if f.deployer.callCount() != 0 {
		t.Errorf("deleted branch must NOT trigger deploy; got %d calls", f.deployer.callCount())
	}
}

func TestWebhook_TagPush_NoOp(t *testing.T) {
	f := setupWebhookTest(t)
	branch := "main"
	f.seedApp("web", "https://github.com/owner/repo", &branch)

	body := pushBody(t, "refs/tags/v1.0.0", "abc123", "owner/repo",
		"https://github.com/owner/repo.git", false)

	rec := f.doRequest("push", body, f.sign(body), "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d body=%s", rec.Code, rec.Body.String())
	}
	if f.deployer.callCount() != 0 {
		t.Errorf("tag push must NOT trigger deploy; got %d calls", f.deployer.callCount())
	}
}

func TestWebhook_NonMatchingBranch(t *testing.T) {
	f := setupWebhookTest(t)
	branch := "main"
	f.seedApp("web", "https://github.com/owner/repo", &branch)

	body := pushBody(t, "refs/heads/develop", "abc123", "owner/repo",
		"https://github.com/owner/repo.git", false)

	rec := f.doRequest("push", body, f.sign(body), "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d body=%s", rec.Code, rec.Body.String())
	}
	if f.deployer.callCount() != 0 {
		t.Errorf("non-matching branch must NOT trigger deploy; got %d calls", f.deployer.callCount())
	}
	// Response should have empty matched array.
	var resp struct {
		Matched []map[string]string `json:"matched"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Matched) != 0 {
		t.Errorf("matched should be empty, got %v", resp.Matched)
	}
}

func TestWebhook_MatchingBranch_TriggersDeploy(t *testing.T) {
	f := setupWebhookTest(t)
	branch := "main"
	appID := f.seedApp("web", "https://github.com/owner/repo", &branch)
	// Also seed a non-matching app on the same source to ensure the
	// filter actually filters.
	otherBranch := "develop"
	f.seedApp("other", "https://github.com/owner/repo", &otherBranch)

	f.deployer.nextID = "fixed-deploy-id"

	body := pushBody(t, "refs/heads/main", "deadbeef", "owner/repo",
		"https://github.com/owner/repo.git", false)
	rec := f.doRequest("push", body, f.sign(body), "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d body=%s", rec.Code, rec.Body.String())
	}
	if f.deployer.callCount() != 1 {
		t.Fatalf("want exactly 1 deploy, got %d", f.deployer.callCount())
	}
	if got := f.deployer.calls[0].AppID; got != appID {
		t.Errorf("deploy fired for wrong app: got %s want %s", got, appID)
	}
	if got := f.deployer.calls[0].Opts.Branch; got != "main" {
		t.Errorf("deploy branch: got %q want main", got)
	}
	if got := f.deployer.calls[0].Opts.CommitSHA; got != "deadbeef" {
		t.Errorf("deploy commit: got %q want deadbeef", got)
	}

	var resp struct {
		Matched []struct {
			AppID        string `json:"app_id"`
			DeploymentID string `json:"deployment_id"`
		} `json:"matched"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Matched) != 1 {
		t.Fatalf("matched: want 1, got %d", len(resp.Matched))
	}
	if resp.Matched[0].AppID != appID {
		t.Errorf("response app_id: got %s want %s", resp.Matched[0].AppID, appID)
	}
	if resp.Matched[0].DeploymentID != "fixed-deploy-id" {
		t.Errorf("response deployment_id: got %q want fixed-deploy-id", resp.Matched[0].DeploymentID)
	}
}

func TestWebhook_MatchesSSHURL(t *testing.T) {
	f := setupWebhookTest(t)
	branch := "main"
	// App stored with SSH form; webhook carries both HTTPS clone + SSH.
	appID := f.seedApp("web", "git@github.com:owner/repo.git", &branch)

	body := pushBody(t, "refs/heads/main", "abc", "owner/repo",
		"https://github.com/owner/repo.git", false)
	rec := f.doRequest("push", body, f.sign(body), "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d body=%s", rec.Code, rec.Body.String())
	}
	if f.deployer.callCount() != 1 {
		t.Fatalf("want 1 deploy (SSH-stored URL should still match), got %d", f.deployer.callCount())
	}
	if f.deployer.calls[0].AppID != appID {
		t.Errorf("wrong app: %s", f.deployer.calls[0].AppID)
	}
}

func TestWebhook_SignatureVerification_RoundTrip(t *testing.T) {
	// Sanity test for verifySignature itself — feeds a body through the
	// known-good HMAC and through a tampered one. Catches regressions in
	// the prefix / hex / hmac.Equal wiring without needing the full HTTP
	// scaffolding.
	body := []byte(`{"hello":"world"}`)
	secret := "shared-secret"

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	good := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	if !verifySignature(secret, good, body) {
		t.Error("verifySignature rejected a valid signature")
	}
	if verifySignature(secret, good, append([]byte{}, append(body, 'x')...)) {
		t.Error("verifySignature accepted a tampered body")
	}
	if verifySignature(secret, "sha256=zzzz", body) {
		t.Error("verifySignature accepted invalid hex")
	}
	if verifySignature(secret, "sha1="+hex.EncodeToString(mac.Sum(nil)), body) {
		t.Error("verifySignature accepted wrong algorithm prefix")
	}
	if verifySignature("", good, body) {
		t.Error("empty secret must reject every signature")
	}
	if verifySignature(secret, "", body) {
		t.Error("empty header must be rejected")
	}
}
