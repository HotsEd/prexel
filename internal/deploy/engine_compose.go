package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/prexel/prexel/internal/app"
	"github.com/prexel/prexel/internal/build"
	"github.com/prexel/prexel/internal/dockersvc"
)

// swapCompose is the multi-container equivalent of swap() — used when
// the app has BuildType=docker_compose. The high-level shape mirrors
// the single-container swap but iterates over every service in the
// compose spec:
//
//	for each service:
//	  - pick container name (prexel-<app>-<service>)
//	  - start temp candidate on prexel-net (with `service` as a network
//	    alias so peers can resolve it the same way they would inside
//	    a `docker compose up` network)
//	  - apply per-service env (compose env merged on top of app env +
//	    runtime secrets) and per-service mounts from app_volumes
//	  - wait briefly to let containers come up
//	for each service:
//	  - swap: stop+remove old canonical, rename temp -> canonical
//	for each domain bound to (app, service):
//	  - caddy UpsertRoute to canonical container at the domain's port
//	for any orphan canonical container (in old compose, not in new):
//	  - stop+remove
//
// Things explicitly deferred (and noted in operator-facing log lines):
//   - per-service healthcheck honouring the YAML's `healthcheck:`
//     block. We sleep a fixed 5s per service for now.
//   - pre/post-deploy hooks. Single-container concept; would need a
//     "primary service" pointer or per-service hooks to be meaningful.
//   - dependency ordering. We iterate in the order composespec returned
//     (effectively map iteration order). `depends_on` is parsed but
//     not enforced yet — typical apps tolerate this because containers
//     retry connections.
func (e *Engine) swapCompose(ctx context.Context, a *app.App, provider dockersvc.Provider, res *build.Result, d *Deployment, logFile io.Writer) error {
	if res == nil || res.Compose == nil {
		return errors.New("swap compose: missing compose result")
	}
	if len(res.Compose.Services) == 0 {
		return errors.New("swap compose: no services to deploy")
	}

	_ = e.repo.updateFields(ctx, d.ID, map[string]any{"status": "deploying"})
	_ = e.Apps.SetStatus(ctx, a.ID, "deploying")
	e.publish(a.ID, d.ID, "app.status_changed", map[string]any{"status": "deploying"})

	if a.PreDeployCommand != nil && strings.TrimSpace(*a.PreDeployCommand) != "" {
		fmt.Fprintf(logFile, "%s [stdout] note: pre-deploy hooks are not honoured for docker_compose apps yet — skipping.\n",
			time.Now().UTC().Format(time.RFC3339Nano))
	}

	// Resolve runtime env once at the app level — every service starts
	// with this baseline, then layers its own compose `environment:`
	// on top. Compose values win on conflict (matches docker-compose).
	baseEnv := map[string]string{}
	for k, v := range a.EnvVars {
		baseEnv[k] = v
	}
	if e.Secrets != nil {
		runtimeSecs, err := e.Secrets.ResolveRuntime(ctx, a.ID)
		if err != nil {
			return fmt.Errorf("resolve runtime secrets: %w", err)
		}
		for k, v := range runtimeSecs {
			baseEnv[k] = v
		}
	}

	// Domains keyed by service name so we can route after the rename.
	primary, aliases, hostForceHTTPS, err := e.appDomains(ctx, a.ID)
	if err != nil {
		return fmt.Errorf("load domains: %w", err)
	}
	_, _ = primary, aliases // single-container path uses these; compose uses the per-service map below.
	perServiceDomains, err := e.appDomainsByService(ctx, a.ID)
	if err != nil {
		return fmt.Errorf("load per-service domains: %w", err)
	}

	// Volumes resolved once per service-name lookup.
	allVolumes, err := e.collectComposeVolumes(ctx, a, provider)
	if err != nil {
		return err
	}

	restartPolicy := a.RestartPolicy
	if restartPolicy == "" {
		restartPolicy = "unless-stopped"
	}

	// Phase 1 — start every candidate.
	candidates := make([]composeCandidate, 0, len(res.Compose.Services))
	for _, svc := range res.Compose.Services {
		c, err := e.startComposeCandidate(ctx, a, provider, d, svc, baseEnv, allVolumes[svc.Name], restartPolicy, logFile)
		if err != nil {
			// Rollback already-started candidates so we don't leak
			// half-deployed state if one service fails to start.
			for _, prev := range candidates {
				_ = dockersvc.StopContainer(ctx, provider, prev.tempName, 5*time.Second)
				_ = dockersvc.RemoveContainer(ctx, provider, prev.tempName, true)
			}
			return err
		}
		candidates = append(candidates, c)
	}

	// Settle window. Replaces per-service healthchecks for now —
	// containers that fail their entrypoint will surface as exit-1
	// at the rename step (docker rename succeeds even on stopped
	// containers; the subsequent Caddy upstream will then 502, which
	// is the same failure surface a healthcheck would catch).
	fmt.Fprintf(logFile, "%s [stdout] Waiting 5s for compose services to settle (per-service healthchecks not honoured yet)...\n",
		time.Now().UTC().Format(time.RFC3339Nano))
	select {
	case <-time.After(5 * time.Second):
	case <-ctx.Done():
		for _, c := range candidates {
			_ = dockersvc.StopContainer(ctx, provider, c.tempName, 5*time.Second)
			_ = dockersvc.RemoveContainer(ctx, provider, c.tempName, true)
		}
		return ctx.Err()
	}

	// Phase 2 — swap each service: stop+remove old canonical, rename
	// temp to canonical. We do not point Caddy at the temp container
	// during this phase because compose has no "primary" service —
	// some apps don't expose any port externally. The brief downtime
	// window per service is bounded by the rename op (~100ms).
	newServiceNames := make(map[string]struct{}, len(candidates))
	for _, c := range candidates {
		newServiceNames[c.serviceName] = struct{}{}
	}

	for _, c := range candidates {
		// Stop+remove the previous canonical with this name (if any),
		// AND the legacy `prexel-<app>-<service>` name from before
		// the prefix was dropped — apps that existed in the old
		// scheme would otherwise leave a stale container running
		// alongside the new one (orphan cleanup wouldn't catch it
		// because the prexel.service label matches the new set).
		for _, name := range []string{c.canonicalName, legacyComposeName(a.Name, c.serviceName)} {
			if err := dockersvc.StopContainer(ctx, provider, name, 10*time.Second); err != nil && !errors.Is(err, dockersvc.ErrContainerNotFound) {
				fmt.Fprintf(logFile, "%s [stderr] Warning: stop old %s: %v\n",
					time.Now().UTC().Format(time.RFC3339Nano), name, err)
			}
			if err := dockersvc.RemoveContainer(ctx, provider, name, true); err != nil && !errors.Is(err, dockersvc.ErrContainerNotFound) {
				fmt.Fprintf(logFile, "%s [stderr] Warning: remove old %s: %v\n",
					time.Now().UTC().Format(time.RFC3339Nano), name, err)
			}
		}
		if err := dockersvc.RenameContainer(ctx, provider, c.tempName, c.canonicalName); err != nil {
			return fmt.Errorf("rename %s -> %s: %w", c.tempName, c.canonicalName, err)
		}
		fmt.Fprintf(logFile, "%s [stdout] [%s] container %s is now canonical.\n",
			time.Now().UTC().Format(time.RFC3339Nano), c.serviceName, c.canonicalName)
	}

	// Phase 3 — route Caddy. Per-service: for each domain bound to
	// this service, point Caddy at the canonical container at the
	// service's configured port.
	if e.Caddy != nil {
		for _, c := range candidates {
			doms := perServiceDomains[c.serviceName]
			for _, dom := range doms {
				port := 0
				if dom.port != nil && *dom.port > 0 {
					port = *dom.port
				}
				// Fall back to the first port the service exposes.
				if port == 0 && len(c.ports) > 0 {
					port = c.ports[0]
				}
				if port == 0 {
					fmt.Fprintf(logFile, "%s [stderr] Warning: domain %s bound to service %s has no port — skipping Caddy route.\n",
						time.Now().UTC().Format(time.RFC3339Nano), dom.host, c.serviceName)
					continue
				}
				force := hostForceHTTPS[dom.host]
				if err := e.Caddy.UpsertRoute(ctx, dom.host, c.canonicalName, port, force); err != nil {
					fmt.Fprintf(logFile, "%s [stderr] Warning: caddy upsert %s -> %s:%d: %v\n",
						time.Now().UTC().Format(time.RFC3339Nano), dom.host, c.canonicalName, port, err)
				}
			}
		}
	}

	// Phase 4 — orphan cleanup. Containers labelled for this app that
	// don't appear in the new compose are leftovers from an older
	// service set; remove them so the running state matches the spec.
	if err := e.pruneOrphanComposeContainers(ctx, provider, a, newServiceNames, logFile); err != nil {
		fmt.Fprintf(logFile, "%s [stderr] Warning: orphan cleanup: %v\n",
			time.Now().UTC().Format(time.RFC3339Nano), err)
	}

	if a.PostDeployCommand != nil && strings.TrimSpace(*a.PostDeployCommand) != "" {
		fmt.Fprintf(logFile, "%s [stdout] note: post-deploy hooks are not honoured for docker_compose apps yet — skipping.\n",
			time.Now().UTC().Format(time.RFC3339Nano))
	}

	return nil
}

// composeCandidate carries the per-service state we need across the
// start → swap → route phases. Lives here (not at engine scope)
// because it's purely a function-local data carrier.
type composeCandidate struct {
	serviceName   string
	canonicalName string
	tempName      string
	ports         []int
}

func (e *Engine) startComposeCandidate(
	ctx context.Context,
	a *app.App,
	provider dockersvc.Provider,
	d *Deployment,
	svc build.ComposeServiceSummary,
	baseEnv map[string]string,
	mounts []dockersvc.Mount,
	defaultRestart string,
	logFile io.Writer,
) (composeCandidate, error) {
	canonicalName := composeCanonicalName(a.Name, svc.Name)
	tempName := canonicalName + "-deploying-" + shortDeployID(d.ID)

	// Compose env layered on top of app env / secrets — compose wins.
	envMap := make(map[string]string, len(baseEnv)+len(svc.Env))
	for k, v := range baseEnv {
		envMap[k] = v
	}
	for k, v := range svc.Env {
		envMap[k] = v
	}

	labels := map[string]string{}
	for k, v := range a.DockerLabels {
		labels[k] = v
	}
	labels["prexel.app_id"] = a.ID
	labels["prexel.app_name"] = a.Name
	labels["prexel.deployment"] = d.ID
	labels["prexel.role"] = "candidate"
	labels["prexel.compose"] = "true"
	labels["prexel.service"] = svc.Name
	// Docker Compose project / service labels — the standard keys
	// `docker compose ps`, Docker Desktop, lazydocker, and friends
	// look at to group containers under a project. Without them, our
	// compose services show up as top-level orphans in the GUI;
	// with them, all services of an app collapse under a single
	// `<app-name>` group. Pure UX — no functional impact on the
	// containers themselves.
	labels["com.docker.compose.project"] = a.Name
	labels["com.docker.compose.service"] = svc.Name
	labels["com.docker.compose.container-number"] = "1"
	labels["com.docker.compose.oneoff"] = "False"

	restartPolicy := strings.TrimSpace(svc.Restart)
	if restartPolicy == "" {
		restartPolicy = defaultRestart
	}

	// Remove any stale temp from a previous failed deploy with this id.
	_ = dockersvc.RemoveContainer(ctx, provider, tempName, true)

	fmt.Fprintf(logFile, "%s [stdout] [%s] starting candidate %s (image=%s)\n",
		time.Now().UTC().Format(time.RFC3339Nano), svc.Name, tempName, svc.Image)

	opts := dockersvc.ContainerOpts{
		Name:    tempName,
		Image:   svc.Image,
		Env:     envMap,
		Labels:  labels,
		Cmd:     svc.Command,
		// Aliases: both the temp name AND the service name. The service
		// name lets peers do `postgresql.local` / `db` / etc. just like
		// docker-compose does on its private network. We add the
		// canonical name too so the network identity is stable across
		// rename ops.
		Aliases:       []string{svc.Name, canonicalName, tempName},
		RestartPolicy: restartPolicy,
		MemoryLimit:   derefStr(a.LimitsMemory),
		CPULimit:      derefStr(a.LimitsCPUs),
		MemorySwap:    derefStr(a.LimitsMemorySwap),
		// Advanced cgroup knobs aren't currently surfaced per-service
		// in compose YAML — applied at the app level only.
		MemorySwappiness:  intPtrToInt64Ptr(a.LimitsMemorySwappiness),
		MemoryReservation: derefStr(a.LimitsMemoryReservation),
		CPUSet:            derefStr(a.LimitsCPUSet),
		CPUShares:         intPtrToInt64(a.LimitsCPUShares),
		Mounts:            mounts,
	}

	if _, err := dockersvc.RunContainer(ctx, provider, opts); err != nil {
		return composeCandidate{}, fmt.Errorf("[%s] run container: %w", svc.Name, err)
	}

	return composeCandidate{
		serviceName:   svc.Name,
		canonicalName: canonicalName,
		tempName:      tempName,
		ports:         svc.Ports,
	}, nil
}

// collectComposeVolumes returns a map of serviceName -> mounts derived
// from `app_volumes` rows where `service` matches the service. Named
// volumes are pre-created and tagged with `prexel.app_id` so the
// cleanup worker can wipe them when the app is deleted; bind mounts
// pass through the host path.
//
// Volumes with service == NULL (single-container scope) are NOT
// applied here — those only make sense for non-compose apps.
func (e *Engine) collectComposeVolumes(ctx context.Context, a *app.App, provider dockersvc.Provider) (map[string][]dockersvc.Mount, error) {
	out := map[string][]dockersvc.Mount{}
	if e.AppVolumes == nil {
		return out, nil
	}
	vols, err := e.AppVolumes.ListForApp(ctx, a.ID)
	if err != nil {
		// Non-fatal — a missing volume row shouldn't kill the deploy.
		// Single-container code path takes the same lenient stance.
		return out, nil
	}
	for _, v := range vols {
		if v.Service == nil || strings.TrimSpace(*v.Service) == "" {
			continue
		}
		m := dockersvc.Mount{
			Target:   v.MountPath,
			ReadOnly: v.ReadOnly,
		}
		if v.IsNamed {
			m.Type = "volume"
			m.Source = namedVolumeName(a.Name, *v.Service+"-"+v.MountPath)
			// Pre-create with app labels so the cleanup worker can
			// find these volumes when the app is deleted. Silent
			// failure here surfaces as a clearer "no such volume"
			// error at ContainerCreate, which is fine.
			_ = dockersvc.EnsureVolume(ctx, provider, m.Source, map[string]string{
				"prexel.app_id":   a.ID,
				"prexel.app_name": a.Name,
				"prexel.service":  *v.Service,
			})
		} else {
			if v.HostPath == nil {
				continue
			}
			m.Type = "bind"
			m.Source = *v.HostPath
		}
		out[*v.Service] = append(out[*v.Service], m)
	}
	return out, nil
}

// pruneOrphanComposeContainers removes containers labelled
// prexel.app_id=<a.ID> whose prexel.service label is NOT in
// `liveServices`. This handles the "service removed from compose"
// case so the runtime state doesn't drift from the spec.
func (e *Engine) pruneOrphanComposeContainers(
	ctx context.Context,
	provider dockersvc.Provider,
	a *app.App,
	liveServices map[string]struct{},
	logFile io.Writer,
) error {
	list, err := dockersvc.ListContainers(ctx, provider, map[string][]string{
		"label": {"prexel.app_id=" + a.ID},
	})
	if err != nil {
		return err
	}
	for _, c := range list {
		svc := c.Labels["prexel.service"]
		if svc == "" {
			// Single-container leftover — not ours to prune here.
			continue
		}
		if _, ok := liveServices[svc]; ok {
			continue
		}
		// Container.Names is []string with a leading slash.
		name := ""
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		if name == "" {
			continue
		}
		fmt.Fprintf(logFile, "%s [stdout] orphan: removing %s (service=%s not in current compose)\n",
			time.Now().UTC().Format(time.RFC3339Nano), name, svc)
		_ = dockersvc.StopContainer(ctx, provider, name, 5*time.Second)
		_ = dockersvc.RemoveContainer(ctx, provider, name, true)
	}
	return nil
}

// composeCanonicalName produces the per-service container name.
// Format: `<app>-<service>`. Matches the single-container scheme
// (`<app>`) but disambiguates by service. We deliberately don't
// prefix with `prexel-` — operators recognise their app's
// containers by name, and Prexel ownership is already tracked via
// the `prexel.*` labels.
func composeCanonicalName(appName, serviceName string) string {
	return appName + "-" + serviceName
}

// legacyComposeName is the pre-cleanup container name that earlier
// versions of Prexel used (`prexel-<app>-<service>`). Kept so the
// swap logic can stop+remove leftovers when an existing app is
// re-deployed under the new naming scheme — otherwise the old
// container keeps running alongside the new one with no orphan
// cleanup trigger (both carry the same prexel.service label).
func legacyComposeName(appName, serviceName string) string {
	return "prexel-" + appName + "-" + serviceName
}

// shortDeployID returns the first 7 chars of a deployment ID for
// container naming. UUIDs are dash-separated, so the leading hex
// segment carries enough entropy without dragging the dashes into
// container names.
func shortDeployID(id string) string {
	id = strings.ReplaceAll(id, "-", "")
	if len(id) > 7 {
		return id[:7]
	}
	return id
}

// composeDomainRow is a per-host projection of the domains table we
// keep small for the compose router. host carries the FQDN, port is
// the in-container port the operator wired (may be nil — means "use
// the service's first declared port").
type composeDomainRow struct {
	host string
	port *int
}

// appDomainsByService groups domains by their `service` column so
// the compose swap can route each service's containers without
// re-querying the table N times. Domains with NULL service are
// ignored here — those are single-container bindings and only apply
// to non-compose apps.
func (e *Engine) appDomainsByService(ctx context.Context, appID string) (map[string][]composeDomainRow, error) {
	id := appID
	doms, err := e.Domains.List(ctx, &id)
	if err != nil {
		return nil, err
	}
	out := map[string][]composeDomainRow{}
	for _, d := range doms {
		if d.Service == nil || strings.TrimSpace(*d.Service) == "" {
			continue
		}
		out[*d.Service] = append(out[*d.Service], composeDomainRow{host: d.Name, port: d.Port})
	}
	return out, nil
}
