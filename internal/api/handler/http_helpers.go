package handler

// Thin shims over internal/api/httpx so existing handler-package call sites
// (writeJSON / writeError / decodeJSON, ~270 of them) keep working without
// a sweeping rewrite. NEW code should import httpx directly instead of
// adding callers here.
//
// The shims exist because every handler used to declare its own helper
// (setup.go had one, rbac.go had another, and they drifted in subtle
// ways — different field names, different defaults). Centralising the
// real implementation in `httpx` killed the drift; this file keeps the
// internal aliasing one-liner so the diff stayed cosmetic for handlers.

import (
	"net/http"

	"github.com/prexel/prexel/internal/api/httpx"
)

func writeJSON(w http.ResponseWriter, status int, body any) {
	httpx.WriteJSON(w, status, body)
}

func writeError(w http.ResponseWriter, status int, code string) {
	httpx.WriteError(w, status, code)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	// httpx.DecodeJSON writes a 400 response on failure; the handler
	// is then expected to bail. Preserve the exact original semantics.
	return httpx.DecodeJSON(w, r, v)
}
