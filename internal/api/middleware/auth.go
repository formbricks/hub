package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/formbricks/hub/internal/repository"
)

type contextKey string

const APIKeyContextKey contextKey = "api_key"

// Auth middleware validates API keys from the Authorization header
func Auth(apiKeyRepo *repository.APIKeyRepository) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				http.Error(w, "Missing Authorization header", http.StatusUnauthorized)
				return
			}

			// Expected format: "Bearer <api-key>"
			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
				http.Error(w, "Invalid Authorization header format. Expected: Bearer <api-key>", http.StatusUnauthorized)
				return
			}

			apiKey := parts[1]
			if apiKey == "" {
				http.Error(w, "API key is empty", http.StatusUnauthorized)
				return
			}

			// Validate the API key
			validatedKey, err := apiKeyRepo.ValidateAPIKey(r.Context(), apiKey)
			if err != nil {
				http.Error(w, "Invalid or inactive API key", http.StatusUnauthorized)
				return
			}

			// Update last used timestamp asynchronously (don't block the request)
			go func() {
				// Create a new context for the background operation
				bgCtx := context.Background()
				_ = apiKeyRepo.UpdateLastUsedAt(bgCtx, validatedKey.KeyHash)
			}()

			// Store the validated API key in the request context
			ctx := context.WithValue(r.Context(), APIKeyContextKey, validatedKey)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
