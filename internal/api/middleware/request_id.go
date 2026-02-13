package middleware

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	"github.com/formbricks/hub/internal/observability"
)

const requestIDHeader = "X-Request-ID"

// RequestID runs first in the chain: ensures every request has an X-Request-ID in context
// and in the response header. If the client sends X-Request-ID, it is propagated; otherwise one is generated.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(requestIDHeader)
		if id == "" {
			id = uuid.Must(uuid.NewV7()).String()
		}

		ctx := context.WithValue(r.Context(), observability.RequestIDKey, id)
		w.Header().Set(requestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
