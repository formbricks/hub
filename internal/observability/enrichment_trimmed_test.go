package observability

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestEnrichmentTrimmedMetricsCountByEnrichment(t *testing.T) {
	reader := metric.NewManualReader()
	provider := metric.NewMeterProvider(metric.WithReader(reader))

	metrics, err := NewEnrichmentTrimmedMetrics(provider.Meter("test"))
	require.NoError(t, err)
	require.NotNil(t, metrics)

	ctx := context.Background()
	metrics.RecordTrimmed(ctx, "sentiment")
	metrics.RecordTrimmed(ctx, "sentiment")
	metrics.RecordTrimmed(ctx, "embedding")

	var collected metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(ctx, &collected))

	// The enrichment is a label, not part of the name, so one series per pipeline rather than one
	// instrument per pipeline: an operator asking "is anything being cut here" reads one metric.
	assert.Equal(t, int64(2), counterValue(collected, MetricNameEnrichmentTextTrimmedTotal, AttrEnrichment, "sentiment"))
	assert.Equal(t, int64(1), counterValue(collected, MetricNameEnrichmentTextTrimmedTotal, AttrEnrichment, "embedding"))
	assert.Equal(t, int64(0), counterValue(collected, MetricNameEnrichmentTextTrimmedTotal, AttrEnrichment, "translation"),
		"translation skips rather than trims, so it must not appear here")
}

func TestEnrichmentTrimmedMetricsDisabledWithoutMeter(t *testing.T) {
	metrics, err := NewEnrichmentTrimmedMetrics(nil)
	require.NoError(t, err)
	assert.Nil(t, metrics, "a nil meter means metrics are off, not a typed-nil that panics on use")
}
