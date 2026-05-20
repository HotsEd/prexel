package rbac

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

func uniqueStrings(in []string) []string {
	seen := map[string]struct{}{}
	out := []string{}
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "role"
	}
	return out
}

func uniqueSlug(ctx context.Context, db *sql.DB, table, base string) string {
	if !slugRegex.MatchString(base) {
		base = "role"
	}
	for i := 0; i < 1000; i++ {
		candidate := base
		if i > 0 {
			candidate = fmt.Sprintf("%s-%d", base, i+1)
		}
		var n int
		_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table+` WHERE slug = ?`, candidate).Scan(&n)
		if n == 0 {
			return candidate
		}
	}
	return base + "-" + uuid.NewString()[:8]
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
