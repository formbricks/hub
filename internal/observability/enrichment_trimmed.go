package observability

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// EnrichmentTrimmedMetrics counts calls whose input had to be cut to the configured cap, labeled by
// enrichment. Trimming is not a failure — the call still runs and still stores a result — but it is
// the one place where the pipeline silently works with less than the whole record, so the rate has
// to be visible rather than inferred from a config value. A rising rate means either the cap is too
// low for the deployment's records or its model, or records are arriving far larger than expected.
//
// Deliberately its own family rather than a reason on EnrichmentFailureMetrics: that family means
// "this record did not get enriched", and a trimmed record did.
type EnrichmentTrimmedMetrics interface {
	RecordTrimmed(ctx context.Context, enrichment string)
}

// enrichmentTrimmedMetrics implements EnrichmentTrimmedMetrics.
type enrichmentTrimmedMetrics struct {
	trimmed metric.Int64Counter
}

// NewEnrichmentTrimmedMetrics creates EnrichmentTrimmedMetrics. Returns (nil, nil) when meter is nil
// (metrics disabled).
func NewEnrichmentTrimmedMetrics(meter metric.Meter) (EnrichmentTrimmedMetrics, error) {
	if meter == nil {
		//nolint:nilnil // intentional: callers use "if metrics != nil" when metrics disabled
		return nil, nil
	}

	trimmed, err := meter.Int64Counter(
		MetricNameEnrichmentTextTrimmedTotal,
		metric.WithDescription(
			"Enrichment calls whose input was cut to the configured byte cap, labeled by "+
				"enrichment. The call still runs: a rising rate means the cap is too low for the "+
				"records and model in this deployment, or records outgrew it."),
		metric.WithUnit("1"),
	)
	if err != nil {
		return nil, fmt.Errorf("create enrichment text trimmed counter: %w", err)
	}

	return &enrichmentTrimmedMetrics{trimmed: trimmed}, nil
}

func (m *enrichmentTrimmedMetrics) RecordTrimmed(ctx context.Context, enrichment string) {
	m.trimmed.Add(ctx, 1, metric.WithAttributes(attribute.String(AttrEnrichment, enrichment)))
}
