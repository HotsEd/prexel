// Tags do Prexel. Há duas árvores:
//
//   - `prexel tag …` — dicionário global de tags (instance-wide).
//   - `prexel app tag …` — associação tag↔app (por app).
//
// Backend: GET/POST/DELETE /api/v1/tags (global) e
// GET/PUT /api/v1/apps/{id}/tags (por app — PUT substitui o set).

package command

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/prexel/prexel/internal/cli/client"
	"github.com/prexel/prexel/internal/cli/tui"
	"github.com/spf13/cobra"
)

type tagDTO struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Color     *string `json:"color,omitempty"`
	CreatedAt int64   `json:"created_at"`
}

func newTagCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "tag",
		Aliases: []string{"tags"},
		Short:   "Gerencia o dicionário global de tags",
	}
	c.AddCommand(
		newTagListCmd(),
		newTagCreateCmd(),
		newTagRemoveCmd(),
	)
	return c
}

func newTagListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Lista todas as tags da instância",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newClient(true)
			if err != nil {
				return err
			}
			tags, err := listTags(c, cmd.Context())
			if err != nil {
				return err
			}
			if len(tags) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "(no tags)")
				return nil
			}
			tw := newTab(cmd.OutOrStdout())
			fmt.Fprintln(tw, "NAME\tCOLOR\tID")
			for _, t := range tags {
				fmt.Fprintf(tw, "%s\t%s\t%s\n", t.Name, strOrDash(t.Color), t.ID)
			}
			return tw.Flush()
		},
	}
}

func newTagCreateCmd() *cobra.Command {
	var color string
	c := &cobra.Command{
		Use:   "create <name>",
		Short: "Cria (ou retorna a existente) uma tag — POST é idempotente",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(true)
			if err != nil {
				return err
			}
			body := map[string]any{"name": args[0]}
			if color != "" {
				body["color"] = color
			}
			var out tagDTO
			if err := c.Post(cmd.Context(), "/api/v1/tags", body, &out); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Tag %s (id=%s)\n", out.Name, out.ID)
			return nil
		},
	}
	c.Flags().StringVar(&color, "color", "", "Cor opcional (ex: #ff0000)")
	return c
}

func newTagRemoveCmd() *cobra.Command {
	var force bool
	c := &cobra.Command{
		Use:     "remove <name-or-id>",
		Aliases: []string{"rm"},
		Short:   "Remove uma tag do dicionário (CASCADE em app_tags)",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(true)
			if err != nil {
				return err
			}
			t, err := resolveTag(c, cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if !force && !tui.Confirm(fmt.Sprintf("Remover tag %s e desassociá-la de todos os apps?", t.Name)) {
				return errors.New("cancelled")
			}
			if err := c.Delete(cmd.Context(), "/api/v1/tags/"+t.ID); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Tag %s removida\n", t.Name)
			return nil
		},
	}
	c.Flags().BoolVar(&force, "force", false, "Pula confirmação")
	return c
}

// listTags + resolveTag são compartilhados com a árvore app-tag — a
// resolução por nome só existe no client (o backend espera id em
// DELETE /tags/:id), então mantemos um único caminho aqui.
func listTags(c *client.Client, ctx context.Context) ([]tagDTO, error) {
	var out []tagDTO
	if err := c.Get(ctx, "/api/v1/tags", &out); err != nil {
		return nil, err
	}
	return out, nil
}

func resolveTag(c *client.Client, ctx context.Context, ref string) (*tagDTO, error) {
	tags, err := listTags(c, ctx)
	if err != nil {
		return nil, err
	}
	for i := range tags {
		if tags[i].ID == ref || strings.EqualFold(tags[i].Name, ref) {
			return &tags[i], nil
		}
	}
	return nil, fmt.Errorf("tag %q não encontrada", ref)
}

// ---------------------------------------------------------------------------
// `prexel app tag …` — per-app subtree, registrado em newAppCmd.
// ---------------------------------------------------------------------------

func newAppTagCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "tag",
		Short: "Gerencia tags atribuídas a um app",
	}
	c.AddCommand(
		newAppTagListCmd(),
		newAppTagAddCmd(),
		newAppTagRemoveCmd(),
		newAppTagSetCmd(),
	)
	return c
}

func newAppTagListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <app>",
		Short: "Lista as tags atribuídas a um app",
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
			tags, err := listAppTags(c, cmd.Context(), a.ID)
			if err != nil {
				return err
			}
			if len(tags) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "(no tags)")
				return nil
			}
			tw := newTab(cmd.OutOrStdout())
			fmt.Fprintln(tw, "NAME\tCOLOR")
			for _, t := range tags {
				fmt.Fprintf(tw, "%s\t%s\n", t.Name, strOrDash(t.Color))
			}
			return tw.Flush()
		},
	}
}

func newAppTagAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add <app> <tag> [<tag>...]",
		Short: "Adiciona uma ou mais tags ao app (tags inexistentes são criadas)",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(true)
			if err != nil {
				return err
			}
			a, err := resolveApp(c, cmd.Context(), args[0])
			if err != nil {
				return err
			}
			cur, err := listAppTags(c, cmd.Context(), a.ID)
			if err != nil {
				return err
			}
			// União: PUT substitui o set, então precisamos enviar o
			// estado FINAL que o operador quer (atuais + novas).
			set := map[string]bool{}
			for _, t := range cur {
				set[t.Name] = true
			}
			for _, n := range args[1:] {
				set[n] = true
			}
			return putAppTags(cmd, c, a.ID, a.Name, setToSlice(set))
		},
	}
}

func newAppTagRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "remove <app> <tag> [<tag>...]",
		Aliases: []string{"rm"},
		Short:   "Remove uma ou mais tags do app",
		Args:    cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(true)
			if err != nil {
				return err
			}
			a, err := resolveApp(c, cmd.Context(), args[0])
			if err != nil {
				return err
			}
			cur, err := listAppTags(c, cmd.Context(), a.ID)
			if err != nil {
				return err
			}
			drop := map[string]bool{}
			for _, n := range args[1:] {
				drop[n] = true
			}
			final := make([]string, 0, len(cur))
			for _, t := range cur {
				if !drop[t.Name] {
					final = append(final, t.Name)
				}
			}
			return putAppTags(cmd, c, a.ID, a.Name, final)
		},
	}
}

func newAppTagSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <app> <tag> [<tag>...]",
		Short: "Substitui o conjunto de tags do app (envia o set verbatim)",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(true)
			if err != nil {
				return err
			}
			a, err := resolveApp(c, cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return putAppTags(cmd, c, a.ID, a.Name, args[1:])
		},
	}
}

func listAppTags(c *client.Client, ctx context.Context, appID string) ([]tagDTO, error) {
	var out []tagDTO
	if err := c.Get(ctx, "/api/v1/apps/"+appID+"/tags", &out); err != nil {
		return nil, err
	}
	return out, nil
}

func putAppTags(cmd *cobra.Command, c *client.Client, appID, appName string, tags []string) error {
	body := map[string]any{"tags": tags}
	var out []tagDTO
	if err := c.Put(cmd.Context(), "/api/v1/apps/"+appID+"/tags", body, &out); err != nil {
		return err
	}
	names := make([]string, len(out))
	for i, t := range out {
		names[i] = t.Name
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Tags em %s: %s\n", appName, strings.Join(names, ", "))
	return nil
}

func setToSlice(s map[string]bool) []string {
	out := make([]string, 0, len(s))
	for k := range s {
		out = append(out, k)
	}
	return out
}
