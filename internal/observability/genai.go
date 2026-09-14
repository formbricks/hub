package observability

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/formbricks/hub/internal/llm"
)

// OpenTelemetry GenAI semantic conventions.
//
// These names are NOT the hub_* Prometheus-style ones used elsewhere in this package, and that is
// deliberate: any OTel-native backend already understands gen_ai.*, so a cost dashboard works
// without per-deployment configuration. The mixed styles in one binary look inconsistent but are
// correct — the Prometheus exporter appends _total/_seconds suffixes on export, while these carry
// UCUM units instead.
//
// The conventions moved to their own repository (open-telemetry/semantic-conventions-genai) on
// 2026-06-12 with semconv v1.42.0, and every gen_ai.* attribute and metric is still marked
// Development rather than Stable. Pinned here so a future reader knows which revision this
// matched; expect churn.
// word "token", not credentials.
//
//nolint:gosec // G101 false positive: these are metric and attribute NAMES containing the
const (
	MetricNameGenAITokenUsage        = "gen_ai.client.token.usage"
	MetricNameGenAIOperationDuration = "gen_ai.client.operation.duration"
)

// GenAI attribute keys, all Required or Conditionally Required for the two metrics above except
// error.type, which is Stable and applies only to failures.
//
//nolint:gosec // G101 false positive: attribute names, not credentials.
const (
	AttrGenAIOperationName = "gen_ai.operation.name"
	AttrGenAIProviderName  = "gen_ai.provider.name"
	AttrGenAITokenType     = "gen_ai.token.type"
	AttrGenAIRequestModel  = "gen_ai.request.model"
	AttrErrorType          = "error.type"
)

// Token type values for gen_ai.token.type.
const (
	genAITokenTypeInput  = "input"
	genAITokenTypeOutput = "output"
)

// genAIMetrics implements llm.UsageRecorder against OTel instruments.
type genAIMetrics struct {
	tokens   metric.Int64Histogram
	duration metric.Float64Histogram
}

// NewGenAIMetrics builds the GenAI usage recorder. Returns nil when meter is nil (metrics
// disabled), and callers pass that straight through — the provider clients treat a nil recorder as
// "do not record" rather than nil-checking at every call site.
func NewGenAIMetrics(meter metric.Meter) (llm.UsageRecorder, error) {
	if meter == nil {
		return nil, nil //nolint:nilnil // intentional: a nil recorder disables recording
	}

	tokens, err := meter.Int64Histogram(MetricNameGenAITokenUsage,
		metric.WithDescription("Number of input and output tokens used."),
		metric.WithUnit("{token}"))
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", MetricNameGenAITokenUsage, err)
	}

	duration, err := meter.Float64Histogram(MetricNameGenAIOperationDuration,
		metric.WithDescription("GenAI operation duration."),
		metric.WithUnit("s"))
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", MetricNameGenAIOperationDuration, err)
	}

	return &genAIMetrics{tokens: tokens, duration: duration}, nil
}

// RecordUsage emits one call's duration and, when the provider reported them, its token counts.
//
// tenant_id is deliberately absent. It is unbounded, and the same rule already applies to the
// enrichment backlog gauge: aggregate cost belongs in metrics, per-tenant cost is answered from
// the record counts the status endpoint already returns.
func (g *genAIMetrics) RecordUsage(ctx context.Context, usage llm.Usage) {
	base := []attribute.KeyValue{
		attribute.String(AttrGenAIOperationName, string(usage.Operation)),
		attribute.String(AttrGenAIProviderName, string(usage.Provider)),
	}

	durationAttrs := base
	if usage.Model != "" {
		durationAttrs = append(durationAttrs, attribute.String(AttrGenAIRequestModel, usage.Model))
	}

	if usage.ErrorType != "" {
		durationAttrs = append(durationAttrs, attribute.String(AttrErrorType, usage.ErrorType))
	}

	g.duration.Record(ctx, usage.Duration.Seconds(), metric.WithAttributes(durationAttrs...))

	// Zero is not "cost nothing" — it is "the provider told us nothing", which happens on every
	// failed call. Emitting it would drag the average down and make a provider outage look cheap.
	if usage.InputTokens > 0 {
		g.tokens.Record(ctx, usage.InputTokens, metric.WithAttributes(
			append(base, attribute.String(AttrGenAITokenType, genAITokenTypeInput))...))
	}

	if usage.OutputTokens > 0 {
		g.tokens.Record(ctx, usage.OutputTokens, metric.WithAttributes(
			append(base, attribute.String(AttrGenAITokenType, genAITokenTypeOutput))...))
	}
}
