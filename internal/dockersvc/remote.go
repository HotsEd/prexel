package dockersvc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sync"

	"github.com/docker/cli/cli/connhelper"
	"github.com/docker/docker/client"
)

// remoteProvider talks to a Docker engine on another host over SSH. Internally
// we delegate the SSH transport to the standard `ssh` binary via
// docker/cli/connhelper. We force key-based auth by writing the private key
// (chmod 600) into a temp file and passing `-i <path> -o IdentitiesOnly=yes
// -o StrictHostKeyChecking=accept-new -o BatchMode=yes` to ssh.
//
// The temp key file is removed in Close().
//
// We deliberately do NOT use golang.org/x/crypto/ssh as a Dialer here: the
// Moby SDK's commandconn transport is what `docker -H ssh://…` uses in
// production and is the best-tested code path for SSH-tunnelled API calls.
type remoteProvider struct {
	name string
	host string
	port int
	user string

	keyPath string

	mu     sync.Mutex
	cli    *client.Client
	closed bool
}

// NewRemoteProvider constructs a Provider that reaches a remote Docker engine
// via `ssh://user@host:port`. The provided PEM-encoded private key is
// persisted to a chmod-600 temp file for the lifetime of the provider; call
// Close() to scrub it.
//
// Connection is lazy: no SSH handshake is performed until Client() is invoked.
func NewRemoteProvider(name, host string, port int, user string, privateKeyPEM []byte) (Provider, error) {
	if name == "" {
		return nil, errors.New("docker: remote provider: empty name")
	}
	if host == "" {
		return nil, errors.New("docker: remote provider: empty host")
	}
	if user == "" {
		return nil, errors.New("docker: remote provider: empty user")
	}
	if port <= 0 {
		port = 22
	}
	if len(privateKeyPEM) == 0 {
		return nil, errors.New("docker: remote provider: empty private key")
	}

	f, err := os.CreateTemp("", "prexel-ssh-*.key")
	if err != nil {
		return nil, fmt.Errorf("docker: temp key file: %w", err)
	}
	keyPath := f.Name()
	if err := os.Chmod(keyPath, 0o600); err != nil {
		_ = f.Close()
		_ = os.Remove(keyPath)
		return nil, fmt.Errorf("docker: chmod key file: %w", err)
	}
	if _, err := f.Write(privateKeyPEM); err != nil {
		_ = f.Close()
		_ = os.Remove(keyPath)
		return nil, fmt.Errorf("docker: write key file: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(keyPath)
		return nil, fmt.Errorf("docker: close key file: %w", err)
	}

	return &remoteProvider{
		name:    name,
		host:    host,
		port:    port,
		user:    user,
		keyPath: keyPath,
	}, nil
}

func (p *remoteProvider) Type() string { return string(ProviderRemote) }
func (p *remoteProvider) Name() string { return p.name }

func (p *remoteProvider) daemonURL() string {
	return fmt.Sprintf("ssh://%s@%s:%d", p.user, p.host, p.port)
}

func (p *remoteProvider) Client(ctx context.Context) (*client.Client, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return nil, fmt.Errorf("docker: remote provider %q is closed", p.name)
	}
	if p.cli != nil {
		return p.cli, nil
	}

	sshFlags := []string{
		"-i", p.keyPath,
		"-o", "IdentitiesOnly=yes",
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "BatchMode=yes",
		"-o", "ServerAliveInterval=30",
	}

	helper, err := connhelper.GetConnectionHelperWithSSHOpts(p.daemonURL(), sshFlags)
	if err != nil {
		return nil, fmt.Errorf("docker: build ssh connection helper: %w", err)
	}
	if helper == nil {
		return nil, fmt.Errorf("docker: no connection helper for %q", p.daemonURL())
	}

	httpClient := &http.Client{
		Transport: &http.Transport{
			DialContext: helper.Dialer,
		},
	}

	cli, err := client.NewClientWithOpts(
		client.WithHTTPClient(httpClient),
		client.WithHost(helper.Host),
		client.WithDialContext(helper.Dialer),
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, fmt.Errorf("docker: new remote client: %w", err)
	}

	// Verify connectivity by issuing a Ping. This is the first round-trip and
	// catches DNS / auth / Docker-not-installed problems early.
	if _, err := cli.Ping(ctx); err != nil {
		_ = cli.Close()
		return nil, fmt.Errorf("docker: ping remote %s@%s:%d: %w", p.user, p.host, p.port, err)
	}

	p.cli = cli
	return p.cli, nil
}

func (p *remoteProvider) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true

	var firstErr error
	if p.cli != nil {
		if err := p.cli.Close(); err != nil {
			firstErr = err
		}
		p.cli = nil
	}
	if p.keyPath != "" {
		if err := os.Remove(p.keyPath); err != nil && !os.IsNotExist(err) {
			if firstErr == nil {
				firstErr = err
			}
		}
		p.keyPath = ""
	}
	return firstErr
}
