package command

import (
	"io/fs"

	"github.com/spf13/cobra"
)

// Assets are the embedded filesystems injected from main.go.
type Assets struct {
	Migrations fs.FS
	WebDist    fs.FS
}

// NewRoot returns the root `prexel` command with all subcommands registered.
func NewRoot(a Assets) *cobra.Command {
	root := &cobra.Command{
		Use:           "prexel",
		Short:         "Prexel — self-hosted deploy platform",
		Long:          "Prexel is a self-hosted deploy platform. Run `prexel serve` on the server, or use other subcommands as a client.",
		SilenceUsage:  true,
		SilenceErrors: false,
	}
	root.PersistentFlags().StringVar(&urlOverride, "url", "", "Prexel instance URL (overrides ~/.prexel/config.yaml)")

	root.AddCommand(
		// Server-side
		newServeCmd(a),
		newSetupCmd(),
		newAdminCmd(),

		// Foundation
		newVersionCmd(),
		newLoginCmd(),
		newLogoutCmd(),
		newWhoamiCmd(),
		newHealthcheckCmd(),

		// CRUD
		newServerCmd(),
		newAppCmd(),
		newDomainCmd(),
		newTagCmd(),

		// Deploy lifecycle
		newDeployCmd(),
		newLogsCmd(),
		newExecCmd(),
		newRollbackCmd(),
		newDeploymentsCmd(),
		newSecretsCmd(),
		newEnvCmd(),
		newGitSourcesCmd(),

		// Observability
		newEventsCmd(),

		// DNS / domain infra
		newDNSZonesCmd(),

		// Account / instance / RBAC / backups / tokens
		newAuthCmd(),
		newInstanceCmd(),
		newTeamCmd(),
		newRoleCmd(),
		newBackupCmd(),
		newTokenCmd(),
	)
	return root
}
