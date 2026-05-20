// `prexel instance` — GET/PATCH em /api/v1/instance/settings.
//
// O backend é opinativo: settings é singleton tipado, sem k/v genérico.
// Aqui só expomos os campos que aceitam mutação via UpdateInput; nada
// de "knob mágico". `prexel instance set` cobre 1 chave por chamada
// (ou batch via --file).

package command

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// settingsDTO mirrors internal/instance.Settings.
type settingsDTO struct {
	InstanceURL           string `json:"instance_url"`
	TLSMode               string `json:"tls_mode"`
	DefaultMemoryLimit    string `json:"default_memory_limit"`
	DefaultCPULimit       string `json:"default_cpu_limit"`
	CleanupEnabled        bool   `json:"cleanup_enabled"`
	CleanupSchedule       string `json:"cleanup_schedule"`
	CleanupDiskThreshold  int    `json:"cleanup_disk_threshold"`
	CleanupImageRetention int    `json:"cleanup_image_retention"`
	MaxConcurrentDeploys  int    `json:"max_concurrent_deploys"`
	MaintenanceMode       bool   `json:"maintenance_mode"`
	MaintenanceMessage    string `json:"maintenance_message"`
	UpdatedAt             int64  `json:"updated_at"`
}

// allowedSettingKeys é a whitelist de chaves que `set` aceita. Espelha
// 1:1 os campos de UpdateInput do backend; novos knobs precisam de
// migração + adição aqui. O valor mapeado é uma função que converte
// uma string CLI no tipo correto (e produz o payload final).
var allowedSettingKeys = map[string]func(string) (any, error){
	"instance_url":            asString,
	"tls_mode":                asString,
	"default_memory_limit":    asString,
	"default_cpu_limit":       asString,
	"cleanup_enabled":         asBool,
	"cleanup_schedule":        asString,
	"cleanup_disk_threshold":  asInt,
	"cleanup_image_retention": asInt,
	"max_concurrent_deploys":  asInt,
	"maintenance_mode":        asBool,
	"maintenance_message":     asString,
}

func newInstanceCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "instance",
		Short: "Configurações globais da instância",
	}
	c.AddCommand(
		newInstanceGetCmd(),
		newInstanceSetCmd(),
	)
	return c
}

func newInstanceGetCmd() *cobra.Command {
	var outputJSON bool
	c := &cobra.Command{
		Use:   "get",
		Short: "Mostra as configurações globais (singleton)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cli, err := newClient(true)
			if err != nil {
				return err
			}
			var s settingsDTO
			if err := cli.Get(cmd.Context(), "/api/v1/instance/settings", &s); err != nil {
				return err
			}
			if outputJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(s)
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "instance_url:             %s\n", s.InstanceURL)
			fmt.Fprintf(w, "tls_mode:                 %s\n", s.TLSMode)
			fmt.Fprintf(w, "default_memory_limit:     %s\n", s.DefaultMemoryLimit)
			fmt.Fprintf(w, "default_cpu_limit:        %s\n", s.DefaultCPULimit)
			fmt.Fprintf(w, "max_concurrent_deploys:   %d\n", s.MaxConcurrentDeploys)
			fmt.Fprintf(w, "cleanup_enabled:          %v\n", s.CleanupEnabled)
			fmt.Fprintf(w, "cleanup_schedule:         %s\n", s.CleanupSchedule)
			fmt.Fprintf(w, "cleanup_disk_threshold:   %d\n", s.CleanupDiskThreshold)
			fmt.Fprintf(w, "cleanup_image_retention:  %d\n", s.CleanupImageRetention)
			fmt.Fprintf(w, "maintenance_mode:         %v\n", s.MaintenanceMode)
			if s.MaintenanceMessage != "" {
				fmt.Fprintf(w, "maintenance_message:      %s\n", s.MaintenanceMessage)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&outputJSON, "output", false, "JSON output (use --output)")
	c.Flags().Lookup("output").NoOptDefVal = "json"
	return c
}

func newInstanceSetCmd() *cobra.Command {
	var fileFlag string
	c := &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Atualiza uma config (ou várias via --file JSON)",
		Long: "Atualiza uma chave de instance_settings. Chaves válidas: instance_url, " +
			"tls_mode, default_memory_limit, default_cpu_limit, cleanup_enabled, " +
			"cleanup_schedule, cleanup_disk_threshold, cleanup_image_retention, " +
			"max_concurrent_deploys, maintenance_mode, maintenance_message.\n\n" +
			"Para múltiplas chaves use --file <path.json> com um objeto JSON.",
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := newClient(true)
			if err != nil {
				return err
			}
			var payload map[string]any
			if fileFlag != "" {
				raw, err := os.ReadFile(fileFlag)
				if err != nil {
					return fmt.Errorf("read --file: %w", err)
				}
				if err := json.Unmarshal(raw, &payload); err != nil {
					return fmt.Errorf("--file inválido (esperado JSON object): %w", err)
				}
				// Sanity: rejeita chaves desconhecidas para evitar
				// surpresa silenciosa caso o backend ignore.
				for k := range payload {
					if _, ok := allowedSettingKeys[k]; !ok {
						return fmt.Errorf("chave desconhecida no --file: %s", k)
					}
				}
			} else {
				if len(args) != 2 {
					return errors.New("uso: prexel instance set <key> <value>  (ou --file <path>)")
				}
				key := strings.TrimSpace(args[0])
				conv, ok := allowedSettingKeys[key]
				if !ok {
					keys := make([]string, 0, len(allowedSettingKeys))
					for k := range allowedSettingKeys {
						keys = append(keys, k)
					}
					return fmt.Errorf("chave desconhecida %q. Aceitas: %s", key, strings.Join(keys, ", "))
				}
				val, err := conv(args[1])
				if err != nil {
					return fmt.Errorf("valor inválido para %s: %w", key, err)
				}
				payload = map[string]any{key: val}
			}
			var out settingsDTO
			if err := cli.Patch(cmd.Context(), "/api/v1/instance/settings", payload, &out); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Configuração atualizada.")
			return nil
		},
	}
	c.Flags().StringVar(&fileFlag, "file", "", "Caminho para JSON com múltiplas chaves")
	return c
}

// ---------- conversores ----------

func asString(s string) (any, error) { return s, nil }
func asBool(s string) (any, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes", "on", "y":
		return true, nil
	case "false", "0", "no", "off", "n":
		return false, nil
	}
	return nil, fmt.Errorf("esperado bool (true/false), recebi %q", s)
}
func asInt(s string) (any, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return nil, err
	}
	return n, nil
}
