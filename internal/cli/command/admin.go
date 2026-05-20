package command

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/prexel/prexel/internal/auth"
	"github.com/prexel/prexel/internal/config"
	"github.com/prexel/prexel/internal/db"
	"github.com/spf13/cobra"
)

func newAdminCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "admin",
		Short: "Local administrative commands (must run on the server)",
		Long: "Administrative subcommands that operate directly on the local Prexel " +
			"database. Intended to be executed on the server itself when the API " +
			"is unreachable (e.g. forgotten admin password).",
	}
	cmd.AddCommand(newAdminResetPasswordCmd())
	cmd.AddCommand(newAdminRestoreCmd())
	return cmd
}

func newAdminResetPasswordCmd() *cobra.Command {
	var (
		email    string
		password string
	)
	c := &cobra.Command{
		Use:   "reset-password",
		Short: "Reset an admin password by writing directly to the local DB",
		RunE: func(cmd *cobra.Command, args []string) error {
			if email == "" || password == "" {
				return errors.New("--email and --password are required")
			}
			if err := auth.ValidateStrong(password); err != nil {
				return fmt.Errorf("password too weak: %w", err)
			}
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("config: %w", err)
			}
			dbPath := filepath.Join(cfg.DataDir, "prexel.db")
			if _, err := os.Stat(dbPath); err != nil {
				return fmt.Errorf("local database not found at %s — run this on the server", dbPath)
			}
			database, err := db.Open(cfg)
			if err != nil {
				return fmt.Errorf("open db: %w", err)
			}
			defer func() { _ = database.Close() }()

			var userID string
			if err := database.QueryRow(`SELECT id FROM users WHERE email = ?`, email).Scan(&userID); err != nil {
				return fmt.Errorf("user not found: %s", email)
			}
			hash, err := auth.HashPassword(password)
			if err != nil {
				return fmt.Errorf("hash: %w", err)
			}
			tx, err := database.Begin()
			if err != nil {
				return err
			}
			defer func() { _ = tx.Rollback() }()
			if _, err := tx.Exec(`UPDATE users SET password_hash = ? WHERE id = ?`, hash, userID); err != nil {
				return fmt.Errorf("update: %w", err)
			}
			if _, err := tx.Exec(
				`UPDATE refresh_tokens SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL`,
				time.Now().Unix(), userID,
			); err != nil {
				return fmt.Errorf("revoke tokens: %w", err)
			}
			if err := tx.Commit(); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "password reset for %s (user_id=%s)\n", email, userID)
			return nil
		},
	}
	c.Flags().StringVar(&email, "email", "", "email of the admin user")
	c.Flags().StringVar(&password, "password", "", "new password (≥12 chars, mixed case, digit, symbol)")
	return c
}
