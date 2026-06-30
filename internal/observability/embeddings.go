package observability

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/metric"
)

// EmbeddingMetrics records embedding pipeline metrics (provider, worker).
// Backed by the shared enrichmentMetrics implementation.
type EmbeddingMetrics interface {
	RecordJobsEnqueued(ctx context.Context, count int64)
	RecordProviderError(ctx context.Context, reason string)
	RecordEmbeddingOutcome(ctx context.Context, status string)
	RecordWorkerError(ctx context.Context, reason string)
	RecordEmbeddingDuration(ctx context.Context, duration time.Duration, status string)
}

// embeddingMetrics adapts the shared impl to the EmbeddingMetrics outcome/duration names.
type embeddingMetrics struct{ *enrichmentMetrics }

func (m embeddingMetrics) RecordEmbeddingOutcome(ctx context.Context, status string) {
	m.recordOutcome(ctx, status)
}

func (m embeddingMetrics) RecordEmbeddingDuration(ctx context.Context, duration time.Duration, status string) {
	m.recordDuration(ctx, duration, status)
}

// NewEmbeddingMetrics creates EmbeddingMetrics. Returns (nil, nil) when meter is nil (metrics disabled).
func NewEmbeddingMetrics(meter metric.Meter) (EmbeddingMetrics, error) {
	shared, err := newEnrichmentMetrics(meter, enrichmentMetricsSpec{
		jobsEnqueuedName:      MetricNameEmbeddingJobsEnqueued,
		providerErrorsName:    MetricNameEmbeddingProviderErrors,
		outcomesName:          MetricNameEmbeddingOutcomes,
		workerErrorsName:      MetricNameEmbeddingWorkerErrors,
		durationName:          MetricNameEmbeddingDuration,
		providerErrorsDesc:    "Total embedding provider errors (enqueue failures)",
		workerErrorsDesc:      "Total embedding worker errors (get record, provider, update)",
		allowedProviderReason: AllowedEmbeddingProviderReason,
		allowedWorkerReason:   AllowedEmbeddingWorkerReason,
		allowedOutcomeStatus:  AllowedEmbeddingOutcomeStatus,
	})
	if err != nil || shared == nil {
		// shared == nil means the meter is disabled; return a nil interface, not a typed nil.
		return nil, err
	}

	return embeddingMetrics{shared}, nil
}
