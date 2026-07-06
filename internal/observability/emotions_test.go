package observability

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestEmotionsAllowedReasonsAndStatuses(t *testing.T) {
	for _, reason := range []string{"settings_read_failed", "enqueue_failed"} {
		assert.True(t, AllowedEmotionsProviderReason(reason), reason)
		assert.Equal(t, reason, NormalizeReason(reason, AllowedEmotionsProviderReason))
	}

	workerReasons := []string{
		"emotions_api_failed", "get_record_failed", "settings_read_failed",
		"superseded", "update_failed", "tenant_write_conflict", "rate_limited",
	}
	for _, reason := range workerReasons {
		assert.True(t, AllowedEmotionsWorkerReason(reason), reason)
		assert.Equal(t, reason, NormalizeReason(reason, AllowedEmotionsWorkerReason))
	}

	for _, status := range []string{"success", "retry", "failed_final", "skipped"} {
		assert.True(t, AllowedEmotionsOutcomeStatus(status), status)
	}

	// Unknown values normalize to a bounded "other" bucket (keeps metric cardinality fixed).
	assert.False(t, AllowedEmotionsProviderReason("nope"))
	assert.Equal(t, "other", NormalizeReason("nope", AllowedEmotionsProviderReason))
	assert.False(t, AllowedEmotionsOutcomeStatus("nope"))
}

func TestNewEmotionsMetricsNilMeterDisabled(t *testing.T) {
	metrics, err := NewEmotionsMetrics(nil)
	require.NoError(t, err)
	assert.Nil(t, metrics, "a nil meter disables metrics")
}

func TestNewMetricsIncludesEmotions(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	metrics, err := NewMetrics(provider.Meter("test"))
	require.NoError(t, err)
	require.NotNil(t, metrics)
	assert.NotNil(t, metrics.Emotions, "aggregate Metrics wires up the emotion collector")
}

func TestEmotionsMetricsRecordEmitsDataPoints(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	metrics, err := NewEmotionsMetrics(provider.Meter("test"))
	require.NoError(t, err)
	require.NotNil(t, metrics)

	ctx := context.Background()
	metrics.RecordJobsEnqueued(ctx, 2)
	metrics.RecordProviderError(ctx, "enqueue_failed")
	metrics.RecordProviderError(ctx, "bogus") // unknown reason -> normalized to "other"
	metrics.RecordEmotionsOutcome(ctx, "success")
	metrics.RecordEmotionsOutcome(ctx, "bogus") // unknown status -> normalized to "other"
	metrics.RecordWorkerError(ctx, "rate_limited")
	metrics.RecordEmotionsDuration(ctx, 150*time.Millisecond, "success")

	var collected metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(ctx, &collected))

	assert.Equal(t, int64(2), counterValue(collected, MetricNameEmotionsJobsEnqueued, "", ""))
	assert.Equal(t, int64(1), counterValue(collected, MetricNameEmotionsProviderErrors, AttrReason, "enqueue_failed"))
	assert.Equal(t, int64(1), counterValue(collected, MetricNameEmotionsProviderErrors, AttrReason, "other"))
	assert.Equal(t, int64(1), counterValue(collected, MetricNameEmotionsOutcomes, AttrStatus, "success"))
	assert.Equal(t, int64(1), counterValue(collected, MetricNameEmotionsOutcomes, AttrStatus, "other"))
	assert.Equal(t, int64(1), counterValue(collected, MetricNameEmotionsWorkerErrors, AttrReason, "rate_limited"))
	assert.Equal(t, uint64(1), histogramCount(collected, MetricNameEmotionsDuration, "success"))
}
