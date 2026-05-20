// Volume CRUD para os volumes per-app (migration 011). O CLI é
// estritamente client HTTP — toda a validação (named XOR host,
// formato de mount_path, etc.) acontece no backend; aqui só
// montamos o body e exibimos a resposta.

package command

import (
	"errors"
	"fmt"

	"github.com/prexel/prexel/internal/cli/tui"
	"github.com/spf13/cobra"
)

// volumeDTO mirrors internal/appvolume.Volume para decode JSON.
type volumeDTO struct {
	ID        string  `json:"id"`
	AppID     string  `json:"app_id"`
	Service   *string `json:"service,omitempty"`
	MountPath string  `json:"mount_path"`
	HostPath  *string `json:"host_path,omitempty"`
	IsNamed   bool    `json:"is_named"`
	ReadOnly  bool    `json:"read_only"`
	CreatedAt int64   `json:"created_at"`
	UpdatedAt int64   `json:"updated_at"`
}

func newAppVolumeCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "volume",
		Aliases: []string{"volumes"},
		Short:   "Gerencia volumes persistentes do app",
	}
	c.AddCommand(
		newAppVolumeListCmd(),
		newAppVolumeAddCmd(),
		newAppVolumeRemoveCmd(),
	)
	return c
}

func newAppVolumeListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <app>",
		Short: "Lista os volumes de um app",
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
			var vols []volumeDTO
			if err := c.Get(cmd.Context(), "/api/v1/apps/"+a.ID+"/volumes", &vols); err != nil {
				return err
			}
			if len(vols) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "(no volumes)")
				return nil
			}
			tw := newTab(cmd.OutOrStdout())
			fmt.Fprintln(tw, "ID\tMOUNT\tTYPE\tSOURCE\tSERVICE\tRO")
			for _, v := range vols {
				typ := "bind"
				src := strOrDash(v.HostPath)
				if v.IsNamed {
					typ = "named"
					src = "<docker volume>"
				}
				ro := "✗"
				if v.ReadOnly {
					ro = "✓"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
					v.ID, v.MountPath, typ, src, strOrDash(v.Service), ro)
			}
			return tw.Flush()
		},
	}
}

func newAppVolumeAddCmd() *cobra.Command {
	var (
		mount    string
		host     string
		named    bool
		service  string
		readOnly bool
	)
	c := &cobra.Command{
		Use:   "add <app>",
		Short: "Adiciona um volume ao app (named OU bind mount)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if mount == "" {
				return errors.New("--mount é obrigatório")
			}
			// Default to named volume when neither flag is set — matches
			// the backend default and the "give me a docker volume at
			// /data" simple case from the spec.
			if !named && host == "" {
				named = true
			}
			if named && host != "" {
				return errors.New("--named e --host são mutuamente exclusivos")
			}
			c, err := newClient(true)
			if err != nil {
				return err
			}
			a, err := resolveApp(c, cmd.Context(), args[0])
			if err != nil {
				return err
			}
			body := map[string]any{
				"mount_path": mount,
				"is_named":   named,
				"read_only":  readOnly,
			}
			if !named {
				body["host_path"] = host
			}
			if service != "" {
				body["service"] = service
			}
			var out volumeDTO
			if err := c.Post(cmd.Context(), "/api/v1/apps/"+a.ID+"/volumes", body, &out); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Volume %s criado em %s (id=%s)\n", out.MountPath, a.Name, out.ID)
			return nil
		},
	}
	c.Flags().StringVar(&mount, "mount", "", "Caminho de mount no container (obrigatório)")
	c.Flags().StringVar(&host, "host", "", "Caminho no host (bind mount)")
	c.Flags().BoolVar(&named, "named", false, "Cria como named volume gerenciado pelo Docker (default quando --host vazio)")
	c.Flags().StringVar(&service, "service", "", "Serviço do compose ao qual o volume pertence")
	c.Flags().BoolVar(&readOnly, "read-only", false, "Monta como read-only")
	return c
}

func newAppVolumeRemoveCmd() *cobra.Command {
	var force bool
	c := &cobra.Command{
		Use:     "remove <app> <volume-id>",
		Aliases: []string{"rm"},
		Short:   "Remove um volume do app",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(true)
			if err != nil {
				return err
			}
			a, err := resolveApp(c, cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if !force && !tui.Confirm(fmt.Sprintf("Remover volume %s do app %s?", args[1], a.Name)) {
				return errors.New("cancelled")
			}
			// Backend não apaga dados no disco — só remove a linha. Reforçamos
			// isso no mensagem para o operador não tomar susto.
			if err := c.Delete(cmd.Context(), "/api/v1/apps/"+a.ID+"/volumes/"+args[1]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Volume %s removido (dados no disco não foram tocados)\n", args[1])
			return nil
		},
	}
	c.Flags().BoolVar(&force, "force", false, "Pula confirmação")
	return c
}
