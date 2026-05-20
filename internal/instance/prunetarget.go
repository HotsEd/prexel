package instance

import (
	"context"
	"sort"

	"github.com/prexel/prexel/internal/dockersvc"
)

// DockerPruneTarget implements PruneTarget against a single Docker host
// (typically the local server). Remote servers are out of scope for v0.1
// cleanup — they will get the same treatment in a future loop variant
// that fans out across all "connected" servers.
//
// We accept a dockersvc.Provider rather than constructing one from a
// server.Service because cleanup is host-shaped, not domain-shaped:
// the loop runs against "wherever this Prexel can talk to Docker", which
// for the local case is /var/run/docker.sock.
type DockerPruneTarget struct {
	Provider dockersvc.Provider
}

// PruneDangling removes <none>:<none> images and stopped containers.
// SpaceReclaimed is summed across both operations.
func (t DockerPruneTarget) PruneDangling(ctx context.Context) (int64, error) {
	imgBytes, err := dockersvc.PruneImages(ctx, t.Provider)
	if err != nil {
		return 0, err
	}
	ctnBytes, err := dockersvc.PruneContainers(ctx, t.Provider)
	if err != nil {
		// Image prune already succeeded; return what we did manage to reclaim
		// so the operator sees progress even when container prune misfires.
		return imgBytes, err
	}
	return imgBytes + ctnBytes, nil
}

// RetainImagesPerApp lists every image carrying a prexel.app_id label, groups
// by that id, sorts newest-first, and removes anything beyond `n`. Returns
// the count of removed images.
//
// The deploy engine has its own opportunistic retention (run after every
// successful deploy) — this is the catch-up sweep for cases where deploys
// failed mid-way, the engine crashed, or images were imported manually.
func (t DockerPruneTarget) RetainImagesPerApp(ctx context.Context, n int) (int, error) {
	if n < 1 {
		n = 1
	}
	imgs, err := dockersvc.ListImages(ctx, t.Provider, map[string]string{"prexel.app_id": ""})
	if err != nil {
		return 0, err
	}
	// Group by app_id label.
	groups := map[string][]imgEntry{}
	for _, im := range imgs {
		appID := im.Labels["prexel.app_id"]
		if appID == "" {
			continue
		}
		ref := firstNonEmpty(im.RepoTags, im.RepoDigests, []string{im.ID})
		groups[appID] = append(groups[appID], imgEntry{Created: im.Created, Ref: ref})
	}

	removed := 0
	for _, list := range groups {
		if len(list) <= n {
			continue
		}
		sort.Slice(list, func(i, j int) bool { return list[i].Created > list[j].Created })
		for _, victim := range list[n:] {
			if err := dockersvc.RemoveImage(ctx, t.Provider, victim.Ref); err == nil {
				removed++
			}
			// Errors are swallowed — the next sweep will retry. A failing
			// remove is usually "image still in use by a running container"
			// which is correct behaviour: don't pull the rug.
		}
	}
	return removed, nil
}

type imgEntry struct {
	Created int64
	Ref     string
}

// firstNonEmpty returns the first slice that is non-empty; if all are empty,
// returns the last (which the caller seeds with a fallback).
func firstNonEmpty(slices ...[]string) string {
	for _, s := range slices {
		if len(s) > 0 && s[0] != "" {
			return s[0]
		}
	}
	return ""
}
