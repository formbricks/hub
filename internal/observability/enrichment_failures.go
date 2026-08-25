package observability

import (
	"context"
	"fmt"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// EnrichmentFailureMetrics reports enrichment failures: a counter of permanent give-ups by cause,
// and a gauge of how many records are currently sitting failed.
//
// Deliberately a NEW metric family rather than more columns on the four per-type families
// (hub_sentiment_*, hub_emotions_*, …). Those carry the enrichment in the metric NAME, which is
// the thing a pending consolidation will change; this one carries it as a label from the start, so
// it is already in the shape that consolidation is heading for and will not need renaming twice.
type EnrichmentFailureMetrics interface {
	// RecordTerminalFailure counts one record given up on permanently. The reason is what makes
	// this worth having: "we are losing 5% of records to the content filter" is not answerable
	// from any existing signal, and it is a very different operational problem from a provider
	// being down.
	RecordTerminalFailure(ctx context.Context, enrichment, reason string)
	// SetFailedRecords reports how many records are currently failed and still un-enriched, split
	// by whether a retry could help. Refreshed by the same leader-elected poller as the backlog
	// gauge, so exactly one replica publishes it.
	SetFailedRecords(enrichment string, terminal bool, count int64)
	// ClearFailedRecords withdraws every series this process publishes, for the same reason
	// ClearEnrichmentPending exists: an async gauge re-observes stored values on every collection,
	// so a process that loses leadership would otherwise export a frozen copy alongside the new
	// leader's live one forever.
	ClearFailedRecords()
}

// AttrTerminal labels the failed-records gauge with whether the failure is permanent. Two values,
// so cardinality is bounded.
const AttrTerminal = "terminal"

type enrichmentFailureMetrics struct {
	terminal metric.Int64Counter

	mu     sync.Mutex
	failed map[failedKey]int64
}

type failedKey struct {
	enrichment string
	terminal   bool
}

// NewEnrichmentFailureMetrics builds the failure metrics. Returns (nil, nil) when meter is nil
// (metrics disabled); callers propagate that as a nil interface and the worker skips recording.
func NewEnrichmentFailureMetrics(meter metric.Meter) (EnrichmentFailureMetrics, error) {
	if meter == nil {
		return nil, nil //nolint:nilnil // intentional: callers translate nil into "metrics disabled"
	}

	terminal, err := meter.Int64Counter(MetricNameEnrichmentTerminalTotal,
		metric.WithDescription(
			"Records given up on permanently, by enrichment and cause. These never resolve on "+
				"their own: the outcome is a property of the record's own text."))
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", MetricNameEnrichmentTerminalTotal, err)
	}

	failureMetrics := &enrichmentFailureMetrics{terminal: terminal, failed: map[failedKey]int64{}}

	_, err = meter.Int64ObservableGauge(MetricNameEnrichmentFailedRecords,
		metric.WithDescription(
			"Records whose last enrichment attempt failed and which are still un-enriched, "+
				"split by whether a retry could help."),
		metric.WithInt64Callback(func(_ context.Context, observer metric.Int64Observer) error {
			failureMetrics.mu.Lock()
			defer failureMetrics.mu.Unlock()

			for key, count := range failureMetrics.failed {
				observer.Observe(count, metric.WithAttributes(
					attribute.String(AttrEnrichment, key.enrichment),
					attribute.Bool(AttrTerminal, key.terminal)))
			}

			return nil
		}))
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", MetricNameEnrichmentFailedRecords, err)
	}

	return failureMetrics, nil
}

func (e *enrichmentFailureMetrics) RecordTerminalFailure(ctx context.Context, enrichment, reason string) {
	e.terminal.Add(ctx, 1, metric.WithAttributes(
		attribute.String(AttrEnrichment, enrichment),
		attribute.String(AttrReason, reason)))
}

func (e *enrichmentFailureMetrics) SetFailedRecords(enrichment string, terminal bool, count int64) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.failed[failedKey{enrichment: enrichment, terminal: terminal}] = count
}

func (e *enrichmentFailureMetrics) ClearFailedRecords() {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.failed = map[failedKey]int64{}
}
