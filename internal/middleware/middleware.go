// Package middleware provides HTTP middleware for authentication, logging, rate limiting,
// and request validation. All middleware is router-agnostic and works with both Chi
// and Huma's middleware systems.
//
// Available middleware:
//   - APIKeyAuth: Optional API key authentication via X-API-Key header
//   - Logging: Structured request/response logging with slog
//   - MaxBodySize: Limits request body size to prevent memory exhaustion
//   - RateLimiter: Token bucket rate limiting per-IP and globally
package middleware

import (
	"net/http"
)

// MaxBodySize returns a middleware that limits the maximum size of request bodies.
// This prevents memory exhaustion attacks from large payloads.
func MaxBodySize(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Limit the request body size
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			next.ServeHTTP(w, r)
		})
	}
}
