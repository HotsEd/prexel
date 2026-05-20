package deploy

import (
	"context"
	"errors"
	"log/slog"

	"github.com/prexel/prexel/internal/app"
	"github.com/prexel/prexel/internal/dockersvc"
)

// Reconcile runs once at startup. Any app marked building/deploying — but
// whose container is not actually running — is rolled back to running/error
// based on a live inspection of the docker engine. Any pending/in-flight
// deployment row is moved to failed. This addresses Tech Review §14: crash
// during deploy must not leave ghost state.
func (e *Engine) Reconcile(ctx context.Context) error {
	apps, err := e.Apps.ListAll(ctx)
	if err != nil {
		return err
	}

	// Reset stale deployment rows first so subsequent app inspection has a
	// consistent view.
	if n, err := e.repo.failPending(ctx); err != nil {
		slog.Warn("reconcile: fail pending deployments", "err", err)
	} else if n > 0 {
		slog.Info("reconcile: marked deployments failed", "count", n)
	}

	for i := range apps {
		a := apps[i]
		switch a.Status {
		case "building", "deploying":
			// fall through to inspection
		default:
			continue
		}
		if a.ServerID == nil || a.ContainerName == nil {
			_ = e.Apps.SetStatus(ctx, a.ID, "error")
			continue
		}
		newStatus := e.inspectAppContainer(ctx, &a)
		_ = e.Apps.SetStatus(ctx, a.ID, newStatus)
		slog.Info("reconcile: app status normalised", "app", a.Name, "status", newStatus)
	}
	return nil
}

func (e *Engine) inspectAppContainer(ctx context.Context, a *app.App) string {
	provider, err := e.Servers.Provider(ctx, *a.ServerID)
	if err != nil {
		return "error"
	}
	defer func() { _ = provider.Close() }()

	cli, err := provider.Client(ctx)
	if err != nil {
		return "error"
	}
	insp, err := cli.ContainerInspect(ctx, *a.ContainerName)
	if err != nil {
		if errors.Is(err, dockersvc.ErrContainerNotFound) || isNotFound(err) {
			return "stopped"
		}
		return "error"
	}
	if insp.State != nil && insp.State.Running {
		return "running"
	}
	return "stopped"
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	// Best-effort string match; the docker client exposes IsErrNotFound but
	// gives no typed error for "container 404" via Inspect.
	return errors.Is(err, dockersvc.ErrContainerNotFound)
}
