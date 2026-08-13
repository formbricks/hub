package observability

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestNewEmbeddingMetricsNilMeterDisabled(t *testing.T) {
	metrics, err := NewEmbeddingMetrics(nil)
	require.NoError(t, err)
	assert.Nil(t, metrics, "a nil meter disables metrics")
}

func TestEmbeddingBatchMetrics(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	metrics, err := NewEmbeddingMetrics(provider.Meter("test"))
	require.NoError(t, err)
	require.NotNil(t, metrics)

	ctx := context.Background()
	metrics.RecordEmbeddingBatch(ctx, 8, 100*time.Millisecond, "success")
	metrics.RecordEmbeddingBatch(ctx, 2, 50*time.Millisecond, "unexpected-status")
	metrics.AddEmbeddingBatchInFlight(ctx, 1)

	var collected metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(ctx, &collected))

	wantNames := map[string]bool{
		MetricNameEmbeddingBatchSize:     false,
		MetricNameEmbeddingBatchInputs:   false,
		MetricNameEmbeddingBatchRequests: false,
		MetricNameEmbeddingBatchDuration: false,
		MetricNameEmbeddingBatchInFlight: false,
	}
	statuses := map[string]bool{}

	for _, scope := range collected.ScopeMetrics {
		for _, m := range scope.Metrics {
			if _, wanted := wantNames[m.Name]; wanted {
				wantNames[m.Name] = true
			}

			if m.Name != MetricNameEmbeddingBatchRequests {
				continue
			}

			sum, ok := m.Data.(metricdata.Sum[int64])
			require.True(t, ok)

			for _, point := range sum.DataPoints {
				status, present := point.Attributes.Value(attribute.Key(AttrStatus))
				require.True(t, present)

				statuses[status.AsString()] = true
			}
		}
	}

	for name, found := range wantNames {
		assert.Truef(t, found, "batch metric %s was not collected", name)
	}

	assert.Equal(t, map[string]bool{"success": true, "other": true}, statuses,
		"batch outcome labels must remain bounded")
}

// TestRegisterHNSWIterativeScanGauge verifies the observable gauge reports the latch state polled
// from its callback: 0 while healthy, 1 once degraded. The callback is invoked on each collect, so
// flipping the source between collects must change the reported value.
func TestRegisterHNSWIterativeScanGauge(t *testing.T) {
	t.Run("nil meter and nil callback are no-ops", func(t *testing.T) {
		require.NoError(t, RegisterHNSWIterativeScanGauge(nil, func() bool { return true }))

		reader := sdkmetric.NewManualReader()
		provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
		require.NoError(t, RegisterHNSWIterativeScanGauge(provider.Meter("test"), nil))
	})

	t.Run("reports 0 when healthy and 1 when degraded", func(t *testing.T) {
		reader := sdkmetric.NewManualReader()
		provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

		degraded := false

		require.NoError(t, RegisterHNSWIterativeScanGauge(provider.Meter("test"), func() bool { return degraded }))

		assert.Equal(t, int64(0), hnswGaugeValue(t, reader), "healthy latch reports 0")

		degraded = true

		assert.Equal(t, int64(1), hnswGaugeValue(t, reader), "latched-off scan reports 1")
	})
}

// hnswGaugeValue collects metrics and returns the single data point of the iterative-scan gauge.
func hnswGaugeValue(t *testing.T, reader sdkmetric.Reader) int64 {
	t.Helper()

	var collected metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &collected))

	for _, scope := range collected.ScopeMetrics {
		for _, metric := range scope.Metrics {
			if metric.Name != MetricNameHNSWIterativeScanDegraded {
				continue
			}

			gauge, ok := metric.Data.(metricdata.Gauge[int64])
			require.True(t, ok, "expected Gauge[int64] for %s", MetricNameHNSWIterativeScanDegraded)
			require.Len(t, gauge.DataPoints, 1)

			return gauge.DataPoints[0].Value
		}
	}

	t.Fatalf("gauge %q not found in collected metrics", MetricNameHNSWIterativeScanDegraded)

	return 0
}
