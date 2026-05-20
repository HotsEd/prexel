// `prexel token` — gerencia PATs (Personal Access Tokens) do usuário
// atual. Backend: /api/v1/me/tokens. O PAT raw só aparece uma vez na
// resposta de create; depois disso, só metadata (last_four, etc.).

package command

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/prexel/prexel/internal/cli/tui"
	"github.com/spf13/cobra"
)

type tokenDTO struct {
	ID         string     `json:"id"`
	UserID     string     `json:"user_id"`
	Name       string     `json:"name"`
	LastFour   string     `json:"last_four"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

func newTokenCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "token",
		Short: "Gerencia tokens pessoais de API (PATs)",
	}
	c.AddCommand(
		newTokenListCmd(),
		newTokenCreateCmd(),
		newTokenRevokeCmd(),
	)
	return c
}

func newTokenListCmd() *cobra.Command {
	var outputJSON bool
	c := &cobra.Command{
		Use:   "list",
		Short: "Lista os PATs do usuário atual",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cli, err := newClient(true)
			if err != nil {
				return err
			}
			var envelope struct {
				Tokens []tokenDTO `json:"tokens"`
			}
			if err := cli.Get(cmd.Context(), "/api/v1/me/tokens", &envelope); err != nil {
				return err
			}
			if outputJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(envelope.Tokens)
			}
			if len(envelope.Tokens) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "(no tokens)")
				return nil
			}
			tw := newTab(cmd.OutOrStdout())
			fmt.Fprintln(tw, "NAME\tLAST4\tEXPIRES\tLAST USED\tID")
			for _, t := range envelope.Tokens {
				exp := "never"
				if t.ExpiresAt != nil {
					exp = t.ExpiresAt.UTC().Format("2006-01-02")
				}
				used := "-"
				if t.LastUsedAt != nil {
					used = t.LastUsedAt.UTC().Format("2006-01-02 15:04Z")
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", t.Name, t.LastFour, exp, used, t.ID)
			}
			return tw.Flush()
		},
	}
	c.Flags().BoolVar(&outputJSON, "output", false, "JSON output (use --output)")
	c.Flags().Lookup("output").NoOptDefVal = "json"
	return c
}

func newTokenCreateCmd() *cobra.Command {
	var (
		name      string
		expiresIn string
		expiresAt string
	)
	c := &cobra.Command{
		Use:   "create",
		Short: "Cria um novo PAT (mostra o token raw UMA vez)",
		Long: "Cria um Personal Access Token. O token raw (prx_pat_...) é impresso UMA " +
			"vez no stdout — guarde-o agora. Em chamadas subsequentes só metadata aparece.\n\n" +
			"Expiração via --expires-in (ex: 30d, 12h, 90d) OU --expires-at (RFC3339).",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cli, err := newClient(true)
			if err != nil {
				return err
			}
			if name == "" {
				return errors.New("--name obrigatório")
			}
			body := map[string]any{"name": name}
			switch {
			case expiresAt != "":
				body["expires_at"] = expiresAt
			case expiresIn != "":
				dur, err := parseHumanDuration(expiresIn)
				if err != nil {
					return fmt.Errorf("--expires-in inválido: %w", err)
				}
				body["expires_at"] = time.Now().UTC().Add(dur).Format(time.RFC3339)
			}
			var out struct {
				Token tokenDTO `json:"token"`
				Raw   string   `json:"raw"`
			}
			if err := cli.Post(cmd.Context(), "/api/v1/me/tokens", body, &out); err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "Token criado: %s (id=%s)\n", out.Token.Name, out.Token.ID)
			fmt.Fprintln(w, "\n*** Guarde o token abaixo AGORA — ele não será mostrado de novo: ***")
			fmt.Fprintln(w, out.Raw)
			if out.Token.ExpiresAt != nil {
				fmt.Fprintf(w, "\nExpira em: %s\n", out.Token.ExpiresAt.UTC().Format(time.RFC3339))
			}
			return nil
		},
	}
	c.Flags().StringVar(&name, "name", "", "Nome do token (1-100 chars)")
	c.Flags().StringVar(&expiresIn, "expires-in", "", "Duração antes de expirar (ex: 30d, 12h, 6m)")
	c.Flags().StringVar(&expiresAt, "expires-at", "", "Data de expiração (RFC3339, ex: 2026-12-31T00:00:00Z)")
	return c
}

func newTokenRevokeCmd() *cobra.Command {
	var force bool
	c := &cobra.Command{
		Use:   "revoke <id>",
		Short: "Revoga um PAT (irreversível)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := newClient(true)
			if err != nil {
				return err
			}
			if !force && !tui.Confirm(fmt.Sprintf("Revogar token %s? (irreversível)", args[0])) {
				return errors.New("cancelado")
			}
			if err := cli.Delete(cmd.Context(), "/api/v1/me/tokens/"+args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Token %s revogado.\n", args[0])
			return nil
		},
	}
	c.Flags().BoolVar(&force, "force", false, "Pula confirmação")
	return c
}

// parseHumanDuration aceita os sufixos s/m/h/d/w/M. time.ParseDuration
// não conhece d/w/M, então tratamos manualmente.
var humanDurRe = regexp.MustCompile(`^(\d+)([smhdwM])$`)

func parseHumanDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if d, err := time.ParseDuration(s); err == nil {
		return d, nil
	}
	m := humanDurRe.FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("formato esperado: <N><s|m|h|d|w|M>")
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, err
	}
	switch m[2] {
	case "s":
		return time.Duration(n) * time.Second, nil
	case "m":
		return time.Duration(n) * time.Minute, nil
	case "h":
		return time.Duration(n) * time.Hour, nil
	case "d":
		return time.Duration(n) * 24 * time.Hour, nil
	case "w":
		return time.Duration(n) * 7 * 24 * time.Hour, nil
	case "M":
		// Approximation: 30d per "month". Backend só checa is-future,
		// então a aproximação é suficiente.
		return time.Duration(n) * 30 * 24 * time.Hour, nil
	}
	return 0, fmt.Errorf("sufixo desconhecido: %s", m[2])
}
