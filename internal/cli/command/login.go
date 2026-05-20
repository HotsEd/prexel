package command

import (
	"context"
	"fmt"
	"strings"

	"github.com/prexel/prexel/internal/cli/client"
	clicfg "github.com/prexel/prexel/internal/cli/config"
	"github.com/prexel/prexel/internal/cli/tui"
	"github.com/spf13/cobra"
)

func newLoginCmd() *cobra.Command {
	var (
		flagURL      string
		flagEmail    string
		flagPassword string
	)
	c := &cobra.Command{
		Use:   "login",
		Short: "Authenticate against a Prexel instance",
		Long: "Authenticate against a Prexel instance. The URL and credentials may be " +
			"passed via flags or prompted interactively.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			cfg, err := clicfg.Load()
			if err != nil {
				return err
			}
			url := flagURL
			if url == "" {
				url = urlOverride
			}
			if url == "" {
				url = cfg.InstanceURL
			}
			if url == "" {
				if !tui.IsStdinTTY() {
					return fmt.Errorf("no --url supplied and no instance configured")
				}
				ans, err := tui.Prompt("Prexel instance URL: ")
				if err != nil {
					return err
				}
				url = ans
			}
			url = normalizeURL(url)

			prompt := func(q string) bool {
				fmt.Fprintln(cmd.ErrOrStderr(), q)
				return tui.ConfirmDefaultYes("Trust?")
			}
			cli, err := client.New(cfg, url, prompt)
			if err != nil {
				return err
			}

			email := strings.TrimSpace(flagEmail)
			if email == "" {
				if !tui.IsStdinTTY() {
					return fmt.Errorf("--email required (non-interactive)")
				}
				email, err = tui.Prompt("Email: ")
				if err != nil {
					return err
				}
			}
			password := flagPassword
			if password == "" {
				if !tui.IsStdinTTY() {
					return fmt.Errorf("--password required (non-interactive)")
				}
				password, err = tui.PromptPassword("Password: ")
				if err != nil {
					return err
				}
			}
			if email == "" || password == "" {
				return fmt.Errorf("email and password are required")
			}

			if _, err := cli.Login(ctx, email, password); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Authenticated as %s\n", email)

			// Fetch and surface instance info (best-effort).
			var status struct {
				Completed  bool   `json:"completed"`
				InstanceID string `json:"instance_id"`
				Version    string `json:"version"`
			}
			if err := cli.Get(context.Background(), "/api/v1/setup/status", &status); err == nil {
				fmt.Fprintf(cmd.OutOrStdout(), "Instance: %s (server version %s)\n", status.InstanceID, status.Version)
			}
			return nil
		},
	}
	c.Flags().StringVar(&flagURL, "url", "", "Prexel instance URL (overrides config)")
	c.Flags().StringVar(&flagEmail, "email", "", "Admin email")
	c.Flags().StringVar(&flagPassword, "password", "", "Admin password (omit for interactive prompt)")
	return c
}
