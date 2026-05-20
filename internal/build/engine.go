// Package build owns the "image build" half of a deploy: cloning the repo
// (when applicable), assembling a tar build context, invoking docker build (or
// pulling an existing image), and streaming the output line-by-line to a log
// file + eventbus.
//
// The package is intentionally narrow: it knows about apps, git sources,
// secrets (build-time) and the docker provider, but NOT about deployments,
// containers, or Caddy — those belong to internal/deploy.
package build

import (
	"archive/tar"
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/image"
	"github.com/google/uuid"
	"github.com/prexel/prexel/internal/app"
	"github.com/prexel/prexel/internal/composespec"
	"github.com/prexel/prexel/internal/dockersvc"
	"github.com/prexel/prexel/internal/eventbus"
	"github.com/prexel/prexel/internal/gitsrc"
	"github.com/prexel/prexel/internal/secret"
)

// Engine performs build operations for a single Deploy invocation. It is safe
// for concurrent use across deployments — Build allocates its own tmp dirs.
type Engine struct {
	Git     *gitsrc.Cloner
	GitRepo *gitsrc.Repo
	Secrets *secret.Service
	Bus     *eventbus.Bus
}

// New wires an Engine.
func New(git *gitsrc.Cloner, gitRepo *gitsrc.Repo, secrets *secret.Service, bus *eventbus.Bus) *Engine {
	return &Engine{Git: git, GitRepo: gitRepo, Secrets: secrets, Bus: bus}
}

// Result captures the outcome of a successful build.
type Result struct {
	ImageRef    string // final image tag/ref pushed to the server (e.g. "prexel-myapp-abc1234:1700000000")
	ShortSHA    string // 7-char commit sha (or "inline"/"image" for non-clone builds)
	CommitSHA   string // full commit sha when known
	BuildTimeMs int64
	// Compose carries the parsed compose spec when BuildType is
	// docker_compose. nil for single-container builds.
	Compose *ComposeResult
}

// ComposeResult is the per-service projection produced by a compose
// build. Images is a map of service name -> image ref ready to docker
// run on the target server. Services preserves the original spec so
// the deploy engine can read ports / restart / env without re-parsing
// the YAML twice.
type ComposeResult struct {
	// Images is keyed by service name and holds the ref a `docker run`
	// against the provider will pull from cache (we already pulled
	// each one in the build step, so the runtime is local).
	Images map[string]string
	// Services holds the parsed compose service projection in the
	// order the deploy engine should bring them up. depends_on is
	// honoured (topological sort) so a "web" depending on "db" gets
	// started after "db".
	Services []ComposeServiceSummary
}

// ComposeServiceSummary is the run-time view of one service. Exported
// because the deploy package consumes it directly; field shapes mirror
// composespec.Service for the bits we need at run-time.
type ComposeServiceSummary struct {
	Name    string
	Image   string
	Ports   []int
	Env     map[string]string
	Command []string
	Restart string
}

// LogSink represents anything the engine can write log lines to. The deploy
// engine passes in a file writer; tests can pass io.Discard.
type LogSink interface {
	io.Writer
}

// Build runs the build for `a` and writes streaming output to `sink`.
// `opts` controls per-deploy parameters (commit, branch override). On success
// the returned ImageRef must already exist on the server fronted by `provider`.
type BuildOptions struct {
	DeploymentID string
	// AppID is the app this build belongs to. Carried through so the
	// build engine can include it in eventbus payloads — the SSE
	// handler at /apps/{id}/events filters `deploy.*` events by
	// `payload.app_id == <app>`, and without this every build-log
	// line gets silently dropped client-side. Populated by the
	// deploy engine; CLI/webhook callers don't need to fill it (the
	// log file still gets written; only the live SSE stream cares).
	AppID     string
	Branch    string // overrides app.Branch when non-empty
	CommitSHA string // overrides app.GitCommitSHA when non-empty
	ImageTag  string // for docker_image — overrides app.ImageTag when non-empty
}

// Build executes the build pipeline. The caller is expected to have already
// inserted a `deployments` row and to be holding the per-app deploy lock.
func (e *Engine) Build(ctx context.Context, provider dockersvc.Provider, a *app.App, opts BuildOptions, sink LogSink) (*Result, error) {
	started := time.Now()
	switch a.BuildType {
	case "docker_image":
		return e.pullImage(ctx, provider, a, opts, sink, started)
	case "dockerfile":
		return e.dockerfileBuild(ctx, provider, a, opts, sink, started)
	case "docker_compose":
		return e.composeBuild(ctx, provider, a, opts, sink, started)
	default:
		return nil, fmt.Errorf("build: unsupported build_type %q", a.BuildType)
	}
}

// ---------------------------------------------------------------------------
// docker_image — pull only
// ---------------------------------------------------------------------------

func (e *Engine) pullImage(ctx context.Context, provider dockersvc.Provider, a *app.App, opts BuildOptions, sink LogSink, started time.Time) (*Result, error) {
	if a.ImageName == nil || strings.TrimSpace(*a.ImageName) == "" {
		return nil, errors.New("build: docker_image app missing image_name")
	}
	tag := a.ImageTag
	if opts.ImageTag != "" {
		tag = opts.ImageTag
	}
	if tag == "" {
		tag = "latest"
	}
	ref := strings.TrimSpace(*a.ImageName) + ":" + tag

	e.line(sink, opts, "stdout", "Pulling image "+ref+"...")
	cli, err := provider.Client(ctx)
	if err != nil {
		return nil, fmt.Errorf("build: docker client: %w", err)
	}
	rc, err := cli.ImagePull(ctx, ref, image.PullOptions{})
	if err != nil {
		return nil, fmt.Errorf("build: image pull %q: %w", ref, err)
	}
	defer func() { _ = rc.Close() }()
	if err := e.streamJSONMessage(rc, sink, opts); err != nil {
		return nil, fmt.Errorf("build: stream pull: %w", err)
	}

	e.line(sink, opts, "stdout", "Image pulled: "+ref)
	return &Result{
		ImageRef:    ref,
		ShortSHA:    "image",
		BuildTimeMs: time.Since(started).Milliseconds(),
	}, nil
}

// ---------------------------------------------------------------------------
// dockerfile build — clone (or write inline), tar, build
// ---------------------------------------------------------------------------

func (e *Engine) dockerfileBuild(ctx context.Context, provider dockersvc.Provider, a *app.App, opts BuildOptions, sink LogSink, started time.Time) (*Result, error) {
	tmpDir, err := os.MkdirTemp("", "prexel-build-*")
	if err != nil {
		return nil, fmt.Errorf("build: tmp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	var (
		commitSHA  string
		shortSHA   string
		contextDir string
	)

	hasInline := a.DockerfileInline != nil && strings.TrimSpace(*a.DockerfileInline) != ""
	hasRepo := a.RepoURL != nil && strings.TrimSpace(*a.RepoURL) != ""

	switch {
	case hasInline:
		// Write the inline Dockerfile into tmpDir/Dockerfile; the build context
		// is the same directory.
		dfPath := filepath.Join(tmpDir, "Dockerfile")
		if err := os.WriteFile(dfPath, []byte(*a.DockerfileInline), 0o644); err != nil {
			return nil, fmt.Errorf("build: write inline dockerfile: %w", err)
		}
		contextDir = tmpDir
		shortSHA = "inline"
		e.line(sink, opts, "stdout", "Using inline Dockerfile")
	case hasRepo:
		cloneDir := filepath.Join(tmpDir, "src")
		branch := strings.TrimSpace(opts.Branch)
		if branch == "" {
			branch = a.Branch
		}
		commit := strings.TrimSpace(opts.CommitSHA)
		if commit == "" && a.GitCommitSHA != nil {
			commit = strings.TrimSpace(*a.GitCommitSHA)
		}

		var source *gitsrc.Source
		if a.GitSourceID != nil && strings.TrimSpace(*a.GitSourceID) != "" {
			source, err = e.GitRepo.Get(strings.TrimSpace(*a.GitSourceID))
			if err != nil {
				return nil, fmt.Errorf("build: load git_source: %w", err)
			}
		}

		e.line(sink, opts, "stdout", fmt.Sprintf("Cloning %s (branch=%s)...", *a.RepoURL, branch))
		if source != nil {
			if err := e.Git.Clone(ctx, gitsrc.CloneOptions{
				Source:    source,
				RepoURL:   *a.RepoURL,
				Branch:    branch,
				CommitSHA: commit,
				Dest:      cloneDir,
			}); err != nil {
				return nil, fmt.Errorf("build: clone: %w", err)
			}
		} else {
			// Public repo — clone via plain git command, no credentials.
			if err := publicClone(ctx, *a.RepoURL, branch, commit, cloneDir); err != nil {
				return nil, fmt.Errorf("build: clone (public): %w", err)
			}
		}

		// Resolve commit sha for image tagging.
		commitSHA = strings.TrimSpace(readGitHead(cloneDir))
		if commitSHA == "" {
			commitSHA = commit
		}
		shortSHA = shortenSHA(commitSHA)
		if shortSHA == "" {
			shortSHA = "src"
		}
		contextDir = filepath.Join(cloneDir, a.BuildContext)
		if a.BuildContext == "" || a.BuildContext == "." {
			contextDir = cloneDir
		}
		e.line(sink, opts, "stdout", "Clone complete (commit="+shortSHA+")")
	default:
		return nil, errors.New("build: dockerfile app has neither repo_url nor dockerfile_inline")
	}

	// Build args from build-time secrets — only when the app opts in.
	// BuildArgsInject defaults to true (set by the repo layer); the
	// toggle lets operators skip the secret-resolution step entirely to
	// keep build cache hits intact when secrets churn but the
	// Dockerfile doesn't read them.
	buildArgs := map[string]*string{}
	if a.BuildArgsInject && e.Secrets != nil {
		secs, err := e.Secrets.ResolveBuildTime(ctx, a.ID)
		if err != nil {
			return nil, fmt.Errorf("build: resolve build-time secrets: %w", err)
		}
		for k, v := range secs {
			vv := v
			buildArgs[k] = &vv
		}
	}

	// Optional SOURCE_COMMIT build arg. Off by default because changing
	// it on every commit busts every cache layer downstream of it; opt
	// in per-app when the Dockerfile actually consumes it (e.g. baking
	// a release SHA into the image).
	if a.BuildArgsSourceCommit && commitSHA != "" {
		sc := commitSHA
		buildArgs["SOURCE_COMMIT"] = &sc
	}

	// Tag.
	imageRef := imageRefFor(a.Name, shortSHA)

	dockerfile := a.DockerfilePath
	if hasInline {
		dockerfile = "Dockerfile"
	}

	if err := e.buildImageFromContext(ctx, provider, a, opts, sink, buildImageInput{
		ContextDir: contextDir,
		Dockerfile: dockerfile,
		ImageRef:   imageRef,
		BuildArgs:  buildArgs,
		Labels:     nil, // dockerfileBuild uses the default app-level labels.
	}); err != nil {
		return nil, err
	}

	return &Result{
		ImageRef:    imageRef,
		ShortSHA:    shortSHA,
		CommitSHA:   commitSHA,
		BuildTimeMs: time.Since(started).Milliseconds(),
	}, nil
}

// buildImageInput is the per-invocation knobs for buildImageFromContext.
// Kept as a struct because the args list was getting wide (context dir,
// dockerfile path, tag, build args, extra labels) and the call site is
// the deploy-engine seam where readability matters.
type buildImageInput struct {
	ContextDir string
	Dockerfile string
	ImageRef   string
	BuildArgs  map[string]*string
	// Labels merged with the always-present prexel.* labels. Compose
	// services pass `prexel.service` here so the running container is
	// traceable back to the YAML.
	Labels map[string]string
}

// buildImageFromContext tars the build context, hands it to
// `docker build`, and streams the build output. Shared between
// `dockerfileBuild` (single-container apps) and `composeBuild`
// (per-service `build:` directives in compose YAML).
//
// On failure returns a wrapped error; on success the image with
// tag `in.ImageRef` exists on the target provider.
func (e *Engine) buildImageFromContext(ctx context.Context, provider dockersvc.Provider, a *app.App, opts BuildOptions, sink LogSink, in buildImageInput) error {
	dockerfile := in.Dockerfile
	if dockerfile == "" {
		dockerfile = "Dockerfile"
	}

	tarStream, errCh := tarDirectory(in.ContextDir)
	defer func() { _ = tarStream.Close() }()

	cli, err := provider.Client(ctx)
	if err != nil {
		return fmt.Errorf("build: docker client: %w", err)
	}

	// prexel.* labels are merged LAST so operator-supplied labels
	// can't shadow our ownership keys (the exec/logs handlers gate
	// on prexel.app_id).
	labels := map[string]string{}
	for k, v := range in.Labels {
		labels[k] = v
	}
	labels["prexel.managed"] = "true"
	labels["prexel.app_id"] = a.ID
	labels["prexel.app_name"] = a.Name
	labels["prexel.deployment"] = opts.DeploymentID

	e.line(sink, opts, "stdout", "Starting docker build (tag="+in.ImageRef+")...")
	resp, err := cli.ImageBuild(ctx, tarStream, types.ImageBuildOptions{
		Tags:        []string{in.ImageRef},
		Dockerfile:  dockerfile,
		BuildArgs:   in.BuildArgs,
		Remove:      true,
		ForceRemove: true,
		PullParent:  false,
		Labels:      labels,
	})
	if err != nil {
		return fmt.Errorf("build: image build: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if err := e.streamJSONMessage(resp.Body, sink, opts); err != nil {
		return fmt.Errorf("build: stream build output: %w", err)
	}
	select {
	case terr := <-errCh:
		if terr != nil {
			return fmt.Errorf("build: tar build context: %w", terr)
		}
	default:
	}

	e.line(sink, opts, "stdout", "Build complete: "+in.ImageRef)
	return nil
}

// ---------------------------------------------------------------------------
// docker_compose — parse spec, pull/build each service
// ---------------------------------------------------------------------------

// composeBuild reads the app's compose YAML (inline or cloned from
// the repo), parses it, and prepares each service's image on the
// target provider.
//
// Per-service behaviour:
//   - `image:` → pull the upstream ref as-is
//   - `build:` → tar the build context (resolved relative to the
//     compose file's directory) and run docker build, tagging the
//     result `prexel-<app>-<service>-<short>:<ts>`
//   - both — `image:` takes precedence (matches docker-compose:
//     image is used when present, build is fallback for missing tags)
//
// Pulled images keep their upstream ref. Built images get a
// Prexel-managed tag so the deploy engine can address them without
// confusion when the same upstream tag changes between deploys.
func (e *Engine) composeBuild(ctx context.Context, provider dockersvc.Provider, a *app.App, opts BuildOptions, sink LogSink, started time.Time) (*Result, error) {
	loaded, err := e.loadComposeWorkspace(ctx, a, opts, sink)
	if err != nil {
		return nil, err
	}
	defer loaded.cleanup()

	spec, err := composespec.Parse(loaded.yaml)
	if err != nil {
		return nil, fmt.Errorf("build: parse compose: %w", err)
	}
	if len(spec.Services) == 0 {
		return nil, errors.New("build: compose file has no services")
	}

	// Reject services that have NEITHER image nor build. compose
	// itself would error too — surface it here so the operator gets
	// a clean message before we burn time on pulls/builds.
	var emptyServices []string
	for _, s := range spec.Services {
		if s.Image == "" && s.Build == nil {
			emptyServices = append(emptyServices, s.Name)
		}
	}
	if len(emptyServices) > 0 {
		return nil, fmt.Errorf(
			"build: services with no `image:` or `build:`: %s",
			strings.Join(emptyServices, ", "),
		)
	}

	// `build:` requires the cloned working tree to tar build contexts.
	// `compose_inline` apps can't have `build:` because there's no
	// source tree to read from — reject up front with a clear hint.
	if loaded.composeDir == "" {
		for _, s := range spec.Services {
			if s.Image == "" && s.Build != nil {
				return nil, fmt.Errorf(
					"build: service %q uses `build:` but app is configured with compose_inline (no source tree) — set repo_url + compose_file or pre-build the image",
					s.Name,
				)
			}
		}
	}

	cli, err := provider.Client(ctx)
	if err != nil {
		return nil, fmt.Errorf("build: docker client: %w", err)
	}

	// Build-time secrets resolved once and shared across every
	// `build:` service. Matches the single-container behaviour: app
	// owns the secrets, individual services don't.
	sharedBuildArgs := map[string]*string{}
	if a.BuildArgsInject && e.Secrets != nil {
		secs, secErr := e.Secrets.ResolveBuildTime(ctx, a.ID)
		if secErr != nil {
			return nil, fmt.Errorf("build: resolve build-time secrets: %w", secErr)
		}
		for k, v := range secs {
			vv := v
			sharedBuildArgs[k] = &vv
		}
	}
	if a.BuildArgsSourceCommit && loaded.commitSHA != "" {
		sc := loaded.commitSHA
		sharedBuildArgs["SOURCE_COMMIT"] = &sc
	}

	images := make(map[string]string, len(spec.Services))
	summaries := make([]ComposeServiceSummary, 0, len(spec.Services))
	shortSHA := shortenSHA(loaded.commitSHA)
	if shortSHA == "" {
		shortSHA = "compose"
	}

	for _, s := range spec.Services {
		// Prefer image when both are set (docker-compose semantics:
		// `image:` is the pre-built ref to use; `build:` is the
		// recipe for rebuilding it locally).
		switch {
		case s.Image != "":
			ref := strings.TrimSpace(s.Image)
			e.line(sink, opts, "stdout", fmt.Sprintf("[%s] pulling %s...", s.Name, ref))
			rc, perr := cli.ImagePull(ctx, ref, image.PullOptions{})
			if perr != nil {
				return nil, fmt.Errorf("build: pull %s (%s): %w", s.Name, ref, perr)
			}
			if perr := e.streamJSONMessage(rc, sink, opts); perr != nil {
				_ = rc.Close()
				return nil, fmt.Errorf("build: stream pull %s: %w", s.Name, perr)
			}
			_ = rc.Close()
			e.line(sink, opts, "stdout", fmt.Sprintf("[%s] image ready: %s", s.Name, ref))
			images[s.Name] = ref

		case s.Build != nil:
			imageRef, bErr := e.buildComposeService(ctx, provider, a, opts, sink, loaded, s, shortSHA, sharedBuildArgs)
			if bErr != nil {
				return nil, bErr
			}
			images[s.Name] = imageRef
		}

		summaries = append(summaries, ComposeServiceSummary{
			Name:    s.Name,
			Image:   images[s.Name],
			Ports:   s.Ports,
			Env:     s.Env,
			Command: s.Command,
			Restart: s.Restart,
		})
	}

	// ImageRef on the multi-service Result is the first service's
	// image — kept so the deployments table's `image_tag` column,
	// which is single-valued, still gets something useful. The
	// authoritative per-service map lives on Compose.Images.
	first := ""
	if len(summaries) > 0 {
		first = summaries[0].Image
	}

	return &Result{
		ImageRef:    first,
		ShortSHA:    shortSHA,
		CommitSHA:   loaded.commitSHA,
		BuildTimeMs: time.Since(started).Milliseconds(),
		Compose: &ComposeResult{
			Images:   images,
			Services: summaries,
		},
	}, nil
}

// buildComposeService runs a single `docker build` for one service in
// a compose file. Returns the freshly-tagged image ref. `composeDir`
// is the directory holding the compose YAML — service build contexts
// are resolved relative to it, matching docker-compose semantics.
func (e *Engine) buildComposeService(
	ctx context.Context,
	provider dockersvc.Provider,
	a *app.App,
	opts BuildOptions,
	sink LogSink,
	loaded *composeWorkspace,
	svc composespec.Service,
	shortSHA string,
	sharedBuildArgs map[string]*string,
) (string, error) {
	if svc.Build == nil {
		return "", fmt.Errorf("build: service %q has no build block", svc.Name)
	}

	// Resolve context. compose semantics: context is relative to
	// the directory of the compose file. Empty → ".".
	ctxRel := strings.TrimSpace(svc.Build.Context)
	if ctxRel == "" {
		ctxRel = "."
	}
	contextDir := filepath.Join(loaded.composeDir, ctxRel)

	// Defensive — prevent build contexts escaping the clone. Compose
	// `context: ../../../etc` would otherwise let a malicious YAML
	// reach the docker host's filesystem.
	if rel, err := filepath.Rel(loaded.composeDir, contextDir); err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("build: service %q build context escapes the repo: %s", svc.Name, ctxRel)
	}
	if st, err := os.Stat(contextDir); err != nil || !st.IsDir() {
		return "", fmt.Errorf("build: service %q build context not found: %s", svc.Name, ctxRel)
	}

	dockerfile := strings.TrimSpace(svc.Build.Dockerfile)
	if dockerfile == "" {
		dockerfile = "Dockerfile"
	}

	// Compose `args:` layered on top of shared (app-level) build args.
	// Compose args win on conflict — matches the operator's mental
	// model that anything in the YAML is more specific than instance
	// defaults.
	buildArgs := map[string]*string{}
	for k, v := range sharedBuildArgs {
		buildArgs[k] = v
	}
	for k, v := range svc.Build.Args {
		vv := v
		buildArgs[k] = &vv
	}

	imageRef := composeImageRefFor(a.Name, svc.Name, shortSHA)
	e.line(sink, opts, "stdout", fmt.Sprintf("[%s] building %s (context=%s, dockerfile=%s)", svc.Name, imageRef, ctxRel, dockerfile))

	if err := e.buildImageFromContext(ctx, provider, a, opts, sink, buildImageInput{
		ContextDir: contextDir,
		Dockerfile: dockerfile,
		ImageRef:   imageRef,
		BuildArgs:  buildArgs,
		Labels: map[string]string{
			"prexel.compose": "true",
			"prexel.service": svc.Name,
		},
	}); err != nil {
		return "", fmt.Errorf("build: service %q: %w", svc.Name, err)
	}
	return imageRef, nil
}

// composeWorkspace is the on-disk state a compose build needs to keep
// around for the duration of the build (so it can tar service build
// contexts). Cleanup MUST be called by the caller.
type composeWorkspace struct {
	yaml []byte
	// composeDir is the directory containing the compose file inside
	// the clone (or "" for inline compose, which has no source tree).
	composeDir string
	commitSHA  string
	cleanup    func()
}

// loadComposeWorkspace resolves the compose YAML + (when applicable)
// keeps the clone around so the caller can build per-service contexts.
//
// Three sources, in priority order:
//  1. app.ComposeInline — operator typed YAML directly. cleanup is a
//     no-op; composeDir is "" so any `build:` references error out
//     with a clear message (we have no source tree to point at).
//  2. app.RepoURL + app.ComposeFile — clone the repo and locate the
//     compose file in the working tree. composeDir is the dir holding
//     the YAML so service build contexts can resolve relative paths.
//  3. neither → error.
func (e *Engine) loadComposeWorkspace(ctx context.Context, a *app.App, opts BuildOptions, sink LogSink) (*composeWorkspace, error) {
	if a.ComposeInline != nil && strings.TrimSpace(*a.ComposeInline) != "" {
		e.line(sink, opts, "stdout", "Using inline docker-compose.yml")
		return &composeWorkspace{
			yaml:    []byte(*a.ComposeInline),
			cleanup: func() {},
		}, nil
	}
	if a.RepoURL == nil || strings.TrimSpace(*a.RepoURL) == "" {
		return nil, errors.New("build: compose app has neither compose_inline nor repo_url")
	}

	tmpDir, err := os.MkdirTemp("", "prexel-compose-*")
	if err != nil {
		return nil, fmt.Errorf("build: tmp dir: %w", err)
	}
	// The caller owns the cleanup — keeps the working tree alive
	// across the per-service build calls below.
	cleanup := func() { _ = os.RemoveAll(tmpDir) }

	cloneDir := filepath.Join(tmpDir, "src")
	branch := strings.TrimSpace(opts.Branch)
	if branch == "" {
		branch = a.Branch
	}
	commit := strings.TrimSpace(opts.CommitSHA)
	if commit == "" && a.GitCommitSHA != nil {
		commit = strings.TrimSpace(*a.GitCommitSHA)
	}

	var source *gitsrc.Source
	if a.GitSourceID != nil && strings.TrimSpace(*a.GitSourceID) != "" {
		source, err = e.GitRepo.Get(strings.TrimSpace(*a.GitSourceID))
		if err != nil {
			cleanup()
			return nil, fmt.Errorf("build: load git_source: %w", err)
		}
	}

	e.line(sink, opts, "stdout", fmt.Sprintf("Cloning %s (branch=%s)...", *a.RepoURL, branch))
	if source != nil {
		if err := e.Git.Clone(ctx, gitsrc.CloneOptions{
			Source:    source,
			RepoURL:   *a.RepoURL,
			Branch:    branch,
			CommitSHA: commit,
			Dest:      cloneDir,
		}); err != nil {
			cleanup()
			return nil, fmt.Errorf("build: clone: %w", err)
		}
	} else {
		if err := publicClone(ctx, *a.RepoURL, branch, commit, cloneDir); err != nil {
			cleanup()
			return nil, fmt.Errorf("build: clone (public): %w", err)
		}
	}

	commitSHA := strings.TrimSpace(readGitHead(cloneDir))
	if commitSHA == "" {
		commitSHA = commit
	}

	composeRel := strings.TrimSpace(derefString(a.ComposeFile))
	if composeRel == "" {
		composeRel = "docker-compose.yml"
	}
	composePath := filepath.Join(cloneDir, composeRel)
	data, err := os.ReadFile(composePath)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("build: read compose file %q: %w", composeRel, err)
	}
	e.line(sink, opts, "stdout", fmt.Sprintf("Loaded %s (%d bytes, commit=%s)", composeRel, len(data), shortenSHA(commitSHA)))
	return &composeWorkspace{
		yaml:       data,
		composeDir: filepath.Dir(composePath),
		commitSHA:  commitSHA,
		cleanup:    cleanup,
	}, nil
}

// composeImageRefFor builds the Prexel-managed tag for a service we
// just built. Includes service name so two services in the same app
// don't collide on the same tag.
func composeImageRefFor(appName, serviceName, shortSHA string) string {
	ts := time.Now().Unix()
	return fmt.Sprintf("prexel-%s-%s-%s:%d", appName, serviceName, shortSHA, ts)
}

// derefString is a local nil-safe deref for *string.
func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// streamJSONMessage parses the Docker JSON-stream emitted by ImageBuild /
// ImagePull and writes a human-readable line per record to sink + eventbus.
func (e *Engine) streamJSONMessage(rc io.Reader, sink LogSink, opts BuildOptions) error {
	scanner := bufio.NewScanner(rc)
	scanner.Buffer(make([]byte, 0, 1<<16), 1<<22)
	for scanner.Scan() {
		raw := scanner.Bytes()
		if len(raw) == 0 {
			continue
		}
		var msg jsonMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			// Not JSON — emit raw.
			e.line(sink, opts, "stdout", string(raw))
			continue
		}
		if msg.Error != "" {
			e.line(sink, opts, "stderr", msg.Error)
			return errors.New(msg.Error)
		}
		switch {
		case msg.Stream != "":
			for _, l := range splitLines(msg.Stream) {
				if l == "" {
					continue
				}
				e.line(sink, opts, "stdout", l)
			}
		case msg.Status != "":
			s := msg.Status
			if msg.ID != "" {
				s = msg.ID + ": " + s
			}
			if msg.Progress != "" {
				s += " " + msg.Progress
			}
			e.line(sink, opts, "stdout", s)
		case msg.Aux != nil:
			// auxiliary objects (e.g. final image id) — skip.
		}
	}
	return scanner.Err()
}

// line writes a single log line to sink, prefixed with a timestamp, and
// publishes it on the eventbus.
func (e *Engine) line(sink LogSink, opts BuildOptions, stream, text string) {
	text = strings.TrimRight(text, "\r\n")
	if text == "" {
		return
	}
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	if sink != nil {
		_, _ = fmt.Fprintf(sink, "%s [%s] %s\n", ts, stream, text)
	}
	if e.Bus != nil && opts.DeploymentID != "" {
		// Topic carries deployment id so subscribers that want a
		// per-deploy stream can filter on prefix; the payload carries
		// app_id because the per-app SSE endpoint
		// (/apps/{id}/events) filters `deploy.*` events by
		// payload.app_id (see internal/api/handler/event.go). Without
		// app_id in the payload, the live build feed never reaches
		// the deployment-detail terminal.
		e.Bus.Publish(
			"deploy."+opts.DeploymentID+".log",
			"deploy.log",
			map[string]any{
				"app_id":        opts.AppID,
				"deployment_id": opts.DeploymentID,
				"stream":        stream,
				"line":          text,
				"ts":            ts,
			},
		)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

type jsonMessage struct {
	Stream      string `json:"stream,omitempty"`
	Status      string `json:"status,omitempty"`
	ID          string `json:"id,omitempty"`
	Progress    string `json:"progress,omitempty"`
	Error       string `json:"error,omitempty"`
	ErrorDetail *struct {
		Message string `json:"message"`
	} `json:"errorDetail,omitempty"`
	Aux json.RawMessage `json:"aux,omitempty"`
}

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func shortenSHA(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 7 {
		return s[:7]
	}
	return s
}

func imageRefFor(appName, shortSHA string) string {
	if shortSHA == "" {
		shortSHA = uuid.NewString()[:7]
	}
	return "prexel-" + appName + "-" + shortSHA + ":" + strconv.FormatInt(time.Now().Unix(), 10)
}

// readGitHead returns the resolved HEAD commit sha of the given clone (the
// content of .git/HEAD or the resolved ref). Best-effort — empty on failure.
func readGitHead(cloneDir string) string {
	out, err := os.ReadFile(filepath.Join(cloneDir, ".git", "HEAD"))
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(out))
	if strings.HasPrefix(s, "ref:") {
		ref := strings.TrimSpace(strings.TrimPrefix(s, "ref:"))
		b, err := os.ReadFile(filepath.Join(cloneDir, ".git", ref))
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(b))
	}
	return s
}

// publicClone runs a credential-less git clone for public repos. Used when
// the app has no git_source_id attached.
func publicClone(ctx context.Context, repoURL, branch, commitSHA, dest string) error {
	args := []string{"clone", "--branch", branch, "--single-branch", "--depth", "1", repoURL, dest}
	cmd := osexec.CommandContext(ctx, "git", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git clone: %w: %s", err, string(out))
	}
	if commitSHA != "" {
		// Need to fetch the full history to checkout an arbitrary sha. Drop
		// the shallow clone first.
		if out, err := osexec.CommandContext(ctx, "git", "-C", dest, "fetch", "--unshallow").CombinedOutput(); err != nil {
			return fmt.Errorf("git fetch unshallow: %w: %s", err, string(out))
		}
		if out, err := osexec.CommandContext(ctx, "git", "-C", dest, "checkout", commitSHA).CombinedOutput(); err != nil {
			return fmt.Errorf("git checkout %s: %w: %s", commitSHA, err, string(out))
		}
	}
	return nil
}

// tarDirectory streams `dir` as a tar archive on the returned reader. Errors
// surface on the error channel after the stream is closed.
func tarDirectory(dir string) (io.ReadCloser, <-chan error) {
	pr, pw := io.Pipe()
	errCh := make(chan error, 1)
	go func() {
		tw := tar.NewWriter(pw)
		err := filepath.Walk(dir, func(path string, fi os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			rel, err := filepath.Rel(dir, path)
			if err != nil {
				return err
			}
			if rel == "." {
				return nil
			}
			// Skip .git directory — large and unneeded inside the image.
			if rel == ".git" || strings.HasPrefix(rel, ".git"+string(filepath.Separator)) {
				if fi.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			link := ""
			if fi.Mode()&os.ModeSymlink != 0 {
				l, err := os.Readlink(path)
				if err != nil {
					return err
				}
				link = l
			}
			hdr, err := tar.FileInfoHeader(fi, link)
			if err != nil {
				return err
			}
			hdr.Name = filepath.ToSlash(rel)
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
			if fi.Mode().IsRegular() {
				f, err := os.Open(path)
				if err != nil {
					return err
				}
				_, copyErr := io.Copy(tw, f)
				_ = f.Close()
				if copyErr != nil {
					return copyErr
				}
			}
			return nil
		})
		_ = tw.Close()
		_ = pw.CloseWithError(err)
		errCh <- err
		close(errCh)
	}()
	return pr, errCh
}
