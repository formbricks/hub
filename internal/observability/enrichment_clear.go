package observability

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// EnrichmentClearMetrics counts enrichment outputs nulled by the eager-clear when a record's
// value_text changes, labeled by output (sentiment, emotions, translation). It makes the clear
// rate queryable — an output that clears but never re-enriches is the backfill-recovery case —
// rather than only grep-able in logs.
type EnrichmentClearMetrics interface {
	RecordOutputCleared(ctx context.Context, output string)
}

// enrichmentClearMetrics implements EnrichmentClearMetrics.
type enrichmentClearMetrics struct {
	cleared metric.Int64Counter
}

// NewEnrichmentClearMetrics creates EnrichmentClearMetrics. Returns (nil, nil) when meter is nil (metrics disabled).
func NewEnrichmentClearMetrics(meter metric.Meter) (EnrichmentClearMetrics, error) {
	if meter == nil {
		//nolint:nilnil // intentional: callers use "if metrics != nil" when metrics disabled
		return nil, nil
	}

	cleared, err := meter.Int64Counter(
		MetricNameEnrichmentOutputsCleared,
		metric.WithDescription(
			"Enrichment outputs nulled by an edit's eager-clear, labeled by output "+
				"(sentiment, emotions, translation). A high clear rate with no matching re-enrichment "+
				"points at backfill-recovery cases.",
		),
		metric.WithUnit("1"),
	)
	if err != nil {
		return nil, fmt.Errorf("create enrichment cleared counter: %w", err)
	}

	return &enrichmentClearMetrics{cleared: cleared}, nil
}

func (m *enrichmentClearMetrics) RecordOutputCleared(ctx context.Context, output string) {
	m.cleared.Add(ctx, 1, metric.WithAttributes(attribute.String(AttrOutput, NormalizeClearedOutput(output))))
}
