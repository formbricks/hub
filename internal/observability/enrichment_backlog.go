package observability

import (
	"context"
	"fmt"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Enrichment type label values for MetricNameEnrichmentPendingRecords — a fixed, bounded set that
// keeps the gauge's cardinality low. Callers pass these to SetEnrichmentPending.
const (
	EnrichmentTypeTranslation = "translation"
	EnrichmentTypeSentiment   = "sentiment"
	EnrichmentTypeEmotions    = "emotions"
)

// EnrichmentBacklogMetrics reports the aggregate (cross-tenant) count of eligible-but-unenriched
// feedback records per enrichment type — a data-derived "how far behind is enrichment" gauge that
// complements the transient River queue-depth gauge. A background poller refreshes the values; an
// async gauge observes them. Labeled by enrichment type only (a fixed, bounded set); tenant_id is
// deliberately NOT a label, so per-tenant detail stays in the API and metric cardinality is bounded.
type EnrichmentBacklogMetrics interface {
	SetEnrichmentPending(enrichment string, count int64)
}

// enrichmentBacklogMetrics implements EnrichmentBacklogMetrics. The latest per-enrichment value is
// stored under mu and read by the gauge callback.
type enrichmentBacklogMetrics struct {
	mu      sync.Mutex
	pending map[string]int64
	gauge   metric.Int64ObservableGauge
}

// NewEnrichmentBacklogMetrics registers the pending-records gauge. Returns (nil, nil) when meter is
// nil (metrics disabled); the caller translates that into a nil interface.
func NewEnrichmentBacklogMetrics(meter metric.Meter) (EnrichmentBacklogMetrics, error) {
	if meter == nil {
		//nolint:nilnil // intentional: callers use "if metrics != nil" when metrics disabled
		return nil, nil
	}

	m := &enrichmentBacklogMetrics{pending: make(map[string]int64)}

	gauge, err := meter.Int64ObservableGauge(
		MetricNameEnrichmentPendingRecords,
		metric.WithDescription(
			"Eligible-but-unenriched feedback records per enrichment type (translation, sentiment, "+
				"emotions), aggregated across all tenants. A data-derived backlog/completeness signal; "+
				"unlike the River queue depth it persists across queue drains.",
		),
		metric.WithUnit("1"),
		metric.WithInt64Callback(func(_ context.Context, observer metric.Int64Observer) error {
			m.mu.Lock()
			defer m.mu.Unlock()

			for enrichment, count := range m.pending {
				observer.Observe(count, metric.WithAttributes(attribute.String(AttrEnrichment, enrichment)))
			}

			return nil
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", MetricNameEnrichmentPendingRecords, err)
	}

	m.gauge = gauge

	return m, nil
}

// SetEnrichmentPending stores the latest backlog count for an enrichment type; the registered gauge
// callback reports it on the next collection.
func (m *enrichmentBacklogMetrics) SetEnrichmentPending(enrichment string, count int64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.pending[enrichment] = count
}
