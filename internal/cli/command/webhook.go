// Rotação de webhook secret de uma git source GitHub App. Endpoint
// dedicado porque o secret só é exposto uma vez na resposta — depois
// disso o backend só guarda o set/unset flag e não conseguimos ler o
// valor de volta.

package command

import (
	"errors"
	"fmt"

	"github.com/prexel/prexel/internal/cli/tui"
	"github.com/spf13/cobra"
)

// newGitSourcesRegenerateWebhookCmd is registered as a subcommand of
// `git-sources`. Wiring lives in gitsources.go (newGitSourcesCmd) to
// keep the existing pattern; this file just provides the constructor.
func newGitSourcesRegenerateWebhookCmd() *cobra.Command {
	var force bool
	c := &cobra.Command{
		Use:   "regenerate-webhook <source-id-or-name>",
		Short: "Rotaciona o webhook secret de uma git source (GitHub App)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(true)
			if err != nil {
				return err
			}
			src, err := resolveGitSource(c, cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if !force && !tui.Confirm(
				fmt.Sprintf("Rotacionar webhook de %s? Pushes do GitHub vão falhar até você atualizar o secret no App.", src.Name)) {
				return errors.New("cancelled")
			}
			// O endpoint retorna o sourceDTO com WebhookSecret revelado
			// uma única vez. Não chamamos GET depois — o backend volta
			// a mascarar.
			var out struct {
				WebhookURL    string `json:"webhook_url"`
				WebhookSecret string `json:"webhook_secret"`
			}
			if err := c.Post(cmd.Context(),
				"/api/v1/git-sources/"+src.ID+"/regenerate-webhook", nil, &out); err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			fmt.Fprintln(w, "Novo webhook secret gerado. Atualize no GitHub App AGORA:")
			fmt.Fprintf(w, "  webhook URL:    %s\n", out.WebhookURL)
			fmt.Fprintf(w, "  webhook secret: %s\n", out.WebhookSecret)
			fmt.Fprintln(w, "")
			fmt.Fprintln(w, "Este secret não será exibido novamente.")
			return nil
		},
	}
	c.Flags().BoolVar(&force, "force", false, "Pula confirmação")
	return c
}
