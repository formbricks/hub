package observability

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/metric"
)

// SentimentMetrics records sentiment pipeline metrics (provider, worker).
// Backed by the shared enrichmentMetrics implementation.
type SentimentMetrics interface {
	RecordJobsEnqueued(ctx context.Context, count int64)
	RecordProviderError(ctx context.Context, reason string)
	RecordSentimentOutcome(ctx context.Context, status string)
	RecordWorkerError(ctx context.Context, reason string)
	RecordSentimentDuration(ctx context.Context, duration time.Duration, status string)
}

// sentimentMetrics adapts the shared impl to the SentimentMetrics outcome/duration names.
type sentimentMetrics struct{ *enrichmentMetrics }

func (m sentimentMetrics) RecordSentimentOutcome(ctx context.Context, status string) {
	m.recordOutcome(ctx, status)
}

func (m sentimentMetrics) RecordSentimentDuration(ctx context.Context, duration time.Duration, status string) {
	m.recordDuration(ctx, duration, status)
}

// NewSentimentMetrics creates SentimentMetrics. Returns (nil, nil) when meter is nil (metrics disabled).
func NewSentimentMetrics(meter metric.Meter) (SentimentMetrics, error) {
	shared, err := newEnrichmentMetrics(meter, enrichmentMetricsSpec{
		jobsEnqueuedName:      MetricNameSentimentJobsEnqueued,
		providerErrorsName:    MetricNameSentimentProviderErrors,
		outcomesName:          MetricNameSentimentOutcomes,
		workerErrorsName:      MetricNameSentimentWorkerErrors,
		durationName:          MetricNameSentimentDuration,
		providerErrorsDesc:    "Total sentiment provider errors (settings/enqueue failures)",
		workerErrorsDesc:      "Total sentiment worker errors (get record, provider, update)",
		allowedProviderReason: AllowedSentimentProviderReason,
		allowedWorkerReason:   AllowedSentimentWorkerReason,
		allowedOutcomeStatus:  AllowedSentimentOutcomeStatus,
	})
	if err != nil || shared == nil {
		// shared == nil means the meter is disabled; return a nil interface, not a typed nil.
		return nil, err
	}

	return sentimentMetrics{shared}, nil
}
