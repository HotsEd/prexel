package command

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newDeploymentsGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <deployment-id>",
		Short: "Show detailed info for a deployment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(true)
			if err != nil {
				return err
			}
			var d deploymentDTO
			if err := c.Get(cmd.Context(), "/api/v1/deployments/"+args[0], &d); err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "id:           %s\n", d.ID)
			fmt.Fprintf(w, "app_id:       %s\n", d.AppID)
			fmt.Fprintf(w, "status:       %s\n", colorStatus(d.Status))
			fmt.Fprintf(w, "commit_sha:   %s\n", strOrDash(d.CommitSHA))
			fmt.Fprintf(w, "commit_msg:   %s\n", strOrDash(d.CommitMsg))
			fmt.Fprintf(w, "branch:       %s\n", strOrDash(d.Branch))
			fmt.Fprintf(w, "image_tag:    %s\n", strOrDash(d.ImageTag))
			fmt.Fprintf(w, "rollback_of:  %s\n", strOrDash(d.RollbackOf))
			fmt.Fprintf(w, "log_path:     %s\n", strOrDash(d.LogPath))
			fmt.Fprintf(w, "started_at:   %s\n", fmtUnixOrDash(d.StartedAt))
			fmt.Fprintf(w, "finished_at:  %s\n", fmtUnixOrDash(d.FinishedAt))
			if d.StartedAt != nil && d.FinishedAt != nil {
				fmt.Fprintf(w, "duration:     %ds\n", *d.FinishedAt-*d.StartedAt)
			}
			fmt.Fprintf(w, "created_at:   %s\n", fmtUnix(d.CreatedAt))
			return nil
		},
	}
}

func newDeploymentsLogsCmd() *cobra.Command {
	var (
		tail     int
		noFollow bool
	)
	c := &cobra.Command{
		Use:   "logs <deployment-id>",
		Short: "Print build log for a deployment",
		Long: "Print the build log file produced by a deployment. The endpoint " +
			"returns the log as plain text (not streamed); --no-follow is the default.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(true)
			if err != nil {
				return err
			}
			path := "/api/v1/deployments/" + args[0] + "/logs"
			if tail > 0 {
				path = fmt.Sprintf("%s?tail=%d", path, tail)
			}
			body, err := c.GetRaw(cmd.Context(), path)
			if err != nil {
				return err
			}
			_, _ = cmd.OutOrStdout().Write(body)
			return nil
		},
	}
	c.Flags().IntVar(&tail, "tail", 0, "Print only the last N lines (0 = full log)")
	// --no-follow is the documented default and exists for symmetry with `prexel
	// logs`. There's no streaming endpoint for build logs, so the flag is a no-op.
	c.Flags().BoolVar(&noFollow, "no-follow", true, "Do not stream (always true for build logs)")
	return c
}
