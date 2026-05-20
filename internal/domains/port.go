package domains

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
)

// Port range used for apps without a domain (Tech Review §16). Apps with at
// least one Prexel-managed domain are reached via Caddy on 80/443; ones
// without a domain bind a host port in this range so users can reach them
// directly at host:<port>.
const (
	PortRangeStart = 40000
	PortRangeEnd   = 50000
)

// FindAvailablePort scans the [start, end) range and returns the first port
// that successfully accepts a bind on 127.0.0.1. It tries up to 50 ports
// before giving up — that's enough to absorb a handful of races.
//
// The function leaves a hole: another process may grab the same port between
// the close() and the eventual container start. We accept that as the cost
// of not running a port allocator. Apps are expected to recover by retrying.
func FindAvailablePort(start, end int) (int, error) {
	if start <= 0 || end <= start {
		return 0, fmt.Errorf("port: invalid range [%d, %d)", start, end)
	}
	tried := 0
	for port := start; port < end && tried < 50; port++ {
		l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			tried++
			continue
		}
		_ = l.Close()
		return port, nil
	}
	return 0, errors.New("port: no available port in range")
}

// AssignHostPort allocates a fresh port and writes it to apps.host_port for
// the given appID. Exposed here (rather than in internal/app) because both
// the domain layer (for instance routing logic) and the future deploy engine
// need to call it; keeping it in `domain` avoids the import cycle that
// `internal/app` would otherwise need.
//
// Returns the port chosen so callers can log it / publish events. The
// function is idempotent in the sense that it always picks a new port — it
// does not first check whether the row already has one. Callers that want
// to preserve an existing assignment should read host_port first.
func AssignHostPort(ctx context.Context, db *sql.DB, appID string) (int, error) {
	port, err := FindAvailablePort(PortRangeStart, PortRangeEnd)
	if err != nil {
		return 0, err
	}
	res, err := db.ExecContext(ctx,
		`UPDATE apps SET host_port = ?, updated_at = unixepoch() WHERE id = ?`,
		port, appID)
	if err != nil {
		return 0, fmt.Errorf("port: assign: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, ErrAppNotFound
	}
	return port, nil
}
