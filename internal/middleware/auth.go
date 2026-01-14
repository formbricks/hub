package middleware

import (
	"crypto/subtle"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// APIKeyAuth creates a middleware that validates API key authentication.
// If apiKey is empty, the middleware is a no-op (authentication disabled).
// When enabled, requests must include an "X-API-Key" header matching the configured key.
// Public endpoints like /health and /docs are always excluded from authentication.
func APIKeyAuth(api huma.API, apiKey string) func(ctx huma.Context, next func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		// Skip auth for public endpoints
		path := ctx.URL().Path
		if path == "/health" || path == "/docs" || path == "/openapi.json" || path == "/openapi.yaml" {
			next(ctx)
			return
		}

		// Get API key from header
		providedKey := ctx.Header("X-API-Key")

		// Validate API key using constant-time comparison to prevent timing attacks
		if !secureCompare(providedKey, apiKey) {
			_ = huma.WriteErr(api, ctx, http.StatusUnauthorized,
				"Invalid or missing API key",
			)
			return
		}

		// Continue to next middleware/handler
		next(ctx)
	}
}

// secureCompare performs a constant-time comparison of two strings to prevent timing attacks.
// Returns true if the strings are equal, false otherwise.
// Pads inputs to equal length to avoid leaking information about the expected key length.
func secureCompare(a, b string) bool {
	aBytes := []byte(a)
	bBytes := []byte(b)

	// Pad to equal length to avoid leaking key length via timing
	maxLen := len(aBytes)
	if len(bBytes) > maxLen {
		maxLen = len(bBytes)
	}

	// Pad both to maxLen
	aPadded := make([]byte, maxLen)
	bPadded := make([]byte, maxLen)
	copy(aPadded, aBytes)
	copy(bPadded, bBytes)

	// Now perform constant-time comparison on equal-length slices
	// This always takes the same time regardless of whether lengths matched
	match := subtle.ConstantTimeCompare(aPadded, bPadded)

	// Also check lengths matched in constant time
	lengthMatch := subtle.ConstantTimeEq(int32(len(aBytes)), int32(len(bBytes)))

	// Both must be true: lengths match AND bytes match
	return match == 1 && lengthMatch == 1
}
