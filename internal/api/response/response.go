package response

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/formbricks/hub/internal/observability"
)

// RespondError maps err to an RFC 9457 problem response, logs it exactly once,
// and writes it. It is the single error exit point for handlers: domain and
// sentinel errors are translated to the right status, code, and invalid_params,
// and the underlying cause of server errors is logged but never sent to clients.
func RespondError(w http.ResponseWriter, r *http.Request, err error) {
	writeProblem(w, r, problemFromError(err), err)
}

// RespondProblem writes an explicit problem response when there is no error
// value to map, e.g. for request preconditions like a missing path parameter
// or an unsupported HTTP method.
func RespondProblem(w http.ResponseWriter, r *http.Request, status int, detail string) {
	writeProblem(w, r, newProblem(status, detail), nil)
}

// RespondUnauthorized writes a 401 Unauthorized problem. The writer adds the
// required WWW-Authenticate challenge for the 401 status (RFC 9110 §11.6.1).
func RespondUnauthorized(w http.ResponseWriter, r *http.Request, detail string) {
	RespondProblem(w, r, http.StatusUnauthorized, detail)
}

// RespondNotFound writes a 404 Not Found problem.
func RespondNotFound(w http.ResponseWriter, r *http.Request, detail string) {
	RespondProblem(w, r, http.StatusNotFound, detail)
}

// RespondServiceUnavailable writes a 503 Service Unavailable problem.
func RespondServiceUnavailable(w http.ResponseWriter, r *http.Request, detail string) {
	RespondProblem(w, r, http.StatusServiceUnavailable, detail)
}

// RespondInvalidParams writes a 400 validation problem describing one or more
// invalid request fields. Use it for request-level checks (path or query
// parameters) where there is no error value to map through RespondError, so
// field errors share one machine-readable shape across the API.
func RespondInvalidParams(w http.ResponseWriter, r *http.Request, params ...InvalidParam) {
	problem := newValidationProblem()
	problem.InvalidParams = params

	writeProblem(w, r, problem, nil)
}

// writeProblem populates instance + request_id from the request, logs the
// problem exactly once, and encodes it as application/problem+json.
func writeProblem(w http.ResponseWriter, r *http.Request, problem ProblemDetails, cause error) {
	ctx := r.Context()

	if problem.Instance == "" {
		problem.Instance = r.URL.Path
	}

	problem.RequestID = observability.RequestIDFromContext(ctx)

	logProblem(ctx, r, problem, cause)

	w.Header().Set("Content-Type", problemContentType)
	w.Header().Set("Cache-Control", "no-store")

	// RFC 9110 §11.6.1 requires a WWW-Authenticate challenge on 401 responses.
	if problem.Status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", "Bearer")
	}

	w.WriteHeader(problem.Status)

	if err := json.NewEncoder(w).Encode(problem); err != nil {
		slog.ErrorContext(ctx, "Failed to encode problem response", "error", err)
	}
}

// logProblem logs every error response exactly once. A response is logged at
// Error only when it is a server error (5xx) that carries an underlying cause —
// i.e. an unexpected failure worth alerting on. Everything else (client errors,
// and deliberate 5xx like a disabled feature returning 503 with no cause) is
// logged at Warn, so expected operational states do not trigger false alarms.
// trace_id and request_id are added automatically by the slog handler from ctx.
func logProblem(ctx context.Context, r *http.Request, problem ProblemDetails, cause error) {
	attrs := []any{"status", problem.Status, "code", problem.Code, "method", r.Method, "path", r.URL.Path}

	if problem.Status >= http.StatusInternalServerError && cause != nil {
		attrs = append(attrs, "error", cause)
		slog.ErrorContext(ctx, "Request failed", attrs...) // #nosec G706 -- structured slog key-values

		return
	}

	attrs = append(attrs, "detail", problem.Detail)
	slog.WarnContext(ctx, "Request rejected", attrs...) // #nosec G706 -- structured slog key-values
}

// RespondJSON writes a JSON response directly without wrapping.
func RespondJSON(w http.ResponseWriter, statusCode int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	if err := json.NewEncoder(w).Encode(data); err != nil {
		slog.Error("Failed to encode JSON response", "error", err)
	}
}
