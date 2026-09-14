package observability

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// The bounded value sets for AttrOutcome. Both are closed enums declared here rather than passed
// through from call sites, because an outcome label sourced from an error string is how a counter
// acquires unbounded cardinality.
const (
	// OutcomeSuccess is a sweep that completed.
	OutcomeSuccess = "success"
	// OutcomeError is a sweep that failed part-way through.
	OutcomeError = "error"
)

// The retry counter uses the domain's OWN outcome enum (models.RetryOutcome) rather than the two
// above, because "cleared" and "cooling_down" say something a generic success/error cannot: they
// are the two halves of the cost bound, and being able to see one turn into the other is the point
// of having the metric. Listed here rather than imported so observability keeps depending on
// nothing; a test in internal/service asserts the two sets agree.
const (
	RetryOutcomeCleared     = "cleared"
	RetryOutcomeCoolingDown = "cooling_down"
	RetryOutcomeDisabled    = "disabled"
	// RetryOutcomeError is not a domain outcome: it is where anything unrecognised lands, so a new
	// outcome added without updating this list under-reports instead of growing the label set.
	RetryOutcomeError = "error"
)

// EnrichmentReconcileMetrics reports the level-triggered sweep and the manual retry endpoint.
//
// These are the only signals for two paths that spend provider money without a user watching. The
// sweep runs unattended on the elected leader and the retry endpoint is called by another service,
// so neither surfaces in request dashboards; without these the only evidence either ran is a log
// line. Enqueued-and-duration together answer the question that actually matters after a deploy —
// is the backlog draining, and is the sweep keeping up with its own interval.
//
// Like EnrichmentFailureMetrics this carries the enrichment as a LABEL rather than in the metric
// name, so it is already in the shape the pending per-type consolidation is heading for.
type EnrichmentReconcileMetrics interface {
	// RecordSweep counts one sweep and observes how long it took. A duration creeping toward
	// ENRICHMENT_RECONCILE_INTERVAL_SECONDS means ticks are about to start overlapping, which the
	// job's uniqueness will then silently coalesce.
	RecordSweep(ctx context.Context, outcome string, duration time.Duration)
	// RecordEnqueued counts jobs the sweep actually inserted, by enrichment. Rate-of-change is the
	// drain rate; a flat non-zero line means work is being found but not finished.
	RecordEnqueued(ctx context.Context, enrichment string, count int)
	// RecordRetry counts one enrichment's outcome within a retry request, using the domain's
	// outcome values. This is the audit counterpart to the slog line: the log says who and how
	// many, this says how often, and a caller whose "cleared" count keeps climbing rather than
	// settling into "cooling_down" is the cost amplification the cooldown exists to bound.
	RecordRetry(ctx context.Context, enrichment, outcome string)
}

// AttrOutcome labels sweeps and retries with a bounded outcome enum (see OutcomeSuccess and
// friends). Distinct from AttrReason, which carries a provider's cause for giving up on a record.
const AttrOutcome = "outcome"

type enrichmentReconcileMetrics struct {
	sweeps   metric.Int64Counter
	duration metric.Float64Histogram
	enqueued metric.Int64Counter
	retries  metric.Int64Counter
}

// NewEnrichmentReconcileMetrics builds the reconcile metrics. Returns (nil, nil) when meter is nil
// (metrics disabled); callers propagate that as a nil interface and skip recording.
func NewEnrichmentReconcileMetrics(meter metric.Meter) (EnrichmentReconcileMetrics, error) {
	if meter == nil {
		return nil, nil //nolint:nilnil // intentional: callers translate nil into "metrics disabled"
	}

	sweeps, err := meter.Int64Counter(MetricNameEnrichmentReconcileSweeps,
		metric.WithDescription(
			"Reconcile sweeps run, by outcome. Convergence depends on these ticking: a run of "+
				"errors means enrichment coverage has stopped being guaranteed."))
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", MetricNameEnrichmentReconcileSweeps, err)
	}

	duration, err := meter.Float64Histogram(MetricNameEnrichmentReconcileDuration,
		metric.WithDescription(
			"How long one reconcile sweep took. Approaching the configured interval means ticks "+
				"are about to overlap and be coalesced by the job's uniqueness."),
		metric.WithUnit("s"))
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", MetricNameEnrichmentReconcileDuration, err)
	}

	enqueued, err := meter.Int64Counter(MetricNameEnrichmentReconcileEnqueued,
		metric.WithDescription(
			"Jobs the reconciler enqueued, by enrichment. Each one is a provider call the event "+
				"path did not already make."))
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", MetricNameEnrichmentReconcileEnqueued, err)
	}

	retries, err := meter.Int64Counter(MetricNameEnrichmentRetryRequests,
		metric.WithDescription(
			"Manual terminal-failure clears requested, by enrichment and outcome. A caller "+
				"repeatedly clearing the same terminal set is the amplification the cooldown bounds."))
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", MetricNameEnrichmentRetryRequests, err)
	}

	return &enrichmentReconcileMetrics{
		sweeps: sweeps, duration: duration, enqueued: enqueued, retries: retries,
	}, nil
}

func (m *enrichmentReconcileMetrics) RecordSweep(ctx context.Context, outcome string, duration time.Duration) {
	attrs := metric.WithAttributes(attribute.String(AttrOutcome, normalizeOutcome(outcome)))

	m.sweeps.Add(ctx, 1, attrs)
	m.duration.Record(ctx, duration.Seconds(), attrs)
}

func (m *enrichmentReconcileMetrics) RecordEnqueued(ctx context.Context, enrichment string, count int) {
	if count <= 0 {
		return
	}

	m.enqueued.Add(ctx, int64(count), metric.WithAttributes(
		attribute.String(AttrEnrichment, enrichment)))
}

func (m *enrichmentReconcileMetrics) RecordRetry(ctx context.Context, enrichment, outcome string) {
	m.retries.Add(ctx, 1, metric.WithAttributes(
		attribute.String(AttrEnrichment, enrichment),
		attribute.String(AttrOutcome, normalizeRetryOutcome(outcome))))
}

// normalizeOutcome collapses anything outside the declared enum to OutcomeError. The allowlist is
// the cardinality bound: a caller that grows a new outcome and forgets to declare it here gets a
// metric that under-reports rather than a label set that grows without limit.
func normalizeOutcome(outcome string) string {
	switch outcome {
	case OutcomeSuccess, OutcomeError:
		return outcome
	default:
		return OutcomeError
	}
}

// normalizeRetryOutcome is normalizeOutcome for the retry counter's separate enum.
func normalizeRetryOutcome(outcome string) string {
	switch outcome {
	case RetryOutcomeCleared, RetryOutcomeCoolingDown, RetryOutcomeDisabled, RetryOutcomeError:
		return outcome
	default:
		return RetryOutcomeError
	}
}
