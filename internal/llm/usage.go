package llm

import (
	"context"
	"errors"
	"strconv"
	"time"
)

// Operation names the kind of provider call, following the OpenTelemetry GenAI semantic
// conventions' well-known values for gen_ai.operation.name.
type Operation string

const (
	// OperationChat is a chat-completion call — sentiment, emotions and translation all use one.
	OperationChat Operation = "chat"
	// OperationEmbeddings is an embeddings call.
	OperationEmbeddings Operation = "embeddings"
)

// Provider names the GenAI provider, following the conventions' well-known values for
// gen_ai.provider.name. Not the Hub's own provider identifiers: "google" is `gcp.vertex_ai` or
// `gcp.gemini` there, depending on the endpoint.
const (
	ProviderOpenAI Provider = "openai"
	// ProviderGCPVertexAI is the aiplatform.googleapis.com endpoint, which is what the Hub's
	// Google client uses when a project and location are configured.
	ProviderGCPVertexAI Provider = "gcp.vertex_ai"
	// ProviderGCPGemini is the generativelanguage.googleapis.com endpoint (AI Studio).
	ProviderGCPGemini Provider = "gcp.gemini"
)

// Provider is a gen_ai.provider.name value.
type Provider string

// Usage is one provider call's cost and outcome, in provider-agnostic terms.
//
// It exists because both clients were discarding the `usage` block every provider returns, which
// left no way to answer what enrichment actually costs — not per tenant, not per enrichment, not
// in total. Recording it is nearly free: the number is already in the response.
type Usage struct {
	Operation Operation
	Provider  Provider
	// Model is the model the request asked for (gen_ai.request.model).
	Model string
	// InputTokens and OutputTokens are the provider's own counts. Zero when it reported none —
	// which is not the same as a call that cost nothing, so recorders skip rather than emit zero.
	InputTokens  int64
	OutputTokens int64
	// Duration is the wall time of the call, successful or not.
	Duration time.Duration
	// ErrorType is the error.type attribute: a SHORT, BOUNDED class such as "timeout" or a status
	// code — never a provider message. Empty when the call succeeded. Unbounded values here would
	// blow up metric cardinality, which is why the clients map rather than pass through.
	ErrorType string
}

// UsageRecorder receives one Usage per provider call. Declared here rather than in observability
// so the provider clients depend on the behaviour, not on the metrics implementation; a nil
// recorder means usage is not recorded and the client behaves exactly as before.
type UsageRecorder interface {
	RecordUsage(ctx context.Context, usage Usage)
}

// Record sends one call's usage to recorder, or does nothing when recorder is nil.
//
// Both provider clients build the same struct from the same six inputs and both had to remember
// the nil check; this keeps that shape in one place so the two cannot drift on what a Usage means.
// What stays with each client is the part that genuinely differs: pulling token counts out of a
// provider-shaped response, and mapping a provider-shaped error to a bounded class.
func Record(
	ctx context.Context, recorder UsageRecorder,
	operation Operation, provider Provider, model string,
	started time.Time, inputTokens, outputTokens int64, errorType string,
) {
	if recorder == nil {
		return
	}

	recorder.RecordUsage(ctx, Usage{
		Operation:    operation,
		Provider:     provider,
		Model:        model,
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		Duration:     time.Since(started),
		ErrorType:    errorType,
	})
}

// Bounded error.type values shared by both clients. An HTTP status becomes its own number; these
// cover what has no status.
const (
	// ErrorTypeTimeout is a deadline the caller set, not one the provider reported.
	ErrorTypeTimeout = "timeout"
	// ErrorTypeCancelled is the surrounding context going away — a shutdown, usually.
	ErrorTypeCancelled = "cancelled"
	// ErrorTypeOther is everything else, deliberately vague. The alternative is the provider's own
	// message, which is unbounded and would blow up metric cardinality the first time a provider
	// echoed user input back in an error.
	ErrorTypeOther = "other"
)

// ClassifyError maps an error to a bounded error.type. status extracts a provider-specific HTTP
// status when the error carries one; it is the only part that differs between providers.
func ClassifyError(err error, status func(error) (int, bool)) string {
	if err == nil {
		return ""
	}

	if code, ok := status(err); ok && code != 0 {
		return strconv.Itoa(code)
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return ErrorTypeTimeout
	}

	if errors.Is(err, context.Canceled) {
		return ErrorTypeCancelled
	}

	return ErrorTypeOther
}
