package command

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"
)

// healthcheckTimeout caps the whole probe. The Dockerfile's HEALTHCHECK
// default is 30s; we stay well inside that so the container doesn't get killed
// while waiting on a stuck request.
const healthcheckTimeout = 5 * time.Second

// newHealthcheckCmd builds the `prexel healthcheck` subcommand.
//
// Purpose: the Dockerfile HEALTHCHECK invokes this; we want a probe that's
// independent of CLI auth, doesn't read ~/.prexel/config.yaml, and tolerates
// the self-signed loopback cert that `prexel serve` generates on first boot.
//
// It intentionally bypasses internal/cli/client (that path requires an access
// token and would force every container to bootstrap credentials just to be
// considered healthy). InsecureSkipVerify is acceptable here because we only
// ever hit 127.0.0.1 — anything that can MITM loopback already owns the box.
func newHealthcheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "healthcheck",
		Short: "Probe the local Prexel daemon and exit 0 if healthy",
		Long: "Probes https://127.0.0.1:${PREXEL_PORT:-3000}/api/v1/healthz and exits 0 on " +
			"HTTP 200, 1 otherwise. Designed for Docker HEALTHCHECK and systemd " +
			"watchdog use. Does not require login or auth tokens.",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			port := os.Getenv("PREXEL_PORT")
			if port == "" {
				port = "3000"
			}
			url := fmt.Sprintf("https://127.0.0.1:%s/api/v1/healthz", port)

			ctx, cancel := context.WithTimeout(cmd.Context(), healthcheckTimeout)
			defer cancel()

			client := &http.Client{
				Timeout: healthcheckTimeout,
				Transport: &http.Transport{
					TLSClientConfig: &tls.Config{
						// Loopback only — see RunE doc above.
						InsecureSkipVerify: true, //nolint:gosec
					},
					// Match Timeout to keep a slow TLS handshake from
					// blowing past our budget.
					TLSHandshakeTimeout:   healthcheckTimeout,
					ResponseHeaderTimeout: healthcheckTimeout,
				},
			}

			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if err != nil {
				return fmt.Errorf("healthcheck: build request: %w", err)
			}
			resp, err := client.Do(req)
			if err != nil {
				return fmt.Errorf("healthcheck: %s: %w", url, err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("healthcheck: %s returned %d", url, resp.StatusCode)
			}
			return nil
		},
	}
}
