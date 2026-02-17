package observability

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// WebhookMetrics records webhook pipeline metrics (provider, worker, sender).
type WebhookMetrics interface {
	RecordJobsEnqueued(eventType string, count int64)
	RecordProviderError(reason string)
	RecordDelivery(eventType, status string)
	RecordWebhookDisabled(reason string)
	RecordDispatchError(reason string)
	RecordWebhookDeliveryDuration(duration time.Duration, eventType, status string)
}

// webhookMetrics implements WebhookMetrics.
type webhookMetrics struct {
	jobsEnqueued     metric.Int64Counter
	providerErrors   metric.Int64Counter
	deliveries       metric.Int64Counter
	disabled         metric.Int64Counter
	dispatchErrors   metric.Int64Counter
	deliveryDuration metric.Float64Histogram
}

// NewWebhookMetrics creates WebhookMetrics. Returns (nil, nil) when meter is nil (metrics disabled).
func NewWebhookMetrics(meter metric.Meter) (WebhookMetrics, error) {
	if meter == nil {
		//nolint:nilnil // intentional: callers use "if metrics != nil" when metrics disabled
		return nil, nil
	}

	jobsEnqueued, err := meter.Int64Counter(
		MetricNameWebhookJobsEnqueued,
		metric.WithDescription("Total webhook jobs enqueued"),
	)
	if err != nil {
		return nil, fmt.Errorf("create webhook jobs enqueued counter: %w", err)
	}

	providerErrors, err := meter.Int64Counter(
		MetricNameWebhookProviderErrors,
		metric.WithDescription("Total webhook provider errors (list/enqueue failures)"),
	)
	if err != nil {
		return nil, fmt.Errorf("create webhook provider errors counter: %w", err)
	}

	deliveries, err := meter.Int64Counter(
		MetricNameWebhookDeliveries,
		metric.WithDescription("Total webhook delivery outcomes by status"),
	)
	if err != nil {
		return nil, fmt.Errorf("create webhook deliveries counter: %w", err)
	}

	disabled, err := meter.Int64Counter(
		MetricNameWebhookDisabled,
		metric.WithDescription("Total webhooks disabled (410 or max attempts)"),
	)
	if err != nil {
		return nil, fmt.Errorf("create webhook disabled counter: %w", err)
	}

	dispatchErrors, err := meter.Int64Counter(
		MetricNameWebhookDispatchErrors,
		metric.WithDescription("Total webhook dispatch errors (e.g. get webhook failed)"),
	)
	if err != nil {
		return nil, fmt.Errorf("create webhook dispatch errors counter: %w", err)
	}

	deliveryDuration, err := meter.Float64Histogram(
		MetricNameWebhookDeliveryDuration,
		metric.WithDescription("Webhook delivery duration (seconds)"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, fmt.Errorf("create webhook delivery duration histogram: %w", err)
	}

	return &webhookMetrics{
		jobsEnqueued:     jobsEnqueued,
		providerErrors:   providerErrors,
		deliveries:       deliveries,
		disabled:         disabled,
		dispatchErrors:   dispatchErrors,
		deliveryDuration: deliveryDuration,
	}, nil
}

func (wm *webhookMetrics) RecordJobsEnqueued(eventType string, count int64) {
	eventType = NormalizeEventType(eventType)
	wm.jobsEnqueued.Add(context.Background(), count, metric.WithAttributes(attrEventType(eventType)))
}

func (wm *webhookMetrics) RecordProviderError(reason string) {
	reason = NormalizeReason(reason, AllowedProviderReason)
	wm.providerErrors.Add(context.Background(), 1, metric.WithAttributes(attribute.String(AttrReason, reason)))
}

func (wm *webhookMetrics) RecordDelivery(eventType, status string) {
	eventType = NormalizeEventType(eventType)
	status = NormalizeStatus(status)
	wm.deliveries.Add(context.Background(), 1,
		metric.WithAttributes(attrEventType(eventType), attribute.String(AttrStatus, status)))
}

func (wm *webhookMetrics) RecordWebhookDisabled(reason string) {
	reason = NormalizeReason(reason, AllowedDisabledReason)
	wm.disabled.Add(context.Background(), 1, metric.WithAttributes(attribute.String(AttrReason, reason)))
}

func (wm *webhookMetrics) RecordDispatchError(reason string) {
	reason = NormalizeReason(reason, AllowedDispatchReason)
	wm.dispatchErrors.Add(context.Background(), 1, metric.WithAttributes(attribute.String(AttrReason, reason)))
}

func (wm *webhookMetrics) RecordWebhookDeliveryDuration(duration time.Duration, eventType, status string) {
	eventType = NormalizeEventType(eventType)
	status = NormalizeStatus(status)
	wm.deliveryDuration.Record(context.Background(), duration.Seconds(),
		metric.WithAttributes(attrEventType(eventType), attribute.String(AttrStatus, status)))
}
