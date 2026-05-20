package instance

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// PruneTarget is the small surface the cleanup loop needs from whatever
// component knows how to delete images on a Docker host. The production
// wiring lives outside this package (in cli/command/wire.go) because it
// needs Provider + image listing, which would pull dockersvc here.
type PruneTarget interface {
	// PruneDangling removes images with <none>:<none> tags and stopped
	// containers. Returns reclaimed bytes (best effort, 0 if unknown).
	PruneDangling(ctx context.Context) (int64, error)

	// RetainImagesPerApp keeps the newest `n` images per Prexel-managed app
	// (those carrying the `prexel.app_id` label) and removes the rest.
	// Returns the number of images deleted.
	RetainImagesPerApp(ctx context.Context, n int) (int, error)
}

// DiskUsage queries the data dir's filesystem and returns the used %
// (0..100). statfs on macOS / linux. Used by the cleanup loop to decide
// whether to actually prune.
type DiskUsage func(path string) (percentUsed int, err error)

// DefaultDiskUsage uses syscall.Statfs. Returns 0 + nil on platforms where
// Statfs is unavailable (callers treat 0 as "below threshold, skip prune",
// which is the safe default).
func DefaultDiskUsage(path string) (int, error) {
	var s syscall.Statfs_t
	if err := syscall.Statfs(path, &s); err != nil {
		return 0, fmt.Errorf("statfs %s: %w", path, err)
	}
	if s.Blocks == 0 {
		return 0, nil
	}
	used := s.Blocks - s.Bfree
	percent := int((used * 100) / s.Blocks)
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	return percent, nil
}

// CleanupLoop runs in the background, ticks once a minute, and triggers
// cleanup when the cron schedule matches the current minute AND
// cleanup_enabled is true. Disk threshold is evaluated at trigger time —
// not at tick time — so a daily schedule still respects "skip when disk
// is healthy".
func CleanupLoop(ctx context.Context, svc *Service, target PruneTarget, dataDir string, du DiskUsage) {
	if target == nil {
		slog.Warn("instance.CleanupLoop: nil PruneTarget, loop will be a noop")
		return
	}
	if du == nil {
		du = DefaultDiskUsage
	}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case t := <-ticker.C:
			cfg := svc.Cleanup()
			if !cfg.Enabled {
				continue
			}
			if !scheduleMatches(cfg.Schedule, t) {
				continue
			}
			runCleanup(ctx, cfg, target, dataDir, du)
		}
	}
}

func runCleanup(ctx context.Context, cfg CleanupConfig, target PruneTarget, dataDir string, du DiskUsage) {
	logger := slog.With("component", "cleanup")

	if cfg.DiskThreshold > 0 {
		used, err := du(dataDir)
		if err != nil {
			logger.Warn("disk usage probe failed; running prune anyway", "err", err)
		} else if used < cfg.DiskThreshold {
			logger.Info("disk below threshold, skipping prune",
				"used_pct", used, "threshold_pct", cfg.DiskThreshold)
			// Still enforce per-app retention even when disk is healthy —
			// the retention cap is about correctness (rollback only goes
			// back N versions) not about disk pressure.
			if removed, err := target.RetainImagesPerApp(ctx, cfg.ImageRetention); err != nil {
				logger.Warn("retention sweep failed", "err", err)
			} else if removed > 0 {
				logger.Info("retention sweep done", "removed", removed)
			}
			return
		}
	}

	reclaimed, err := target.PruneDangling(ctx)
	if err != nil {
		logger.Warn("prune dangling failed", "err", err)
	} else {
		logger.Info("prune done", "reclaimed_bytes", reclaimed)
	}
	removed, err := target.RetainImagesPerApp(ctx, cfg.ImageRetention)
	if err != nil {
		logger.Warn("retention sweep failed", "err", err)
	} else {
		logger.Info("retention sweep done", "removed", removed)
	}
}

// scheduleMatches takes a 5-field cron expression and decides whether `now`
// falls on a tick. We support the four numeric/wildcard combinations the
// admin is realistically going to write:
//
//	"0 3 * * *"   → daily at 03:00
//	"*/15 * * * *" → every 15 min          (NOT supported in v0.1; falls through to false)
//	"0 */6 * * *"  → every 6 hours         (also NOT supported)
//	"0 3 * * 0"   → Sundays at 03:00
//
// Step/range syntax (`*/N`, `1-5`, `1,3,5`) is out of scope for this
// MVP — we use it as a fast input sanity check but the loop only fires
// when the minute+hour+(dow|*) line up.
func scheduleMatches(expr string, now time.Time) bool {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return false
	}
	minute, mok := atoiOrStar(fields[0])
	hour, hok := atoiOrStar(fields[1])
	if !mok || !hok {
		return false // step / range / list — not in v0.1 scope
	}
	if minute != -1 && minute != now.Minute() {
		return false
	}
	if hour != -1 && hour != now.Hour() {
		return false
	}
	// Day-of-month and month are passed through as "any" for v0.1.
	// Day-of-week supports numeric or *.
	if dow, dok := atoiOrStar(fields[4]); !dok {
		return false
	} else if dow != -1 && dow != int(now.Weekday()) {
		return false
	}
	return true
}

// atoiOrStar returns (-1, true) for "*", (n, true) for "n", (0, false) for
// anything else (step/list/range — declined for v0.1).
func atoiOrStar(s string) (int, bool) {
	if s == "*" {
		return -1, true
	}
	n, err := strconv.Atoi(s)
	if err != nil || errors.Is(err, strconv.ErrRange) {
		return 0, false
	}
	return n, true
}
