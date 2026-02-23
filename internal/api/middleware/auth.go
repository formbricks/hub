// Package middleware provides HTTP middleware (auth, logging, CORS).
package middleware

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/formbricks/hub/internal/api/response"
)

// Auth middleware validates API keys from the Authorization header
// It compares the provided key against the API key from configuration
// The apiKey parameter must not be empty (enforced at server startup).
func Auth(apiKey string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				response.RespondUnauthorized(w, "Missing Authorization header")

				return
			}

			// Expected format: "Bearer <api-key>"
			const bearerParts = 2

			parts := strings.SplitN(authHeader, " ", bearerParts)
			if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
				response.RespondUnauthorized(w, "Invalid Authorization header format. Expected: Bearer <api-key>")

				return
			}

			providedKey := parts[1]
			if providedKey == "" {
				response.RespondUnauthorized(w, "API key is empty")

				return
			}

			// Constant-time comparison to avoid timing side-channels.
			provided := []byte(providedKey)

			expected := []byte(apiKey)
			if len(provided) != len(expected) {
				subtle.ConstantTimeCompare(expected, expected) // dummy to keep constant time
				response.RespondUnauthorized(w, "Invalid API key")

				return
			}

			if subtle.ConstantTimeCompare(provided, expected) != 1 {
				response.RespondUnauthorized(w, "Invalid API key")

				return
			}

			// Key is valid, proceed with the request
			next.ServeHTTP(w, r)
		})
	}
}
