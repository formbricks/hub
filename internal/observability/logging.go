package observability

import (
	"context"
	"fmt"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

// requestIDKey is the context key for the request ID (X-Request-ID).
// Middleware sets it; the TraceContextHandler adds it to log records.
type requestIDKey struct{}

// RequestIDKey is the context key for storing the request ID.
var RequestIDKey = &requestIDKey{}

// TraceContextHandler wraps a slog.Handler and injects trace_id, span_id, and request_id
// from the context into each log record when present.
type TraceContextHandler struct {
	inner slog.Handler
}

// NewTraceContextHandler returns a handler that adds trace context and request_id to records.
func NewTraceContextHandler(inner slog.Handler) *TraceContextHandler {
	return &TraceContextHandler{inner: inner}
}

// Enabled reports whether the inner handler is enabled for the given level.
func (h *TraceContextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// Handle adds trace_id, span_id, and request_id from context to the record, then forwards to the inner handler.
func (h *TraceContextHandler) Handle(ctx context.Context, r slog.Record) error {
	if span := trace.SpanFromContext(ctx); span.SpanContext().IsValid() {
		sc := span.SpanContext()
		r.AddAttrs(
			slog.String("trace_id", sc.TraceID().String()),
			slog.String("span_id", sc.SpanID().String()),
		)
	}

	if id, ok := ctx.Value(RequestIDKey).(string); ok && id != "" {
		r.AddAttrs(slog.String("request_id", id))
	}

	if err := h.inner.Handle(ctx, r); err != nil {
		return fmt.Errorf("inner handler: %w", err)
	}

	return nil
}

// WithAttrs returns a handler whose attributes are the concatenation of the inner's and attrs.
func (h *TraceContextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &TraceContextHandler{inner: h.inner.WithAttrs(attrs)}
}

// WithGroup returns a handler for the given group.
func (h *TraceContextHandler) WithGroup(name string) slog.Handler {
	return &TraceContextHandler{inner: h.inner.WithGroup(name)}
}
