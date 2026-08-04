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

func TestTaxonomyMetricsEmitBoundedAttributesWithoutIdentifiers(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	metrics, err := NewTaxonomyMetrics(provider.Meter("test"))
	require.NoError(t, err)
	require.NotNil(t, metrics)

	ctx := context.Background()
	metrics.RecordRunStarted(ctx, "field")
	metrics.RecordRunOutcome(ctx, "failed", "provider-secret-value", "tenant-secret-value")
	metrics.RecordRunDuration(ctx, 3*time.Second, "failed", "field")
	metrics.RecordDispatchError(ctx, "unbounded-error-text")
	metrics.RecordRunsReaped(ctx, 2)

	var collected metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(ctx, &collected))
	assert.Equal(t, int64(1), counterValue(collected, MetricNameTaxonomyRunsStarted, AttrScopeType, "field"))
	assert.Equal(t, int64(1), counterValue(collected, MetricNameTaxonomyRunOutcomes, AttrFailureCode, "other"))
	assert.Equal(t, int64(1), counterValue(collected, MetricNameTaxonomyDispatchError, AttrReason, "other"))
	assert.Equal(t, int64(2), counterValue(collected, MetricNameTaxonomyRunsReaped, "", ""))

	for _, scope := range collected.ScopeMetrics {
		for _, item := range scope.Metrics {
			if sum, ok := item.Data.(metricdata.Sum[int64]); ok {
				for _, point := range sum.DataPoints {
					for _, forbidden := range []string{"run_id", "request_id", "tenant_id", "source_id", "field_id"} {
						_, present := point.Attributes.Value(attribute.Key(forbidden))
						assert.False(t, present, "%s must never be a metric attribute", forbidden)
					}
				}
			}
		}
	}
}
