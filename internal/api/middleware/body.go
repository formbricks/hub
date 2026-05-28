package middleware

import "net/http"

// MaxBodyBytes caps the size of request bodies. When a handler reads past the
// limit, the read returns an *http.MaxBytesError, which the response layer maps
// to 413 Payload Too Large. A limit <= 0 disables the cap. This bounds memory
// use and protects JSON decoding from oversized payloads.
func MaxBodyBytes(limit int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if limit > 0 && r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, limit)
			}

			next.ServeHTTP(w, r)
		})
	}
}
