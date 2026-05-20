package command

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/prexel/prexel/internal/auth"
	"github.com/prexel/prexel/internal/cli/tui"
	"github.com/prexel/prexel/internal/config"
	"github.com/spf13/cobra"
)

// newSetupCmd implements `prexel setup` — a TUI wizard that walks the operator
// through the four-step bootstrap, talking to the local server over loopback
// HTTPS. Refuses to run if the local DB is not present (i.e. not on the server).
func newSetupCmd() *cobra.Command {
	var localURL string
	c := &cobra.Command{
		Use:   "setup",
		Short: "Run the interactive setup wizard (must be executed on the server)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("server config: %w", err)
			}
			dbPath := filepath.Join(cfg.DataDir, "prexel.db")
			if _, err := os.Stat(dbPath); err != nil {
				return fmt.Errorf("prexel setup must be run on the server (no DB at %s)", dbPath)
			}
			if localURL == "" {
				localURL = fmt.Sprintf("https://localhost:%d", cfg.Port)
			}
			httpc := &http.Client{
				Transport: &http.Transport{
					TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // loopback only
				},
			}

			// Step 0: status check.
			status, err := setupStatus(httpc, localURL)
			if err != nil {
				return fmt.Errorf("contact local API: %w", err)
			}
			if status.Completed {
				fmt.Fprintln(cmd.OutOrStdout(), "Setup is already complete.")
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Prexel %s — setup wizard\n\n", status.Version)

			// Step 1: admin
			email, password, err := promptAdmin()
			if err != nil {
				return err
			}
			if err := postJSON(httpc, localURL, "/api/v1/setup/admin",
				map[string]string{"email": email, "password": password, "password_confirmation": password}); err != nil {
				return fmt.Errorf("admin: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "  ✓ admin created")

			// Step 2: instance
			instURL, err := tui.PromptDefault("Instance URL (blank = self-signed only)", "")
			if err != nil {
				return err
			}
			tlsMode := "self-signed"
			if instURL != "" {
				ans, err := tui.PromptDefault("TLS mode (self-signed/letsencrypt)", "letsencrypt")
				if err != nil {
					return err
				}
				tlsMode = ans
			}
			if err := postJSON(httpc, localURL, "/api/v1/setup/instance",
				map[string]string{"instance_url": instURL, "tls_mode": tlsMode}); err != nil {
				return fmt.Errorf("instance: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "  ✓ instance saved")

			// Step 3: server
			fmt.Fprintln(cmd.OutOrStdout(), "\nFirst server:")
			srvType, err := tui.PromptDefault("Type (local/remote)", "local")
			if err != nil {
				return err
			}
			srvBody := map[string]any{"type": srvType, "name": "local", "port": 22}
			if srvType == "remote" {
				if srvBody["name"], err = tui.Prompt("Name: "); err != nil {
					return err
				}
				if srvBody["host"], err = tui.Prompt("Host: "); err != nil {
					return err
				}
				portS, err := tui.PromptDefault("Port", "22")
				if err != nil {
					return err
				}
				if n, err := strconv.Atoi(portS); err == nil {
					srvBody["port"] = n
				}
				if srvBody["user"], err = tui.PromptDefault("User", "root"); err != nil {
					return err
				}
				srvBody["generate_key"] = true
			}
			var srvOut map[string]any
			if err := postJSONOut(httpc, localURL, "/api/v1/setup/server", srvBody, &srvOut); err != nil {
				return fmt.Errorf("server: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "  ✓ server registered")
			if pk, ok := srvOut["public_key"].(string); ok && pk != "" {
				fmt.Fprintln(cmd.OutOrStdout(), "\nAdd this SSH key to the remote host:")
				fmt.Fprintln(cmd.OutOrStdout(), pk)
				_ = tui.Confirm("Press Enter once added")
			}

			// Step 4: complete
			if !tui.ConfirmDefaultYes("Finalize setup?") {
				return errors.New("cancelled")
			}
			if err := postJSON(httpc, localURL, "/api/v1/setup/complete", map[string]any{}); err != nil {
				return fmt.Errorf("complete: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "\n✓ Setup complete.")
			fmt.Fprintf(cmd.OutOrStdout(), "Next, from your laptop:\n\n  prexel login --url %s --email %s\n",
				orDefault(instURL, localURL), email)
			return nil
		},
	}
	c.Flags().StringVar(&localURL, "local-url", "", "Loopback API URL (default https://localhost:PORT)")
	return c
}

type setupStatusResp struct {
	Completed  bool   `json:"completed"`
	InstanceID string `json:"instance_id"`
	Version    string `json:"version"`
}

func setupStatus(httpc *http.Client, base string) (*setupStatusResp, error) {
	resp, err := httpc.Get(base + "/api/v1/setup/status")
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, body)
	}
	var s setupStatusResp
	if err := json.Unmarshal(body, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func postJSON(httpc *http.Client, base, path string, body any) error {
	return postJSONOut(httpc, base, path, body, nil)
}

func postJSONOut(httpc *http.Client, base, path string, body, out any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, base+path, strings.NewReader(string(buf)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("http %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	if out != nil && len(respBody) > 0 {
		return json.Unmarshal(respBody, out)
	}
	return nil
}

func promptAdmin() (string, string, error) {
	email, err := tui.Prompt("Admin email: ")
	if err != nil {
		return "", "", err
	}
	for {
		pw, err := tui.PromptPassword("Password (≥12 chars, mixed case, digit, symbol): ")
		if err != nil {
			return "", "", err
		}
		pw2, err := tui.PromptPassword("Confirm: ")
		if err != nil {
			return "", "", err
		}
		if pw != pw2 {
			fmt.Fprintln(os.Stderr, "passwords don't match")
			continue
		}
		if err := auth.ValidateStrong(pw); err != nil {
			fmt.Fprintf(os.Stderr, "weak password: %v\n", err)
			continue
		}
		return email, pw, nil
	}
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	if !strings.Contains(v, "://") {
		return "https://" + v
	}
	return v
}
