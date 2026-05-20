package command

import (
	"errors"
	"fmt"

	"github.com/prexel/prexel/internal/cli/tui"
	"github.com/spf13/cobra"
)

func newRollbackCmd() *cobra.Command {
	var (
		toID  string
		watch bool
	)
	c := &cobra.Command{
		Use:   "rollback <app>",
		Short: "Rollback an app to a previous successful deployment",
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
			if toID == "" {
				var deps []deploymentDTO
				if err := c.Get(cmd.Context(), "/api/v1/apps/"+a.ID+"/deployments?limit=20", &deps); err != nil {
					return err
				}
				var candidates []deploymentDTO
				for _, d := range deps {
					if d.Status == "success" {
						candidates = append(candidates, d)
					}
				}
				if len(candidates) < 2 {
					return errors.New("not enough successful deployments to rollback to")
				}
				// Drop the most recent (current) one.
				candidates = candidates[1:]
				_, chosen, err := tui.Select("Select rollback target:", candidates, func(d deploymentDTO) string {
					commit := strOrDash(d.CommitSHA)
					return fmt.Sprintf("%s %s %s", d.ID, d.Status, commit)
				})
				if err != nil {
					return err
				}
				toID = chosen.ID
			}
			if !tui.ConfirmDefaultYes(fmt.Sprintf("Rollback %s to %s?", a.Name, toID)) {
				return errors.New("cancelled")
			}
			body := map[string]string{"to": toID}
			if err := c.Post(cmd.Context(), "/api/v1/apps/"+a.ID+"/rollback", body, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Rollback queued for %s → %s\n", a.Name, toID)
			if watch {
				return watchDeploy(cmd.Context(), c, a.ID)
			}
			return nil
		},
	}
	c.Flags().StringVar(&toID, "to", "", "Target deployment id (interactive picker if omitted)")
	c.Flags().BoolVar(&watch, "watch", false, "Stream events until completion")
	return c
}

func newDeploymentsCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "deployments",
		Short: "Inspect deployments",
	}
	// Default behavior: `prexel deployments <app>` lists the history. Keep that
	// path working as a sibling of the explicit `list` subcommand.
	list := newDeploymentsListCmd()
	c.AddCommand(
		list,
		newDeploymentsGetCmd(),
		newDeploymentsLogsCmd(),
	)
	return c
}

func newDeploymentsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <app>",
		Short: "Show deployment history for an app",
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
			var deps []deploymentDTO
			if err := c.Get(cmd.Context(), "/api/v1/apps/"+a.ID+"/deployments?limit=50", &deps); err != nil {
				return err
			}
			if len(deps) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "(no deployments)")
				return nil
			}
			tw := newTab(cmd.OutOrStdout())
			fmt.Fprintln(tw, "ID\tSTATUS\tCOMMIT\tBRANCH\tDURATION\tCREATED")
			for _, d := range deps {
				dur := "-"
				if d.StartedAt != nil && d.FinishedAt != nil {
					dur = fmt.Sprintf("%ds", *d.FinishedAt-*d.StartedAt)
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
					d.ID, colorStatus(d.Status), truncate(strOrDash(d.CommitSHA), 8),
					strOrDash(d.Branch), dur, fmtUnix(d.CreatedAt))
			}
			return tw.Flush()
		},
	}
}
