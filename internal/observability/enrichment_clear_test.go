package observability

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestNewEnrichmentClearMetricsNilMeterDisabled(t *testing.T) {
	metrics, err := NewEnrichmentClearMetrics(nil)
	require.NoError(t, err)
	assert.Nil(t, metrics, "a nil meter disables metrics")
}

// TestEnrichmentClearMetricsRecords verifies the counter increments per cleared output and bounds
// the output label to the known set (unknown collapses to "other").
func TestEnrichmentClearMetricsRecords(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	metrics, err := NewEnrichmentClearMetrics(provider.Meter("test"))
	require.NoError(t, err)
	require.NotNil(t, metrics)

	ctx := context.Background()
	metrics.RecordOutputCleared(ctx, "sentiment")
	metrics.RecordOutputCleared(ctx, "sentiment")
	metrics.RecordOutputCleared(ctx, "emotions")
	metrics.RecordOutputCleared(ctx, "translation")
	metrics.RecordOutputCleared(ctx, "bogus") // unknown output -> normalized to "other"

	var collected metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(ctx, &collected))

	assert.Equal(t, int64(2), counterValue(collected, MetricNameEnrichmentOutputsCleared, AttrOutput, "sentiment"))
	assert.Equal(t, int64(1), counterValue(collected, MetricNameEnrichmentOutputsCleared, AttrOutput, "emotions"))
	assert.Equal(t, int64(1), counterValue(collected, MetricNameEnrichmentOutputsCleared, AttrOutput, "translation"))
	assert.Equal(t, int64(1), counterValue(collected, MetricNameEnrichmentOutputsCleared, AttrOutput, "other"))
}
