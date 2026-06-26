package observability

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// TranslationMetrics records translation pipeline metrics (provider, worker).
// Methods accept ctx for future exemplar support. Mirrors EmbeddingMetrics.
type TranslationMetrics interface {
	RecordJobsEnqueued(ctx context.Context, count int64)
	RecordProviderError(ctx context.Context, reason string)
	RecordTranslationOutcome(ctx context.Context, status string)
	RecordWorkerError(ctx context.Context, reason string)
	RecordTranslationDuration(ctx context.Context, duration time.Duration, status string)
}

// translationMetrics implements TranslationMetrics.
type translationMetrics struct {
	jobsEnqueued   metric.Int64Counter
	providerErrors metric.Int64Counter
	outcomes       metric.Int64Counter
	workerErrors   metric.Int64Counter
	duration       metric.Float64Histogram
}

// NewTranslationMetrics creates TranslationMetrics. Returns (nil, nil) when meter is nil (metrics disabled).
func NewTranslationMetrics(meter metric.Meter) (TranslationMetrics, error) {
	if meter == nil {
		//nolint:nilnil // intentional: callers use "if metrics != nil" when metrics disabled
		return nil, nil
	}

	jobsEnqueued, err := meter.Int64Counter(
		MetricNameTranslationJobsEnqueued,
		metric.WithDescription("Total translation jobs enqueued"),
	)
	if err != nil {
		return nil, fmt.Errorf("create translation jobs enqueued counter: %w", err)
	}

	providerErrors, err := meter.Int64Counter(
		MetricNameTranslationProviderErrors,
		metric.WithDescription("Total translation provider errors (settings/enqueue failures)"),
	)
	if err != nil {
		return nil, fmt.Errorf("create translation provider errors counter: %w", err)
	}

	outcomes, err := meter.Int64Counter(
		MetricNameTranslationOutcomes,
		metric.WithDescription("Total translation job outcomes by status"),
	)
	if err != nil {
		return nil, fmt.Errorf("create translation outcomes counter: %w", err)
	}

	workerErrors, err := meter.Int64Counter(
		MetricNameTranslationWorkerErrors,
		metric.WithDescription("Total translation worker errors (get record, provider, update)"),
	)
	if err != nil {
		return nil, fmt.Errorf("create translation worker errors counter: %w", err)
	}

	duration, err := meter.Float64Histogram(
		MetricNameTranslationDuration,
		metric.WithDescription("Translation job duration (seconds)"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, fmt.Errorf("create translation duration histogram: %w", err)
	}

	return &translationMetrics{
		jobsEnqueued:   jobsEnqueued,
		providerErrors: providerErrors,
		outcomes:       outcomes,
		workerErrors:   workerErrors,
		duration:       duration,
	}, nil
}

func (t *translationMetrics) RecordJobsEnqueued(ctx context.Context, count int64) {
	t.jobsEnqueued.Add(ctx, count)
}

func (t *translationMetrics) RecordProviderError(ctx context.Context, reason string) {
	reason = NormalizeReason(reason, AllowedTranslationProviderReason)
	t.providerErrors.Add(ctx, 1, metric.WithAttributes(attribute.String(AttrReason, reason)))
}

func (t *translationMetrics) RecordTranslationOutcome(ctx context.Context, status string) {
	status = normalizeTranslationStatus(status)
	t.outcomes.Add(ctx, 1, metric.WithAttributes(attribute.String(AttrStatus, status)))
}

func (t *translationMetrics) RecordWorkerError(ctx context.Context, reason string) {
	reason = NormalizeReason(reason, AllowedTranslationWorkerReason)
	t.workerErrors.Add(ctx, 1, metric.WithAttributes(attribute.String(AttrReason, reason)))
}

func (t *translationMetrics) RecordTranslationDuration(ctx context.Context, duration time.Duration, status string) {
	status = normalizeTranslationStatus(status)
	t.duration.Record(ctx, duration.Seconds(), metric.WithAttributes(attribute.String(AttrStatus, status)))
}

func normalizeTranslationStatus(status string) string {
	if AllowedTranslationOutcomeStatus(status) {
		return status
	}

	return "other"
}
