package command

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

func newEnvCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "env <app>",
		Short: "Manage plain-text environment variables for an app",
		Args:  cobra.MinimumNArgs(1),
	}
	c.AddCommand(
		newEnvListCmd(),
		newEnvSetCmd(),
		newEnvRemoveCmd(),
		newEnvSyncCmd(),
	)
	return c
}

func newEnvListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <app>",
		Short: "List env vars for an app",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(true)
			if err != nil {
				return err
			}
			a, err := resolveApp(c, cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if len(a.EnvVars) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "(no env vars)")
				return nil
			}
			keys := make([]string, 0, len(a.EnvVars))
			for k := range a.EnvVars {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			tw := newTab(cmd.OutOrStdout())
			fmt.Fprintln(tw, "KEY\tVALUE")
			for _, k := range keys {
				fmt.Fprintf(tw, "%s\t%s\n", k, a.EnvVars[k])
			}
			return tw.Flush()
		},
	}
}

func newEnvSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <app> KEY=VALUE [KEY=VALUE ...]",
		Short: "Set one or more env vars (merges into existing)",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(true)
			if err != nil {
				return err
			}
			a, err := resolveApp(c, cmd.Context(), args[0])
			if err != nil {
				return err
			}
			env := map[string]string{}
			for k, v := range a.EnvVars {
				env[k] = v
			}
			for _, kv := range args[1:] {
				eq := strings.IndexByte(kv, '=')
				if eq <= 0 {
					return fmt.Errorf("invalid KEY=VALUE: %q", kv)
				}
				env[kv[:eq]] = kv[eq+1:]
			}
			if err := c.Put(cmd.Context(), "/api/v1/apps/"+a.ID+"/env-vars", env, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Updated %d env var(s) on %s\n", len(args)-1, a.Name)
			return nil
		},
	}
}

func newEnvRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "remove <app> KEY",
		Aliases: []string{"rm"},
		Short:   "Remove an env var",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(true)
			if err != nil {
				return err
			}
			a, err := resolveApp(c, cmd.Context(), args[0])
			if err != nil {
				return err
			}
			env := map[string]string{}
			for k, v := range a.EnvVars {
				if k != args[1] {
					env[k] = v
				}
			}
			if err := c.Put(cmd.Context(), "/api/v1/apps/"+a.ID+"/env-vars", env, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed %s from %s\n", args[1], a.Name)
			return nil
		},
	}
}

// newEnvSyncCmd implements `prexel env <app> sync --file .env [--build-time]`
// — the Coolify-inspired bulk import (Análise Coolify).
//
//   - `--build-time` routes entries into secrets (is_build_time=true).
//   - Otherwise entries land in apps.env_vars (PUT replaces the merged map).
//   - Never deletes keys missing from the file (Coolify parity).
//   - Reports `added/updated/unchanged` counts.
func newEnvSyncCmd() *cobra.Command {
	var (
		filePath  string
		buildTime bool
	)
	c := &cobra.Command{
		Use:   "sync <app>",
		Short: "Import a .env file into the app's env vars (Coolify-style)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if filePath == "" {
				return fmt.Errorf("--file is required")
			}
			c, err := newClient(true)
			if err != nil {
				return err
			}
			a, err := resolveApp(c, cmd.Context(), args[0])
			if err != nil {
				return err
			}
			entries, err := parseDotEnvFile(filePath)
			if err != nil {
				return err
			}
			if buildTime {
				// Read existing secrets to compute added/updated/unchanged.
				var existing []secretDTO
				_ = c.Get(cmd.Context(), "/api/v1/apps/"+a.ID+"/secrets", &existing)
				existSet := map[string]bool{}
				for _, s := range existing {
					existSet[s.Key] = true
				}
				payload := map[string]map[string]any{}
				for k, v := range entries {
					payload[k] = map[string]any{
						"value":         v,
						"is_build_time": true,
						"is_multiline":  strings.Contains(v, "\n"),
					}
				}
				if err := c.Put(cmd.Context(), "/api/v1/apps/"+a.ID+"/secrets", payload, nil); err != nil {
					return err
				}
				added, updated := 0, 0
				for k := range entries {
					if existSet[k] {
						updated++
					} else {
						added++
					}
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Synced %d build-time secret(s): +%d new, ~%d updated\n",
					len(entries), added, updated)
				return nil
			}
			// Regular env vars: merge + PUT.
			merged := map[string]string{}
			for k, v := range a.EnvVars {
				merged[k] = v
			}
			added, updated, unchanged := 0, 0, 0
			for k, v := range entries {
				if old, ok := merged[k]; ok {
					if old == v {
						unchanged++
					} else {
						updated++
					}
				} else {
					added++
				}
				merged[k] = v
			}
			if err := c.Put(cmd.Context(), "/api/v1/apps/"+a.ID+"/env-vars", merged, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Synced %d env var(s): +%d new, ~%d updated, =%d unchanged\n",
				len(entries), added, updated, unchanged)
			return nil
		},
	}
	c.Flags().StringVar(&filePath, "file", "", "Path to .env file")
	c.Flags().BoolVar(&buildTime, "build-time", false, "Import as build-time secrets")
	return c
}

// parseDotEnvFile reads a .env file in the common KEY=VALUE format. Supports
// quoted values (single + double) and `# comment` lines.
func parseDotEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("open env file: %w", err)
	}
	defer func() { _ = f.Close() }()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<14), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Strip optional `export ` prefix.
		line = strings.TrimPrefix(line, "export ")
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		// Strip surrounding quotes.
		if len(val) >= 2 {
			first, last := val[0], val[len(val)-1]
			if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
				val = val[1 : len(val)-1]
				if first == '"' {
					// minimal escape processing
					val = strings.ReplaceAll(val, `\n`, "\n")
					val = strings.ReplaceAll(val, `\"`, `"`)
				}
			}
		}
		out[key] = val
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read env file: %w", err)
	}
	return out, nil
}
