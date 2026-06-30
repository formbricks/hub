package observability

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// SentimentMetrics records sentiment pipeline metrics (provider, worker).
// Methods accept ctx for future exemplar support. Mirrors TranslationMetrics.
type SentimentMetrics interface {
	RecordJobsEnqueued(ctx context.Context, count int64)
	RecordProviderError(ctx context.Context, reason string)
	RecordSentimentOutcome(ctx context.Context, status string)
	RecordWorkerError(ctx context.Context, reason string)
	RecordSentimentDuration(ctx context.Context, duration time.Duration, status string)
}

// sentimentMetrics implements SentimentMetrics.
type sentimentMetrics struct {
	jobsEnqueued   metric.Int64Counter
	providerErrors metric.Int64Counter
	outcomes       metric.Int64Counter
	workerErrors   metric.Int64Counter
	duration       metric.Float64Histogram
}

// NewSentimentMetrics creates SentimentMetrics. Returns (nil, nil) when meter is nil (metrics disabled).
func NewSentimentMetrics(meter metric.Meter) (SentimentMetrics, error) {
	if meter == nil {
		//nolint:nilnil // intentional: callers use "if metrics != nil" when metrics disabled
		return nil, nil
	}

	jobsEnqueued, err := meter.Int64Counter(
		MetricNameSentimentJobsEnqueued,
		metric.WithDescription("Total sentiment jobs enqueued"),
	)
	if err != nil {
		return nil, fmt.Errorf("create sentiment jobs enqueued counter: %w", err)
	}

	providerErrors, err := meter.Int64Counter(
		MetricNameSentimentProviderErrors,
		metric.WithDescription("Total sentiment provider errors (enqueue failures)"),
	)
	if err != nil {
		return nil, fmt.Errorf("create sentiment provider errors counter: %w", err)
	}

	outcomes, err := meter.Int64Counter(
		MetricNameSentimentOutcomes,
		metric.WithDescription("Total sentiment job outcomes by status"),
	)
	if err != nil {
		return nil, fmt.Errorf("create sentiment outcomes counter: %w", err)
	}

	workerErrors, err := meter.Int64Counter(
		MetricNameSentimentWorkerErrors,
		metric.WithDescription("Total sentiment worker errors (get record, provider, update)"),
	)
	if err != nil {
		return nil, fmt.Errorf("create sentiment worker errors counter: %w", err)
	}

	duration, err := meter.Float64Histogram(
		MetricNameSentimentDuration,
		metric.WithDescription("Sentiment job duration (seconds)"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, fmt.Errorf("create sentiment duration histogram: %w", err)
	}

	return &sentimentMetrics{
		jobsEnqueued:   jobsEnqueued,
		providerErrors: providerErrors,
		outcomes:       outcomes,
		workerErrors:   workerErrors,
		duration:       duration,
	}, nil
}

func (s *sentimentMetrics) RecordJobsEnqueued(ctx context.Context, count int64) {
	s.jobsEnqueued.Add(ctx, count)
}

func (s *sentimentMetrics) RecordProviderError(ctx context.Context, reason string) {
	reason = NormalizeReason(reason, AllowedSentimentProviderReason)
	s.providerErrors.Add(ctx, 1, metric.WithAttributes(attribute.String(AttrReason, reason)))
}

func (s *sentimentMetrics) RecordSentimentOutcome(ctx context.Context, status string) {
	status = normalizeSentimentStatus(status)
	s.outcomes.Add(ctx, 1, metric.WithAttributes(attribute.String(AttrStatus, status)))
}

func (s *sentimentMetrics) RecordWorkerError(ctx context.Context, reason string) {
	reason = NormalizeReason(reason, AllowedSentimentWorkerReason)
	s.workerErrors.Add(ctx, 1, metric.WithAttributes(attribute.String(AttrReason, reason)))
}

func (s *sentimentMetrics) RecordSentimentDuration(ctx context.Context, duration time.Duration, status string) {
	status = normalizeSentimentStatus(status)
	s.duration.Record(ctx, duration.Seconds(), metric.WithAttributes(attribute.String(AttrStatus, status)))
}

func normalizeSentimentStatus(status string) string {
	if AllowedSentimentOutcomeStatus(status) {
		return status
	}

	return "other"
}
