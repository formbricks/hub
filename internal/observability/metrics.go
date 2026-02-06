// Package observability provides OpenTelemetry metrics (Prometheus exporter) and optional tracing/Sentry wiring.
package observability

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel/attribute"
	prometheusexporter "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
)

const (
	meterScope         = "github.com/formbricks/hub/internal/observability"
	defaultServiceName = "formbricks-hub"
	cardinalityLimit   = 2000
)

// latencyHistogramBoundaries are Prometheus-style buckets (seconds) for request and webhook duration histograms.
var latencyHistogramBoundaries = []float64{0.005, 0.025, 0.1, 0.5, 1, 2.5, 5}

// HubMetrics is the single metrics interface for the Hub (HTTP, publisher, webhooks).
type HubMetrics interface {
	RecordRequest(ctx context.Context, method, route, statusClass string, duration time.Duration)
	RecordEventDropped(ctx context.Context, eventType string)
	RecordWebhookJobsEnqueued(ctx context.Context, eventType string, count int)
	RecordWebhookEnqueueError(ctx context.Context, eventType string)
	RecordWebhookDelivery(ctx context.Context, eventType, outcome string, duration time.Duration)
	RecordWebhookDisabled(ctx context.Context, reason string)
}

// MeterProviderShutdown is the subset of the SDK MeterProvider needed for shutdown.
type MeterProviderShutdown interface {
	Shutdown(ctx context.Context) error
}

// MeterProviderConfig holds configuration for creating the MeterProvider and metrics.
type MeterProviderConfig struct {
	// ServiceName is used in the resource (default: formbricks-hub).
	ServiceName string
}

// NewMeterProvider creates a MeterProvider with Prometheus exporter and returns the provider,
// an HTTP handler for /metrics, and HubMetrics that use the provider's Meter.
// Caller must call provider.Shutdown on exit. When metrics are disabled, pass nil for metrics at call sites.
func NewMeterProvider(_ context.Context, cfg MeterProviderConfig) (provider MeterProviderShutdown, metricsHandler http.Handler, metrics HubMetrics, err error) {
	serviceNameVal := cfg.ServiceName
	if serviceNameVal == "" {
		serviceNameVal = defaultServiceName
	}

	// Use a single resource to avoid Schema URL conflicts when merging with resource.Default().
	res := resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(serviceNameVal),
	)

	reg := prometheus.NewRegistry()

	exporter, err := prometheusexporter.New(
		prometheusexporter.WithRegisterer(reg),
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create prometheus exporter: %w", err)
	}

	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(exporter),
		sdkmetric.WithCardinalityLimit(cardinalityLimit),
		sdkmetric.WithView(
			sdkmetric.NewView(
				sdkmetric.Instrument{Name: "http.server.duration"},
				sdkmetric.Stream{Aggregation: sdkmetric.AggregationExplicitBucketHistogram{Boundaries: latencyHistogramBoundaries}},
			),
			sdkmetric.NewView(
				sdkmetric.Instrument{Name: "webhook_delivery_duration_seconds"},
				sdkmetric.Stream{Aggregation: sdkmetric.AggregationExplicitBucketHistogram{Boundaries: latencyHistogramBoundaries}},
			),
		),
	)
	provider = mp
	meter := mp.Meter(meterScope)

	metrics, err = newMetricsFromMeter(meter)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create metrics instruments: %w", err)
	}

	metricsHandler = promhttp.HandlerFor(reg, promhttp.HandlerOpts{})

	return provider, metricsHandler, metrics, nil
}

func newMetricsFromMeter(meter metric.Meter) (*hubMetricsImpl, error) {
	requestCount, err := meter.Int64Counter(
		"http.server.request_count",
		metric.WithDescription("Total HTTP requests"),
	)
	if err != nil {
		return nil, fmt.Errorf("request_count: %w", err)
	}

	requestDuration, err := meter.Float64Histogram(
		"http.server.duration",
		metric.WithDescription("HTTP request duration in seconds"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, fmt.Errorf("http.server.duration: %w", err)
	}

	eventsDropped, err := meter.Int64Counter(
		"events_dropped_total",
		metric.WithDescription("Events dropped when publisher channel is full"),
	)
	if err != nil {
		return nil, fmt.Errorf("events_dropped_total: %w", err)
	}

	webhookJobsEnqueued, err := meter.Int64Counter(
		"webhook_jobs_enqueued_total",
		metric.WithDescription("Webhook dispatch jobs enqueued per event type"),
	)
	if err != nil {
		return nil, fmt.Errorf("webhook_jobs_enqueued_total: %w", err)
	}

	webhookEnqueueErrors, err := meter.Int64Counter(
		"webhook_jobs_enqueue_errors_total",
		metric.WithDescription("Webhook job enqueue failures"),
	)
	if err != nil {
		return nil, fmt.Errorf("webhook_jobs_enqueue_errors_total: %w", err)
	}

	webhookDeliveries, err := meter.Int64Counter(
		"webhook_deliveries_total",
		metric.WithDescription("Webhook delivery outcomes"),
	)
	if err != nil {
		return nil, fmt.Errorf("webhook_deliveries_total: %w", err)
	}

	webhookDeliveryDuration, err := meter.Float64Histogram(
		"webhook_delivery_duration_seconds",
		metric.WithDescription("Webhook delivery duration in seconds"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, fmt.Errorf("webhook_delivery_duration_seconds: %w", err)
	}

	webhookDisabled, err := meter.Int64Counter(
		"webhook_disabled_total",
		metric.WithDescription("Webhooks disabled by reason (410_gone, max_retries)"),
	)
	if err != nil {
		return nil, fmt.Errorf("webhook_disabled_total: %w", err)
	}

	return &hubMetricsImpl{
		requestCount:         requestCount,
		requestDuration:      requestDuration,
		eventsDropped:        eventsDropped,
		webhookJobsEnqueued:  webhookJobsEnqueued,
		webhookEnqueueErrors: webhookEnqueueErrors,
		webhookDeliveries:    webhookDeliveries,
		webhookDeliveryDur:   webhookDeliveryDuration,
		webhookDisabled:      webhookDisabled,
	}, nil
}

type hubMetricsImpl struct {
	requestCount         metric.Int64Counter
	requestDuration      metric.Float64Histogram
	eventsDropped        metric.Int64Counter
	webhookJobsEnqueued  metric.Int64Counter
	webhookEnqueueErrors metric.Int64Counter
	webhookDeliveries    metric.Int64Counter
	webhookDeliveryDur   metric.Float64Histogram
	webhookDisabled      metric.Int64Counter
}

func (m *hubMetricsImpl) RecordRequest(ctx context.Context, method, route, statusClass string, duration time.Duration) {
	attrs := attribute.NewSet(
		attribute.String("method", method),
		attribute.String("route", route),
		attribute.String("status_class", statusClass),
	)
	m.requestCount.Add(ctx, 1, metric.WithAttributeSet(attrs))

	durAttrs := attribute.NewSet(
		attribute.String("method", method),
		attribute.String("route", route),
	)
	m.requestDuration.Record(ctx, duration.Seconds(), metric.WithAttributeSet(durAttrs))
}

func (m *hubMetricsImpl) RecordEventDropped(ctx context.Context, eventType string) {
	eventType = normalizeEventType(eventType)
	m.eventsDropped.Add(ctx, 1, metric.WithAttributes(attribute.String("event_type", eventType)))
}

func (m *hubMetricsImpl) RecordWebhookJobsEnqueued(ctx context.Context, eventType string, count int) {
	eventType = normalizeEventType(eventType)
	m.webhookJobsEnqueued.Add(ctx, int64(count), metric.WithAttributes(attribute.String("event_type", eventType)))
}

func (m *hubMetricsImpl) RecordWebhookEnqueueError(ctx context.Context, eventType string) {
	eventType = normalizeEventType(eventType)
	m.webhookEnqueueErrors.Add(ctx, 1, metric.WithAttributes(attribute.String("event_type", eventType)))
}

func (m *hubMetricsImpl) RecordWebhookDelivery(ctx context.Context, eventType, outcome string, duration time.Duration) {
	eventType = normalizeEventType(eventType)
	outcome = normalizeOutcome(outcome)
	m.webhookDeliveries.Add(ctx, 1, metric.WithAttributes(
		attribute.String("event_type", eventType),
		attribute.String("outcome", outcome),
	))
	m.webhookDeliveryDur.Record(ctx, duration.Seconds(), metric.WithAttributes(
		attribute.String("event_type", eventType),
		attribute.String("outcome", outcome),
	))
}

func (m *hubMetricsImpl) RecordWebhookDisabled(ctx context.Context, reason string) {
	reason = normalizeDisabledReason(reason)
	m.webhookDisabled.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", reason)))
}

// normalizeEventType maps event type to a bounded set for cardinality control.
func normalizeEventType(s string) string {
	switch s {
	case "feedback_record.created", "feedback_record.updated", "feedback_record.deleted",
		"webhook.created", "webhook.updated", "webhook.deleted":
		return s
	default:
		return "unknown"
	}
}

// normalizeOutcome maps delivery outcome to a bounded set.
func normalizeOutcome(s string) string {
	switch s {
	case "success", "retryable_failure", "disabled_410", "disabled_max_retries":
		return s
	default:
		return "unknown"
	}
}

// normalizeDisabledReason maps disabled reason to a bounded set.
func normalizeDisabledReason(s string) string {
	switch s {
	case "410_gone", "max_retries":
		return s
	default:
		return "unknown"
	}
}
