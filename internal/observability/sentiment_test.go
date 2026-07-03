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

func TestSentimentAllowedReasonsAndStatuses(t *testing.T) {
	for _, reason := range []string{"settings_read_failed", "enqueue_failed"} {
		assert.True(t, AllowedSentimentProviderReason(reason), reason)
		assert.Equal(t, reason, NormalizeReason(reason, AllowedSentimentProviderReason))
	}

	workerReasons := []string{
		"sentiment_api_failed", "get_record_failed", "settings_read_failed",
		"update_failed", "tenant_write_conflict", "rate_limited",
	}
	for _, reason := range workerReasons {
		assert.True(t, AllowedSentimentWorkerReason(reason), reason)
		assert.Equal(t, reason, NormalizeReason(reason, AllowedSentimentWorkerReason))
	}

	for _, status := range []string{"success", "retry", "failed_final", "skipped"} {
		assert.True(t, AllowedSentimentOutcomeStatus(status), status)
	}

	// Unknown values normalize to a bounded "other" bucket (keeps metric cardinality fixed).
	assert.False(t, AllowedSentimentProviderReason("nope"))
	assert.Equal(t, "other", NormalizeReason("nope", AllowedSentimentProviderReason))
	assert.False(t, AllowedSentimentOutcomeStatus("nope"))
}

func TestNewSentimentMetricsNilMeterDisabled(t *testing.T) {
	metrics, err := NewSentimentMetrics(nil)
	require.NoError(t, err)
	assert.Nil(t, metrics, "a nil meter disables metrics")
}

func TestNewMetricsIncludesSentiment(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	metrics, err := NewMetrics(provider.Meter("test"))
	require.NoError(t, err)
	require.NotNil(t, metrics)
	assert.NotNil(t, metrics.Sentiment, "aggregate Metrics wires up the sentiment collector")
}

func TestSentimentMetricsRecordEmitsDataPoints(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	metrics, err := NewSentimentMetrics(provider.Meter("test"))
	require.NoError(t, err)
	require.NotNil(t, metrics)

	ctx := context.Background()
	metrics.RecordJobsEnqueued(ctx, 2)
	metrics.RecordProviderError(ctx, "enqueue_failed")
	metrics.RecordProviderError(ctx, "bogus") // unknown reason -> normalized to "other"
	metrics.RecordSentimentOutcome(ctx, "success")
	metrics.RecordSentimentOutcome(ctx, "bogus") // unknown status -> normalized to "other"
	metrics.RecordWorkerError(ctx, "rate_limited")
	metrics.RecordSentimentDuration(ctx, 150*time.Millisecond, "success")

	var collected metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(ctx, &collected))

	assert.Equal(t, int64(2), counterValue(collected, MetricNameSentimentJobsEnqueued, "", ""))
	assert.Equal(t, int64(1), counterValue(collected, MetricNameSentimentProviderErrors, AttrReason, "enqueue_failed"))
	assert.Equal(t, int64(1), counterValue(collected, MetricNameSentimentProviderErrors, AttrReason, "other"))
	assert.Equal(t, int64(1), counterValue(collected, MetricNameSentimentOutcomes, AttrStatus, "success"))
	assert.Equal(t, int64(1), counterValue(collected, MetricNameSentimentOutcomes, AttrStatus, "other"))
	assert.Equal(t, int64(1), counterValue(collected, MetricNameSentimentWorkerErrors, AttrReason, "rate_limited"))
	assert.Equal(t, uint64(1), histogramCount(collected, MetricNameSentimentDuration, "success"))
}
