package observability

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// newOTLPTraceExporter creates an OTLP HTTP trace exporter. The SDK reads
// OTEL_EXPORTER_OTLP_ENDPOINT (and scheme/insecure) from the environment.
func newOTLPTraceExporter(ctx context.Context) (sdktrace.SpanExporter, error) {
	exp, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("create OTLP HTTP trace exporter: %w", err)
	}

	return exp, nil
}

func newStdoutTraceExporter() (sdktrace.SpanExporter, error) {
	exp, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
	if err != nil {
		return nil, fmt.Errorf("create stdout trace exporter: %w", err)
	}

	return exp, nil
}
