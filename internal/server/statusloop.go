package server

import (
	"context"
	"log/slog"
	"time"
)

// Loop runs a lightweight health probe over every server every `interval`. It
// updates `status`, `docker_version`, and `last_checked_at`. When a server
// flips to `disconnected`, every app attached to it gets `status=unreachable`.
//
// The loop terminates when ctx is cancelled. interval <= 0 falls back to 60s.
func Loop(ctx context.Context, svc *Service, interval time.Duration) {
	if interval <= 0 {
		interval = 60 * time.Second
	}
	// Wait one tick before the first probe — gives the HTTP server a moment to
	// settle and avoids racing with setup completion.
	t := time.NewTimer(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			runOnce(ctx, svc)
			t.Reset(interval)
		}
	}
}

func runOnce(ctx context.Context, svc *Service) {
	servers, err := svc.List(ctx)
	if err != nil {
		slog.Warn("server: status loop: list failed", "err", err)
		return
	}
	for i := range servers {
		srv := &servers[i]
		prev := srv.Status
		status, ver, perr := svc.ping(ctx, srv)
		if perr != nil {
			slog.Debug("server: status loop: ping failed", "server", srv.Name, "err", perr)
		}
		if err := svc.repo.updateStatus(ctx, srv.ID, status, ver); err != nil {
			slog.Warn("server: status loop: persist failed", "server", srv.Name, "err", err)
			continue
		}
		if status == "disconnected" && prev != "disconnected" {
			slog.Warn("server: marked disconnected", "server", srv.Name)
			if err := svc.repo.markAppsUnreachable(ctx, srv.ID); err != nil {
				slog.Warn("server: mark apps unreachable failed", "server", srv.Name, "err", err)
			}
		}
	}
}
