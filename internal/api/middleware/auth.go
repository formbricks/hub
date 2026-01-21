package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/formbricks/hub/internal/api/handlers"
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
				handlers.RespondUnauthorized(w, "Missing Authorization header")
				return
			}

			// Expected format: "Bearer <api-key>"
			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
				handlers.RespondUnauthorized(w, "Invalid Authorization header format. Expected: Bearer <api-key>")
				return
			}

			apiKey := parts[1]
			if apiKey == "" {
				handlers.RespondUnauthorized(w, "API key is empty")
				return
			}

			// Validate the API key
			validatedKey, err := apiKeyRepo.ValidateAPIKey(r.Context(), apiKey)
			if err != nil {
				handlers.RespondUnauthorized(w, "Invalid or inactive API key")
				return
			}

			// Update last used timestamp asynchronously (don't block the request)
			go func() {
				// Create a new context for the background operation
				bgCtx := context.Background()
				err = apiKeyRepo.UpdateLastUsedAt(bgCtx, validatedKey.KeyHash)
				if err != nil {
					slog.Error("Failed to update last used timestamp", "error", err)
				}
			}()

			// Store the validated API key in the request context
			ctx := context.WithValue(r.Context(), APIKeyContextKey, validatedKey)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
