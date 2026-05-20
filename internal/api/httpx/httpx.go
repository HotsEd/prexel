// Package httpx holds tiny HTTP helpers shared by every handler in the
// `api/handler` package — and by any future package that needs to write
// the canonical Prexel JSON envelope.
//
// History: each handler used to roll its own writeJSON / writeError /
// decodeJSON (`setup.go`, `rbac.go`, …), which made the wire format drift
// slightly (different field names, different status codes for similar
// failures). Centralising here gives one definition for "what an error
// looks like to the frontend".
//
// The envelope is intentionally minimal:
//
//	error response:   {"error": "<code>"}        (+ optional "message" for hints)
//	success response: handler-shaped JSON
//
// Codes are stable, machine-readable, snake_case strings (e.g. "invalid_json",
// "not_found", "forbidden"). The frontend keys translations off them.
package httpx

import (
	"encoding/json"
	"net/http"
)

// WriteJSON serialises body as JSON, sets Content-Type and writes status.
// Encode errors are swallowed because there is no recovery path once headers
// are out the door.
func WriteJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// WriteError writes the canonical {"error": code} envelope. Use Codes that
// are stable strings — the frontend translates them.
func WriteError(w http.ResponseWriter, status int, code string) {
	WriteJSON(w, status, map[string]string{"error": code})
}

// WriteErrorMessage writes {"error": code, "message": message} for cases
// where a free-text hint helps the caller (validation failures, mostly).
// Avoid leaking internal details through `message` — keep it terse.
func WriteErrorMessage(w http.ResponseWriter, status int, code, message string) {
	WriteJSON(w, status, map[string]string{"error": code, "message": message})
}

// DecodeJSON unmarshals r.Body into v. On failure it writes a 400
// "invalid_json" response and returns false — the caller should bail
// immediately. The body is left half-consumed; that's fine because the
// caller is going to return anyway.
func DecodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_json")
		return false
	}
	return true
}
