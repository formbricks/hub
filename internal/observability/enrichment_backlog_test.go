package observability

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestNewEnrichmentBacklogMetricsNilMeterDisabled(t *testing.T) {
	metrics, err := NewEnrichmentBacklogMetrics(nil)
	require.NoError(t, err)
	assert.Nil(t, metrics, "a nil meter disables metrics")
}

// TestEnrichmentBacklogMetricsGauge verifies the async gauge reports the latest per-enrichment
// value under the enrichment label, and that a later Set overwrites (a gauge, not a counter).
func TestEnrichmentBacklogMetricsGauge(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	metrics, err := NewEnrichmentBacklogMetrics(provider.Meter("test"))
	require.NoError(t, err)
	require.NotNil(t, metrics)

	metrics.SetEnrichmentPending("translation", 12)
	metrics.SetEnrichmentPending("sentiment", 5)
	metrics.SetEnrichmentPending("emotions", 0)

	assert.Equal(t, int64(12), backlogGaugeValue(t, reader, "translation"))
	assert.Equal(t, int64(5), backlogGaugeValue(t, reader, "sentiment"))
	assert.Equal(t, int64(0), backlogGaugeValue(t, reader, "emotions"))

	// A gauge reports the latest value, not a running sum.
	metrics.SetEnrichmentPending("translation", 3)
	assert.Equal(t, int64(3), backlogGaugeValue(t, reader, "translation"))
}

// TestEnrichmentBacklogMetricsClearWithdrawsSeries verifies a demoted leader stops exporting
// entirely rather than freezing at its last reading. The async gauge re-observes stored values on
// every collection, so without this a former leader's stale series would coexist with the new
// leader's live one — a sum would double-count and the frozen copy would look like a stuck backlog.
func TestEnrichmentBacklogMetricsClearWithdrawsSeries(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	metrics, err := NewEnrichmentBacklogMetrics(provider.Meter("test"))
	require.NoError(t, err)
	require.NotNil(t, metrics)

	metrics.SetEnrichmentPending(EnrichmentTypeTranslation, 42)
	assert.Equal(t, int64(42), backlogGaugeValue(t, reader, EnrichmentTypeTranslation))

	metrics.ClearEnrichmentPending()

	var collected metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &collected))

	for _, scope := range collected.ScopeMetrics {
		for _, m := range scope.Metrics {
			assert.NotEqual(t, MetricNameEnrichmentPendingRecords, m.Name,
				"a cleared gauge must export no data points at all, not a stale value")
		}
	}
}

// TestEnrichmentBacklogMetricsPollErrors verifies failed refreshes are counted, so a gauge frozen
// at its last value (which looks like a healthy steady backlog) is still alertable.
func TestEnrichmentBacklogMetricsPollErrors(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	metrics, err := NewEnrichmentBacklogMetrics(provider.Meter("test"))
	require.NoError(t, err)
	require.NotNil(t, metrics)

	ctx := context.Background()
	metrics.RecordPollError(ctx)
	metrics.RecordPollError(ctx)

	var collected metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(ctx, &collected))

	var total int64

	for _, scope := range collected.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != MetricNameEnrichmentBacklogPollErrs {
				continue
			}

			sum, ok := m.Data.(metricdata.Sum[int64])
			require.True(t, ok, "expected Sum[int64] for %s", MetricNameEnrichmentBacklogPollErrs)

			for _, point := range sum.DataPoints {
				total += point.Value
			}
		}
	}

	assert.Equal(t, int64(2), total, "each failed poll increments the error counter")
}

// backlogGaugeValue collects metrics and returns the pending-records gauge value for one enrichment.
func backlogGaugeValue(t *testing.T, reader sdkmetric.Reader, enrichment string) int64 {
	t.Helper()

	var collected metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &collected))

	for _, scope := range collected.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != MetricNameEnrichmentPendingRecords {
				continue
			}

			gauge, ok := m.Data.(metricdata.Gauge[int64])
			require.True(t, ok, "expected Gauge[int64] for %s", MetricNameEnrichmentPendingRecords)

			for _, point := range gauge.DataPoints {
				if value, present := point.Attributes.Value(attribute.Key(AttrEnrichment)); present &&
					value.AsString() == enrichment {
					return point.Value
				}
			}
		}
	}

	t.Fatalf("gauge %q has no data point for enrichment %q", MetricNameEnrichmentPendingRecords, enrichment)

	return 0
}
