package command

import (
	"fmt"

	clicfg "github.com/prexel/prexel/internal/cli/config"
	"github.com/spf13/cobra"
)

// Version is the build version, overridable at link time:
//
//	go build -ldflags "-X github.com/prexel/prexel/internal/cli/command.Version=v0.1.0"
var Version = "0.1.0-dev"

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the prexel version (client and, if logged in, server)",
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintf(cmd.OutOrStdout(), "client: %s\n", Version)
			// Best-effort server version. Don't error out if the call fails —
			// `prexel version` must always work, even with no config.
			cfg, err := clicfg.Load()
			if err != nil || cfg.AccessToken == "" {
				return
			}
			c, err := newClient(false)
			if err != nil {
				return
			}
			var status struct {
				Version string `json:"version"`
			}
			if err := c.Get(cmd.Context(), "/api/v1/setup/status", &status); err != nil {
				return
			}
			fmt.Fprintf(cmd.OutOrStdout(), "server: %s\n", status.Version)
		},
	}
}
