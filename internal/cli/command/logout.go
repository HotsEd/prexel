package command

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Revoke the local credentials and clear stored tokens",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newClient(false)
			if err != nil {
				return err
			}
			if err := c.Logout(cmd.Context()); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Logged out.")
			return nil
		},
	}
}
