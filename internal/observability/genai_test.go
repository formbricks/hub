package observability

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/formbricks/hub/internal/llm"
)

// collect runs the recorder against a real SDK meter and returns what was actually exported, so
// these assertions are about emitted metrics rather than about calls into an interface.
func collect(t *testing.T, record func(llm.UsageRecorder)) metricdata.ResourceMetrics {
	t.Helper()

	reader := metric.NewManualReader()
	provider := metric.NewMeterProvider(metric.WithReader(reader))

	recorder, err := NewGenAIMetrics(provider.Meter("test"))
	require.NoError(t, err)
	require.NotNil(t, recorder)

	record(recorder)

	var got metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &got))

	return got
}

func instrument(t *testing.T, got metricdata.ResourceMetrics, name string) metricdata.Aggregation {
	t.Helper()

	for _, scope := range got.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name == name {
				return m.Data
			}
		}
	}

	t.Fatalf("instrument %q was not exported", name)

	return nil
}

func TestGenAIMetricsExportTheConventionNames(t *testing.T) {
	got := collect(t, func(r llm.UsageRecorder) {
		r.RecordUsage(context.Background(), llm.Usage{
			Operation: llm.OperationChat, Provider: llm.ProviderOpenAI, Model: "gpt-4o-mini",
			InputTokens: 40, OutputTokens: 12, Duration: 250 * time.Millisecond,
		})
	})

	// The names are the contract: an OTel-native backend recognises gen_ai.* without any
	// per-deployment configuration, which is the reason for not using a hub_* name here.
	tokens, ok := instrument(t, got, MetricNameGenAITokenUsage).(metricdata.Histogram[int64])
	require.True(t, ok, "token usage must be an int64 histogram")
	assert.Len(t, tokens.DataPoints, 2, "input and output are separate series, not one total")

	_, ok = instrument(t, got, MetricNameGenAIOperationDuration).(metricdata.Histogram[float64])
	require.True(t, ok, "operation duration must be a float64 histogram")
}

func TestGenAIMetricsOmitZeroTokenCounts(t *testing.T) {
	// A failed call reports no usage block. Recording zero would drag the average down and make a
	// provider outage look cheap, so the token series must simply not be emitted.
	got := collect(t, func(r llm.UsageRecorder) {
		r.RecordUsage(context.Background(), llm.Usage{
			Operation: llm.OperationChat, Provider: llm.ProviderOpenAI, Model: "m",
			Duration: 3 * time.Second, ErrorType: "500",
		})
	})

	for _, scope := range got.ScopeMetrics {
		for _, m := range scope.Metrics {
			assert.NotEqual(t, MetricNameGenAITokenUsage, m.Name,
				"a call with no reported tokens must emit no token series")
		}
	}

	duration, ok := instrument(t, got, MetricNameGenAIOperationDuration).(metricdata.Histogram[float64])
	require.True(t, ok)
	require.Len(t, duration.DataPoints, 1, "the failed call is still timed")
}

func TestGenAIMetricsCarryNoTenantLabel(t *testing.T) {
	// tenant_id is unbounded. The same rule already governs the enrichment backlog gauge: aggregate
	// cost belongs in metrics, per-tenant cost is answered from the counts the API already returns.
	got := collect(t, func(r llm.UsageRecorder) {
		r.RecordUsage(context.Background(), llm.Usage{
			Operation: llm.OperationEmbeddings, Provider: llm.ProviderOpenAI, Model: "m",
			InputTokens: 8, Duration: time.Millisecond,
		})
	})

	for _, scope := range got.ScopeMetrics {
		for _, m := range scope.Metrics {
			if hist, ok := m.Data.(metricdata.Histogram[int64]); ok {
				for _, dp := range hist.DataPoints {
					for _, attr := range dp.Attributes.ToSlice() {
						assert.NotContains(t, string(attr.Key), "tenant",
							"no tenant dimension may reach a metric label")
					}
				}
			}
		}
	}
}

// TestFailedRecordsGaugeWithdrawsOnClear pins the withdraw-not-stale rule. An async gauge
// re-observes its stored values on every collection, so a value left behind after a failed refresh
// or a lost leadership keeps being exported indefinitely — indistinguishable from a live reading,
// and invisible to any alert that looks for staleness by value.
func TestFailedRecordsGaugeWithdrawsOnClear(t *testing.T) {
	reader := metric.NewManualReader()
	provider := metric.NewMeterProvider(metric.WithReader(reader))

	failures, err := NewEnrichmentFailureMetrics(provider.Meter("test"))
	require.NoError(t, err)
	require.NotNil(t, failures)

	failures.SetFailedRecords(EnrichmentTypeSentiment, true, 7)

	var got metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &got))
	require.Positive(t, countDataPoints(got, MetricNameEnrichmentFailedRecords),
		"the gauge publishes what was set")

	failures.ClearFailedRecords()

	require.NoError(t, reader.Collect(context.Background(), &got))
	assert.Zero(t, countDataPoints(got, MetricNameEnrichmentFailedRecords),
		"after withdrawal the series must be ABSENT, not frozen at its last value")
}

func countDataPoints(got metricdata.ResourceMetrics, name string) int {
	for _, scope := range got.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != name {
				continue
			}

			if gauge, ok := m.Data.(metricdata.Gauge[int64]); ok {
				return len(gauge.DataPoints)
			}
		}
	}

	return 0
}
