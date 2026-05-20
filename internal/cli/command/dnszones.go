package command

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/prexel/prexel/internal/cli/client"
	"github.com/prexel/prexel/internal/cli/tui"
	"github.com/spf13/cobra"
)

// dnsZoneDTO mirrors internal/dnszone.Zone for JSON decoding. The CLI is a
// thin HTTP client, so we keep a local copy here instead of importing the
// server-side package (which would drag DB + resolver code into the CLI).
type dnsZoneDTO struct {
	ID                 string `json:"id"`
	Apex               string `json:"apex"`
	TargetIP           string `json:"target_ip,omitempty"`
	ApexVerified       bool   `json:"apex_verified"`
	ApexVerifiedAt     *int64 `json:"apex_verified_at,omitempty"`
	ApexLastCheck      *int64 `json:"apex_last_check,omitempty"`
	WildcardVerified   bool   `json:"wildcard_verified"`
	WildcardVerifiedAt *int64 `json:"wildcard_verified_at,omitempty"`
	WildcardLastCheck  *int64 `json:"wildcard_last_check,omitempty"`
	Notes              string `json:"notes,omitempty"`
	SubdomainCount     int    `json:"subdomain_count,omitempty"`
	CreatedAt          int64  `json:"created_at"`
	UpdatedAt          int64  `json:"updated_at"`
}

// dnsProbeDTO mirrors dnszone.ProbeResult.
type dnsProbeDTO struct {
	OK         bool     `json:"ok"`
	ResolvedTo []string `json:"resolved_to,omitempty"`
	Expected   string   `json:"expected,omitempty"`
	Probed     string   `json:"probed,omitempty"`
	Error      string   `json:"error,omitempty"`
}

// dnsVerifyDTO mirrors dnszone.VerificationResult.
type dnsVerifyDTO struct {
	Apex     dnsProbeDTO  `json:"apex"`
	Wildcard dnsProbeDTO  `json:"wildcard"`
	Hostname *dnsProbeDTO `json:"hostname,omitempty"`
}

func newDNSZonesCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "dns-zones",
		Aliases: []string{"dns-zone", "zones", "zone"},
		Short:   "Gerencia zonas DNS (apex domains)",
	}
	c.AddCommand(
		newDNSZonesListCmd(),
		newDNSZonesGetCmd(),
		newDNSZonesCreateCmd(),
		newDNSZonesPatchCmd(),
		newDNSZonesDeleteCmd(),
		newDNSZonesVerifyCmd(),
	)
	return c
}

// resolveDNSZone aceita um ID exato ou um apex. Faz list e procura match.
func resolveDNSZone(c *client.Client, ctx context.Context, ref string) (*dnsZoneDTO, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, errors.New("zona não informada")
	}
	// Tenta primeiro como ID — barato e direto.
	var z dnsZoneDTO
	if err := c.Get(ctx, "/api/v1/dns-zones/"+url.PathEscape(ref), &z); err == nil {
		return &z, nil
	}
	// Fallback: lista e procura por apex (case-insensitive).
	var zones []dnsZoneDTO
	if err := c.Get(ctx, "/api/v1/dns-zones", &zones); err != nil {
		return nil, err
	}
	needle := strings.ToLower(strings.TrimSuffix(ref, "."))
	for i := range zones {
		if zones[i].ID == ref || strings.ToLower(zones[i].Apex) == needle {
			return &zones[i], nil
		}
	}
	return nil, fmt.Errorf("zona %q não encontrada", ref)
}

func boolMark(b bool) string {
	if b {
		return "✓"
	}
	return "✗"
}

func newDNSZonesListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Lista todas as zonas DNS",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newClient(true)
			if err != nil {
				return err
			}
			var zones []dnsZoneDTO
			if err := c.Get(cmd.Context(), "/api/v1/dns-zones", &zones); err != nil {
				return err
			}
			if len(zones) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "(nenhuma zona)")
				return nil
			}
			tw := newTab(cmd.OutOrStdout())
			fmt.Fprintln(tw, "APEX\tTARGET_IP\tAPEX\tWILDCARD\tSUBDOMAINS\tNOTES")
			for _, z := range zones {
				ip := z.TargetIP
				if ip == "" {
					ip = "-"
				}
				notes := truncate(z.Notes, 40)
				if notes == "" {
					notes = "-"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\n",
					z.Apex, ip, boolMark(z.ApexVerified), boolMark(z.WildcardVerified),
					z.SubdomainCount, notes)
			}
			return tw.Flush()
		},
	}
}

func newDNSZonesGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <id-or-apex>",
		Short: "Mostra detalhes de uma zona DNS",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(true)
			if err != nil {
				return err
			}
			z, err := resolveDNSZone(c, cmd.Context(), args[0])
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			ip := z.TargetIP
			if ip == "" {
				ip = "-"
			}
			notes := z.Notes
			if notes == "" {
				notes = "-"
			}
			fmt.Fprintf(w, "id:                    %s\n", z.ID)
			fmt.Fprintf(w, "apex:                  %s\n", z.Apex)
			fmt.Fprintf(w, "target_ip:             %s\n", ip)
			fmt.Fprintf(w, "apex_verified:         %s\n", boolMark(z.ApexVerified))
			fmt.Fprintf(w, "apex_verified_at:      %s\n", fmtUnixOrDash(z.ApexVerifiedAt))
			fmt.Fprintf(w, "apex_last_check:       %s\n", fmtUnixOrDash(z.ApexLastCheck))
			fmt.Fprintf(w, "wildcard_verified:     %s\n", boolMark(z.WildcardVerified))
			fmt.Fprintf(w, "wildcard_verified_at:  %s\n", fmtUnixOrDash(z.WildcardVerifiedAt))
			fmt.Fprintf(w, "wildcard_last_check:   %s\n", fmtUnixOrDash(z.WildcardLastCheck))
			fmt.Fprintf(w, "subdomain_count:       %d\n", z.SubdomainCount)
			fmt.Fprintf(w, "notes:                 %s\n", notes)
			fmt.Fprintf(w, "created_at:            %s\n", fmtUnix(z.CreatedAt))
			fmt.Fprintf(w, "updated_at:            %s\n", fmtUnix(z.UpdatedAt))
			return nil
		},
	}
}

func newDNSZonesCreateCmd() *cobra.Command {
	var notes string
	c := &cobra.Command{
		Use:   "create <apex>",
		Short: "Cria uma nova zona DNS (apex)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(true)
			if err != nil {
				return err
			}
			body := map[string]any{"apex": args[0]}
			if notes != "" {
				body["notes"] = notes
			}
			var out dnsZoneDTO
			if err := c.Post(cmd.Context(), "/api/v1/dns-zones", body, &out); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Zona criada: %s (id=%s)\n", out.Apex, out.ID)
			return nil
		},
	}
	c.Flags().StringVar(&notes, "notes", "", "Anotação livre sobre a zona")
	return c
}

func newDNSZonesPatchCmd() *cobra.Command {
	var notes string
	c := &cobra.Command{
		Use:   "patch <id>",
		Short: "Atualiza campos editáveis de uma zona (hoje: notes)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(true)
			if err != nil {
				return err
			}
			if !cmd.Flags().Changed("notes") {
				return errors.New("nada para atualizar — informe --notes")
			}
			z, err := resolveDNSZone(c, cmd.Context(), args[0])
			if err != nil {
				return err
			}
			body := map[string]any{"notes": notes}
			var out dnsZoneDTO
			if err := c.Patch(cmd.Context(), "/api/v1/dns-zones/"+url.PathEscape(z.ID), body, &out); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Zona %s atualizada.\n", out.Apex)
			return nil
		},
	}
	c.Flags().StringVar(&notes, "notes", "", "Nova anotação (string vazia limpa)")
	return c
}

func newDNSZonesDeleteCmd() *cobra.Command {
	var yes bool
	c := &cobra.Command{
		Use:     "delete <id>",
		Aliases: []string{"rm", "remove"},
		Short:   "Apaga uma zona DNS",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(true)
			if err != nil {
				return err
			}
			z, err := resolveDNSZone(c, cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if !yes && !tui.Confirm(fmt.Sprintf("Apagar zona %s?", z.Apex)) {
				return errors.New("cancelado")
			}
			if err := c.Delete(cmd.Context(), "/api/v1/dns-zones/"+url.PathEscape(z.ID)); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Zona %s apagada.\n", z.Apex)
			return nil
		},
	}
	c.Flags().BoolVar(&yes, "yes", false, "Pula confirmação interativa")
	return c
}

func newDNSZonesVerifyCmd() *cobra.Command {
	var hostname string
	c := &cobra.Command{
		Use:   "verify <id>",
		Short: "Roda os probes DNS (apex + wildcard, opcionalmente um FQDN)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(true)
			if err != nil {
				return err
			}
			z, err := resolveDNSZone(c, cmd.Context(), args[0])
			if err != nil {
				return err
			}
			path := "/api/v1/dns-zones/" + url.PathEscape(z.ID) + "/verify"
			if hostname != "" {
				path += "?hostname=" + url.QueryEscape(hostname)
			}
			var out dnsVerifyDTO
			err = tui.RunSpinner(fmt.Sprintf("Verificando zona %s", z.Apex), func() error {
				return c.Post(cmd.Context(), path, nil, &out)
			})
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			printProbeCard(w, "Apex", &out.Apex)
			fmt.Fprintln(w)
			printProbeCard(w, "Wildcard", &out.Wildcard)
			if out.Hostname != nil {
				fmt.Fprintln(w)
				printProbeCard(w, "Hostname", out.Hostname)
			}
			return nil
		},
	}
	c.Flags().StringVar(&hostname, "hostname", "", "FQDN específico para também testar (ex: api.foo.com)")
	return c
}

func printProbeCard(w io.Writer, title string, p *dnsProbeDTO) {
	fmt.Fprintf(w, "[%s] %s\n", title, boolMark(p.OK))
	if p.Probed != "" {
		fmt.Fprintf(w, "  probed:      %s\n", p.Probed)
	}
	if p.Expected != "" {
		fmt.Fprintf(w, "  expected:    %s\n", p.Expected)
	}
	if len(p.ResolvedTo) > 0 {
		fmt.Fprintf(w, "  resolved_to: %s\n", strings.Join(p.ResolvedTo, ", "))
	} else {
		fmt.Fprintf(w, "  resolved_to: -\n")
	}
	if p.Error != "" {
		fmt.Fprintf(w, "  error:       %s\n", p.Error)
	}
}
