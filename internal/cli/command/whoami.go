package command

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func newWhoamiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show the current authenticated user and instance",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newClient(true)
			if err != nil {
				return err
			}
			var status struct {
				Completed  bool   `json:"completed"`
				InstanceID string `json:"instance_id"`
				Version    string `json:"version"`
			}
			if err := c.Get(cmd.Context(), "/api/v1/setup/status", &status); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "instance_id: %s\n", status.InstanceID)
			fmt.Fprintf(cmd.OutOrStdout(), "version:     %s\n", status.Version)
			fmt.Fprintf(cmd.OutOrStdout(), "completed:   %v\n", status.Completed)
			fmt.Fprintf(cmd.OutOrStdout(), "instance_url: %s\n", c.BaseURL())
			if sub, ok := decodeJWTSubject(c.Config().AccessToken); ok {
				fmt.Fprintf(cmd.OutOrStdout(), "user_id:     %s\n", sub)
			}
			return nil
		},
	}
}

// decodeJWTSubject reads the JWT payload (no signature verification — purely
// for display).
func decodeJWTSubject(tok string) (string, bool) {
	parts := strings.Split(tok, ".")
	if len(parts) < 2 {
		return "", false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", false
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", false
	}
	if v, ok := claims["sub"].(string); ok {
		return v, true
	}
	return "", false
}
