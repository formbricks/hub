package observability

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/formbricks/hub/internal/config"
)

func newOTLPTraceExporter(ctx context.Context, cfg *config.Config) (sdktrace.SpanExporter, error) {
	opts := []otlptracehttp.Option{}
	if cfg.OtelExporterOtlpEndpoint != "" {
		opts = append(opts, otlptracehttp.WithEndpoint(cfg.OtelExporterOtlpEndpoint))
	}

	exp, err := otlptracehttp.New(ctx, opts...)
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
