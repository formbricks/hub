// Package middleware provides HTTP middleware (auth, logging, CORS).
package middleware

import (
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
			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
				response.RespondUnauthorized(w, "Invalid Authorization header format. Expected: Bearer <api-key>")
				return
			}

			providedKey := parts[1]
			if providedKey == "" {
				response.RespondUnauthorized(w, "API key is empty")
				return
			}

			// Compare the provided key with the configured key
			if providedKey != apiKey {
				response.RespondUnauthorized(w, "Invalid API key")
				return
			}

			// Key is valid, proceed with the request
			next.ServeHTTP(w, r)
		})
	}
}
