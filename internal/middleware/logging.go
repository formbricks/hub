package middleware

import (
	"log/slog"
	"time"

	"github.com/danielgtaylor/huma/v2"
)

// Logging creates a middleware that logs HTTP requests and responses.
// It logs request details (method, path, remote IP) and response details
// (status code, duration, size) using structured logging with slog.
func Logging(logger *slog.Logger) func(ctx huma.Context, next func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		start := time.Now()

		// Get request details
		method := ctx.Method()
		path := ctx.URL().Path
		remoteAddr := ctx.RemoteAddr()

		// Log request
		logger.Debug("incoming request",
			"method", method,
			"path", path,
			"remote_addr", remoteAddr,
		)

		// Call next middleware/handler
		next(ctx)

		// Calculate duration
		duration := time.Since(start)

		// Get response details
		status := ctx.Status()

		// Log response
		logger.Info("request completed",
			"method", method,
			"path", path,
			"status", status,
			"duration_ms", duration.Milliseconds(),
			"remote_addr", remoteAddr,
		)
	}
}
