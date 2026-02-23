package observability

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// EmbeddingMetrics records embedding pipeline metrics (provider, worker).
// Methods accept ctx for future exemplar support.
type EmbeddingMetrics interface {
	RecordJobsEnqueued(ctx context.Context, count int64)
	RecordProviderError(ctx context.Context, reason string)
	RecordEmbeddingOutcome(ctx context.Context, status string)
	RecordWorkerError(ctx context.Context, reason string)
	RecordEmbeddingDuration(ctx context.Context, duration time.Duration, status string)
}

// embeddingMetrics implements EmbeddingMetrics.
type embeddingMetrics struct {
	jobsEnqueued   metric.Int64Counter
	providerErrors metric.Int64Counter
	outcomes       metric.Int64Counter
	workerErrors   metric.Int64Counter
	duration       metric.Float64Histogram
}

// NewEmbeddingMetrics creates EmbeddingMetrics. Returns (nil, nil) when meter is nil (metrics disabled).
func NewEmbeddingMetrics(meter metric.Meter) (EmbeddingMetrics, error) {
	if meter == nil {
		//nolint:nilnil // intentional: callers use "if metrics != nil" when metrics disabled
		return nil, nil
	}

	jobsEnqueued, err := meter.Int64Counter(
		MetricNameEmbeddingJobsEnqueued,
		metric.WithDescription("Total embedding jobs enqueued"),
	)
	if err != nil {
		return nil, fmt.Errorf("create embedding jobs enqueued counter: %w", err)
	}

	providerErrors, err := meter.Int64Counter(
		MetricNameEmbeddingProviderErrors,
		metric.WithDescription("Total embedding provider errors (enqueue failures)"),
	)
	if err != nil {
		return nil, fmt.Errorf("create embedding provider errors counter: %w", err)
	}

	outcomes, err := meter.Int64Counter(
		MetricNameEmbeddingOutcomes,
		metric.WithDescription("Total embedding job outcomes by status"),
	)
	if err != nil {
		return nil, fmt.Errorf("create embedding outcomes counter: %w", err)
	}

	workerErrors, err := meter.Int64Counter(
		MetricNameEmbeddingWorkerErrors,
		metric.WithDescription("Total embedding worker errors (get record, openai, update)"),
	)
	if err != nil {
		return nil, fmt.Errorf("create embedding worker errors counter: %w", err)
	}

	duration, err := meter.Float64Histogram(
		MetricNameEmbeddingDuration,
		metric.WithDescription("Embedding job duration (seconds)"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, fmt.Errorf("create embedding duration histogram: %w", err)
	}

	return &embeddingMetrics{
		jobsEnqueued:   jobsEnqueued,
		providerErrors: providerErrors,
		outcomes:       outcomes,
		workerErrors:   workerErrors,
		duration:       duration,
	}, nil
}

func (e *embeddingMetrics) RecordJobsEnqueued(ctx context.Context, count int64) {
	e.jobsEnqueued.Add(ctx, count)
}

func (e *embeddingMetrics) RecordProviderError(ctx context.Context, reason string) {
	reason = NormalizeReason(reason, AllowedEmbeddingProviderReason)
	e.providerErrors.Add(ctx, 1, metric.WithAttributes(attribute.String(AttrReason, reason)))
}

func (e *embeddingMetrics) RecordEmbeddingOutcome(ctx context.Context, status string) {
	status = normalizeEmbeddingStatus(status)
	e.outcomes.Add(ctx, 1, metric.WithAttributes(attribute.String(AttrStatus, status)))
}

func (e *embeddingMetrics) RecordWorkerError(ctx context.Context, reason string) {
	reason = NormalizeReason(reason, AllowedEmbeddingWorkerReason)
	e.workerErrors.Add(ctx, 1, metric.WithAttributes(attribute.String(AttrReason, reason)))
}

func (e *embeddingMetrics) RecordEmbeddingDuration(ctx context.Context, duration time.Duration, status string) {
	status = normalizeEmbeddingStatus(status)
	e.duration.Record(ctx, duration.Seconds(), metric.WithAttributes(attribute.String(AttrStatus, status)))
}

func normalizeEmbeddingStatus(status string) string {
	if AllowedEmbeddingOutcomeStatus(status) {
		return status
	}

	return "other"
}
