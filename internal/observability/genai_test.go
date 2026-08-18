package observability

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
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

// attributeKeys returns every attribute key the named instrument actually exported.
//
// It handles each aggregation shape explicitly and FAILS on an unrecognised one rather than
// returning an empty set. That is the whole point: an earlier version of this check type-asserted
// a single shape, so the duration histogram — a Float64Histogram, and the only instrument carrying
// the model and the error class — was silently skipped by an assertion that appeared to cover
// everything.
func attributeKeys(t *testing.T, got metricdata.ResourceMetrics, name string) map[string]struct{} {
	t.Helper()

	keys := map[string]struct{}{}
	add := func(set attribute.Set) {
		for _, attr := range set.ToSlice() {
			keys[string(attr.Key)] = struct{}{}
		}
	}

	found := false

	for _, scope := range got.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != name {
				continue
			}

			found = true

			switch data := m.Data.(type) {
			case metricdata.Histogram[int64]:
				for _, dp := range data.DataPoints {
					add(dp.Attributes)
				}
			case metricdata.Histogram[float64]:
				for _, dp := range data.DataPoints {
					add(dp.Attributes)
				}
			case metricdata.Sum[int64]:
				for _, dp := range data.DataPoints {
					add(dp.Attributes)
				}
			case metricdata.Gauge[int64]:
				for _, dp := range data.DataPoints {
					add(dp.Attributes)
				}
			default:
				t.Fatalf("instrument %q exports an unhandled aggregation %T: extend this helper "+
					"rather than letting its attributes go unchecked", name, m.Data)
			}
		}
	}

	require.True(t, found, "instrument %q was not exported", name)

	return keys
}

// assertAttributeKeys pins an instrument's attribute keys to an ALLOWLIST.
//
// A denylist ("no key contains tenant") only forbids what someone thought of. The two things that
// must never reach a label here are unbounded tenant dimensions and GenAI content attributes —
// gen_ai.input.messages / gen_ai.output.messages, which carry the prompt and the completion, i.e.
// customer feedback text, and which off-the-shelf GenAI instrumentation enables by configuration.
// Adding either fails this test; adding a legitimate attribute means updating the list here, which
// is exactly the moment to think about what it costs.
func assertAttributeKeys(t *testing.T, got metricdata.ResourceMetrics, name string, allowed ...string) {
	t.Helper()

	permitted := map[string]struct{}{}
	for _, key := range allowed {
		permitted[key] = struct{}{}
	}

	for key := range attributeKeys(t, got, name) {
		_, ok := permitted[key]
		assert.True(t, ok, "%s: attribute %q is not on the allowlist for this instrument", name, key)
	}
}

func TestGenAIMetricsCarryOnlyAllowedAttributes(t *testing.T) {
	// One success and one failure, so the model and the error class are both present: recording
	// only a success would leave the two conditional attributes unexercised and the allowlist
	// unproven for the shape that actually carries them.
	got := collect(t, func(r llm.UsageRecorder) {
		r.RecordUsage(context.Background(), llm.Usage{
			Operation: llm.OperationChat, Provider: llm.ProviderOpenAI, Model: "gpt-4o-mini",
			InputTokens: 40, OutputTokens: 12, Duration: 250 * time.Millisecond,
		})
		r.RecordUsage(context.Background(), llm.Usage{
			Operation: llm.OperationEmbeddings, Provider: llm.ProviderGCPVertexAI, Model: "text-embedding-004",
			Duration: 10 * time.Millisecond, ErrorType: "429",
		})
	})

	assertAttributeKeys(t, got, MetricNameGenAITokenUsage,
		AttrGenAIOperationName, AttrGenAIProviderName, AttrGenAITokenType)
	assertAttributeKeys(t, got, MetricNameGenAIOperationDuration,
		AttrGenAIOperationName, AttrGenAIProviderName, AttrGenAIRequestModel, AttrErrorType)
}

func TestEnrichmentFailureMetricsCarryOnlyAllowedAttributes(t *testing.T) {
	reader := metric.NewManualReader()
	provider := metric.NewMeterProvider(metric.WithReader(reader))

	failures, err := NewEnrichmentFailureMetrics(provider.Meter("test"))
	require.NoError(t, err)
	require.NotNil(t, failures)

	failures.RecordTerminalFailure(context.Background(), EnrichmentTypeSentiment, "content_filter")
	failures.SetFailedRecords(EnrichmentTypeTranslation, true, 3)

	var got metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &got))

	// The reason is aggregate-only for a reason: a per-tenant breakdown by cause would characterise
	// one customer's content ("this tenant trips the content filter 30% of the time"), which is a
	// statement about their data rather than about the deployment.
	assertAttributeKeys(t, got, MetricNameEnrichmentTerminalTotal, AttrEnrichment, AttrReason)
	assertAttributeKeys(t, got, MetricNameEnrichmentFailedRecords, AttrEnrichment, AttrTerminal)
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
