package apimiddleware

import (
	"encoding/json"
	"net/http"
	"strings"
)

// SetupStateReader is the minimal contract the gate needs from the setup
// service: a cached bool that is fast on every request.
type SetupStateReader interface {
	Completed() bool
}

// SetupGate guards the API surface based on the wizard state.
//
// While !completed:
//   - /api/v1/setup/*, /api/v1/healthz pass through.
//   - Other /api/v1/* → 403 {error: "setup_not_completed"}.
//   - Other paths (SPA) → 302 /setup. Effective once the Vue app is mounted.
//
// While completed:
//   - /api/v1/setup/* → 404 (the wizard is sealed).
//   - Everything else passes.
func SetupGate(s SetupStateReader) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path
			completed := s.Completed()

			isSetupPath := strings.HasPrefix(path, "/api/v1/setup/")
			isHealthz := path == "/api/v1/healthz" || path == "/healthz"
			isSetupStatus := path == "/api/v1/setup/status"
			isAPI := strings.HasPrefix(path, "/api/v1/")

			if !completed {
				if isSetupPath || isHealthz {
					next.ServeHTTP(w, r)
					return
				}
				if isAPI {
					writeJSONError(w, http.StatusForbidden, "setup_not_completed")
					return
				}
				// Already on the wizard route (or its subpaths/assets) — pass
				// through so the SPA can render. Other SPA paths redirect.
				if path == "/setup" || strings.HasPrefix(path, "/setup/") || strings.HasPrefix(path, "/assets/") {
					next.ServeHTTP(w, r)
					return
				}
				http.Redirect(w, r, "/setup", http.StatusFound)
				return
			}

			// Completed: seal the wizard except for the status probe, which the
			// frontend still polls to know whether to redirect.
			if isSetupPath && !isSetupStatus {
				writeJSONError(w, http.StatusNotFound, "not_found")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func writeJSONError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}
