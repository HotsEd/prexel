package command

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/prexel/prexel/internal/cli/client"
	"github.com/prexel/prexel/internal/cli/tui"
	"github.com/spf13/cobra"
)

// domainDTO mirrors internal/domain.Domain for JSON unmarshalling.
type domainDTO struct {
	ID                string  `json:"id"`
	Name              string  `json:"name"`
	AppID             *string `json:"app_id,omitempty"`
	IsPrimary         bool    `json:"is_primary"`
	SSLStatus         string  `json:"ssl_status"`
	SSLExpiresAt      *int64  `json:"ssl_expires_at,omitempty"`
	DNSVerified       bool    `json:"dns_verified"`
	DNSVerifiedAt     *int64  `json:"dns_verified_at,omitempty"`
	DNSLastCheck      *int64  `json:"dns_last_check,omitempty"`
	DNSCheckCount     int     `json:"dns_check_count"`
	ZoneID            *string `json:"zone_id,omitempty"`
	CoveredByWildcard bool    `json:"covered_by_wildcard"`
	CreatedAt         int64   `json:"created_at"`
	UpdatedAt         int64   `json:"updated_at"`
}

func newDomainCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "domain",
		Aliases: []string{"domains"},
		Short:   "Manage domains attached to apps",
	}
	c.AddCommand(
		newDomainListCmd(),
		newDomainAddCmd(),
		newDomainRemoveCmd(),
		newDomainVerifyCmd(),
		newDomainStatusCmd(),
		newDomainInfoCmd(),
		newDomainEditCmd(),
		newDomainRetryCmd(),
		newDomainSetCmd(),
	)
	return c
}

// newDomainSetCmd is a flag-based partial update for the domain — the
// counterpart to `app set`. Today it only exposes --force-https; new
// per-domain toggles can be added by following the same Changed/PATCH
// pattern without growing a YAML editor surface.
//
// We accept the flag as a string ("true"/"false") so the user can be
// explicit ("--force-https=false" disables the redirect, not just
// "leave unset"). A bool flag would conflate "not passed" with
// "passed as false" once we ever default it to true.
func newDomainSetCmd() *cobra.Command {
	var forceHTTPS string
	c := &cobra.Command{
		Use:   "set <domain>",
		Short: "Atualiza configurações do domínio (PATCH parcial via flags)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(true)
			if err != nil {
				return err
			}
			d, err := resolveDomainByRef(c, cmd.Context(), args[0])
			if err != nil {
				return err
			}
			body := map[string]any{}
			if cmd.Flags().Changed("force-https") {
				v, err := parseBoolFlag("force-https", forceHTTPS)
				if err != nil {
					return err
				}
				body["force_https"] = v
			}
			if len(body) == 0 {
				return errors.New("nada para atualizar — passe ao menos uma flag")
			}
			var out domainDTO
			if err := c.Patch(cmd.Context(), "/api/v1/domains/"+d.ID, body, &out); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Atualizado %s\n", out.Name)
			return nil
		},
	}
	c.Flags().StringVar(&forceHTTPS, "force-https", "", "Força redirect HTTP→HTTPS (true|false)")
	return c
}

// parseBoolFlag aceita as strings comuns para true/false. Existe porque
// a alternativa (cobra.BoolVar) não distingue "não passou" de "passou
// false" depois que algum default mudar.
func parseBoolFlag(name, raw string) (bool, error) {
	switch raw {
	case "true", "1", "yes", "on":
		return true, nil
	case "false", "0", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("--%s espera true|false, recebi %q", name, raw)
	}
}

// resolveDomainByRef finds a domain by ID or by name. It first tries GET by id;
// on a 404 / not-found it falls back to listing all domains and matching by name.
func resolveDomainByRef(c *client.Client, ctx context.Context, ref string) (*domainDTO, error) {
	// Try direct GET first (works when ref is an id).
	var d domainDTO
	if err := c.Get(ctx, "/api/v1/domains/"+url.PathEscape(ref), &d); err == nil {
		return &d, nil
	}
	// Fall back to listing and matching by name.
	var all []domainDTO
	if err := c.Get(ctx, "/api/v1/domains", &all); err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].Name == ref || all[i].ID == ref {
			return &all[i], nil
		}
	}
	return nil, fmt.Errorf("domain %q not found", ref)
}

func newDomainInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "info <id-or-name>",
		Short: "Show detailed info for a domain",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(true)
			if err != nil {
				return err
			}
			d, err := resolveDomainByRef(c, cmd.Context(), args[0])
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "id:                   %s\n", d.ID)
			fmt.Fprintf(w, "name:                 %s\n", d.Name)
			fmt.Fprintf(w, "app_id:               %s\n", strOrDash(d.AppID))
			fmt.Fprintf(w, "is_primary:           %v\n", d.IsPrimary)
			fmt.Fprintf(w, "dns_verified:         %v\n", d.DNSVerified)
			fmt.Fprintf(w, "dns_verified_at:      %s\n", fmtUnixOrDash(d.DNSVerifiedAt))
			fmt.Fprintf(w, "dns_last_check:       %s\n", fmtUnixOrDash(d.DNSLastCheck))
			fmt.Fprintf(w, "dns_check_count:      %d\n", d.DNSCheckCount)
			fmt.Fprintf(w, "ssl_status:           %s\n", d.SSLStatus)
			fmt.Fprintf(w, "ssl_expires_at:       %s\n", fmtUnixOrDash(d.SSLExpiresAt))
			fmt.Fprintf(w, "zone_id:              %s\n", strOrDash(d.ZoneID))
			fmt.Fprintf(w, "covered_by_wildcard:  %v\n", d.CoveredByWildcard)
			fmt.Fprintf(w, "created_at:           %s\n", fmtUnix(d.CreatedAt))
			fmt.Fprintf(w, "updated_at:           %s\n", fmtUnix(d.UpdatedAt))
			return nil
		},
	}
}

func newDomainEditCmd() *cobra.Command {
	var (
		primary  bool
		app      string
		clearApp bool
	)
	c := &cobra.Command{
		Use:   "edit <id> [flags]",
		Short: "Edit a domain (primary flag, app rebind)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(true)
			if err != nil {
				return err
			}
			if app != "" && clearApp {
				return errors.New("--app and --clear-app are mutually exclusive")
			}
			d, err := resolveDomainByRef(c, cmd.Context(), args[0])
			if err != nil {
				return err
			}
			body := map[string]any{}
			if cmd.Flags().Changed("primary") {
				body["is_primary"] = primary
			}
			if app != "" {
				a, err := resolveApp(c, cmd.Context(), app)
				if err != nil {
					return fmt.Errorf("resolve app: %w", err)
				}
				body["app_id"] = a.ID
			}
			if clearApp {
				body["clear_app_id"] = true
			}
			if len(body) == 0 {
				return errors.New("nothing to update — pass --primary, --app or --clear-app")
			}
			var out domainDTO
			if err := c.Patch(cmd.Context(), "/api/v1/domains/"+d.ID, body, &out); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Updated %s\n", out.Name)
			return nil
		},
	}
	c.Flags().BoolVar(&primary, "primary", false, "Set this domain as the app's primary (use with care)")
	c.Flags().StringVar(&app, "app", "", "Rebind the domain to another app (name or id)")
	c.Flags().BoolVar(&clearApp, "clear-app", false, "Detach the domain from its current app")
	return c
}

func newDomainRetryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "retry <id>",
		Short: "Retry SSL issuance for a domain",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(true)
			if err != nil {
				return err
			}
			d, err := resolveDomainByRef(c, cmd.Context(), args[0])
			if err != nil {
				return err
			}
			var out domainDTO
			err = tui.RunSpinner(fmt.Sprintf("Re-issuing SSL for %s", d.Name), func() error {
				return c.Post(cmd.Context(), "/api/v1/domains/"+d.ID+"/retry", nil, &out)
			})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Re-issuing SSL for %s...\n", out.Name)
			fmt.Fprintf(cmd.OutOrStdout(), "ssl: %s\n", out.SSLStatus)
			return nil
		},
	}
}

func newDomainListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list [<app>]",
		Short: "List domains, optionally filtered to one app",
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(true)
			if err != nil {
				return err
			}
			endpoint := "/api/v1/domains"
			if len(args) == 1 {
				a, err := resolveApp(c, cmd.Context(), args[0])
				if err != nil {
					return err
				}
				endpoint = "/api/v1/domains?app_id=" + url.QueryEscape(a.ID)
			}
			var doms []domainDTO
			if err := c.Get(cmd.Context(), endpoint, &doms); err != nil {
				return err
			}
			if len(doms) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "(no domains)")
				return nil
			}
			appNames := resolveAppNames(c, cmd.Context())
			tw := newTab(cmd.OutOrStdout())
			fmt.Fprintln(tw, "DOMAIN\tAPP\tPRIMARY\tDNS\tSSL\tEXPIRES")
			for _, d := range doms {
				app := "-"
				if d.AppID != nil {
					if n, ok := appNames[*d.AppID]; ok {
						app = n
					} else {
						app = *d.AppID
					}
				}
				dns := "pending"
				if d.DNSVerified {
					dns = "verified"
				}
				exp := fmtUnixOrDash(d.SSLExpiresAt)
				fmt.Fprintf(tw, "%s\t%s\t%v\t%s\t%s\t%s\n",
					d.Name, app, d.IsPrimary, dns, d.SSLStatus, exp)
			}
			return tw.Flush()
		},
	}
}

func resolveAppNames(c *client.Client, ctx context.Context) map[string]string {
	out := map[string]string{}
	var apps []appDTO
	if err := c.Get(ctx, "/api/v1/apps", &apps); err == nil {
		for _, a := range apps {
			out[a.ID] = a.Name
		}
	}
	return out
}

func newDomainAddCmd() *cobra.Command {
	var primary bool
	c := &cobra.Command{
		Use:   "add <app> <domain>",
		Short: "Attach a domain to an app",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(true)
			if err != nil {
				return err
			}
			a, err := resolveApp(c, cmd.Context(), args[0])
			if err != nil {
				return err
			}
			body := map[string]any{
				"name":       args[1],
				"app_id":     a.ID,
				"is_primary": primary,
			}
			var out domainDTO
			if err := c.Post(cmd.Context(), "/api/v1/domains", body, &out); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Attached %s to %s (id=%s)\n", out.Name, a.Name, out.ID)
			fmt.Fprintln(cmd.OutOrStdout(), "DNS check will run automatically; SSL is issued once DNS matches the server IP.")
			return nil
		},
	}
	c.Flags().BoolVar(&primary, "primary", false, "Mark as the app's primary domain")
	return c
}

func resolveDomain(c *client.Client, ctx context.Context, appRef, domainName string) (*domainDTO, error) {
	a, err := resolveApp(c, ctx, appRef)
	if err != nil {
		return nil, err
	}
	var doms []domainDTO
	if err := c.Get(ctx, "/api/v1/apps/"+a.ID+"/domains", &doms); err != nil {
		return nil, err
	}
	for i := range doms {
		if doms[i].Name == domainName || doms[i].ID == domainName {
			return &doms[i], nil
		}
	}
	return nil, fmt.Errorf("domain %q not found on app %s", domainName, a.Name)
}

func newDomainRemoveCmd() *cobra.Command {
	var force bool
	c := &cobra.Command{
		Use:     "remove <app> <domain>",
		Aliases: []string{"rm"},
		Short:   "Detach and delete a domain",
		Args:    cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(true)
			if err != nil {
				return err
			}
			var d *domainDTO
			if len(args) == 2 {
				d, err = resolveDomain(c, cmd.Context(), args[0], args[1])
			} else {
				// global lookup by name
				var all []domainDTO
				if err := c.Get(cmd.Context(), "/api/v1/domains", &all); err != nil {
					return err
				}
				for i := range all {
					if all[i].Name == args[0] || all[i].ID == args[0] {
						d = &all[i]
						break
					}
				}
				if d == nil {
					err = fmt.Errorf("domain %q not found", args[0])
				}
			}
			if err != nil {
				return err
			}
			if !force && !tui.Confirm(fmt.Sprintf("Remove domain %s?", d.Name)) {
				return errors.New("cancelled")
			}
			if err := c.Delete(cmd.Context(), "/api/v1/domains/"+d.ID); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed %s\n", d.Name)
			return nil
		},
	}
	c.Flags().BoolVar(&force, "force", false, "Skip confirmation")
	return c
}

func newDomainVerifyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "verify <app> <domain>",
		Short: "Force an immediate DNS check for a domain",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(true)
			if err != nil {
				return err
			}
			d, err := resolveDomain(c, cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			var out domainDTO
			err = tui.RunSpinner(fmt.Sprintf("Verifying %s", d.Name), func() error {
				return c.Post(cmd.Context(), "/api/v1/domains/"+d.ID+"/verify", nil, &out)
			})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "domain:   %s\n", out.Name)
			fmt.Fprintf(cmd.OutOrStdout(), "dns:      %v (count=%d)\n", out.DNSVerified, out.DNSCheckCount)
			fmt.Fprintf(cmd.OutOrStdout(), "ssl:      %s\n", out.SSLStatus)
			return nil
		},
	}
}

func newDomainStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status <app>",
		Short: "Show all domains and SSL status for an app",
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
			var doms []domainDTO
			if err := c.Get(cmd.Context(), "/api/v1/apps/"+a.ID+"/domains", &doms); err != nil {
				return err
			}
			if len(doms) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "(no domains)")
				return nil
			}
			tw := newTab(cmd.OutOrStdout())
			fmt.Fprintln(tw, "DOMAIN\tPRIMARY\tDNS\tDNS_CHECKS\tSSL\tEXPIRES")
			for _, d := range doms {
				dns := "pending"
				if d.DNSVerified {
					dns = "verified"
				}
				fmt.Fprintf(tw, "%s\t%v\t%s\t%d\t%s\t%s\n",
					d.Name, d.IsPrimary, dns, d.DNSCheckCount, d.SSLStatus, fmtUnixOrDash(d.SSLExpiresAt))
			}
			return tw.Flush()
		},
	}
}
