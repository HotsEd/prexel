package command

import (
	"fmt"
	"io"
)

// renderServerList writes a tab-separated table of servers to w. The appCounts
// map maps server.ID -> app count (zero when unknown). This helper exists so
// golden tests can render deterministic output without touching the network.
func renderServerList(w io.Writer, servers []serverDTO, appCounts map[string]int) error {
	if len(servers) == 0 {
		_, err := fmt.Fprintln(w, "(no servers)")
		return err
	}
	tw := newTab(w)
	if _, err := fmt.Fprintln(tw, "NAME\tTYPE\tHOST\tSTATUS\tAPPS\tDOCKER"); err != nil {
		return err
	}
	for _, s := range servers {
		host := strOrDash(s.Host)
		if s.Type == "local" {
			host = "<host>"
		}
		dock := strOrDash(s.DockerVersion)
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\n", s.Name, s.Type, host, s.Status, appCounts[s.ID], dock)
	}
	return tw.Flush()
}

// renderAppList writes a tab-separated table of apps to w. serverNames maps
// server.ID -> name; domains maps app.ID -> primary domain (or ""); lastDeploys
// maps app.ID -> last deploy summary (or "-"). All maps are tolerant of missing
// keys.
func renderAppList(w io.Writer, apps []appDTO, serverNames map[string]string, domains map[string]string, lastDeploys map[string]string) error {
	if len(apps) == 0 {
		_, err := fmt.Fprintln(w, "(no apps)")
		return err
	}
	tw := newTab(w)
	if _, err := fmt.Fprintln(tw, "NAME\tSERVER\tSTATUS\tDOMAIN\tLAST DEPLOY"); err != nil {
		return err
	}
	for _, a := range apps {
		srv := "-"
		if a.ServerID != nil {
			if n, ok := serverNames[*a.ServerID]; ok && n != "" {
				srv = n
			}
		}
		dom := domains[a.ID]
		if dom == "" {
			dom = "-"
		}
		last := lastDeploys[a.ID]
		if last == "" {
			last = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", a.Name, srv, a.Status, dom, last)
	}
	return tw.Flush()
}
