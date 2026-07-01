package observability

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/metric"
)

// EmotionsMetrics records emotion pipeline metrics (provider, worker).
// Backed by the shared enrichmentMetrics implementation.
type EmotionsMetrics interface {
	RecordJobsEnqueued(ctx context.Context, count int64)
	RecordProviderError(ctx context.Context, reason string)
	RecordEmotionsOutcome(ctx context.Context, status string)
	RecordWorkerError(ctx context.Context, reason string)
	RecordEmotionsDuration(ctx context.Context, duration time.Duration, status string)
}

// emotionsMetrics adapts the shared impl to the EmotionsMetrics outcome/duration names.
type emotionsMetrics struct{ *enrichmentMetrics }

func (m emotionsMetrics) RecordEmotionsOutcome(ctx context.Context, status string) {
	m.recordOutcome(ctx, status)
}

func (m emotionsMetrics) RecordEmotionsDuration(ctx context.Context, duration time.Duration, status string) {
	m.recordDuration(ctx, duration, status)
}

// NewEmotionsMetrics creates EmotionsMetrics. Returns (nil, nil) when meter is nil (metrics disabled).
func NewEmotionsMetrics(meter metric.Meter) (EmotionsMetrics, error) {
	shared, err := newEnrichmentMetrics(meter, enrichmentMetricsSpec{
		jobsEnqueuedName:      MetricNameEmotionsJobsEnqueued,
		providerErrorsName:    MetricNameEmotionsProviderErrors,
		outcomesName:          MetricNameEmotionsOutcomes,
		workerErrorsName:      MetricNameEmotionsWorkerErrors,
		durationName:          MetricNameEmotionsDuration,
		providerErrorsDesc:    "Total emotion provider errors (settings/enqueue failures)",
		workerErrorsDesc:      "Total emotion worker errors (get record, provider, update)",
		allowedProviderReason: AllowedEmotionsProviderReason,
		allowedWorkerReason:   AllowedEmotionsWorkerReason,
		allowedOutcomeStatus:  AllowedEmotionsOutcomeStatus,
	})
	if err != nil || shared == nil {
		// shared == nil means the meter is disabled; return a nil interface, not a typed nil.
		return nil, err
	}

	return emotionsMetrics{shared}, nil
}
