// `prexel role` — CRUD de papéis (RBAC). Backend: /api/v1/roles e
// /api/v1/permissions (catálogo).

package command

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/prexel/prexel/internal/cli/client"
	"github.com/prexel/prexel/internal/cli/tui"
	"github.com/spf13/cobra"
)

type permissionDTO struct {
	Slug        string `json:"slug"`
	Group       string `json:"group"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

func newRoleCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "role",
		Short: "Gerencia papéis (RBAC)",
	}
	c.AddCommand(
		newRoleListCmd(),
		newRoleInfoCmd(),
		newRoleAddCmd(),
		newRoleSetCmd(),
		newRoleRemoveCmd(),
		newRolePermissionsCmd(),
	)
	return c
}

func newRoleListCmd() *cobra.Command {
	var outputJSON bool
	c := &cobra.Command{
		Use:   "list",
		Short: "Lista os papéis",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cli, err := newClient(true)
			if err != nil {
				return err
			}
			roles, err := listRoles(cli, cmd.Context())
			if err != nil {
				return err
			}
			if outputJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(roles)
			}
			if len(roles) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "(no roles)")
				return nil
			}
			tw := newTab(cmd.OutOrStdout())
			fmt.Fprintln(tw, "NAME\tSLUG\tSCOPE\tADMIN\tSYSTEM\tPERMS\tID")
			for _, r := range roles {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%v\t%v\t%d\t%s\n",
					r.Name, r.Slug, r.Scope, r.IsAdmin, r.IsSystem, len(r.Permissions), r.ID)
			}
			return tw.Flush()
		},
	}
	c.Flags().BoolVar(&outputJSON, "output", false, "JSON output (use --output)")
	c.Flags().Lookup("output").NoOptDefVal = "json"
	return c
}

func newRoleInfoCmd() *cobra.Command {
	var outputJSON bool
	c := &cobra.Command{
		Use:   "info <id-or-slug>",
		Short: "Mostra detalhes de um papel",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := newClient(true)
			if err != nil {
				return err
			}
			r, err := resolveRole(cli, cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if outputJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(r)
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "id:          %s\n", r.ID)
			fmt.Fprintf(w, "slug:        %s\n", r.Slug)
			fmt.Fprintf(w, "name:        %s\n", r.Name)
			fmt.Fprintf(w, "description: %s\n", r.Description)
			fmt.Fprintf(w, "scope:       %s\n", r.Scope)
			fmt.Fprintf(w, "is_admin:    %v\n", r.IsAdmin)
			fmt.Fprintf(w, "is_system:   %v\n", r.IsSystem)
			fmt.Fprintf(w, "permissions: %s\n", strings.Join(r.Permissions, ", "))
			return nil
		},
	}
	c.Flags().BoolVar(&outputJSON, "output", false, "JSON output (use --output)")
	c.Flags().Lookup("output").NoOptDefVal = "json"
	return c
}

func newRoleAddCmd() *cobra.Command {
	var (
		name, desc, scope, perms string
		isAdmin                  bool
	)
	c := &cobra.Command{
		Use:   "add",
		Short: "Cria um novo papel",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cli, err := newClient(true)
			if err != nil {
				return err
			}
			if name == "" {
				return errors.New("--name obrigatório")
			}
			body := map[string]any{
				"name":     name,
				"is_admin": isAdmin,
			}
			if desc != "" {
				body["description"] = desc
			}
			if scope != "" {
				body["scope"] = scope
			}
			if perms != "" {
				body["permissions"] = splitCSV(perms)
			} else {
				body["permissions"] = []string{}
			}
			var out roleDTO
			if err := cli.Post(cmd.Context(), "/api/v1/roles", body, &out); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Papel criado: %s (slug=%s, id=%s)\n", out.Name, out.Slug, out.ID)
			return nil
		},
	}
	c.Flags().StringVar(&name, "name", "", "Nome do papel")
	c.Flags().StringVar(&desc, "description", "", "Descrição")
	c.Flags().StringVar(&scope, "scope", "", "Escopo: global|team|both")
	c.Flags().StringVar(&perms, "permissions", "", "Permissões CSV (ex: apps.view,apps.deploy). Liste via `prexel role permissions`.")
	c.Flags().BoolVar(&isAdmin, "admin", false, "Papel admin (recebe TODAS as permissões)")
	return c
}

func newRoleSetCmd() *cobra.Command {
	var (
		name, desc, scope, perms string
	)
	c := &cobra.Command{
		Use:   "set <id-or-slug>",
		Short: "Atualiza um papel",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := newClient(true)
			if err != nil {
				return err
			}
			r, err := resolveRole(cli, cmd.Context(), args[0])
			if err != nil {
				return err
			}
			body := map[string]any{}
			if cmd.Flags().Changed("name") {
				body["name"] = name
			}
			if cmd.Flags().Changed("description") {
				body["description"] = desc
			}
			if cmd.Flags().Changed("scope") {
				body["scope"] = scope
			}
			if cmd.Flags().Changed("permissions") {
				body["permissions"] = splitCSV(perms)
			}
			if len(body) == 0 {
				return errors.New("nada para atualizar")
			}
			var out roleDTO
			if err := cli.Patch(cmd.Context(), "/api/v1/roles/"+r.ID, body, &out); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Papel atualizado: %s\n", out.Name)
			return nil
		},
	}
	c.Flags().StringVar(&name, "name", "", "Novo nome")
	c.Flags().StringVar(&desc, "description", "", "Nova descrição")
	c.Flags().StringVar(&scope, "scope", "", "Novo escopo")
	c.Flags().StringVar(&perms, "permissions", "", "Nova lista de permissões CSV (substitui o set)")
	return c
}

func newRoleRemoveCmd() *cobra.Command {
	var force bool
	c := &cobra.Command{
		Use:     "remove <id-or-slug>",
		Aliases: []string{"rm"},
		Short:   "Remove um papel (precisa estar sem usuários atribuídos)",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := newClient(true)
			if err != nil {
				return err
			}
			r, err := resolveRole(cli, cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if !force && !tui.Confirm(fmt.Sprintf("Remover papel %s?", r.Name)) {
				return errors.New("cancelado")
			}
			if err := cli.Delete(cmd.Context(), "/api/v1/roles/"+r.ID); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Papel %s removido.\n", r.Name)
			return nil
		},
	}
	c.Flags().BoolVar(&force, "force", false, "Pula confirmação")
	return c
}

func newRolePermissionsCmd() *cobra.Command {
	var outputJSON bool
	c := &cobra.Command{
		Use:   "permissions",
		Short: "Lista o catálogo de permissões disponíveis",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cli, err := newClient(true)
			if err != nil {
				return err
			}
			var perms []permissionDTO
			if err := cli.Get(cmd.Context(), "/api/v1/permissions", &perms); err != nil {
				return err
			}
			if outputJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(perms)
			}
			tw := newTab(cmd.OutOrStdout())
			fmt.Fprintln(tw, "SLUG\tGROUP\tNAME\tDESCRIPTION")
			for _, p := range perms {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", p.Slug, p.Group, p.Name, p.Description)
			}
			return tw.Flush()
		},
	}
	c.Flags().BoolVar(&outputJSON, "output", false, "JSON output (use --output)")
	c.Flags().Lookup("output").NoOptDefVal = "json"
	return c
}

// ---------- helpers ----------

func listRoles(c *client.Client, ctx context.Context) ([]roleDTO, error) {
	var out []roleDTO
	if err := c.Get(ctx, "/api/v1/roles", &out); err != nil {
		return nil, err
	}
	return out, nil
}

func resolveRole(c *client.Client, ctx context.Context, ref string) (*roleDTO, error) {
	roles, err := listRoles(c, ctx)
	if err != nil {
		return nil, err
	}
	for i := range roles {
		if roles[i].ID == ref || roles[i].Slug == ref || strings.EqualFold(roles[i].Name, ref) {
			return &roles[i], nil
		}
	}
	return nil, fmt.Errorf("papel %q não encontrado", ref)
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
