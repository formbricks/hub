package observability

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.27.0"

	"github.com/formbricks/hub/internal/config"
)

// newResource returns a resource with service name "hub-api" merged with default.
func newResource() (*resource.Resource, error) {
	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName("hub-api"),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("merge resource: %w", err)
	}

	return res, nil
}

// NewMeterProvider creates a MeterProvider when metrics are enabled via OTLP push.
// When cfg.OtelMetricsExporter is not "otlp" (or empty), returns (nil, nil).
func NewMeterProvider(cfg *config.Config) (*sdkmetric.MeterProvider, error) {
	if cfg == nil || cfg.OtelMetricsExporter != "otlp" {
		//nolint:nilnil // intentional: metrics disabled or unsupported exporter, caller checks for nil
		return nil, nil
	}

	res, err := newResource()
	if err != nil {
		return nil, fmt.Errorf("create resource: %w", err)
	}

	// SDK reads OTEL_EXPORTER_OTLP_ENDPOINT (and scheme/insecure) from env.
	exp, err := otlpmetrichttp.New(context.Background())
	if err != nil {
		return nil, fmt.Errorf("create OTLP metric exporter: %w", err)
	}

	const metricExportInterval = 60 * time.Second

	reader := sdkmetric.NewPeriodicReader(exp,
		sdkmetric.WithInterval(metricExportInterval),
	)

	// Duration histograms record in seconds; use second-based buckets so quantiles and SLOs
	// (e.g. "95% under 300ms") are accurate. OTel default boundaries are millisecond-oriented.
	durationHistogramBounds := []float64{0, 0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.3, 0.5, 0.75, 1, 2.5, 5, 7.5, 10}
	view := sdkmetric.NewView(
		sdkmetric.Instrument{Name: "hub_*_duration_seconds"},
		sdkmetric.Stream{Aggregation: sdkmetric.AggregationExplicitBucketHistogram{Boundaries: durationHistogramBounds}},
	)

	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(reader),
		sdkmetric.WithView(view),
	)

	return provider, nil
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

	res, err := newResource()
	if err != nil {
		return nil, fmt.Errorf("create resource: %w", err)
	}

	var opts []sdktrace.TracerProviderOption

	opts = append(opts, sdktrace.WithResource(res))

	switch cfg.OtelTracesExporter {
	case "otlp":
		exp, err := newOTLPTraceExporter(context.Background())
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
