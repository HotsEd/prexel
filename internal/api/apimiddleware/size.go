package apimiddleware

import "net/http"

// RequestSize caps the request body to maxBytes. Handlers that read the body
// see io.ErrUnexpectedEOF / http.MaxBytesError if the cap is exceeded.
func RequestSize(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			next.ServeHTTP(w, r)
		})
	}
}
