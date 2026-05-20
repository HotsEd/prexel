package command

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/prexel/prexel/internal/cli/client"
	"github.com/prexel/prexel/internal/cli/tui"
	"github.com/spf13/cobra"
)

// serverDTO mirrors the on-wire shape of internal/server.Server. We keep a
// local copy so the CLI never imports the server package directly (which
// would drag in the entire DB layer for what is just JSON unmarshalling).
type serverDTO struct {
	ID                 string  `json:"id"`
	Name               string  `json:"name"`
	Type               string  `json:"type"`
	Host               *string `json:"host,omitempty"`
	Port               int     `json:"port"`
	User               *string `json:"user,omitempty"`
	HostKeyFingerprint *string `json:"host_key_fingerprint,omitempty"`
	Status             string  `json:"status"`
	DockerVersion      *string `json:"docker_version,omitempty"`
	LastCheckedAt      *int64  `json:"last_checked_at,omitempty"`
	HasPrivateKey      bool    `json:"has_private_key"`
	PublicKey          string  `json:"public_key,omitempty"`
	CreatedAt          int64   `json:"created_at"`
}

type serverTestCheckDTO struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}
type serverTestReportDTO struct {
	ServerID string               `json:"server_id"`
	Type     string               `json:"type"`
	Status   string               `json:"status"`
	Checks   []serverTestCheckDTO `json:"checks"`
}

func newServerCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "server",
		Short: "Manage servers (CRUD of the `server` substantive)",
	}
	c.AddCommand(
		newServerListCmd(),
		newServerInfoCmd(),
		newServerTestCmd(),
		newServerAddCmd(),
		newServerEditCmd(),
		newServerRemoveCmd(),
	)
	return c
}

func newServerListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List servers",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newClient(true)
			if err != nil {
				return err
			}
			var servers []serverDTO
			if err := c.Get(cmd.Context(), "/api/v1/servers", &servers); err != nil {
				return err
			}
			if len(servers) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "(no servers)")
				return nil
			}
			// Resolve app counts in parallel (best-effort).
			tw := newTab(cmd.OutOrStdout())
			fmt.Fprintln(tw, "NAME\tTYPE\tHOST\tSTATUS\tAPPS\tDOCKER")
			for _, s := range servers {
				host := strOrDash(s.Host)
				if s.Type == "local" {
					host = "<host>"
				}
				apps := appsForServer(c, cmd.Context(), s.ID)
				dock := strOrDash(s.DockerVersion)
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\n", s.Name, s.Type, host, s.Status, apps, dock)
			}
			return tw.Flush()
		},
	}
}

func appsForServer(c *client.Client, ctx context.Context, serverID string) int {
	var apps []map[string]any
	if err := c.Get(ctx, "/api/v1/apps?server_id="+url.QueryEscape(serverID), &apps); err != nil {
		return 0
	}
	return len(apps)
}

// resolveServer accepts either an ID or a name and returns the matching server.
func resolveServer(c *client.Client, ctx context.Context, ref string) (*serverDTO, error) {
	var servers []serverDTO
	if err := c.Get(ctx, "/api/v1/servers", &servers); err != nil {
		return nil, err
	}
	for i := range servers {
		if servers[i].ID == ref || servers[i].Name == ref {
			return &servers[i], nil
		}
	}
	return nil, fmt.Errorf("server %q not found", ref)
}

func newServerInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "info <name>",
		Short: "Show detailed info for a server",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(true)
			if err != nil {
				return err
			}
			s, err := resolveServer(c, cmd.Context(), args[0])
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "id:                 %s\n", s.ID)
			fmt.Fprintf(w, "name:               %s\n", s.Name)
			fmt.Fprintf(w, "type:               %s\n", s.Type)
			fmt.Fprintf(w, "host:               %s\n", strOrDash(s.Host))
			fmt.Fprintf(w, "port:               %d\n", s.Port)
			fmt.Fprintf(w, "user:               %s\n", strOrDash(s.User))
			fmt.Fprintf(w, "status:             %s\n", s.Status)
			fmt.Fprintf(w, "docker_version:     %s\n", strOrDash(s.DockerVersion))
			fmt.Fprintf(w, "host_key_fp:        %s\n", strOrDash(s.HostKeyFingerprint))
			fmt.Fprintf(w, "has_private_key:    %v\n", s.HasPrivateKey)
			fmt.Fprintf(w, "last_checked_at:    %s\n", fmtUnixOrDash(s.LastCheckedAt))
			fmt.Fprintf(w, "created_at:         %s\n", fmtUnix(s.CreatedAt))
			return nil
		},
	}
}

func newServerTestCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "test <name>",
		Short: "Run the connectivity checklist against a server",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(true)
			if err != nil {
				return err
			}
			s, err := resolveServer(c, cmd.Context(), args[0])
			if err != nil {
				return err
			}
			var report serverTestReportDTO
			err = tui.RunSpinner(fmt.Sprintf("Testing server %s", s.Name), func() error {
				return c.Post(cmd.Context(), "/api/v1/servers/"+s.ID+"/test", nil, &report)
			})
			if err != nil {
				return err
			}
			rows := make([]tui.ReportRow, 0, len(report.Checks))
			for _, ch := range report.Checks {
				rows = append(rows, tui.ReportRow{Name: ch.Name, OK: ch.OK, Message: ch.Message})
			}
			tui.PrintReport("", rows)
			fmt.Fprintf(cmd.OutOrStdout(), "\nstatus: %s\n", report.Status)
			if report.Status != "connected" {
				return errors.New("server not connected")
			}
			return nil
		},
	}
}

func newServerAddCmd() *cobra.Command {
	var (
		flagName        string
		flagType        string
		flagHost        string
		flagPort        int
		flagUser        string
		flagGenerateKey bool
		flagPrivateKey  string
	)
	c := &cobra.Command{
		Use:   "add",
		Short: "Add a new server (interactive when no flags provided)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newClient(true)
			if err != nil {
				return err
			}
			interactive := flagName == "" && flagType == ""
			if interactive {
				if !tui.IsStdinTTY() {
					return errors.New("interactive `server add` requires a TTY; pass --name/--type instead")
				}
				if flagType, err = tui.PromptDefault("Server type (local/remote)", "remote"); err != nil {
					return err
				}
				if flagName, err = tui.Prompt("Name: "); err != nil {
					return err
				}
				if flagType == "remote" {
					if flagHost, err = tui.Prompt("Host: "); err != nil {
						return err
					}
					var portS string
					if portS, err = tui.PromptDefault("Port", "22"); err != nil {
						return err
					}
					_, _ = fmt.Sscanf(portS, "%d", &flagPort)
					if flagUser, err = tui.PromptDefault("User", "root"); err != nil {
						return err
					}
					genS, err := tui.PromptDefault("Generate SSH key? (yes/no)", "yes")
					if err != nil {
						return err
					}
					flagGenerateKey = strings.HasPrefix(strings.ToLower(genS), "y")
					if !flagGenerateKey {
						if flagPrivateKey, err = tui.Prompt("Paste private key (PEM): "); err != nil {
							return err
						}
					}
				}
			}
			if flagType == "remote" && flagPort == 0 {
				flagPort = 22
			}
			body := map[string]any{
				"type":         flagType,
				"name":         strings.TrimSpace(flagName),
				"host":         flagHost,
				"port":         flagPort,
				"user":         flagUser,
				"generate_key": flagGenerateKey,
				"private_key":  flagPrivateKey,
			}
			var out serverDTO
			if err := c.Post(cmd.Context(), "/api/v1/servers", body, &out); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Created server %s (%s)\n", out.Name, out.ID)
			if out.PublicKey != "" {
				fmt.Fprintln(cmd.OutOrStdout(), "\nAdd this public key to ~/.ssh/authorized_keys on the remote host:")
				fmt.Fprintln(cmd.OutOrStdout(), out.PublicKey)
				fmt.Fprintln(cmd.OutOrStdout(), "\nThen run:")
				fmt.Fprintf(cmd.OutOrStdout(), "  prexel server test %s\n", out.Name)
			}
			return nil
		},
	}
	c.Flags().StringVar(&flagName, "name", "", "Server name")
	c.Flags().StringVar(&flagType, "type", "", "Server type: local or remote")
	c.Flags().StringVar(&flagHost, "host", "", "Remote host (remote only)")
	c.Flags().IntVar(&flagPort, "port", 22, "SSH port (remote only)")
	c.Flags().StringVar(&flagUser, "user", "root", "SSH user (remote only)")
	c.Flags().BoolVar(&flagGenerateKey, "generate-key", false, "Generate Ed25519 SSH keypair (remote only)")
	c.Flags().StringVar(&flagPrivateKey, "private-key", "", "Pasted private key PEM (remote only, alternative to --generate-key)")
	return c
}

func newServerEditCmd() *cobra.Command {
	var (
		flagName       string
		flagHost       string
		flagPort       int
		flagUser       string
		flagSSHKeyFile string
	)
	c := &cobra.Command{
		Use:   "edit <id>",
		Short: "Edit a server's metadata (name/host/port/user/ssh-key)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(true)
			if err != nil {
				return err
			}
			body := map[string]any{}
			if cmd.Flags().Changed("name") {
				body["name"] = strings.TrimSpace(flagName)
			}
			if cmd.Flags().Changed("host") {
				body["host"] = flagHost
			}
			if cmd.Flags().Changed("port") {
				body["port"] = flagPort
			}
			if cmd.Flags().Changed("user") {
				body["user"] = flagUser
			}
			if cmd.Flags().Changed("ssh-key-file") {
				data, err := os.ReadFile(flagSSHKeyFile)
				if err != nil {
					return fmt.Errorf("read ssh key file: %w", err)
				}
				body["ssh_key"] = string(data)
			}
			if len(body) == 0 {
				return errors.New("nothing to update — pass at least one of --name/--host/--port/--user/--ssh-key-file")
			}
			var out serverDTO
			if err := c.Patch(cmd.Context(), "/api/v1/servers/"+args[0], body, &out); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Updated server %s (%s)\n", out.Name, out.ID)
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "name:    %s\n", out.Name)
			fmt.Fprintf(w, "host:    %s\n", strOrDash(out.Host))
			fmt.Fprintf(w, "port:    %d\n", out.Port)
			fmt.Fprintf(w, "user:    %s\n", strOrDash(out.User))
			return nil
		},
	}
	c.Flags().StringVar(&flagName, "name", "", "New server name")
	c.Flags().StringVar(&flagHost, "host", "", "New SSH host")
	c.Flags().IntVar(&flagPort, "port", 0, "New SSH port")
	c.Flags().StringVar(&flagUser, "user", "", "New SSH user")
	c.Flags().StringVar(&flagSSHKeyFile, "ssh-key-file", "", "Path to a private key PEM to replace the stored key")
	return c
}

func newServerRemoveCmd() *cobra.Command {
	var force bool
	c := &cobra.Command{
		Use:     "remove <name>",
		Aliases: []string{"rm"},
		Short:   "Remove a server",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(true)
			if err != nil {
				return err
			}
			s, err := resolveServer(c, cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if !force && !tui.Confirm(fmt.Sprintf("Remove server %s?", s.Name)) {
				return errors.New("cancelled")
			}
			if err := c.Delete(cmd.Context(), "/api/v1/servers/"+s.ID); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed server %s\n", s.Name)
			return nil
		},
	}
	c.Flags().BoolVar(&force, "force", false, "Skip confirmation")
	return c
}
