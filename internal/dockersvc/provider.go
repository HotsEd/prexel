package dockersvc

import (
	"context"

	"github.com/docker/docker/client"
)

// ProviderType discriminates between local-socket and remote-ssh providers.
type ProviderType string

const (
	ProviderLocal  ProviderType = "local"
	ProviderRemote ProviderType = "remote"
)

// Provider is an abstraction over a Docker engine endpoint. Implementations
// are safe for concurrent use and must memoise the underlying *client.Client
// so callers can invoke Client() cheaply on the hot path.
type Provider interface {
	// Type returns the provider kind ("local" or "remote").
	Type() string

	// Name returns the operator-facing identifier for this provider (matches
	// the servers.name row in the prexel DB, e.g. "local" or "production").
	Name() string

	// Client returns a connected Docker API client. The first call may perform
	// API version negotiation (and, for remote providers, an SSH handshake);
	// subsequent calls return the cached client.
	Client(ctx context.Context) (*client.Client, error)

	// Close tears down any resources owned by the provider (cached client,
	// temp files containing private key material, SSH dialers, …).
	Close() error
}
