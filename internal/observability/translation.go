package observability

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/metric"
)

// TranslationMetrics records translation pipeline metrics (provider, worker).
// Backed by the shared enrichmentMetrics implementation.
type TranslationMetrics interface {
	RecordJobsEnqueued(ctx context.Context, count int64)
	RecordProviderError(ctx context.Context, reason string)
	RecordTranslationOutcome(ctx context.Context, status string)
	RecordWorkerError(ctx context.Context, reason string)
	RecordTranslationDuration(ctx context.Context, duration time.Duration, status string)
}

// translationMetrics adapts the shared impl to the TranslationMetrics outcome/duration names.
type translationMetrics struct{ *enrichmentMetrics }

func (m translationMetrics) RecordTranslationOutcome(ctx context.Context, status string) {
	m.recordOutcome(ctx, status)
}

func (m translationMetrics) RecordTranslationDuration(ctx context.Context, duration time.Duration, status string) {
	m.recordDuration(ctx, duration, status)
}

// NewTranslationMetrics creates TranslationMetrics. Returns (nil, nil) when meter is nil (metrics disabled).
func NewTranslationMetrics(meter metric.Meter) (TranslationMetrics, error) {
	shared, err := newEnrichmentMetrics(meter, enrichmentMetricsSpec{
		jobsEnqueuedName:      MetricNameTranslationJobsEnqueued,
		providerErrorsName:    MetricNameTranslationProviderErrors,
		outcomesName:          MetricNameTranslationOutcomes,
		workerErrorsName:      MetricNameTranslationWorkerErrors,
		durationName:          MetricNameTranslationDuration,
		providerErrorsDesc:    "Total translation provider errors (settings/enqueue failures)",
		workerErrorsDesc:      "Total translation worker errors (get record, provider, update)",
		allowedProviderReason: AllowedTranslationProviderReason,
		allowedWorkerReason:   AllowedTranslationWorkerReason,
		allowedOutcomeStatus:  AllowedTranslationOutcomeStatus,
	})
	if err != nil || shared == nil {
		// shared == nil means the meter is disabled; return a nil interface, not a typed nil.
		return nil, err
	}

	return translationMetrics{shared}, nil
}
