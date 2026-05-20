// `prexel auth` — gerencia a conta autenticada: 2FA, profile, senha, email.
//
// Esse comando consome estritamente a REST API. Nada toca DB direto.
// Todo input sensível (senhas, códigos TOTP) usa term.ReadPassword
// quando o stdin é TTY; em CI/scripts, os campos viram flags.

package command

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/prexel/prexel/internal/cli/client"
	"github.com/prexel/prexel/internal/cli/tui"
	qrcode "github.com/skip2/go-qrcode"
	"github.com/spf13/cobra"
)

// userContextDTO espelha o /auth/me quando RBAC está wireado. Mantemos
// só os campos que o CLI mostra; o restante é ignorado pelo decoder.
type userContextDTO struct {
	ID          string   `json:"id"`
	Email       string   `json:"email"`
	Name        string   `json:"name,omitempty"`
	Status      string   `json:"status,omitempty"`
	RoleSlug    string   `json:"role,omitempty"`
	Permissions []string `json:"permissions,omitempty"`
	IsAdmin     bool     `json:"is_admin,omitempty"`
}

func newAuthCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "auth",
		Short: "Gerencia a conta autenticada (2FA, profile, senha, email)",
	}
	c.AddCommand(
		newAuthProfileCmd(),
		newAuthPasswordCmd(),
		newAuthEmailCmd(),
		newAuth2FACmd(),
	)
	return c
}

// ---------- profile ----------

func newAuthProfileCmd() *cobra.Command {
	var outputJSON bool
	c := &cobra.Command{
		Use:   "profile",
		Short: "Mostra os dados do usuário atual",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cli, err := newClient(true)
			if err != nil {
				return err
			}
			me, err := fetchMe(cli, cmd.Context())
			if err != nil {
				return err
			}
			if outputJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(me)
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "id:          %s\n", me.ID)
			fmt.Fprintf(w, "email:       %s\n", me.Email)
			fmt.Fprintf(w, "name:        %s\n", me.Name)
			fmt.Fprintf(w, "role:        %s\n", me.RoleSlug)
			fmt.Fprintf(w, "status:      %s\n", me.Status)
			fmt.Fprintf(w, "is_admin:    %v\n", me.IsAdmin)
			if len(me.Permissions) > 0 {
				fmt.Fprintf(w, "permissions: %s\n", strings.Join(me.Permissions, ", "))
			}
			return nil
		},
	}
	c.Flags().BoolVar(&outputJSON, "output", false, "JSON output (use --output)")
	c.Flags().Lookup("output").NoOptDefVal = "json"

	c.AddCommand(newAuthProfileSetCmd())
	return c
}

func newAuthProfileSetCmd() *cobra.Command {
	var name string
	c := &cobra.Command{
		Use:   "set",
		Short: "Atualiza atributos do profile (--name)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cli, err := newClient(true)
			if err != nil {
				return err
			}
			body := map[string]any{}
			if cmd.Flags().Changed("name") {
				body["name"] = strings.TrimSpace(name)
			}
			if len(body) == 0 {
				return errors.New("nada para atualizar — passe ao menos --name")
			}
			var me userContextDTO
			if err := cli.Patch(cmd.Context(), "/api/v1/auth/profile", body, &me); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Profile atualizado: %s (%s)\n", me.Email, me.Name)
			return nil
		},
	}
	c.Flags().StringVar(&name, "name", "", "Nome do usuário (até 120 chars)")
	return c
}

// ---------- password / email ----------

func newAuthPasswordCmd() *cobra.Command {
	var (
		current  string
		newpw    string
		twoFA    string
		twoMode  string
	)
	c := &cobra.Command{
		Use:   "password",
		Short: "Troca a senha do usuário",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cli, err := newClient(true)
			if err != nil {
				return err
			}
			if current == "" {
				if !tui.IsStdinTTY() {
					return errors.New("--current obrigatório em modo não-interativo")
				}
				current, err = tui.PromptPassword("Senha atual: ")
				if err != nil {
					return err
				}
			}
			if newpw == "" {
				if !tui.IsStdinTTY() {
					return errors.New("--new obrigatório em modo não-interativo")
				}
				newpw, err = tui.PromptPassword("Nova senha: ")
				if err != nil {
					return err
				}
				conf, err := tui.PromptPassword("Confirme a nova senha: ")
				if err != nil {
					return err
				}
				if conf != newpw {
					return errors.New("senhas não conferem")
				}
			}
			body := map[string]any{
				"current_password": current,
				"new_password":     newpw,
			}
			if twoFA != "" {
				body["two_factor_code"] = twoFA
				if twoMode != "" {
					body["two_factor_method"] = twoMode
				}
			}
			if err := cli.Post(cmd.Context(), "/api/v1/auth/password", body, nil); err != nil {
				// 2FA pode ser exigido — repete pedindo o código.
				if isAPIErr(err, "twofactor_required") && tui.IsStdinTTY() {
					code, perr := tui.Prompt("Código 2FA (TOTP) ou recovery code: ")
					if perr != nil {
						return perr
					}
					body["two_factor_code"] = code
					body["two_factor_method"] = guessTwoFAMethod(code)
					if err := cli.Post(cmd.Context(), "/api/v1/auth/password", body, nil); err != nil {
						return err
					}
				} else {
					return err
				}
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Senha trocada — você foi deslogado, rode `prexel login` de novo.")
			return nil
		},
	}
	c.Flags().StringVar(&current, "current", "", "Senha atual (omita para prompt interativo)")
	c.Flags().StringVar(&newpw, "new", "", "Nova senha (omita para prompt interativo)")
	c.Flags().StringVar(&twoFA, "2fa-code", "", "Código 2FA (TOTP ou recovery), quando aplicável")
	c.Flags().StringVar(&twoMode, "2fa-method", "", "totp|recovery (auto-detectado se omitido)")
	return c
}

func newAuthEmailCmd() *cobra.Command {
	var (
		newEmail string
		current  string
		twoFA    string
		twoMode  string
	)
	c := &cobra.Command{
		Use:   "email",
		Short: "Troca o email da conta",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cli, err := newClient(true)
			if err != nil {
				return err
			}
			if newEmail == "" {
				if !tui.IsStdinTTY() {
					return errors.New("--email obrigatório em modo não-interativo")
				}
				newEmail, err = tui.Prompt("Novo email: ")
				if err != nil {
					return err
				}
			}
			if current == "" {
				if !tui.IsStdinTTY() {
					return errors.New("--password obrigatório em modo não-interativo")
				}
				current, err = tui.PromptPassword("Senha atual: ")
				if err != nil {
					return err
				}
			}
			body := map[string]any{
				"email":            strings.TrimSpace(newEmail),
				"current_password": current,
			}
			if twoFA != "" {
				body["two_factor_code"] = twoFA
				if twoMode != "" {
					body["two_factor_method"] = twoMode
				}
			}
			var me userContextDTO
			if err := cli.Post(cmd.Context(), "/api/v1/auth/email", body, &me); err != nil {
				if isAPIErr(err, "twofactor_required") && tui.IsStdinTTY() {
					code, perr := tui.Prompt("Código 2FA (TOTP) ou recovery code: ")
					if perr != nil {
						return perr
					}
					body["two_factor_code"] = code
					body["two_factor_method"] = guessTwoFAMethod(code)
					if err := cli.Post(cmd.Context(), "/api/v1/auth/email", body, &me); err != nil {
						return err
					}
				} else {
					return err
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Email atualizado para %s\n", me.Email)
			return nil
		},
	}
	c.Flags().StringVar(&newEmail, "email", "", "Novo email")
	c.Flags().StringVar(&current, "password", "", "Senha atual (omita para prompt interativo)")
	c.Flags().StringVar(&twoFA, "2fa-code", "", "Código 2FA (quando aplicável)")
	c.Flags().StringVar(&twoMode, "2fa-method", "", "totp|recovery")
	return c
}

// ---------- 2FA ----------

func newAuth2FACmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "2fa",
		Short: "Gerencia 2FA da conta (setup, disable, recovery-codes, status)",
	}
	c.AddCommand(
		newAuth2FAStatusCmd(),
		newAuth2FASetupCmd(),
		newAuth2FADisableCmd(),
		newAuth2FARecoveryCmd(),
	)
	return c
}

func newAuth2FAStatusCmd() *cobra.Command {
	var outputJSON bool
	c := &cobra.Command{
		Use:   "status",
		Short: "Mostra status do 2FA",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cli, err := newClient(true)
			if err != nil {
				return err
			}
			var out struct {
				Enabled                bool   `json:"enabled"`
				ConfirmedAt            *int64 `json:"confirmed_at"`
				RecoveryCodesRemaining int    `json:"recovery_codes_remaining"`
			}
			if err := cli.Get(cmd.Context(), "/api/v1/auth/2fa/status", &out); err != nil {
				return err
			}
			if outputJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(out)
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "enabled:                  %v\n", out.Enabled)
			fmt.Fprintf(w, "confirmed_at:             %s\n", fmtUnixOrDash(out.ConfirmedAt))
			fmt.Fprintf(w, "recovery_codes_remaining: %d\n", out.RecoveryCodesRemaining)
			return nil
		},
	}
	c.Flags().BoolVar(&outputJSON, "output", false, "JSON output (use --output)")
	c.Flags().Lookup("output").NoOptDefVal = "json"
	return c
}

func newAuth2FASetupCmd() *cobra.Command {
	var password string
	c := &cobra.Command{
		Use:   "setup",
		Short: "Pareia um autenticador TOTP e ativa 2FA",
		Long: "Inicia o setup de 2FA: imprime um QR ASCII + otpauth URL, pede o " +
			"código gerado pelo authenticator (Google Authenticator/Authy/1Password/etc.) " +
			"e ativa o 2FA. Os recovery codes são impressos UMA vez no final — guarde-os.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cli, err := newClient(true)
			if err != nil {
				return err
			}
			if password == "" {
				if !tui.IsStdinTTY() {
					return errors.New("--password obrigatório em modo não-interativo")
				}
				password, err = tui.PromptPassword("Senha atual: ")
				if err != nil {
					return err
				}
			}
			var setup struct {
				Secret     string `json:"secret"`
				OtpauthURL string `json:"otpauth_url"`
				// QRCodePNGB64 está disponível mas o CLI renderiza
				// seu próprio QR ASCII para evitar despejar PNG no
				// stdout. Mantido para visibilidade.
				QRCodePNGB64 string `json:"qr_code_png_b64"`
			}
			if err := cli.Post(cmd.Context(), "/api/v1/auth/2fa/setup-initiate",
				map[string]string{"password": password}, &setup); err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			fmt.Fprintln(w, "Escaneie o QR abaixo no seu authenticator (ou cole o otpauth URL):")
			fmt.Fprintln(w)
			qr, err := qrcode.New(setup.OtpauthURL, qrcode.Medium)
			if err != nil {
				return fmt.Errorf("render QR: %w", err)
			}
			fmt.Fprintln(w, qr.ToSmallString(false))
			fmt.Fprintf(w, "otpauth URL: %s\n", setup.OtpauthURL)
			fmt.Fprintf(w, "secret:      %s\n", setup.Secret)
			fmt.Fprintln(w)

			if !tui.IsStdinTTY() {
				return errors.New("setup precisa de TTY para confirmar o código TOTP — rode interativamente")
			}
			code, err := tui.Prompt("Código TOTP gerado pelo app: ")
			if err != nil {
				return err
			}
			var confirm struct {
				RecoveryCodes []string `json:"recovery_codes"`
			}
			if err := cli.Post(cmd.Context(), "/api/v1/auth/2fa/setup-confirm",
				map[string]string{"code": strings.TrimSpace(code)}, &confirm); err != nil {
				return err
			}
			fmt.Fprintln(w, "\n2FA ATIVADO. Guarde os recovery codes abaixo (UMA vez):")
			for _, c := range confirm.RecoveryCodes {
				fmt.Fprintln(w, "  "+c)
			}
			return nil
		},
	}
	c.Flags().StringVar(&password, "password", "", "Senha atual (omita para prompt)")
	return c
}

func newAuth2FADisableCmd() *cobra.Command {
	var (
		password string
		code     string
		force    bool
	)
	c := &cobra.Command{
		Use:   "disable",
		Short: "Desativa 2FA na conta",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cli, err := newClient(true)
			if err != nil {
				return err
			}
			if !force && !tui.Confirm("Desativar 2FA da sua conta?") {
				return errors.New("cancelado")
			}
			if password == "" {
				if !tui.IsStdinTTY() {
					return errors.New("--password obrigatório em modo não-interativo")
				}
				password, err = tui.PromptPassword("Senha atual: ")
				if err != nil {
					return err
				}
			}
			if code == "" {
				if !tui.IsStdinTTY() {
					return errors.New("--code obrigatório em modo não-interativo")
				}
				code, err = tui.Prompt("Código TOTP (ou recovery code): ")
				if err != nil {
					return err
				}
			}
			body := map[string]string{
				"password": password,
				"code":     strings.TrimSpace(code),
			}
			if err := cli.Post(cmd.Context(), "/api/v1/auth/2fa/disable", body, nil); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "2FA desativado.")
			return nil
		},
	}
	c.Flags().StringVar(&password, "password", "", "Senha atual")
	c.Flags().StringVar(&code, "code", "", "Código TOTP atual ou recovery code")
	c.Flags().BoolVar(&force, "force", false, "Pula confirmação")
	return c
}

func newAuth2FARecoveryCmd() *cobra.Command {
	var password string
	c := &cobra.Command{
		Use:     "recovery-codes",
		Aliases: []string{"recovery"},
		Short:   "Regenera os recovery codes (invalida os antigos)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cli, err := newClient(true)
			if err != nil {
				return err
			}
			if password == "" {
				if !tui.IsStdinTTY() {
					return errors.New("--password obrigatório em modo não-interativo")
				}
				password, err = tui.PromptPassword("Senha atual: ")
				if err != nil {
					return err
				}
			}
			var out struct {
				RecoveryCodes []string `json:"recovery_codes"`
			}
			if err := cli.Post(cmd.Context(), "/api/v1/auth/2fa/recovery-codes",
				map[string]string{"password": password}, &out); err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			fmt.Fprintln(w, "Novos recovery codes (os anteriores foram invalidados):")
			for _, c := range out.RecoveryCodes {
				fmt.Fprintln(w, "  "+c)
			}
			return nil
		},
	}
	c.Flags().StringVar(&password, "password", "", "Senha atual (omita para prompt)")
	return c
}

// ---------- helpers ----------

func fetchMe(c *client.Client, ctx context.Context) (*userContextDTO, error) {
	var out userContextDTO
	if err := c.Get(ctx, "/api/v1/auth/me", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// isAPIErr reports whether err is a *client.HTTPError carrying the given code.
func isAPIErr(err error, code string) bool {
	var he *client.HTTPError
	if errors.As(err, &he) {
		return he.Code == code
	}
	return false
}

// guessTwoFAMethod tenta inferir o método pelo formato do código.
// TOTP é 6 dígitos numéricos; recovery codes do Prexel são alfanuméricos
// com 10+ chars (formato xxxx-xxxx ou similar). Heurística simples — se
// a inferência falhar o backend retorna unknown_method e o operador
// pode passar --2fa-method explicitamente.
func guessTwoFAMethod(code string) string {
	c := strings.TrimSpace(code)
	if len(c) == 6 && isAllDigits(c) {
		return "totp"
	}
	return "recovery"
}

func isAllDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}
