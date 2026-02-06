package middleware

import (
	"net/http"
	"regexp"
	"time"

	"github.com/formbricks/hub/internal/observability"
)

// UUID-like path segment: 36 chars and contains hyphen (e.g. 550e8400-e29b-41d4-a716-446655440000).
var uuidSegmentRegex = regexp.MustCompile(`/[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}(/|$)`)

// Metrics returns middleware that records HTTP request count and duration via HubMetrics.
// When metrics is nil, recording is skipped. Put Metrics outermost so duration is full request time.
func Metrics(metrics observability.HubMetrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if metrics == nil {
				next.ServeHTTP(w, r)
				return
			}
			start := time.Now()
			rw := &responseWriter{
				ResponseWriter: w,
				statusCode:     http.StatusOK,
			}
			next.ServeHTTP(rw, r)
			duration := time.Since(start)
			route := normalizeRoute(r.URL.Path)
			statusClass := statusToClass(rw.statusCode)
			metrics.RecordRequest(r.Context(), r.Method, route, statusClass, duration)
		})
	}
}

// normalizeRoute replaces UUID-like path segments with {id} to bound cardinality.
func normalizeRoute(path string) string {
	return uuidSegmentRegex.ReplaceAllString(path, "/{id}$1")
}

// statusToClass maps HTTP status code to 1xx, 2xx, 4xx, 5xx.
func statusToClass(status int) string {
	if status >= 500 {
		return "5xx"
	}
	if status >= 400 {
		return "4xx"
	}
	if status >= 300 {
		return "3xx"
	}
	if status >= 200 {
		return "2xx"
	}
	if status >= 100 {
		return "1xx"
	}
	return "unknown"
}
