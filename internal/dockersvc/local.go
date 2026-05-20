package dockersvc

import (
	"context"
	"fmt"
	"sync"

	"github.com/docker/docker/client"
)

// localProvider talks to the Docker engine over the host UNIX socket. It is
// used by the prexel server for its own host and by any "local" server row.
type localProvider struct {
	name string

	mu     sync.Mutex
	cli    *client.Client
	closed bool
}

// NewLocalProvider returns a Provider backed by /var/run/docker.sock.
// `name` should match the operator-facing server name (typically "local").
func NewLocalProvider(name string) Provider {
	if name == "" {
		name = "local"
	}
	return &localProvider{name: name}
}

func (p *localProvider) Type() string { return string(ProviderLocal) }
func (p *localProvider) Name() string { return p.name }

func (p *localProvider) Client(_ context.Context) (*client.Client, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return nil, fmt.Errorf("docker: local provider %q is closed", p.name)
	}
	if p.cli != nil {
		return p.cli, nil
	}

	cli, err := client.NewClientWithOpts(
		client.WithHost("unix:///var/run/docker.sock"),
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, fmt.Errorf("docker: new local client: %w", err)
	}
	p.cli = cli
	return p.cli, nil
}

func (p *localProvider) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	if p.cli != nil {
		err := p.cli.Close()
		p.cli = nil
		return err
	}
	return nil
}
