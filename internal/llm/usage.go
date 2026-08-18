package llm

import (
	"context"
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
