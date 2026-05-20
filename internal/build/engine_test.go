package build

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	dockerclient "github.com/docker/docker/client"
	"github.com/prexel/prexel/internal/app"
	"github.com/prexel/prexel/internal/eventbus"
)

// TODO(build smoke): the Build entry point depends on docker.Provider.Client
// returning a real *github.com/docker/docker/client.Client (ImageBuild /
// ImagePull are called directly on it). We cannot mock that cleanly without
// either rewriting Engine to take an interface or pulling in testcontainers
// (out of scope per task constraints). The tests below therefore cover:
//   - Build rejecting unsupported types up front (the entire pre-Docker
//     validation surface).
//   - tarDirectory streaming (used by every dockerfile build).
//   - imageRefFor / shortenSHA / splitLines / readGitHead pure helpers.
//   - streamJSONMessage end-to-end against an in-memory stream — same code
//     path as ImageBuild/ImagePull output processing, including eventbus
//     publication of `deploy.log` events.
// What is NOT covered (and would require an interface on Provider.Client):
//   - dockerfile_inline → ImageBuild call with the correct tag/Dockerfile.
//   - docker_image → ImagePull call.
//   - build args from is_build_time secrets (covered indirectly by
//     secret.Service tests).

func TestBuild_RejectsUnsupportedBuildType(t *testing.T) {
	// Unknown build_type should error out before the engine touches docker.
	e := New(nil, nil, nil, nil)
	a := &app.App{ID: "x", Name: "x", BuildType: "rocket"}
	_, err := e.Build(context.Background(), nil, a, BuildOptions{}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "unsupported build_type") {
		t.Errorf("want unsupported build_type error, got %v", err)
	}
}

func TestBuild_Compose_RequiresInlineOrRepo(t *testing.T) {
	// Compose builds need a YAML source — either compose_inline OR a
	// repo_url to clone from. With neither, we reject early before
	// touching the docker provider so the failure carries a clear
	// reason. (Previously the engine returned a flat "not implemented"
	// error; that contract is gone now that compose deploys are
	// actually wired up.)
	e := New(nil, nil, nil, nil)
	a := &app.App{ID: "x", Name: "x", BuildType: "docker_compose"}
	_, err := e.Build(context.Background(), nil, a, BuildOptions{}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "compose app has neither compose_inline nor repo_url") {
		t.Errorf("want missing-compose-source error, got %v", err)
	}
}

func TestBuild_Compose_InlineRejectsBuildDirective(t *testing.T) {
	// Inline compose has no source tree on disk, so a `build:`
	// service can't be tar'd into a docker build context. Operators
	// using inline YAML must switch to `image:` or move the app to
	// `repo_url + compose_file` so we can clone the working tree.
	e := New(nil, nil, nil, nil)
	inline := `
services:
  api:
    build: .
  cache:
    image: redis:7-alpine
`
	a := &app.App{
		ID: "x", Name: "x", BuildType: "docker_compose",
		ComposeInline: &inline,
	}
	_, err := e.Build(context.Background(), nilProvider{}, a, BuildOptions{}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "compose_inline") {
		t.Errorf("want compose_inline + build clash error, got %v", err)
	}
}

func TestBuild_DockerImage_RequiresImageName(t *testing.T) {
	// docker_image apps must carry an image_name BEFORE we touch the docker
	// provider. The pull engine asserts this so the deploy reports a clean
	// validation error rather than a generic docker SDK failure.
	e := New(nil, nil, nil, nil)
	a := &app.App{ID: "x", Name: "x", BuildType: "docker_image"}
	_, err := e.Build(context.Background(), nilProvider{}, a, BuildOptions{}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "image_name") {
		t.Errorf("want missing image_name error, got %v", err)
	}
}

func TestBuild_Dockerfile_RequiresRepoOrInline(t *testing.T) {
	// dockerfile builds reject early when neither repo_url nor
	// dockerfile_inline is provided — important because the failure surfaces
	// in the API before any tmp dir is created.
	e := New(nil, nil, nil, nil)
	a := &app.App{ID: "x", Name: "x", BuildType: "dockerfile"}
	_, err := e.Build(context.Background(), nilProvider{}, a, BuildOptions{}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "neither repo_url nor dockerfile_inline") {
		t.Errorf("want missing repo/inline error, got %v", err)
	}
}

func TestImageRefFor_FormatsCanonicalTag(t *testing.T) {
	// imageRefFor produces "prexel-<app>-<sha>:<ts>". The ts varies; assert
	// the prefix shape.
	ref := ImageRefFor("myapp", "abc1234")
	if !strings.HasPrefix(ref, "prexel-myapp-abc1234:") {
		t.Errorf("imageRefFor = %q, want prefix prexel-myapp-abc1234:", ref)
	}
	// Empty SHA → fallback uuid-derived short ref.
	ref2 := ImageRefFor("myapp", "")
	if !strings.HasPrefix(ref2, "prexel-myapp-") {
		t.Errorf("imageRefFor (empty sha) = %q, want prefix prexel-myapp-", ref2)
	}
}

func TestShortenSHA(t *testing.T) {
	if got := ShortenSHA("abcdef1234567890"); got != "abcdef1" {
		t.Errorf("ShortenSHA(16-char) = %q, want abcdef1", got)
	}
	if got := ShortenSHA("abc"); got != "abc" {
		t.Errorf("ShortenSHA(short) = %q, want abc", got)
	}
	if got := ShortenSHA(""); got != "" {
		t.Errorf("ShortenSHA(empty) = %q, want empty", got)
	}
}

func TestSplitLines(t *testing.T) {
	got := SplitLines("a\nb\nc\n")
	if len(got) != 3 || got[0] != "a" || got[2] != "c" {
		t.Errorf("SplitLines = %v, want [a b c]", got)
	}
	// CR/LF normalisation: \r\n → \n.
	got = SplitLines("x\r\ny")
	if len(got) != 2 || got[0] != "x" || got[1] != "y" {
		t.Errorf("SplitLines(crlf) = %v, want [x y]", got)
	}
	if got := SplitLines(""); got != nil {
		t.Errorf("SplitLines(empty) = %v, want nil", got)
	}
}

func TestReadGitHead(t *testing.T) {
	dir := t.TempDir()
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Direct sha (no ref indirection).
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("abc1234567890\n"), 0o644); err != nil {
		t.Fatalf("write HEAD: %v", err)
	}
	if got := ReadGitHead(dir); got != "abc1234567890" {
		t.Errorf("ReadGitHead(direct) = %q, want abc1234567890", got)
	}

	// Ref indirection: HEAD → refs/heads/main → <sha>.
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatalf("write HEAD ref: %v", err)
	}
	refsDir := filepath.Join(gitDir, "refs", "heads")
	if err := os.MkdirAll(refsDir, 0o755); err != nil {
		t.Fatalf("mkdir refs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(refsDir, "main"), []byte("deadbeef\n"), 0o644); err != nil {
		t.Fatalf("write ref: %v", err)
	}
	if got := ReadGitHead(dir); got != "deadbeef" {
		t.Errorf("ReadGitHead(ref) = %q, want deadbeef", got)
	}

	// Missing HEAD → empty string (best-effort, never returns error).
	if got := ReadGitHead(t.TempDir()); got != "" {
		t.Errorf("ReadGitHead(missing) = %q, want empty", got)
	}
}

func TestTarDirectory_StreamsFilesSkipsGit(t *testing.T) {
	// Build a minimal source tree with one regular file, a nested file, and a
	// .git directory that must be excluded (matches the production rule that
	// keeps build contexts slim).
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatalf("write dockerfile: %v", err)
	}
	subDir := filepath.Join(dir, "sub")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte("[core]"), 0o644); err != nil {
		t.Fatalf("write git config: %v", err)
	}

	rc, errCh := TarDirectory(dir)
	defer func() { _ = rc.Close() }()

	tr := tar.NewReader(rc)
	seen := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar next: %v", err)
		}
		seen[hdr.Name] = true
	}
	if err := <-errCh; err != nil {
		t.Fatalf("tar goroutine: %v", err)
	}

	// Dockerfile + sub/main.go must be present; anything under .git must not.
	if !seen["Dockerfile"] {
		t.Errorf("tar missing Dockerfile; got %v", keys(seen))
	}
	if !seen["sub/main.go"] {
		t.Errorf("tar missing sub/main.go; got %v", keys(seen))
	}
	for k := range seen {
		if strings.HasPrefix(k, ".git") {
			t.Errorf("tar must skip .git; saw %q", k)
		}
	}
}

func TestStreamJSONMessage_StreamsAndPublishes(t *testing.T) {
	// streamJSONMessage handles three message shapes emitted by ImageBuild /
	// ImagePull:
	//   1. {"stream":"...\n"}   → split into lines, write to sink.
	//   2. {"status":"Pulling", "id":"abc", "progress":"50%"} → composed line.
	//   3. {"error":"..."}      → write to sink AND return error.
	// We also verify the eventbus publishes a `deploy.log` event per line on
	// "deploy.<deploymentID>.log" — that's what the SSE log endpoint listens
	// to.
	bus := eventbus.New(0, 0)
	const deploymentID = "deployABC"
	ch, unsub := bus.Subscribe("deploy."+deploymentID+".log", "")
	defer unsub()

	e := New(nil, nil, nil, bus)
	var sink bytes.Buffer
	input := strings.NewReader(
		`{"stream":"Step 1/2: FROM scratch\n"}` + "\n" +
			`{"status":"Pulling fs layer","id":"abc","progress":"50%"}` + "\n" +
			`{"stream":"Step 2/2: CMD [\"app\"]\n"}` + "\n",
	)
	if err := StreamJSONMessage(e, input, &sink, BuildOptions{DeploymentID: deploymentID}); err != nil {
		t.Fatalf("streamJSONMessage: %v", err)
	}

	// Sink must include the human-readable transformed lines.
	s := sink.String()
	if !strings.Contains(s, "Step 1/2: FROM scratch") {
		t.Errorf("sink missing step 1: %s", s)
	}
	if !strings.Contains(s, "abc: Pulling fs layer 50%") {
		t.Errorf("sink missing pulling status: %s", s)
	}

	// Bus must publish at least one event of type deploy.log; channel is
	// buffered, so drain a few and check.
	gotEvent := false
	for i := 0; i < 5; i++ {
		select {
		case ev := <-ch:
			if ev.Type == "deploy.log" {
				gotEvent = true
			}
		default:
			break
		}
		if gotEvent {
			break
		}
	}
	if !gotEvent {
		t.Errorf("expected at least one deploy.log event on the bus")
	}
}

func TestStreamJSONMessage_ErrorAborts(t *testing.T) {
	// An {"error":"..."} message must short-circuit and surface as an error.
	e := New(nil, nil, nil, nil)
	var sink bytes.Buffer
	input := strings.NewReader(
		`{"stream":"building\n"}` + "\n" +
			`{"error":"the build failed: missing FROM"}` + "\n",
	)
	err := StreamJSONMessage(e, input, &sink, BuildOptions{DeploymentID: "x"})
	if err == nil {
		t.Fatal("expected error from {\"error\":...}")
	}
	if !strings.Contains(err.Error(), "missing FROM") {
		t.Errorf("error did not bubble message: %v", err)
	}
}

// keys is a tiny helper so test failure messages list the tar entries.
func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// nilProvider satisfies docker.Provider with all methods unwired. We use it
// for tests that exercise the validation surface of Build BEFORE the
// provider's Client is touched (in dockerfile builds that's after the
// inline/repo guard, in docker_image builds after image_name guard).
type nilProvider struct{}

func (nilProvider) Type() string { return "test" }
func (nilProvider) Name() string { return "test" }
func (nilProvider) Client(_ context.Context) (*dockerclient.Client, error) {
	return nil, errors.New("build_test: provider.Client not implemented")
}
func (nilProvider) Close() error { return nil }
