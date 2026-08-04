package observability

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// TaxonomyMetrics records bounded-cardinality taxonomy lifecycle metrics. Run, request,
// tenant, source, and field identifiers intentionally belong only in correlated logs.
type TaxonomyMetrics interface {
	RecordRunStarted(ctx context.Context, scopeType string)
	RecordRunOutcome(ctx context.Context, status, failureCode, scopeType string)
	RecordRunDuration(ctx context.Context, duration time.Duration, status, scopeType string)
	RecordDispatchError(ctx context.Context, reason string)
	RecordRunsReaped(ctx context.Context, count int64)
}

type taxonomyMetrics struct {
	started       metric.Int64Counter
	outcomes      metric.Int64Counter
	duration      metric.Float64Histogram
	dispatchError metric.Int64Counter
	reaped        metric.Int64Counter
}

// NewTaxonomyMetrics creates taxonomy lifecycle metrics.
func NewTaxonomyMetrics(meter metric.Meter) (TaxonomyMetrics, error) {
	if meter == nil {
		return nil, nil //nolint:nilnil // disabled metrics are represented by a nil interface
	}

	started, err := meter.Int64Counter(MetricNameTaxonomyRunsStarted,
		metric.WithDescription("Taxonomy runs accepted and dispatched by Hub"))
	if err != nil {
		return nil, fmt.Errorf("create taxonomy started counter: %w", err)
	}

	outcomes, err := meter.Int64Counter(MetricNameTaxonomyRunOutcomes,
		metric.WithDescription("Terminal taxonomy run outcomes"))
	if err != nil {
		return nil, fmt.Errorf("create taxonomy outcomes counter: %w", err)
	}

	duration, err := meter.Float64Histogram(MetricNameTaxonomyRunDuration,
		metric.WithDescription("Taxonomy run wall-clock duration"), metric.WithUnit("s"))
	if err != nil {
		return nil, fmt.Errorf("create taxonomy duration histogram: %w", err)
	}

	dispatchError, err := meter.Int64Counter(MetricNameTaxonomyDispatchError,
		metric.WithDescription("Taxonomy dispatch failures"))
	if err != nil {
		return nil, fmt.Errorf("create taxonomy dispatch error counter: %w", err)
	}

	reaped, err := meter.Int64Counter(MetricNameTaxonomyRunsReaped,
		metric.WithDescription("Taxonomy runs failed by the stuck-run reaper"))
	if err != nil {
		return nil, fmt.Errorf("create taxonomy reaped counter: %w", err)
	}

	return &taxonomyMetrics{
		started:       started,
		outcomes:      outcomes,
		duration:      duration,
		dispatchError: dispatchError,
		reaped:        reaped,
	}, nil
}

func (m *taxonomyMetrics) RecordRunStarted(ctx context.Context, scopeType string) {
	m.started.Add(ctx, 1, metric.WithAttributes(attribute.String(AttrScopeType, boundedScopeType(scopeType))))
}

func (m *taxonomyMetrics) RecordRunOutcome(ctx context.Context, status, failureCode, scopeType string) {
	m.outcomes.Add(ctx, 1, metric.WithAttributes(
		attribute.String(AttrStatus, boundedTaxonomyStatus(status)),
		attribute.String(AttrFailureCode, boundedFailureCode(failureCode)),
		attribute.String(AttrScopeType, boundedScopeType(scopeType)),
	))
}

func (m *taxonomyMetrics) RecordRunDuration(
	ctx context.Context, duration time.Duration, status, scopeType string,
) {
	m.duration.Record(ctx, duration.Seconds(), metric.WithAttributes(
		attribute.String(AttrStatus, boundedTaxonomyStatus(status)),
		attribute.String(AttrScopeType, boundedScopeType(scopeType)),
	))
}

func (m *taxonomyMetrics) RecordDispatchError(ctx context.Context, reason string) {
	if reason != "request_failed" && reason != "mark_failed_failed" {
		reason = "other"
	}

	m.dispatchError.Add(ctx, 1, metric.WithAttributes(attribute.String(AttrReason, reason)))
}

func (m *taxonomyMetrics) RecordRunsReaped(ctx context.Context, count int64) {
	if count > 0 {
		m.reaped.Add(ctx, count)
	}
}

func boundedTaxonomyStatus(value string) string {
	switch value {
	case "succeeded", "failed", "canceled":
		return value
	default:
		return "other"
	}
}

func boundedFailureCode(value string) string {
	switch value {
	case "none", "insufficient_data", "service_unavailable", "generation_failed", "invalid_output", "internal_error":
		return value
	default:
		return "other"
	}
}

func boundedScopeType(value string) string {
	if value == "field" || value == "directory" {
		return value
	}

	return "other"
}
