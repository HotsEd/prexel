package dockersvc

import (
	"context"
	"errors"
	"fmt"

	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
)

// PrexelNetwork is the canonical name of the shared Docker bridge that joins
// all containers managed by prexel (apps, Caddy, and the prexel server in
// dev). Containers reach each other by name on this network.
const PrexelNetwork = "prexel-net"

// EnsureNetwork makes sure a bridge network with the given name exists on the
// engine fronted by p. It is idempotent: if the network already exists, it
// returns nil without modifying it. The bridge is created with attachable=true
// so that ad-hoc containers (e.g. one-off debug shells) can join it.
func EnsureNetwork(ctx context.Context, p Provider, name string) error {
	if name == "" {
		return errors.New("docker: ensure network: empty name")
	}
	cli, err := p.Client(ctx)
	if err != nil {
		return err
	}

	if _, err := cli.NetworkInspect(ctx, name, network.InspectOptions{}); err == nil {
		return nil
	} else if !client.IsErrNotFound(err) {
		return fmt.Errorf("docker: inspect network %q: %w", name, err)
	}

	_, err = cli.NetworkCreate(ctx, name, network.CreateOptions{
		Driver:     "bridge",
		Attachable: true,
		Labels: map[string]string{
			"prexel.managed": "true",
		},
	})
	if err != nil {
		// Race: another caller may have created the network between Inspect
		// and Create. Re-check before bubbling the error.
		if _, ierr := cli.NetworkInspect(ctx, name, network.InspectOptions{}); ierr == nil {
			return nil
		}
		return fmt.Errorf("docker: create network %q: %w", name, err)
	}
	return nil
}
