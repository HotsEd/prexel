// `prexel backup` — CRUD em /api/v1/backups e download.
//
// Backup é caro: depende de settings.instance.manage. O CLI espelha
// 1:1 a UI: list/create/download/remove. Não há "restore" remoto — o
// restore roda offline via `prexel admin restore` (e está fora do
// escopo deste comando porque opera direto em disco).

package command

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/prexel/prexel/internal/cli/tui"
	"github.com/spf13/cobra"
)

type backupEntryDTO struct {
	ID        string    `json:"id"`
	FileName  string    `json:"file_name"`
	SizeBytes int64     `json:"size_bytes"`
	CreatedAt time.Time `json:"created_at"`
}

func newBackupCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "backup",
		Short: "Snapshots criptografados da instância",
	}
	c.AddCommand(
		newBackupListCmd(),
		newBackupCreateCmd(),
		newBackupDownloadCmd(),
		newBackupRemoveCmd(),
	)
	return c
}

func newBackupListCmd() *cobra.Command {
	var outputJSON bool
	c := &cobra.Command{
		Use:   "list",
		Short: "Lista backups disponíveis",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cli, err := newClient(true)
			if err != nil {
				return err
			}
			var envelope struct {
				Backups []backupEntryDTO `json:"backups"`
			}
			if err := cli.Get(cmd.Context(), "/api/v1/backups", &envelope); err != nil {
				return err
			}
			if outputJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(envelope.Backups)
			}
			if len(envelope.Backups) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "(no backups)")
				return nil
			}
			tw := newTab(cmd.OutOrStdout())
			fmt.Fprintln(tw, "ID\tSIZE\tCREATED")
			for _, b := range envelope.Backups {
				fmt.Fprintf(tw, "%s\t%s\t%s\n", b.ID, humanSize(b.SizeBytes), b.CreatedAt.UTC().Format("2006-01-02 15:04Z"))
			}
			return tw.Flush()
		},
	}
	c.Flags().BoolVar(&outputJSON, "output", false, "JSON output (use --output)")
	c.Flags().Lookup("output").NoOptDefVal = "json"
	return c
}

func newBackupCreateCmd() *cobra.Command {
	var (
		passphrase string
		download   string
	)
	c := &cobra.Command{
		Use:   "create",
		Short: "Cria um novo backup (pede passphrase de 12+ chars)",
		Long: "Cria um snapshot criptografado da instância. A passphrase é exigida pelo " +
			"backend (12+ chars), nunca persiste no servidor — guarde-a, sem ela o " +
			"backup é inútil. Use --download <path> para baixar o arquivo logo após criar.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cli, err := newClient(true)
			if err != nil {
				return err
			}
			if passphrase == "" {
				if !tui.IsStdinTTY() {
					return errors.New("--passphrase obrigatório em modo não-interativo")
				}
				p1, err := tui.PromptPassword("Passphrase (12+ chars): ")
				if err != nil {
					return err
				}
				p2, err := tui.PromptPassword("Confirme a passphrase: ")
				if err != nil {
					return err
				}
				if p1 != p2 {
					return errors.New("passphrases não conferem")
				}
				passphrase = p1
			}
			body := map[string]string{"passphrase": passphrase}
			var entry backupEntryDTO
			err = tui.RunSpinner("Criando backup", func() error {
				return cli.Post(cmd.Context(), "/api/v1/backups", body, &entry)
			})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Backup criado: %s (%s)\n", entry.ID, humanSize(entry.SizeBytes))
			if download != "" {
				if err := downloadBackupToFile(cmd, cli.BaseURL(), cli.Config().AccessToken, cli.HTTPClient(), entry.ID, download); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Arquivo salvo em %s\n", download)
			}
			return nil
		},
	}
	c.Flags().StringVar(&passphrase, "passphrase", "", "Passphrase de criptografia (12+ chars)")
	c.Flags().StringVar(&download, "download", "", "Após criar, baixa o arquivo para o path indicado")
	return c
}

func newBackupDownloadCmd() *cobra.Command {
	var outPath string
	c := &cobra.Command{
		Use:   "download <id>",
		Short: "Baixa um backup por ID",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := newClient(true)
			if err != nil {
				return err
			}
			id := args[0]
			path := outPath
			if path == "" {
				path = id + ".prxbk"
			}
			if err := downloadBackupToFile(cmd, cli.BaseURL(), cli.Config().AccessToken, cli.HTTPClient(), id, path); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Backup %s salvo em %s\n", id, path)
			return nil
		},
	}
	c.Flags().StringVar(&outPath, "out", "", "Caminho de saída (padrão: <id>.prxbk no diretório atual)")
	return c
}

func newBackupRemoveCmd() *cobra.Command {
	var force bool
	c := &cobra.Command{
		Use:     "remove <id>",
		Aliases: []string{"rm"},
		Short:   "Remove um backup do servidor",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := newClient(true)
			if err != nil {
				return err
			}
			if !force && !tui.Confirm(fmt.Sprintf("Remover backup %s do servidor?", args[0])) {
				return errors.New("cancelado")
			}
			if err := cli.Delete(cmd.Context(), "/api/v1/backups/"+args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Backup %s removido.\n", args[0])
			return nil
		},
	}
	c.Flags().BoolVar(&force, "force", false, "Pula confirmação")
	return c
}

// ---------- helpers ----------

// downloadBackupToFile abre o endpoint /backups/:id/download (que NÃO
// usa timeout no servidor — backups podem ser dezenas de MB) e copia
// para o path local. Usa o http.Client do cliente Prexel para herdar
// o cert-pinning (verifyPeerRaw), só evita o cli.GetRaw porque esse
// faz io.ReadAll na memória.
func downloadBackupToFile(cmd *cobra.Command, baseURL, accessToken string, httpc *http.Client, id, path string) error {
	if strings.TrimSpace(id) == "" {
		return errors.New("id vazio")
	}
	req, err := http.NewRequestWithContext(cmd.Context(), http.MethodGet, baseURL+"/api/v1/backups/"+id+"/download", nil)
	if err != nil {
		return err
	}
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}
	resp, err := httpc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("download falhou: http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("criar arquivo: %w", err)
	}
	defer func() { _ = out.Close() }()
	if _, err := io.Copy(out, resp.Body); err != nil {
		return fmt.Errorf("escrever arquivo: %w", err)
	}
	return nil
}

// humanSize formata bytes em KB/MB/GB com 1 casa.
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for nn := n / unit; nn >= unit; nn /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
