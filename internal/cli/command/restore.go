package command

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/prexel/prexel/internal/backup"
	"github.com/prexel/prexel/internal/config"
)

/*
   `prexel admin restore` — local-only, server-side command.

   Workflow:
     1. Operator stops the running prexel daemon (`systemctl stop prexel`).
     2. Operator runs `prexel admin restore --file backup.prexel-backup`.
     3. CLI prompts for the passphrase (hidden input).
     4. CLI verifies the daemon is NOT holding the DB by attempting a
        non-blocking open + write — if it errors, we abort and tell the
        operator to stop the service first.
     5. CLI decrypts, validates the manifest, atomically swaps
        ${DATA_DIR}/prexel.db with the snapshot's prexel.db.
     6. CLI writes the recovered PREXEL_SECRET_KEY to
        ${DATA_DIR}/SECRET_KEY.restored as a sidecar — does NOT touch
        /etc/prexel/prexel.env directly (that file is owned by the
        installer, and we don't want to assume a particular install
        layout). The operator is told to copy the value over.
     7. Operator: `systemctl start prexel`.

   Design notes:
     * We never run this against a live daemon. The two-process race
       (SQLite WAL) makes a hot swap genuinely unsafe.
     * Passphrase comes from --passphrase OR stdin (terminal echo off).
       The flag exists for CI/automation; humans use the prompt.
     * The output is loud about what was done so the operator can
       reason about the resulting state without re-reading the
       command's source.
*/
func newAdminRestoreCmd() *cobra.Command {
	var (
		file       string
		passphrase string
		yes        bool
	)
	c := &cobra.Command{
		Use:   "restore",
		Short: "Restore a Prexel instance from a .prexel-backup snapshot",
		Long: "Decrypt a Prexel backup file and replace the local SQLite database " +
			"with the snapshot inside. The Prexel daemon must be stopped first " +
			"(systemctl stop prexel). The recovered PREXEL_SECRET_KEY is written " +
			"to ${DATA_DIR}/SECRET_KEY.restored — update your env file to match " +
			"before starting the service back up.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if file == "" {
				return errors.New("--file is required")
			}
			if !strings.HasSuffix(file, backup.FileExtension) {
				return fmt.Errorf("file must end in %s", backup.FileExtension)
			}
			if _, err := os.Stat(file); err != nil {
				return fmt.Errorf("cannot read backup file: %w", err)
			}

			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("config: %w", err)
			}
			dbPath := filepath.Join(cfg.DataDir, "prexel.db")

			// Bail out cleanly if the daemon is holding the database
			// (SQLite cooperative file lock). The signal isn't 100%
			// reliable across all OSes but covers the common case
			// where the operator forgot to stop the service.
			if isDBLocked(dbPath) {
				return fmt.Errorf("prexel.db is currently in use — stop the daemon first: systemctl stop prexel")
			}

			// Passphrase: --passphrase wins (CI mode), otherwise prompt.
			pass := passphrase
			if pass == "" {
				p, err := readPassphraseFromTTY()
				if err != nil {
					return fmt.Errorf("read passphrase: %w", err)
				}
				pass = p
			}

			cmd.Println("Decrypting…")
			res, err := backup.Read(file, pass)
			if err != nil {
				return fmt.Errorf("decrypt: %w", err)
			}
			cmd.Printf("Snapshot from instance %s, taken at %s (Prexel %s).\n",
				res.Manifest.InstanceID, res.Manifest.CreatedAt.Format("2006-01-02 15:04:05 MST"), res.Manifest.AppVersion)

			if !yes {
				cmd.Print("\nThis will OVERWRITE the local database at " + dbPath + ".\nProceed? [y/N]: ")
				if !readYes(cmd) {
					return errors.New("aborted")
				}
			}

			// Backup the current DB to a sibling .pre-restore file so a
			// botched restore is recoverable in-place.
			if _, err := os.Stat(dbPath); err == nil {
				bak := dbPath + ".pre-restore"
				if err := copyFile(dbPath, bak); err != nil {
					return fmt.Errorf("safety copy: %w", err)
				}
				cmd.Printf("Saved current DB to %s\n", bak)
			}

			// Atomic write: temp file in the same dir, then rename.
			tmp := dbPath + ".restoring"
			if err := os.WriteFile(tmp, res.DBBytes, 0o600); err != nil {
				return fmt.Errorf("write db: %w", err)
			}
			if err := os.Rename(tmp, dbPath); err != nil {
				_ = os.Remove(tmp)
				return fmt.Errorf("swap db: %w", err)
			}
			cmd.Printf("Replaced %s (%d bytes).\n", dbPath, len(res.DBBytes))

			// Drop the recovered SECRET_KEY as a sidecar. We do NOT
			// rewrite the operator's env file — they may keep it in
			// /etc/prexel/prexel.env, in systemd EnvironmentFile, or
			// somewhere else entirely. The sidecar is the universal
			// surface they can grep.
			sidecar := filepath.Join(cfg.DataDir, "SECRET_KEY.restored")
			if err := os.WriteFile(sidecar, []byte(res.SecretKey+"\n"), 0o600); err != nil {
				return fmt.Errorf("write secret sidecar: %w", err)
			}
			cmd.Printf("Recovered PREXEL_SECRET_KEY written to %s\n", sidecar)
			cmd.Println("\nNext steps:")
			cmd.Println("  1. Make sure PREXEL_SECRET_KEY in your env matches the value above.")
			cmd.Println("  2. Start the service: systemctl start prexel")
			cmd.Println("  3. Once you confirm the panel works, remove the .pre-restore safety copy.")
			return nil
		},
	}
	c.Flags().StringVarP(&file, "file", "f", "", "Path to the .prexel-backup file (required)")
	c.Flags().StringVar(&passphrase, "passphrase", "", "Passphrase (omit to be prompted)")
	c.Flags().BoolVar(&yes, "yes", false, "Skip the interactive confirmation")
	return c
}

// readPassphraseFromTTY reads a single line with echo off. Errors when
// stdin isn't a terminal so CI accidents (forgetting --passphrase)
// fail loudly instead of hanging on a read that will never see input.
func readPassphraseFromTTY() (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", errors.New("stdin is not a TTY — pass --passphrase explicitly when running non-interactively")
	}
	fmt.Print("Passphrase: ")
	buf, err := term.ReadPassword(fd)
	fmt.Println()
	if err != nil {
		return "", err
	}
	s := strings.TrimSpace(string(buf))
	if s == "" {
		return "", errors.New("empty passphrase")
	}
	return s, nil
}

// readYes is a minimal yes/no reader. Anything other than `y` or `yes`
// (case-insensitive) is treated as no.
func readYes(cmd *cobra.Command) bool {
	var line string
	_, _ = fmt.Fscanln(cmd.InOrStdin(), &line)
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}

// isDBLocked attempts to open the file in exclusive R/W mode. SQLite's
// file lock isn't an OS-level mandatory lock, but the daemon does
// keep the file open for write — so a parallel `os.OpenFile` with
// O_RDWR is enough to detect the case on Linux/macOS. We deliberately
// don't try to acquire SQLite's own lock (would need to import the
// driver here just to read).
func isDBLocked(path string) bool {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return false
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return true
	}
	_ = f.Close()
	return false
}

func copyFile(src, dst string) error {
	in, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, in, 0o600)
}
