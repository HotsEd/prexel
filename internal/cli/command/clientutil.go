package command

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/prexel/prexel/internal/cli/client"
	clicfg "github.com/prexel/prexel/internal/cli/config"
	"github.com/prexel/prexel/internal/cli/tui"
)

// urlOverride is bound to the persistent --url flag on the root command.
var urlOverride string

// newClient builds a Client honouring the persistent --url flag.
// When requireAuth is true, an empty AccessToken is rejected with a friendly
// error pointing at `prexel login`.
func newClient(requireAuth bool) (*client.Client, error) {
	cfg, err := clicfg.Load()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	prompt := func(q string) bool {
		fmt.Fprintln(os.Stderr, q)
		return tui.ConfirmDefaultYes("Trust?")
	}
	base := urlOverride
	if base == "" {
		base = cfg.InstanceURL
	}
	c, err := client.New(cfg, base, prompt)
	if err != nil {
		return nil, err
	}
	if requireAuth && !cfg.HasAuth() {
		return nil, errors.New("not authenticated — run `prexel login` first")
	}
	return c, nil
}

// mustClient wraps newClient with a single fatal exit point for handlers that
// want the boilerplate gone.
func mustClient(ctx context.Context, requireAuth bool) *client.Client {
	c, err := newClient(requireAuth)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	_ = ctx
	return c
}

// normalizeURL ensures a base URL has the https:// scheme.
func normalizeURL(in string) string {
	in = strings.TrimSpace(in)
	if in == "" {
		return ""
	}
	if !strings.Contains(in, "://") {
		return "https://" + in
	}
	return in
}
