// `prexel team` — CRUD de times + membros. Backend: /api/v1/teams* e
// /api/v1/members*. Resolução por id OU slug/name é feita no CLI (o
// backend exige id em path params, mas o operador raramente decora
// UUIDs — `prexel team info dev` precisa funcionar).

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

// teamDTO espelha rbac.Team.
type teamDTO struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
	Color       string `json:"color"`
	MemberCount int    `json:"member_count"`
	AppCount    int    `json:"app_count"`
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
}

type roleDTO struct {
	ID          string   `json:"id"`
	Slug        string   `json:"slug"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Scope       string   `json:"scope"`
	IsAdmin     bool     `json:"is_admin"`
	IsSystem    bool     `json:"is_system"`
	Permissions []string `json:"permissions"`
	CreatedAt   int64    `json:"created_at"`
	UpdatedAt   int64    `json:"updated_at"`
}

type memberDTO struct {
	ID       string    `json:"id"`
	Email    string    `json:"email"`
	Name     string    `json:"name"`
	Status   string    `json:"status"`
	Role     *roleDTO  `json:"role,omitempty"`
	TeamRole *roleDTO  `json:"team_role,omitempty"`
	Teams    []teamDTO `json:"teams"`
}

func newTeamCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "team",
		Short: "Gerencia times (CRUD + membros)",
	}
	c.AddCommand(
		newTeamListCmd(),
		newTeamInfoCmd(),
		newTeamAddCmd(),
		newTeamSetCmd(),
		newTeamRemoveCmd(),
		newTeamMemberCmd(),
	)
	return c
}

// ---------- list / info ----------

func newTeamListCmd() *cobra.Command {
	var outputJSON bool
	c := &cobra.Command{
		Use:   "list",
		Short: "Lista os times da instância",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cli, err := newClient(true)
			if err != nil {
				return err
			}
			teams, err := listTeams(cli, cmd.Context())
			if err != nil {
				return err
			}
			if outputJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(teams)
			}
			if len(teams) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "(no teams)")
				return nil
			}
			tw := newTab(cmd.OutOrStdout())
			fmt.Fprintln(tw, "NAME\tSLUG\tMEMBERS\tAPPS\tID")
			for _, t := range teams {
				fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%s\n", t.Name, t.Slug, t.MemberCount, t.AppCount, t.ID)
			}
			return tw.Flush()
		},
	}
	c.Flags().BoolVar(&outputJSON, "output", false, "JSON output (use --output)")
	c.Flags().Lookup("output").NoOptDefVal = "json"
	return c
}

func newTeamInfoCmd() *cobra.Command {
	var outputJSON bool
	c := &cobra.Command{
		Use:   "info <id-or-slug>",
		Short: "Mostra detalhes de um time + membros",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := newClient(true)
			if err != nil {
				return err
			}
			t, err := resolveTeam(cli, cmd.Context(), args[0])
			if err != nil {
				return err
			}
			var envelope struct {
				Team    teamDTO     `json:"team"`
				Members []memberDTO `json:"members"`
			}
			if err := cli.Get(cmd.Context(), "/api/v1/teams/"+t.ID, &envelope); err != nil {
				return err
			}
			if outputJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(envelope)
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "id:          %s\n", envelope.Team.ID)
			fmt.Fprintf(w, "name:        %s\n", envelope.Team.Name)
			fmt.Fprintf(w, "slug:        %s\n", envelope.Team.Slug)
			fmt.Fprintf(w, "description: %s\n", envelope.Team.Description)
			fmt.Fprintf(w, "color:       %s\n", envelope.Team.Color)
			fmt.Fprintf(w, "members:     %d\n", envelope.Team.MemberCount)
			fmt.Fprintf(w, "apps:        %d\n", envelope.Team.AppCount)
			fmt.Fprintln(w)
			if len(envelope.Members) == 0 {
				fmt.Fprintln(w, "(nenhum membro)")
				return nil
			}
			tw := newTab(w)
			fmt.Fprintln(tw, "EMAIL\tNAME\tTEAM ROLE\tID")
			for _, m := range envelope.Members {
				role := "-"
				if m.TeamRole != nil {
					role = m.TeamRole.Name
				} else if m.Role != nil {
					role = m.Role.Name
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", m.Email, m.Name, role, m.ID)
			}
			return tw.Flush()
		},
	}
	c.Flags().BoolVar(&outputJSON, "output", false, "JSON output (use --output)")
	c.Flags().Lookup("output").NoOptDefVal = "json"
	return c
}

// ---------- add / set / remove ----------

func newTeamAddCmd() *cobra.Command {
	var (
		name, slug, desc, color string
	)
	c := &cobra.Command{
		Use:   "add",
		Short: "Cria um novo time",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cli, err := newClient(true)
			if err != nil {
				return err
			}
			if name == "" {
				return errors.New("--name obrigatório")
			}
			body := map[string]any{"name": name}
			if slug != "" {
				body["slug"] = slug
			}
			if desc != "" {
				body["description"] = desc
			}
			if color != "" {
				body["color"] = color
			}
			var out teamDTO
			if err := cli.Post(cmd.Context(), "/api/v1/teams", body, &out); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Time criado: %s (slug=%s, id=%s)\n", out.Name, out.Slug, out.ID)
			return nil
		},
	}
	c.Flags().StringVar(&name, "name", "", "Nome do time")
	c.Flags().StringVar(&slug, "slug", "", "Slug (kebab-case)")
	c.Flags().StringVar(&desc, "description", "", "Descrição")
	c.Flags().StringVar(&color, "color", "", "Cor (#rrggbb)")
	return c
}

func newTeamSetCmd() *cobra.Command {
	var (
		name, slug, desc, color string
	)
	c := &cobra.Command{
		Use:   "set <id-or-slug>",
		Short: "Atualiza metadados do time",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := newClient(true)
			if err != nil {
				return err
			}
			t, err := resolveTeam(cli, cmd.Context(), args[0])
			if err != nil {
				return err
			}
			body := map[string]any{}
			if cmd.Flags().Changed("name") {
				body["name"] = name
			}
			if cmd.Flags().Changed("slug") {
				body["slug"] = slug
			}
			if cmd.Flags().Changed("description") {
				body["description"] = desc
			}
			if cmd.Flags().Changed("color") {
				body["color"] = color
			}
			if len(body) == 0 {
				return errors.New("nada para atualizar")
			}
			var out teamDTO
			if err := cli.Patch(cmd.Context(), "/api/v1/teams/"+t.ID, body, &out); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Time atualizado: %s (slug=%s)\n", out.Name, out.Slug)
			return nil
		},
	}
	c.Flags().StringVar(&name, "name", "", "Novo nome")
	c.Flags().StringVar(&slug, "slug", "", "Novo slug")
	c.Flags().StringVar(&desc, "description", "", "Nova descrição")
	c.Flags().StringVar(&color, "color", "", "Nova cor")
	return c
}

func newTeamRemoveCmd() *cobra.Command {
	var force bool
	c := &cobra.Command{
		Use:     "remove <id-or-slug>",
		Aliases: []string{"rm"},
		Short:   "Remove um time (precisa estar vazio de apps)",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := newClient(true)
			if err != nil {
				return err
			}
			t, err := resolveTeam(cli, cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if !force && !tui.Confirm(fmt.Sprintf("Remover time %s?", t.Name)) {
				return errors.New("cancelado")
			}
			if err := cli.Delete(cmd.Context(), "/api/v1/teams/"+t.ID); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Time %s removido.\n", t.Name)
			return nil
		},
	}
	c.Flags().BoolVar(&force, "force", false, "Pula confirmação")
	return c
}

// ---------- member subtree ----------

func newTeamMemberCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "member",
		Short: "Gerencia membros de um time",
	}
	c.AddCommand(
		newTeamMemberListCmd(),
		newTeamMemberAddCmd(),
		newTeamMemberRemoveCmd(),
	)
	return c
}

func newTeamMemberListCmd() *cobra.Command {
	var outputJSON bool
	c := &cobra.Command{
		Use:   "list <team-id-or-slug>",
		Short: "Lista membros do time",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := newClient(true)
			if err != nil {
				return err
			}
			t, err := resolveTeam(cli, cmd.Context(), args[0])
			if err != nil {
				return err
			}
			var envelope struct {
				Team    teamDTO     `json:"team"`
				Members []memberDTO `json:"members"`
			}
			if err := cli.Get(cmd.Context(), "/api/v1/teams/"+t.ID, &envelope); err != nil {
				return err
			}
			if outputJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(envelope.Members)
			}
			if len(envelope.Members) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "(nenhum membro)")
				return nil
			}
			tw := newTab(cmd.OutOrStdout())
			fmt.Fprintln(tw, "EMAIL\tNAME\tTEAM ROLE\tID")
			for _, m := range envelope.Members {
				role := "-"
				if m.TeamRole != nil {
					role = m.TeamRole.Name
				} else if m.Role != nil {
					role = m.Role.Name
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", m.Email, m.Name, role, m.ID)
			}
			return tw.Flush()
		},
	}
	c.Flags().BoolVar(&outputJSON, "output", false, "JSON output (use --output)")
	c.Flags().Lookup("output").NoOptDefVal = "json"
	return c
}

// Add usa PUT /teams/:id/members (substitui set). Para preservar os
// membros atuais, lemos a lista e adicionamos o novo antes do PUT.
func newTeamMemberAddCmd() *cobra.Command {
	var (
		email, roleRef string
	)
	c := &cobra.Command{
		Use:   "add <team-id-or-slug>",
		Short: "Adiciona um membro ao time (--email + --role)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := newClient(true)
			if err != nil {
				return err
			}
			if email == "" {
				return errors.New("--email obrigatório")
			}
			t, err := resolveTeam(cli, cmd.Context(), args[0])
			if err != nil {
				return err
			}
			user, err := resolveMemberByEmail(cli, cmd.Context(), email)
			if err != nil {
				return err
			}
			roleID := ""
			if roleRef != "" {
				r, err := resolveRole(cli, cmd.Context(), roleRef)
				if err != nil {
					return err
				}
				roleID = r.ID
			}
			// Read current set, união com o novo.
			cur, err := teamCurrentMemberIDs(cli, cmd.Context(), t.ID)
			if err != nil {
				return err
			}
			payload := buildMemberSetPayload(cur, user.ID, roleID)
			var out []memberDTO
			if err := cli.Put(cmd.Context(), "/api/v1/teams/"+t.ID+"/members", payload, &out); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Membro %s adicionado ao time %s.\n", user.Email, t.Name)
			return nil
		},
	}
	c.Flags().StringVar(&email, "email", "", "Email do membro existente")
	c.Flags().StringVar(&roleRef, "role", "", "Role no time (id ou slug)")
	return c
}

func newTeamMemberRemoveCmd() *cobra.Command {
	var (
		email string
		force bool
	)
	c := &cobra.Command{
		Use:   "remove <team-id-or-slug>",
		Short: "Remove um membro do time (--email)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := newClient(true)
			if err != nil {
				return err
			}
			if email == "" {
				return errors.New("--email obrigatório")
			}
			t, err := resolveTeam(cli, cmd.Context(), args[0])
			if err != nil {
				return err
			}
			user, err := resolveMemberByEmail(cli, cmd.Context(), email)
			if err != nil {
				return err
			}
			if !force && !tui.Confirm(fmt.Sprintf("Remover %s do time %s?", user.Email, t.Name)) {
				return errors.New("cancelado")
			}
			cur, err := teamCurrentMembers(cli, cmd.Context(), t.ID)
			if err != nil {
				return err
			}
			// Reescreve sem o user.ID.
			next := make([]map[string]any, 0, len(cur))
			for _, m := range cur {
				if m.ID == user.ID {
					continue
				}
				entry := map[string]any{"user_id": m.ID}
				if m.TeamRole != nil {
					entry["role_id"] = m.TeamRole.ID
				} else if m.Role != nil {
					entry["role_id"] = m.Role.ID
				}
				next = append(next, entry)
			}
			body := map[string]any{"members": next}
			if err := cli.Put(cmd.Context(), "/api/v1/teams/"+t.ID+"/members", body, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Membro %s removido do time %s.\n", user.Email, t.Name)
			return nil
		},
	}
	c.Flags().StringVar(&email, "email", "", "Email do membro a remover")
	c.Flags().BoolVar(&force, "force", false, "Pula confirmação")
	return c
}

// ---------- helpers ----------

func listTeams(c *client.Client, ctx context.Context) ([]teamDTO, error) {
	var out []teamDTO
	if err := c.Get(ctx, "/api/v1/teams", &out); err != nil {
		return nil, err
	}
	return out, nil
}

func resolveTeam(c *client.Client, ctx context.Context, ref string) (*teamDTO, error) {
	teams, err := listTeams(c, ctx)
	if err != nil {
		return nil, err
	}
	for i := range teams {
		if teams[i].ID == ref || teams[i].Slug == ref || strings.EqualFold(teams[i].Name, ref) {
			return &teams[i], nil
		}
	}
	return nil, fmt.Errorf("time %q não encontrado", ref)
}

func resolveMemberByEmail(c *client.Client, ctx context.Context, email string) (*memberDTO, error) {
	var members []memberDTO
	if err := c.Get(ctx, "/api/v1/members", &members); err != nil {
		return nil, err
	}
	for i := range members {
		if strings.EqualFold(members[i].Email, email) {
			return &members[i], nil
		}
	}
	return nil, fmt.Errorf("membro %q não encontrado (precisa estar na instância antes)", email)
}

func teamCurrentMembers(c *client.Client, ctx context.Context, teamID string) ([]memberDTO, error) {
	var envelope struct {
		Members []memberDTO `json:"members"`
	}
	if err := c.Get(ctx, "/api/v1/teams/"+teamID, &envelope); err != nil {
		return nil, err
	}
	return envelope.Members, nil
}

func teamCurrentMemberIDs(c *client.Client, ctx context.Context, teamID string) ([]memberDTO, error) {
	return teamCurrentMembers(c, ctx, teamID)
}

// buildMemberSetPayload constrói o body do PUT /teams/:id/members
// preservando os membros existentes e adicionando/atualizando newID.
func buildMemberSetPayload(current []memberDTO, newID, newRoleID string) map[string]any {
	members := make([]map[string]any, 0, len(current)+1)
	saw := false
	for _, m := range current {
		entry := map[string]any{"user_id": m.ID}
		if m.ID == newID {
			saw = true
			if newRoleID != "" {
				entry["role_id"] = newRoleID
			} else if m.TeamRole != nil {
				entry["role_id"] = m.TeamRole.ID
			} else if m.Role != nil {
				entry["role_id"] = m.Role.ID
			}
		} else {
			if m.TeamRole != nil {
				entry["role_id"] = m.TeamRole.ID
			} else if m.Role != nil {
				entry["role_id"] = m.Role.ID
			}
		}
		members = append(members, entry)
	}
	if !saw {
		entry := map[string]any{"user_id": newID}
		if newRoleID != "" {
			entry["role_id"] = newRoleID
		}
		members = append(members, entry)
	}
	return map[string]any{"members": members}
}
