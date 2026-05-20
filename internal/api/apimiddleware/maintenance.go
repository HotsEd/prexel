package apimiddleware

import (
	"net/http"
	"strings"

	"github.com/prexel/prexel/internal/api/httpx"
	"github.com/prexel/prexel/internal/instance"
)

// maintenancePass identifies routes that must continue working even when
// maintenance_mode is on. The admin needs login + 2FA + /me to authenticate
// and PATCH /instance/settings to turn maintenance off again. /healthz must
// stay up so external uptime monitors don't lose the host entirely.
//
// We match by prefix (case-sensitive); chi mounts everything under /api/v1.
var maintenancePass = []string{
	"/api/v1/healthz",
	"/api/v1/auth/login",
	"/api/v1/auth/refresh",
	"/api/v1/auth/logout",
	"/api/v1/auth/me",
	"/api/v1/auth/2fa/",
	"/api/v1/setup/",          // setup-gate already 404s post-setup; harmless to allow
	"/api/v1/instance/settings", // PATCH here is how the admin disables maintenance
}

// Maintenance returns a middleware that replies 503 to every API request
// when instance.Settings.MaintenanceMode is true — except routes listed in
// maintenancePass.
//
// Reads the cached settings via instance.Service.Cached() so the hot path
// is lock-free (atomic load). The response carries the operator-defined
// message so the UI / curl callers see context.
func Maintenance(svc *instance.Service) func(http.Handler) http.Handler {
	if svc == nil {
		panic("apimiddleware.Maintenance: instance.Service is required")
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cached := svc.Cached()
			if !cached.MaintenanceMode {
				next.ServeHTTP(w, r)
				return
			}
			if maintenanceAllowed(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			// Hint clients to retry in a minute. The exact value doesn't
			// matter much — it tells well-behaved consumers (and uptime
			// monitors) to back off.
			w.Header().Set("Retry-After", "60")
			httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]string{
				"error":   "maintenance",
				"message": cached.MaintenanceMessage,
			})
		})
	}
}

func maintenanceAllowed(path string) bool {
	for _, p := range maintenancePass {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}
