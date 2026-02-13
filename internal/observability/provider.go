package observability

import (
	"context"
	"fmt"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	prometheusexporter "go.opentelemetry.io/otel/exporters/prometheus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.27.0"

	"github.com/formbricks/hub/internal/config"
)

// NewMeterProvider creates a MeterProvider and an HTTP handler for /metrics when metrics are enabled.
// When cfg.OtelMetricsExporter is not "prometheus", returns (nil, nil, nil).
func NewMeterProvider(cfg *config.Config) (*sdkmetric.MeterProvider, http.Handler, error) {
	if cfg == nil || (cfg.OtelMetricsExporter != "prometheus") {
		return nil, nil, nil
	}

	reg := prometheus.NewRegistry()

	exporter, err := prometheusexporter.New(
		prometheusexporter.WithRegisterer(reg),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("create prometheus exporter: %w", err)
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName("hub-api"),
		),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("create resource: %w", err)
	}

	// Duration histograms record in seconds; use second-based buckets so quantiles and SLOs
	// (e.g. "95% under 300ms") are accurate. OTel default boundaries are millisecond-oriented.
	durationHistogramBounds := []float64{0, 0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.3, 0.5, 0.75, 1, 2.5, 5, 7.5, 10}
	view := sdkmetric.NewView(
		sdkmetric.Instrument{Name: "hub_*_duration_seconds"},
		sdkmetric.Stream{Aggregation: sdkmetric.AggregationExplicitBucketHistogram{Boundaries: durationHistogramBounds}},
	)

	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(exporter),
		sdkmetric.WithView(view),
	)

	handler := promhttp.HandlerFor(reg, promhttp.HandlerOpts{})

	return provider, handler, nil
}

// ShutdownMeterProvider flushes and shuts down the MeterProvider. Safe to call with nil.
func ShutdownMeterProvider(ctx context.Context, provider *sdkmetric.MeterProvider) error {
	if provider == nil {
		return nil
	}

	if err := provider.Shutdown(ctx); err != nil {
		return fmt.Errorf("meter provider shutdown: %w", err)
	}

	return nil
}

// NewTracerProvider creates a TracerProvider when tracing is enabled.
// When cfg.OtelTracesExporter is empty, returns (nil, nil).
func NewTracerProvider(cfg *config.Config) (*sdktrace.TracerProvider, error) {
	if cfg == nil || cfg.OtelTracesExporter == "" {
		//nolint:nilnil // intentional: tracing disabled, caller checks for nil
		return nil, nil
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName("hub-api"),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("create resource: %w", err)
	}

	var opts []sdktrace.TracerProviderOption

	opts = append(opts, sdktrace.WithResource(res))

	switch cfg.OtelTracesExporter {
	case "otlp":
		exp, err := newOTLPTraceExporter(context.Background(), cfg)
		if err != nil {
			return nil, fmt.Errorf("create OTLP trace exporter: %w", err)
		}

		opts = append(opts, sdktrace.WithBatcher(exp))
	case "stdout":
		exp, err := newStdoutTraceExporter()
		if err != nil {
			return nil, fmt.Errorf("create stdout trace exporter: %w", err)
		}

		opts = append(opts, sdktrace.WithBatcher(exp))
	default:
		//nolint:nilnil // unknown exporter value: treat as disabled, caller checks for nil
		return nil, nil
	}

	return sdktrace.NewTracerProvider(opts...), nil
}

// ShutdownTracerProvider flushes and shuts down the TracerProvider. Safe to call with nil.
func ShutdownTracerProvider(ctx context.Context, provider *sdktrace.TracerProvider) error {
	if provider == nil {
		return nil
	}

	if err := provider.Shutdown(ctx); err != nil {
		return fmt.Errorf("tracer provider shutdown: %w", err)
	}

	return nil
}
