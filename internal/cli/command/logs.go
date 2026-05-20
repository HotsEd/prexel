package command

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/prexel/prexel/internal/cli/tui"
	"github.com/spf13/cobra"
)

func newLogsCmd() *cobra.Command {
	var (
		tail      int
		noFollow  bool
		container string
	)
	c := &cobra.Command{
		Use:   "logs <app>",
		Short: "Stream container logs for an app",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(true)
			if err != nil {
				return err
			}
			a, err := resolveApp(c, cmd.Context(), args[0])
			if err != nil {
				return err
			}
			// With --container, hit the per-container endpoint. Without
			// it, fall back to the legacy app-level stream (assumes a
			// single container — set when the deploy engine wrote
			// app.ContainerName).
			var path string
			if strings.TrimSpace(container) != "" {
				path = fmt.Sprintf("/api/v1/apps/%s/containers/%s/logs?tail=%d",
					a.ID, url.PathEscape(container), tail)
			} else {
				path = fmt.Sprintf("/api/v1/apps/%s/logs?tail=%d", a.ID, tail)
			}
			ch, cancel, err := c.SSE(cmd.Context(), path, "")
			if err != nil {
				return err
			}
			defer cancel()
			ctx, ctxCancel := context.WithCancel(cmd.Context())
			defer ctxCancel()
			lineCount := 0
			for ev := range ch {
				var p struct {
					Stream string `json:"stream"`
					Line   string `json:"line"`
				}
				if json.Unmarshal([]byte(ev.Data), &p) != nil {
					continue
				}
				if p.Stream == "stderr" && tui.IsTTY() {
					fmt.Fprintf(cmd.OutOrStdout(), "\033[31m%s\033[0m\n", p.Line)
				} else if p.Stream == "stdout" && tui.IsTTY() {
					fmt.Fprintf(cmd.OutOrStdout(), "\033[2m%s\033[0m\n", p.Line)
				} else {
					fmt.Fprintln(cmd.OutOrStdout(), p.Line)
				}
				lineCount++
				if noFollow && lineCount >= tail {
					ctxCancel()
					return nil
				}
			}
			_ = ctx
			_ = os.Stderr
			return nil
		},
	}
	c.Flags().IntVar(&tail, "tail", 100, "Number of lines to start from")
	c.Flags().BoolVar(&noFollow, "no-follow", false, "Print existing logs and exit (best-effort)")
	c.Flags().StringVar(&container, "container", "", "Container name (Compose apps with multiple services)")
	return c
}

