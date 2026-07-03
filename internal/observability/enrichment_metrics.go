package observability

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// enrichmentMetrics is the shared implementation behind the per-pipeline metrics
// (EmbeddingMetrics, TranslationMetrics, SentimentMetrics): the three pipelines emit the same
// five instruments — jobs enqueued, provider errors, job outcomes, worker errors, duration —
// differing only in metric names and the bounded reason/status label sets. Each pipeline embeds
// this and adds its two differently-named outcome/duration methods (see translation.go etc.).
type enrichmentMetrics struct {
	jobsEnqueued   metric.Int64Counter
	providerErrors metric.Int64Counter
	outcomes       metric.Int64Counter
	workerErrors   metric.Int64Counter
	duration       metric.Float64Histogram

	allowedProviderReason func(string) bool
	allowedWorkerReason   func(string) bool
	allowedOutcomeStatus  func(string) bool
}

// enrichmentMetricsSpec parameterizes one pipeline: instrument names/descriptions and the
// allow-list predicates that bound reason/status cardinality.
type enrichmentMetricsSpec struct {
	jobsEnqueuedName   string
	providerErrorsName string
	outcomesName       string
	workerErrorsName   string
	durationName       string

	providerErrorsDesc string
	workerErrorsDesc   string

	allowedProviderReason func(string) bool
	allowedWorkerReason   func(string) bool
	allowedOutcomeStatus  func(string) bool
}

// newEnrichmentMetrics builds the five collectors from spec. Returns (nil, nil) when meter is nil
// (metrics disabled); per-pipeline constructors propagate that as a nil interface.
func newEnrichmentMetrics(meter metric.Meter, spec enrichmentMetricsSpec) (*enrichmentMetrics, error) {
	if meter == nil {
		//nolint:nilnil // intentional: callers translate a nil impl into a nil interface
		return nil, nil
	}

	jobsEnqueued, err := meter.Int64Counter(spec.jobsEnqueuedName, metric.WithDescription("Total jobs enqueued"))
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", spec.jobsEnqueuedName, err)
	}

	providerErrors, err := meter.Int64Counter(spec.providerErrorsName, metric.WithDescription(spec.providerErrorsDesc))
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", spec.providerErrorsName, err)
	}

	outcomes, err := meter.Int64Counter(spec.outcomesName, metric.WithDescription("Total job outcomes by status"))
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", spec.outcomesName, err)
	}

	workerErrors, err := meter.Int64Counter(spec.workerErrorsName, metric.WithDescription(spec.workerErrorsDesc))
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", spec.workerErrorsName, err)
	}

	duration, err := meter.Float64Histogram(
		spec.durationName, metric.WithDescription("Job duration (seconds)"), metric.WithUnit("s"))
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", spec.durationName, err)
	}

	return &enrichmentMetrics{
		jobsEnqueued:          jobsEnqueued,
		providerErrors:        providerErrors,
		outcomes:              outcomes,
		workerErrors:          workerErrors,
		duration:              duration,
		allowedProviderReason: spec.allowedProviderReason,
		allowedWorkerReason:   spec.allowedWorkerReason,
		allowedOutcomeStatus:  spec.allowedOutcomeStatus,
	}, nil
}

// RecordJobsEnqueued, RecordProviderError, and RecordWorkerError share the same name across the
// three pipeline interfaces, so they are promoted directly from the embedded impl.
func (m *enrichmentMetrics) RecordJobsEnqueued(ctx context.Context, count int64) {
	m.jobsEnqueued.Add(ctx, count)
}

func (m *enrichmentMetrics) RecordProviderError(ctx context.Context, reason string) {
	reason = NormalizeReason(reason, m.allowedProviderReason)
	m.providerErrors.Add(ctx, 1, metric.WithAttributes(attribute.String(AttrReason, reason)))
}

func (m *enrichmentMetrics) RecordWorkerError(ctx context.Context, reason string) {
	reason = NormalizeReason(reason, m.allowedWorkerReason)
	m.workerErrors.Add(ctx, 1, metric.WithAttributes(attribute.String(AttrReason, reason)))
}

// recordOutcome and recordDuration are unexported; each pipeline exposes them under its own
// method name (RecordTranslationOutcome, RecordEmbeddingDuration, …) so the public contracts
// are unchanged.
func (m *enrichmentMetrics) recordOutcome(ctx context.Context, status string) {
	m.outcomes.Add(ctx, 1, metric.WithAttributes(attribute.String(AttrStatus, m.normalizeStatus(status))))
}

func (m *enrichmentMetrics) recordDuration(ctx context.Context, duration time.Duration, status string) {
	m.duration.Record(ctx, duration.Seconds(), metric.WithAttributes(attribute.String(AttrStatus, m.normalizeStatus(status))))
}

// normalizeStatus keeps outcome/duration status labels within the pipeline's allowed set,
// bucketing anything else as "other" to bound cardinality.
func (m *enrichmentMetrics) normalizeStatus(status string) string {
	if m.allowedOutcomeStatus(status) {
		return status
	}

	return "other"
}
