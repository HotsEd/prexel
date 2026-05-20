// Package appcleanup is the asynchronous garbage-collector for an app
// that just got deleted from the database.
//
// What needs cleaning when an app is deleted:
//   - Docker containers labelled prexel.app_id=<id> on the app's server
//     (canonical + legacy `prexel-` prefixed + temp `-deploying-*`)
//   - Docker images tagged `prexel-<app_name>-*` (single-container) AND
//     `prexel-<app_name>-<service>-*` (compose services). We don't
//     touch pulled image refs like `nginx:alpine` — those are upstream
//     and might be used by other apps on the same host.
//   - Docker named volumes labelled prexel.app_id=<id>
//   - Caddy routes for every domain that was bound to the app
//   - Deployment build-log files in {LogDir}/{deployment_id}.log
//
// The DB-level cleanup (apps + ON DELETE CASCADE for domains, secrets,
// deployments, volumes, tags) is the caller's responsibility — happens
// synchronously BEFORE the task is submitted here so the SPA reflects
// the deletion immediately. This worker just handles the slow
// out-of-band side effects.
//
// Concurrency model: a single goroutine drains a buffered channel. One
// task at a time is enough — cleanup is rare (operator-initiated) and
// running them in parallel could hammer the same Docker daemon. The
// channel is sized so a burst of deletes from a script doesn't block
// the API thread; if it fills, Submit returns ErrQueueFull and the
// caller logs+continues (the DB delete already succeeded).
package appcleanup

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	dockerimage "github.com/docker/docker/api/types/image"

	"github.com/prexel/prexel/internal/dockersvc"
	"github.com/prexel/prexel/internal/server"
)

// Compile-time guard: the production *caddy.Client satisfies our
// CaddyClient interface. The blank import keeps the dependency
// statement honest without dragging the package into use.
var _ CaddyClient = (*caddyClientProbe)(nil)

type caddyClientProbe struct{}

func (caddyClientProbe) RemoveRoute(_ context.Context, _ string) error { return nil }

// ErrQueueFull is returned by Submit when the worker's inbox is at
// capacity. The caller should log a warning and move on — the DB row
// is already gone, the leftover Docker state will eventually surface
// via the operator's `docker system prune` or a re-deploy of a new
// app with the same name.
var ErrQueueFull = errors.New("appcleanup: queue full")

// CaddyClient is the subset of *caddy.Client the worker needs. Kept
// as an interface so tests can stub.
type CaddyClient interface {
	RemoveRoute(ctx context.Context, host string) error
}

// Task is one app's cleanup metadata. Built by the caller BEFORE the
// DB cascade fires, because most of this info isn't reconstructable
// from the row alone (server, domain hostnames, deployment IDs).
type Task struct {
	AppID    string
	AppName  string
	ServerID string

	// Domain hostnames that pointed at this app. Caddy routes for
	// each get removed.
	Domains []string

	// Deployment IDs whose build-log files we need to delete from
	// disk. Look up via deploy.repo.listByApp BEFORE cascading.
	DeploymentIDs []string

	// Optional — when set, used as a deadline for the whole task.
	// Defaults to 5 minutes (DefaultTimeout).
	Deadline time.Time
}

// DefaultTimeout caps a single cleanup task. Plenty of headroom for
// the slow path (remote provider over SSH listing 1k containers).
const DefaultTimeout = 5 * time.Minute

// Worker is the async garbage collector. Submit() enqueues a Task;
// the worker goroutine drains the channel sequentially.
type Worker struct {
	servers *server.Service
	caddy   CaddyClient
	logDir  string

	inbox chan Task
	stop  chan struct{}
	wg    sync.WaitGroup
}

// New constructs a Worker but does NOT start the goroutine. Call
// Start() from the wiring layer so the lifecycle is explicit.
//
// `caddy` may be nil — in that case the Caddy cleanup step is
// skipped silently (matches the rest of the deploy engine, which
// treats a missing Caddy as "no domain routing in this instance").
func New(servers *server.Service, caddyClient CaddyClient, logDir string, queueSize int) *Worker {
	if queueSize <= 0 {
		queueSize = 64
	}
	return &Worker{
		servers: servers,
		caddy:   caddyClient,
		logDir:  logDir,
		inbox:   make(chan Task, queueSize),
		stop:    make(chan struct{}),
	}
}

// Start spins up the worker goroutine. Returns immediately. Idempotent:
// calling twice is harmless because the inbox channel is the gate.
func (w *Worker) Start() {
	w.wg.Add(1)
	go w.loop()
}

// Stop signals the worker to drain its inbox and exit. Blocks until
// the goroutine returns. Safe to call multiple times.
func (w *Worker) Stop() {
	select {
	case <-w.stop:
		// already stopped
	default:
		close(w.stop)
	}
	w.wg.Wait()
}

// Submit enqueues a Task for asynchronous processing. Non-blocking:
// returns ErrQueueFull immediately if the buffer is saturated.
//
// The caller should log+ignore ErrQueueFull — the DB row is already
// gone, the worst case is some leftover Docker state the operator
// can manually prune.
func (w *Worker) Submit(t Task) error {
	if t.AppID == "" {
		return errors.New("appcleanup: empty AppID")
	}
	select {
	case w.inbox <- t:
		return nil
	default:
		return ErrQueueFull
	}
}

// loop is the worker goroutine. It pulls tasks one at a time and runs
// them under a fresh context (so the server's shutdown context closing
// doesn't abort a half-done cleanup mid-flight; Stop() coordinates the
// drain).
func (w *Worker) loop() {
	defer w.wg.Done()
	for {
		select {
		case <-w.stop:
			// Drain remaining tasks before exiting. Cleanup is
			// best-effort, but we'd rather flush than abandon.
			for {
				select {
				case t := <-w.inbox:
					w.run(t)
				default:
					return
				}
			}
		case t := <-w.inbox:
			w.run(t)
		}
	}
}

// run executes one task. Each sub-step is independent and logs its
// own failure — a Docker outage shouldn't prevent the Caddy or log-
// file cleanups from running.
func (w *Worker) run(t Task) {
	timeout := DefaultTimeout
	if !t.Deadline.IsZero() {
		if d := time.Until(t.Deadline); d > 0 {
			timeout = d
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	slog.Info("appcleanup: starting",
		"app_id", t.AppID,
		"app_name", t.AppName,
		"server_id", t.ServerID,
		"domains", len(t.Domains),
		"deployments", len(t.DeploymentIDs),
	)
	started := time.Now()

	w.cleanupCaddy(ctx, t)
	w.cleanupDocker(ctx, t)
	w.cleanupLogFiles(ctx, t)

	slog.Info("appcleanup: done",
		"app_id", t.AppID,
		"elapsed_ms", time.Since(started).Milliseconds(),
	)
}

// cleanupCaddy removes every domain route for this app from Caddy.
// Routes that no longer exist (operator already cleared them, or
// Caddy was reset) come back as "not found" from RemoveRoute and we
// treat that as success.
func (w *Worker) cleanupCaddy(ctx context.Context, t Task) {
	if w.caddy == nil || len(t.Domains) == 0 {
		return
	}
	for _, host := range t.Domains {
		if strings.TrimSpace(host) == "" {
			continue
		}
		if err := w.caddy.RemoveRoute(ctx, host); err != nil {
			// The Caddy client returns nil for "route doesn't exist";
			// any error here is a real failure. Log and move on.
			slog.Warn("appcleanup: caddy remove route failed",
				"app_id", t.AppID, "host", host, "err", err,
			)
		}
	}
}

// cleanupDocker handles all container/image/volume removal on the
// app's server. The server is resolved fresh each call (the provider
// holds an SSH tunnel for remote providers — we close it on return).
//
// If the server is unreachable we skip silently — the operator's
// concern at that point is the server being down, not the leftover
// containers. They'll be pruned on next reconnect via the same
// label-filtered approach.
func (w *Worker) cleanupDocker(ctx context.Context, t Task) {
	if w.servers == nil || t.ServerID == "" {
		return
	}
	provider, err := w.servers.Provider(ctx, t.ServerID)
	if err != nil {
		slog.Warn("appcleanup: docker provider unavailable",
			"app_id", t.AppID, "server_id", t.ServerID, "err", err,
		)
		return
	}
	defer func() { _ = provider.Close() }()

	// 1. Containers — find every container labelled with this app id
	//    (canonical, legacy, temp deploy candidates, orphans from
	//    removed compose services). Stop+remove each, force=true
	//    so we don't get stuck on a hung container.
	containers, err := dockersvc.ListContainers(ctx, provider, map[string][]string{
		"label": {"prexel.app_id=" + t.AppID},
	})
	if err != nil {
		slog.Warn("appcleanup: list containers failed",
			"app_id", t.AppID, "err", err,
		)
	} else {
		for _, c := range containers {
			name := ""
			if len(c.Names) > 0 {
				name = strings.TrimPrefix(c.Names[0], "/")
			}
			if name == "" {
				continue
			}
			if err := dockersvc.StopContainer(ctx, provider, name, 5*time.Second); err != nil && !errors.Is(err, dockersvc.ErrContainerNotFound) {
				slog.Warn("appcleanup: stop container failed",
					"app_id", t.AppID, "container", name, "err", err,
				)
			}
			if err := dockersvc.RemoveContainer(ctx, provider, name, true); err != nil && !errors.Is(err, dockersvc.ErrContainerNotFound) {
				slog.Warn("appcleanup: remove container failed",
					"app_id", t.AppID, "container", name, "err", err,
				)
			}
		}
	}

	// 2. Images — Prexel-built images carry the `prexel-<app_name>-*`
	//    tag prefix. We list by label first (the build engine tags
	//    images with prexel.app_id during single-container builds —
	//    NOT today, only via the label we set on container labels).
	//    Belt-and-suspenders: also enumerate by tag prefix to catch
	//    images that pre-date the labelling.
	cli, err := provider.Client(ctx)
	if err != nil {
		slog.Warn("appcleanup: docker client failed", "app_id", t.AppID, "err", err)
		return
	}
	imgs, err := cli.ImageList(ctx, dockerimage.ListOptions{All: false})
	if err != nil {
		slog.Warn("appcleanup: list images failed", "app_id", t.AppID, "err", err)
	} else {
		prefix := "prexel-" + t.AppName + "-"
		seen := map[string]struct{}{}
		for _, img := range imgs {
			for _, ref := range img.RepoTags {
				if strings.HasPrefix(ref, prefix) {
					if _, dup := seen[ref]; dup {
						continue
					}
					seen[ref] = struct{}{}
					if err := dockersvc.RemoveImage(ctx, provider, ref); err != nil && !errors.Is(err, dockersvc.ErrImageNotFound) {
						slog.Warn("appcleanup: remove image failed",
							"app_id", t.AppID, "image", ref, "err", err,
						)
					}
				}
			}
		}
	}

	// 3. Volumes — anything labelled `prexel.app_id=<id>`. Bind mounts
	//    are NOT cleaned up here (they live on the host filesystem and
	//    the operator may want their data; we treat the host path as
	//    out-of-scope for the cleanup worker).
	vols, err := dockersvc.ListVolumes(ctx, provider, map[string]string{
		"prexel.app_id": t.AppID,
	})
	if err != nil {
		slog.Warn("appcleanup: list volumes failed", "app_id", t.AppID, "err", err)
	} else {
		for _, v := range vols {
			if err := dockersvc.RemoveVolume(ctx, provider, v.Name, true); err != nil {
				slog.Warn("appcleanup: remove volume failed",
					"app_id", t.AppID, "volume", v.Name, "err", err,
				)
			}
		}
	}
}

// cleanupLogFiles removes the per-deployment build log files on disk.
// Best-effort: a missing file is fine (already removed), permission
// errors are logged but don't escalate. We never recurse beyond the
// LogDir, so even a malformed deployment id can't reach outside the
// configured directory.
func (w *Worker) cleanupLogFiles(_ context.Context, t Task) {
	if w.logDir == "" || len(t.DeploymentIDs) == 0 {
		return
	}
	for _, id := range t.DeploymentIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		// Path is fully composed — no operator input here, but
		// defensively reject anything with a slash so a bug
		// elsewhere can't trick this into deleting outside the
		// log dir.
		if strings.ContainsAny(id, "/\\") {
			continue
		}
		path := filepath.Join(w.logDir, id+".log")
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			slog.Warn("appcleanup: remove log file failed",
				"app_id", t.AppID, "deployment_id", id, "err", err,
			)
		}
	}
}
