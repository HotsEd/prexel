package command

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"
)

type secretDTO struct {
	ID          string `json:"id"`
	AppID       string `json:"app_id"`
	Key         string `json:"key"`
	IsBuildTime bool   `json:"is_build_time"`
	IsMultiline bool   `json:"is_multiline"`
}

func newSecretsCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "secrets <app>",
		Short: "Manage encrypted secrets for an app",
		Args:  cobra.MinimumNArgs(1),
	}
	c.AddCommand(
		newSecretsListCmd(),
		newSecretsSetCmd(),
		newSecretsRemoveCmd(),
	)
	return c
}

func newSecretsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <app>",
		Short: "List the secret keys for an app (values are never shown)",
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
			var items []secretDTO
			if err := c.Get(cmd.Context(), "/api/v1/apps/"+a.ID+"/secrets", &items); err != nil {
				return err
			}
			if len(items) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "(no secrets)")
				return nil
			}
			tw := newTab(cmd.OutOrStdout())
			fmt.Fprintln(tw, "KEY\tVALUE\tSCOPE\tMULTILINE")
			for _, s := range items {
				scope := "runtime"
				if s.IsBuildTime {
					scope = "build-time"
				}
				fmt.Fprintf(tw, "%s\t****\t%s\t%v\n", s.Key, scope, s.IsMultiline)
			}
			return tw.Flush()
		},
	}
}

func newSecretsSetCmd() *cobra.Command {
	var (
		buildTime bool
		multiline bool
	)
	c := &cobra.Command{
		Use:   "set <app> KEY=VALUE [KEY=VALUE ...]",
		Short: "Set one or more secrets",
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
			payload := map[string]map[string]any{}
			for _, kv := range args[1:] {
				eq := strings.IndexByte(kv, '=')
				if eq <= 0 {
					return fmt.Errorf("invalid KEY=VALUE: %q", kv)
				}
				key, value := kv[:eq], kv[eq+1:]
				payload[key] = map[string]any{
					"value":         value,
					"is_build_time": buildTime,
					"is_multiline":  multiline,
				}
			}
			if err := c.Put(cmd.Context(), "/api/v1/apps/"+a.ID+"/secrets", payload, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Updated %d secret(s) on %s\n", len(payload), a.Name)
			return nil
		},
	}
	c.Flags().BoolVar(&buildTime, "build-time", false, "Mark as build-time secrets (--build-arg)")
	c.Flags().BoolVar(&multiline, "multiline", false, "Mark as multiline (renders with quoting)")
	return c
}

func newSecretsRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "remove <app> KEY",
		Aliases: []string{"rm"},
		Short:   "Remove a single secret",
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
			key := args[1]
			if key == "" {
				return errors.New("missing KEY")
			}
			if err := c.Delete(cmd.Context(), "/api/v1/apps/"+a.ID+"/secrets/"+url.PathEscape(key)); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed %s from %s\n", key, a.Name)
			return nil
		},
	}
}
