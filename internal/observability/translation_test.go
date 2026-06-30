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

func TestTranslationAllowedReasonsAndStatuses(t *testing.T) {
	for _, reason := range []string{"settings_read_failed", "enqueue_failed"} {
		assert.True(t, AllowedTranslationProviderReason(reason), reason)
		assert.Equal(t, reason, NormalizeReason(reason, AllowedTranslationProviderReason))
	}

	workerReasons := []string{
		"translation_api_failed", "get_record_failed", "update_failed", "tenant_write_conflict", "rate_limited",
	}
	for _, reason := range workerReasons {
		assert.True(t, AllowedTranslationWorkerReason(reason), reason)
		assert.Equal(t, reason, NormalizeReason(reason, AllowedTranslationWorkerReason))
	}

	for _, status := range []string{"success", "retry", "failed_final", "skipped"} {
		assert.True(t, AllowedTranslationOutcomeStatus(status), status)
	}

	// Unknown values normalize to a bounded "other" bucket (keeps metric cardinality fixed).
	assert.False(t, AllowedTranslationProviderReason("nope"))
	assert.Equal(t, "other", NormalizeReason("nope", AllowedTranslationProviderReason))
	assert.False(t, AllowedTranslationOutcomeStatus("nope"))
}

func TestNewTranslationMetricsNilMeterDisabled(t *testing.T) {
	metrics, err := NewTranslationMetrics(nil)
	require.NoError(t, err)
	assert.Nil(t, metrics, "a nil meter disables metrics")
}

func TestNewMetricsIncludesTranslation(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	metrics, err := NewMetrics(provider.Meter("test"))
	require.NoError(t, err)
	require.NotNil(t, metrics)
	assert.NotNil(t, metrics.Translation, "aggregate Metrics wires up the translation collector")

	disabled, err := NewMetrics(nil)
	require.NoError(t, err)
	assert.Nil(t, disabled, "a nil meter disables the aggregate")
}

func TestTranslationMetricsRecordEmitsDataPoints(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	metrics, err := NewTranslationMetrics(provider.Meter("test"))
	require.NoError(t, err)
	require.NotNil(t, metrics)

	ctx := context.Background()
	metrics.RecordJobsEnqueued(ctx, 2)
	metrics.RecordProviderError(ctx, "enqueue_failed")
	metrics.RecordProviderError(ctx, "bogus") // unknown reason -> normalized to "other"
	metrics.RecordTranslationOutcome(ctx, "success")
	metrics.RecordTranslationOutcome(ctx, "bogus") // unknown status -> normalized to "other"
	metrics.RecordWorkerError(ctx, "tenant_write_conflict")
	metrics.RecordTranslationDuration(ctx, 150*time.Millisecond, "success")

	var collected metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(ctx, &collected))

	assert.Equal(t, int64(2), counterValue(collected, MetricNameTranslationJobsEnqueued, "", ""))
	assert.Equal(t, int64(1), counterValue(collected, MetricNameTranslationProviderErrors, AttrReason, "enqueue_failed"))
	assert.Equal(t, int64(1), counterValue(collected, MetricNameTranslationProviderErrors, AttrReason, "other"))
	assert.Equal(t, int64(1), counterValue(collected, MetricNameTranslationOutcomes, AttrStatus, "success"))
	assert.Equal(t, int64(1), counterValue(collected, MetricNameTranslationOutcomes, AttrStatus, "other"))
	assert.Equal(t, int64(1), counterValue(collected, MetricNameTranslationWorkerErrors, AttrReason, "tenant_write_conflict"))
	assert.Equal(t, uint64(1), histogramCount(collected, MetricNameTranslationDuration, "success"))
}

// counterValue returns the value of an Int64 counter datapoint matching name and the
// optional attribute key/value. An empty attrKey matches a datapoint with no attributes.
func counterValue(data metricdata.ResourceMetrics, name, attrKey, attrVal string) int64 {
	for _, scope := range data.ScopeMetrics {
		for _, metricItem := range scope.Metrics {
			if metricItem.Name != name {
				continue
			}

			sum, ok := metricItem.Data.(metricdata.Sum[int64])
			if !ok {
				continue
			}

			for _, point := range sum.DataPoints {
				if attrKey == "" {
					return point.Value
				}

				if value, present := point.Attributes.Value(attribute.Key(attrKey)); present && value.AsString() == attrVal {
					return point.Value
				}
			}
		}
	}

	return 0
}

// histogramCount returns the observation count of a float64 histogram datapoint matching
// name and the given status attribute.
func histogramCount(data metricdata.ResourceMetrics, name, status string) uint64 {
	for _, scope := range data.ScopeMetrics {
		for _, metricItem := range scope.Metrics {
			if metricItem.Name != name {
				continue
			}

			hist, ok := metricItem.Data.(metricdata.Histogram[float64])
			if !ok {
				continue
			}

			for _, point := range hist.DataPoints {
				if value, present := point.Attributes.Value(attribute.Key(AttrStatus)); present && value.AsString() == status {
					return point.Count
				}
			}
		}
	}

	return 0
}
