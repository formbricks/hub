package observability

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/metric"
)

// RegisterHNSWIterativeScanGauge registers an observable gauge that reports 1 when HNSW
// iterative_scan has been latched off (the pgvector < 0.8 fallback — nearest-neighbor recall is
// capped at ef_search until restart) and 0 otherwise. degraded is polled on each metric export.
// No-op when meter or degraded is nil (metrics disabled). Register once, at startup.
func RegisterHNSWIterativeScanGauge(meter metric.Meter, degraded func() bool) error {
	if meter == nil || degraded == nil {
		return nil
	}

	_, err := meter.Int64ObservableGauge(
		MetricNameHNSWIterativeScanDegraded,
		metric.WithDescription("1 when HNSW iterative_scan is latched off (pgvector < 0.8 fallback; recall capped at ef_search), else 0"),
		metric.WithInt64Callback(func(_ context.Context, observer metric.Int64Observer) error {
			var value int64
			if degraded() {
				value = 1
			}

			observer.Observe(value)

			return nil
		}),
	)
	if err != nil {
		return fmt.Errorf("hnsw iterative scan gauge: %w", err)
	}

	return nil
}

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
