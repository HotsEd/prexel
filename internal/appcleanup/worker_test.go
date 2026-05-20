package appcleanup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeCaddy is a CaddyClient stub the tests pre-load with desired
// behaviour. It records every host RemoveRoute saw — enough for
// most assertions — and can be wired to return an arbitrary error
// per host via removeErr.
type fakeCaddy struct {
	mu        sync.Mutex
	removed   []string
	removeErr map[string]error
}

func (f *fakeCaddy) RemoveRoute(_ context.Context, host string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, host)
	if err, ok := f.removeErr[host]; ok {
		return err
	}
	return nil
}

func (f *fakeCaddy) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.removed))
	copy(out, f.removed)
	return out
}

// newTestWorker builds a Worker with no server.Service (so docker
// cleanup is skipped) and a tempdir log directory. caddy is the
// fakeCaddy supplied by the caller — pass nil to skip the Caddy step.
func newTestWorker(t *testing.T, caddy CaddyClient, queueSize int) *Worker {
	t.Helper()
	w := New(nil, caddy, t.TempDir(), queueSize)
	w.Start()
	t.Cleanup(w.Stop)
	return w
}

// waitFor blocks until pred() is true or the deadline expires. Used
// by the queue tests to keep timing-tight assertions readable.
func waitFor(t *testing.T, pred func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if pred() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("waitFor timeout: %s", msg)
}

// TestWorkerSubmitAndDrain — submit N tasks, every one should run.
// We count Caddy RemoveRoute calls (one per task here, since each
// task carries one domain) instead of intercepting `run` directly.
func TestWorkerSubmitAndDrain(t *testing.T) {
	caddy := &fakeCaddy{}
	w := newTestWorker(t, caddy, 16)

	for i := 0; i < 3; i++ {
		err := w.Submit(Task{
			AppID:   "app-" + string(rune('a'+i)),
			AppName: "app-" + string(rune('a'+i)),
			Domains: []string{"host-" + string(rune('a'+i)) + ".example.com"},
		})
		if err != nil {
			t.Fatalf("Submit %d: %v", i, err)
		}
	}
	waitFor(t, func() bool { return len(caddy.snapshot()) == 3 }, "3 tasks to land")
}

// TestWorkerQueueFull — once the buffered channel is saturated, the
// nth+1 Submit must return ErrQueueFull synchronously. We block the
// worker on a slow Caddy stub so the channel fills up.
func TestWorkerQueueFull(t *testing.T) {
	// Caddy stub that blocks until released — lets us hold the worker
	// goroutine in a single run call while we cram the inbox.
	release := make(chan struct{})
	slowCaddy := &slowCaddyStub{release: release}

	w := New(nil, slowCaddy, t.TempDir(), 2)
	w.Start()
	t.Cleanup(func() {
		close(release)
		w.Stop()
	})

	// First submit: picked up by the worker immediately, blocks on Caddy.
	if err := w.Submit(Task{AppID: "a", Domains: []string{"a"}}); err != nil {
		t.Fatalf("submit 1: %v", err)
	}
	// Give the loop a chance to pick the first task off the channel —
	// otherwise we fill the buffer + the first task is still in it.
	waitFor(t, func() bool { return atomic.LoadInt32(&slowCaddy.inCall) == 1 }, "worker to enter Caddy call")

	// Next 2 fit in the buffered channel (size=2).
	if err := w.Submit(Task{AppID: "b", Domains: []string{"b"}}); err != nil {
		t.Errorf("submit 2: %v", err)
	}
	if err := w.Submit(Task{AppID: "c", Domains: []string{"c"}}); err != nil {
		t.Errorf("submit 3: %v", err)
	}
	// 4th submit overflows.
	if err := w.Submit(Task{AppID: "d", Domains: []string{"d"}}); !errors.Is(err, ErrQueueFull) {
		t.Errorf("submit 4 = %v, want ErrQueueFull", err)
	}
}

// TestWorkerStopDrains — Stop() flushes anything still in the inbox
// before the goroutine exits. We submit a small batch, immediately
// Stop, and verify every task ran by counting Caddy invocations.
func TestWorkerStopDrains(t *testing.T) {
	caddy := &fakeCaddy{}
	w := New(nil, caddy, t.TempDir(), 16)
	w.Start()

	for i := 0; i < 5; i++ {
		err := w.Submit(Task{
			AppID:   "drain-" + string(rune('a'+i)),
			Domains: []string{"drain-" + string(rune('a'+i)) + ".example.com"},
		})
		if err != nil {
			t.Fatalf("Submit: %v", err)
		}
	}
	w.Stop()

	if got := len(caddy.snapshot()); got != 5 {
		t.Errorf("after Stop: ran %d tasks, want 5 (drained)", got)
	}
	// Second Stop is a no-op (it does NOT panic on close-of-closed).
	w.Stop()
}

// TestWorkerStepIndependence — when one sub-step inside a task
// returns an error (Caddy here), subsequent steps and the next task
// still execute. The worker must not bail out on the first failure.
func TestWorkerStepIndependence(t *testing.T) {
	// Caddy fails for the first task's host but succeeds for the second.
	caddy := &fakeCaddy{removeErr: map[string]error{
		"fail.example.com": errors.New("simulated upstream caddy failure"),
	}}
	w := newTestWorker(t, caddy, 16)

	// Task 1 triggers the Caddy failure. The remaining sub-steps run
	// because each cleanup function logs+continues on its own.
	if err := w.Submit(Task{
		AppID:         "t1",
		AppName:       "t1",
		Domains:       []string{"fail.example.com"},
		DeploymentIDs: []string{"dep-t1"},
	}); err != nil {
		t.Fatalf("submit t1: %v", err)
	}
	// Task 2 — fully clean Caddy + log file removal. Must still run
	// despite t1's Caddy error.
	if err := w.Submit(Task{
		AppID:   "t2",
		AppName: "t2",
		Domains: []string{"ok.example.com"},
	}); err != nil {
		t.Fatalf("submit t2: %v", err)
	}

	waitFor(t, func() bool { return len(caddy.snapshot()) == 2 }, "both tasks attempted Caddy")

	// Both hosts should appear in the recorded calls (t1 failed but was
	// attempted, t2 succeeded). Order matches submit order because the
	// inbox is FIFO and one goroutine drains.
	rs := caddy.snapshot()
	if rs[0] != "fail.example.com" || rs[1] != "ok.example.com" {
		t.Errorf("unexpected Caddy call order: %v", rs)
	}
}

// TestWorkerSubmit_EmptyAppIDRejected covers the validation branch:
// the worker refuses tasks with no AppID rather than silently
// accepting them (and later writing log paths that point nowhere).
func TestWorkerSubmit_EmptyAppIDRejected(t *testing.T) {
	w := newTestWorker(t, nil, 4)
	if err := w.Submit(Task{}); err == nil {
		t.Error("empty AppID returned nil err")
	}
}

// TestNew_DefaultQueueSize covers the "operator passed 0" branch —
// the channel must end up with the default capacity (>0) so the
// first Submit doesn't immediately return ErrQueueFull.
func TestNew_DefaultQueueSize(t *testing.T) {
	w := New(nil, nil, t.TempDir(), 0)
	if w.inbox == nil {
		t.Fatal("inbox is nil after New")
	}
	if cap(w.inbox) != 64 {
		t.Errorf("inbox capacity = %d, want 64 (default)", cap(w.inbox))
	}
}

// TestWorker_LogFileCleanup — when the task carries DeploymentIDs and
// the matching .log files exist on disk, the worker removes them.
// Files outside LogDir are never touched (slash in the id is rejected).
func TestWorker_LogFileCleanup(t *testing.T) {
	logDir := t.TempDir()
	// Pre-create the files we expect to be deleted.
	depIDs := []string{"dep-1", "dep-2"}
	for _, id := range depIDs {
		if err := os.WriteFile(filepath.Join(logDir, id+".log"), []byte("x"), 0o644); err != nil {
			t.Fatalf("seed log file: %v", err)
		}
	}
	// And a sibling file the worker MUST NOT delete — the id contains
	// a slash so the path-traversal guard rejects it.
	keep := filepath.Join(logDir, "keep.log")
	if err := os.WriteFile(keep, []byte("y"), 0o644); err != nil {
		t.Fatalf("seed keep file: %v", err)
	}

	caddy := &fakeCaddy{}
	w := New(nil, caddy, logDir, 4)
	w.Start()
	if err := w.Submit(Task{
		AppID:         "app",
		DeploymentIDs: append(depIDs, "../keep"), // slash → rejected by guard
	}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	w.Stop()

	for _, id := range depIDs {
		if _, err := os.Stat(filepath.Join(logDir, id+".log")); !os.IsNotExist(err) {
			t.Errorf("log %s.log still present after cleanup", id)
		}
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("sibling keep.log was deleted: %v", err)
	}
}

// TestWorker_NoCaddyClientIsSafe — a nil CaddyClient skips the Caddy
// step rather than panicking. Mirrors the "no Caddy in this instance"
// happy path.
func TestWorker_NoCaddyClientIsSafe(t *testing.T) {
	w := New(nil, nil, t.TempDir(), 4)
	w.Start()
	if err := w.Submit(Task{
		AppID:   "x",
		Domains: []string{"x.example.com"}, // would be passed to Caddy, but nil-skipped
	}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	w.Stop()
}

// TestWorker_DeadlineHonoured — when Task.Deadline is set, the run's
// context timeout shrinks. We can't probe the context itself, but we
// CAN observe that the cleanup helpers still ran (no panic on a
// near-immediate deadline).
func TestWorker_DeadlineHonoured(t *testing.T) {
	caddy := &fakeCaddy{}
	w := newTestWorker(t, caddy, 4)
	if err := w.Submit(Task{
		AppID:    "x",
		Domains:  []string{"x.example.com"},
		Deadline: time.Now().Add(100 * time.Millisecond),
	}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitFor(t, func() bool { return len(caddy.snapshot()) == 1 }, "task with deadline to run")
}

// TestWorker_CleanupCaddySkipsEmptyHosts — empty/whitespace domains
// must not flow into RemoveRoute.
func TestWorker_CleanupCaddySkipsEmptyHosts(t *testing.T) {
	caddy := &fakeCaddy{}
	w := newTestWorker(t, caddy, 4)
	if err := w.Submit(Task{
		AppID:   "x",
		Domains: []string{"", "   ", "real.example.com"},
	}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitFor(t, func() bool { return len(caddy.snapshot()) > 0 }, "Caddy to be called")
	got := caddy.snapshot()
	if len(got) != 1 || got[0] != "real.example.com" {
		t.Errorf("Caddy received %v, want only real.example.com", got)
	}
}

// slowCaddyStub blocks on its release channel inside RemoveRoute so
// the worker goroutine stays "busy" while the test stuffs the inbox.
type slowCaddyStub struct {
	inCall  int32
	release chan struct{}
}

func (s *slowCaddyStub) RemoveRoute(_ context.Context, _ string) error {
	atomic.AddInt32(&s.inCall, 1)
	defer atomic.AddInt32(&s.inCall, -1)
	<-s.release
	return nil
}
