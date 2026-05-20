package command

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"
)

// newTab returns a tabwriter writing to w with sensible defaults.
func newTab(w io.Writer) *tabwriter.Writer {
	return tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
}

// fmtUnixOrDash renders a unix epoch second value as RFC3339 or "-" when 0/nil.
func fmtUnixOrDash(ts *int64) string {
	if ts == nil || *ts == 0 {
		return "-"
	}
	return time.Unix(*ts, 0).UTC().Format("2006-01-02 15:04Z")
}

// fmtUnix renders a unix second value, or "-" when 0.
func fmtUnix(ts int64) string {
	if ts == 0 {
		return "-"
	}
	return time.Unix(ts, 0).UTC().Format("2006-01-02 15:04Z")
}

// strOrDash returns "-" when v is nil/empty.
func strOrDash(v *string) string {
	if v == nil || *v == "" {
		return "-"
	}
	return *v
}

// intOrDash returns "-" when v is nil.
func intOrDash(v *int) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprintf("%d", *v)
}

// truncate caps s to n runes and appends ellipsis if it was longer.
func truncate(s string, n int) string {
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

// joinKV renders a key/value map as `k=v,k=v` with stable order.
func joinKV(kv map[string]string, sep string) string {
	if len(kv) == 0 {
		return ""
	}
	parts := make([]string, 0, len(kv))
	for k, v := range kv {
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, sep)
}
