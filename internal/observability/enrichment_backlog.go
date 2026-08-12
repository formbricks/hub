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
	EnrichmentTypeTranslation       = "translation"
	EnrichmentTypeSentiment         = "sentiment"
	EnrichmentTypeEmotions          = "emotions"
	EnrichmentTypeTaxonomyEmbedding = "taxonomy_embedding"
)

// EnrichmentBacklogMetrics reports the aggregate (cross-tenant) count of eligible-but-unenriched
// feedback records per enrichment type — a data-derived "how far behind is enrichment" gauge that
// complements the transient River queue-depth gauge. A background poller refreshes the values; an
// async gauge observes them. Labeled by enrichment type only (a fixed, bounded set); tenant_id is
// deliberately NOT a label, so per-tenant detail stays in the API and metric cardinality is bounded.
type EnrichmentBacklogMetrics interface {
	SetEnrichmentPending(enrichment string, count int64)
	// ClearEnrichmentPending withdraws every series this process exports for the gauge. The async
	// callback re-observes the stored values on EVERY collection, so a process that stops being the
	// leader would otherwise keep exporting its final reading forever: the new leader's live series
	// and the old leader's frozen one would coexist, a sum would double-count, and the frozen copy
	// would look like a permanently stuck backlog. Callers must clear as soon as they are no longer
	// the leader. Exporting nothing is the honest state — absence is visible, a stale value is not.
	ClearEnrichmentPending()
	// RecordPollError counts a failed refresh. A failed poll costs this process its leadership and
	// withdraws the series (see ClearEnrichmentPending), so the symptom is a MISSING gauge rather
	// than a stale value: alert on this counter, or on the absence of the gauge — a value-based
	// staleness rule will not catch it.
	RecordPollError(ctx context.Context)
}

// enrichmentBacklogMetrics implements EnrichmentBacklogMetrics. The latest per-enrichment value is
// stored under mu and read by the gauge callback.
type enrichmentBacklogMetrics struct {
	mu         sync.Mutex
	pending    map[string]int64
	gauge      metric.Int64ObservableGauge
	pollErrors metric.Int64Counter
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
				"emotions, taxonomy_embedding), aggregated across all tenants. A data-derived "+
				"backlog/completeness signal; "+
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

	pollErrors, err := meter.Int64Counter(
		MetricNameEnrichmentBacklogPollErrs,
		metric.WithDescription(
			"Failed refreshes of the enrichment backlog gauge. A failed poll costs the process its "+
				"leadership and withdraws the series, so the symptom is a MISSING "+
				MetricNameEnrichmentPendingRecords+" rather than a stale value — alert on this "+
				"counter, or on absence of that gauge.",
		),
		metric.WithUnit("1"),
	)
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", MetricNameEnrichmentBacklogPollErrs, err)
	}

	m.pollErrors = pollErrors

	return m, nil
}

// ClearEnrichmentPending drops every stored value so the next collection observes nothing and this
// process stops exporting the gauge entirely.
func (m *enrichmentBacklogMetrics) ClearEnrichmentPending() {
	m.mu.Lock()
	defer m.mu.Unlock()

	clear(m.pending)
}

// RecordPollError counts one failed backlog refresh.
func (m *enrichmentBacklogMetrics) RecordPollError(ctx context.Context) {
	m.pollErrors.Add(ctx, 1)
}

// SetEnrichmentPending stores the latest backlog count for an enrichment type; the registered gauge
// callback reports it on the next collection.
func (m *enrichmentBacklogMetrics) SetEnrichmentPending(enrichment string, count int64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.pending[enrichment] = count
}
