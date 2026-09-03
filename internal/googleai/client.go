// Package googleai provides a thin wrapper around the Google Gen AI SDK for
// embeddings and content generation (used for translation).
package googleai

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"google.golang.org/genai"

	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/llm"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/pkg/embeddings"
)

var (
	// ErrEmptyInput is returned when CreateEmbedding is called with empty input.
	ErrEmptyInput = errors.New("googleai: input text is empty")
	// ErrInvalidDims is returned when dimensions is not positive.
	ErrInvalidDims = errors.New("googleai: embedding dimensions must be positive")
	// ErrNoEmbeddingInResponse is returned when the API response contains no embedding data.
	ErrNoEmbeddingInResponse = errors.New("googleai: no embedding in response")
	// ErrDimensionMismatch is returned when the response embedding length does not match configured dimensions.
	ErrDimensionMismatch = errors.New("googleai: embedding dimension mismatch")
	// ErrGoogleGeminiProjectRequired is returned when NewGoogleGeminiClient is called with empty project.
	ErrGoogleGeminiProjectRequired = errors.New("googleai google-gemini client: project is required")
	// ErrGoogleGeminiLocationRequired is returned when NewGoogleGeminiClient is called with empty location.
	ErrGoogleGeminiLocationRequired = errors.New("googleai google-gemini client: location is required")
	// ErrNoCompletionInResponse is returned when a generate-content response contains no usable text.
	ErrNoCompletionInResponse = errors.New("googleai: no completion in response")
)

// Client calls the Gemini embeddings API via the Google Gen AI SDK.
type Client struct {
	client     *genai.Client
	model      string
	dimensions int
	normalize  bool
	// provider is the gen_ai.provider.name this client reports. The two constructors below pick
	// different backends and they are NOT the same provider to a cost dashboard: the API-key path
	// is AI Studio (gcp.gemini) and the ADC path is Vertex (gcp.vertex_ai), billed separately.
	provider llm.Provider
	// usage receives one record per provider call. nil disables recording entirely, which is how
	// the backfill commands and the unit tests opt out.
	usage llm.UsageRecorder
	// thinkingBudgetUnsupported latches once the configured model rejects a zero thinking
	// budget (Pro models cannot disable thinking), so later calls fall back to the model's
	// default thinking behavior instead of failing.
	thinkingBudgetUnsupported atomic.Bool
}

// ClientOption configures the Client.
type ClientOption func(*Client)

// WithDimensions sets the requested embedding dimension (must match DB column).
func WithDimensions(dim int) ClientOption {
	return func(c *Client) {
		c.dimensions = dim
	}
}

// WithModel sets the embedding model name. Empty uses default.
func WithModel(model string) ClientOption {
	return func(c *Client) {
		c.model = model
	}
}

// WithNormalize enables L2 normalization of the embedding vector before returning (e.g. before storing or caching).
func WithNormalize(normalize bool) ClientOption {
	return func(c *Client) {
		c.normalize = normalize
	}
}

// WithUsageRecorder attaches a recorder that receives each call's token counts and duration. The
// provider returns those numbers on every generate-content response; without this they are decoded
// and dropped, and a deployment running on Google records no enrichment cost at all.
func WithUsageRecorder(recorder llm.UsageRecorder) ClientOption {
	return func(c *Client) {
		c.usage = recorder
	}
}

// NewClient creates a Gemini embeddings client.
func NewClient(ctx context.Context, apiKey string, opts ...ClientOption) (*Client, error) {
	genaiClient, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("googleai client: %w", err)
	}

	client := &Client{
		client:     genaiClient,
		dimensions: models.EmbeddingVectorDimensions,
		provider:   llm.ProviderGCPGemini,
	}
	for _, opt := range opts {
		opt(client)
	}

	return client, nil
}

// NewGoogleGeminiClient creates a Google Cloud-hosted Gemini embeddings client using Application Default Credentials (ADC).
// When running outside Google Cloud (e.g. EKS, Railway), set GOOGLE_APPLICATION_CREDENTIALS to the path of a service account key JSON file.
// project is the Google Cloud project ID; location is the region (e.g. europe-west3, global).
func NewGoogleGeminiClient(ctx context.Context, project, location string, opts ...ClientOption) (*Client, error) {
	if strings.TrimSpace(project) == "" {
		return nil, ErrGoogleGeminiProjectRequired
	}

	if strings.TrimSpace(location) == "" {
		return nil, ErrGoogleGeminiLocationRequired
	}

	genaiClient, err := genai.NewClient(ctx, &genai.ClientConfig{
		Project:  project,
		Location: location,
		Backend:  genai.BackendVertexAI,
	})
	if err != nil {
		return nil, fmt.Errorf("googleai google-gemini client: %w", err)
	}

	client := &Client{
		client:     genaiClient,
		dimensions: models.EmbeddingVectorDimensions,
		provider:   llm.ProviderGCPVertexAI,
	}
	for _, opt := range opts {
		opt(client)
	}

	return client, nil
}

// CreateEmbedding returns the embedding vector for the given text using RETRIEVAL_DOCUMENT task type
// (for storing documents/feedback records). The returned slice length equals the configured dimensions.
func (c *Client) CreateEmbedding(ctx context.Context, input string) ([]float32, error) {
	return c.embedWithTaskType(ctx, input, "RETRIEVAL_DOCUMENT")
}

// CreateEmbeddingForQuery returns an embedding for the given search query using RETRIEVAL_QUERY task type,
// which optimizes the vector for asymmetric retrieval against documents embedded with RETRIEVAL_DOCUMENT.
func (c *Client) CreateEmbeddingForQuery(ctx context.Context, input string) ([]float32, error) {
	return c.embedWithTaskType(ctx, input, "RETRIEVAL_QUERY")
}

// Translate generates content with the given system instruction and user text at
// temperature 0 (deterministic) using the configured model, returning the trimmed
// response text. It is the low-level call behind the translation enrichment; the
// service layer builds the prompt.
func (c *Client) Translate(ctx context.Context, systemPrompt, userText string) (string, error) {
	userText = strings.TrimSpace(userText)
	if userText == "" {
		return "", ErrEmptyInput
	}

	temperature := float32(0)
	started := time.Now()

	resp, err := c.client.Models.GenerateContent(ctx, c.model, genai.Text(userText), &genai.GenerateContentConfig{
		Temperature:       &temperature,
		SystemInstruction: genai.NewContentFromText(systemPrompt, genai.RoleUser),
	})

	c.recordGenerate(ctx, started, resp, err)

	if err != nil {
		return "", wrapGenerateContentError(err)
	}

	return generateContentText(resp)
}

// CompleteJSON generates content that must return JSON matching schema, at
// temperature 0 (deterministic) using the configured model, and returns the
// trimmed JSON text. It sets responseMimeType=application/json and a standard
// JSON-Schema responseJsonSchema so the model is constrained to a closed object;
// the service layer builds the schema and parses/validates the result. It is the
// low-level call behind structured enrichments (e.g. sentiment).
func (c *Client) CompleteJSON(ctx context.Context, systemPrompt, userText string, schema llm.Schema) (string, error) {
	userText = strings.TrimSpace(userText)
	if userText == "" {
		return "", ErrEmptyInput
	}

	temperature := float32(0)

	// ResponseJsonSchema (standard JSON Schema) rather than ResponseSchema (the OpenAPI subset):
	// it enforces the closed-object contract (additionalProperties:false), matching the OpenAI
	// strict path. ResponseSchema must be omitted when ResponseJsonSchema is set.
	config := &genai.GenerateContentConfig{
		Temperature:        &temperature,
		SystemInstruction:  genai.NewContentFromText(systemPrompt, genai.RoleUser),
		ResponseMIMEType:   "application/json",
		ResponseJsonSchema: schema.JSONSchema(),
	}

	// Gemini 2.5 models think by default (dynamic budget), which adds thinking-token cost and
	// seconds of latency to every one-line classification for no accuracy need at this task
	// size. Request a zero budget (Flash-family models accept it); Pro models reject zero with
	// an INVALID_ARGUMENT, in which case retry without the config and latch, falling back to
	// the model's default thinking — self-healing, with no model list to maintain.
	if !c.thinkingBudgetUnsupported.Load() {
		config.ThinkingConfig = &genai.ThinkingConfig{ThinkingBudget: new(int32)}
	}

	started := time.Now()

	resp, err := c.client.Models.GenerateContent(ctx, c.model, genai.Text(userText), config)
	if err != nil && isUnsupportedThinkingBudgetError(err) {
		c.thinkingBudgetUnsupported.Store(true)

		config.ThinkingConfig = nil

		resp, err = c.client.Models.GenerateContent(ctx, c.model, genai.Text(userText), config)
	}

	// One record for the whole sequence, including the thinking-budget retry above: the token
	// counts come from whichever request answered, and the duration spans both. That inflates a
	// single call's latency at most once per client per process, since the rejection latches.
	c.recordGenerate(ctx, started, resp, err)

	if err != nil {
		return "", wrapGenerateContentError(err)
	}

	return generateContentText(resp)
}

// isUnsupportedThinkingBudgetError reports whether err is the API rejecting the thinking-budget
// configuration (INVALID_ARGUMENT mentioning thinking, as returned by Pro models for budget 0).
func isUnsupportedThinkingBudgetError(err error) bool {
	var apiErr genai.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != http.StatusBadRequest {
		return false
	}

	return strings.Contains(strings.ToLower(apiErr.Message), "thinking")
}

// generateContentText extracts the trimmed text from a generate-content
// response, or ErrNoCompletionInResponse when there is none. A safety block or
// abnormal finish reason is included in the error — provider metadata, never the
// user's input — so a persistent block is diagnosable in logs instead of looking
// like a provider outage.
func generateContentText(resp *genai.GenerateContentResponse) (string, error) {
	// Checked BEFORE the text, because MAX_TOKENS usually truncates rather than blanks the output.
	// Returning a truncated result would store a half-finished translation as complete, or produce
	// invalid JSON that reads as a transient parse failure when it is permanent for this input.
	if firstFinishReason(resp) == genai.FinishReasonMaxTokens {
		return "", huberrors.NewTerminalProviderError(huberrors.TerminalReasonLength,
			fmt.Errorf("%w: finish reason: %s", ErrNoCompletionInResponse, genai.FinishReasonMaxTokens))
	}

	// resp.Text() guards an empty Candidates slice and a nil Content, but NOT a nil candidate
	// pointer (genai types.go), so it panics on `"candidates": [null]`. Guard before calling it.
	out := ""
	if firstCandidate(resp) != nil {
		out = strings.TrimSpace(resp.Text())
	}

	if out != "" {
		return out, nil
	}

	err := fmt.Errorf("%w%s", ErrNoCompletionInResponse, emptyResponseDetail(resp))

	if reason, terminal := terminalEmptyReason(resp); terminal {
		return "", huberrors.NewTerminalProviderError(reason, err)
	}

	return "", err
}

// firstCandidate returns the first usable candidate, or nil when the response carries none.
//
// Candidates is a slice of POINTERS, so `"candidates": [null]` on the wire unmarshals to a slice
// holding nil, and every bare resp.Candidates[0].X in this file would panic on it. The SDK's own
// Text() has the same gap — it checks the slice length and a nil Content but not a nil candidate —
// so callers must guard before reaching it. Reading candidates through one accessor keeps that in
// a single place.
func firstCandidate(resp *genai.GenerateContentResponse) *genai.Candidate {
	if resp == nil || len(resp.Candidates) == 0 {
		return nil
	}

	return resp.Candidates[0]
}

// firstFinishReason reads the first candidate's finish reason, or "" when there is no usable
// candidate.
func firstFinishReason(resp *genai.GenerateContentResponse) genai.FinishReason {
	candidate := firstCandidate(resp)
	if candidate == nil {
		return ""
	}

	return candidate.FinishReason
}

// terminalEmptyReason classifies an empty response as permanent for this input, or leaves it
// retryable.
//
// Only outcomes determined by the CONTENT are terminal. Anything else — an empty response with
// no metadata, OTHER, LANGUAGE, a malformed function call — stays retryable on purpose: a false
// terminal abandons a record for good, a false transient costs a few wasted calls.
func terminalEmptyReason(resp *genai.GenerateContentResponse) (huberrors.TerminalReason, bool) {
	// A prompt-level block is the provider rejecting the input before generating anything, which
	// is as content-determined as it gets.
	if resp.PromptFeedback != nil && resp.PromptFeedback.BlockReason != "" {
		return huberrors.TerminalReasonContentFilter, true
	}

	switch firstFinishReason(resp) {
	case genai.FinishReasonSafety, genai.FinishReasonProhibitedContent,
		genai.FinishReasonBlocklist, genai.FinishReasonSPII,
		genai.FinishReasonImageSafety, genai.FinishReasonImageProhibitedContent:
		return huberrors.TerminalReasonContentFilter, true
	case genai.FinishReasonRecitation, genai.FinishReasonImageRecitation:
		return huberrors.TerminalReasonRecitation, true
	case genai.FinishReasonMaxTokens:
		return huberrors.TerminalReasonLength, true
	case genai.FinishReasonUnspecified, genai.FinishReasonStop,
		genai.FinishReasonLanguage, genai.FinishReasonOther,
		genai.FinishReasonMalformedFunctionCall, genai.FinishReasonUnexpectedToolCall,
		genai.FinishReasonNoImage, genai.FinishReasonImageOther:
		// Deliberately retryable, listed rather than defaulted so the choice is reviewable.
		// LANGUAGE is arguably permanent, but it is rare and abandoning a record wrongly is the
		// worse error; the image and tool reasons cannot occur for the calls this client makes;
		// STOP with empty text and UNSPECIFIED carry no information at all.
		return "", false
	default:
		// A reason added by a future SDK version. Retry rather than abandon — the same asymmetry.
		return "", false
	}
}

// emptyResponseDetail renders the block/finish metadata explaining an empty response
// (": blocked: SAFETY", ": finish reason: MAX_TOKENS"), or "" when none is present.
//
// BlockReasonMessage is TRUNCATED, matching the 256-char cap the OpenAI client puts on a refusal
// string. Both are free-form provider prose that ends up in an error and therefore in logs, and
// while a block message is usually a fixed phrase, nothing in the API promises that — it is the
// provider describing why it rejected this particular text, which is the category of string that
// can quote the text back. Everything else here is a bounded enum.
func emptyResponseDetail(resp *genai.GenerateContentResponse) string {
	if resp.PromptFeedback != nil && resp.PromptFeedback.BlockReason != "" {
		detail := fmt.Sprintf(": blocked: %s", resp.PromptFeedback.BlockReason)
		if resp.PromptFeedback.BlockReasonMessage != "" {
			detail += fmt.Sprintf(" (%.256s)", resp.PromptFeedback.BlockReasonMessage)
		}

		return detail
	}

	if finish := firstFinishReason(resp); finish != "" && finish != genai.FinishReasonStop {
		return fmt.Sprintf(": finish reason: %s", finish)
	}

	return ""
}

// wrapGenerateContentError wraps a generate-content SDK error, mapping a 429 /
// RESOURCE_EXHAUSTED to a huberrors.RateLimitError (carrying the retry hint) so
// callers can snooze.
func wrapGenerateContentError(err error) error {
	return wrapGenaiError("gemini generate content", err)
}

// wrapGenaiError wraps an SDK error under op, mapping a 429 / RESOURCE_EXHAUSTED to a
// huberrors.RateLimitError (carrying the retry hint) so callers can snooze. Shared by the
// generate-content and embedding call paths — a throttled embedding backfill must snooze,
// not burn retry attempts.
func wrapGenaiError(op string, err error) error {
	wrapped := fmt.Errorf("%s: %w", op, err)

	var apiErr genai.APIError
	if errors.As(err, &apiErr) && (apiErr.Code == http.StatusTooManyRequests || apiErr.Status == "RESOURCE_EXHAUSTED") {
		return huberrors.NewRateLimitError(genaiRetryAfter(apiErr), wrapped)
	}

	if isGenaiInputLengthError(apiErr) {
		return huberrors.NewTerminalProviderError(huberrors.TerminalReasonLength, wrapped)
	}

	return wrapped
}

// isGenaiInputLengthError deliberately requires an explicit token/input limit phrase. INVALID_ARGUMENT
// also covers deployment mistakes, which must stay retryable after an operator corrects them rather
// than becoming permanent record exclusions.
func isGenaiInputLengthError(apiErr genai.APIError) bool {
	if apiErr.Code != http.StatusBadRequest && apiErr.Code != http.StatusRequestEntityTooLarge {
		return false
	}

	message := strings.ToLower(apiErr.Message)
	for _, marker := range []string{
		"input token count exceeds",
		"maximum number of tokens",
		"maximum context length",
		"context length exceeded",
		"input is too long",
		"too many tokens",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}

	return false
}

// genaiRetryAfter extracts the RetryInfo retryDelay from a RESOURCE_EXHAUSTED error's
// details, or 0 when absent or unparseable.
func genaiRetryAfter(apiErr genai.APIError) time.Duration {
	for _, detail := range apiErr.Details {
		typeName, _ := detail["@type"].(string)
		if !strings.Contains(typeName, "RetryInfo") {
			continue
		}

		if delay, ok := detail["retryDelay"].(string); ok {
			if dur, parseErr := time.ParseDuration(delay); parseErr == nil {
				return dur
			}
		}
	}

	return 0
}

// embedWithTaskType calls the Gemini API with the given task type (e.g. RETRIEVAL_DOCUMENT, RETRIEVAL_QUERY).
func (c *Client) embedWithTaskType(ctx context.Context, input, taskType string) ([]float32, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil, ErrEmptyInput
	}

	if c.dimensions <= 0 || c.dimensions > math.MaxInt32 {
		return nil, ErrInvalidDims
	}

	contents := []*genai.Content{genai.NewContentFromText(input, genai.RoleUser)}
	dimInt32 := int32(c.dimensions)
	started := time.Now()

	resp, err := c.client.Models.EmbedContent(ctx, c.model, contents, &genai.EmbedContentConfig{
		TaskType:             taskType,
		OutputDimensionality: &dimInt32,
	})

	// Token counts deliberately zero: an embed response carries no token usage at all — Vertex
	// reports a billable CHARACTER count and AI Studio reports nothing — so there is no number to
	// report in {token} units. The duration still lands, which is what makes a slow embedding
	// provider visible. The recorder skips zero counts rather than emitting them.
	c.recordUsage(ctx, llm.OperationEmbeddings, started, 0, 0, err)

	if err != nil {
		return nil, wrapGenaiError("gemini embedding", err)
	}

	if len(resp.Embeddings) == 0 {
		return nil, ErrNoEmbeddingInResponse
	}

	emb := resp.Embeddings[0].Values
	if len(emb) != c.dimensions {
		return nil, fmt.Errorf("%w: got %d, want %d", ErrDimensionMismatch, len(emb), c.dimensions)
	}

	out := make([]float32, len(emb))
	copy(out, emb)

	if c.normalize {
		embeddings.NormalizeL2(out)
	}

	return out, nil
}

// recordGenerate reports one generate-content call, pulling the token counts off the response's
// usage metadata.
//
// Output tokens include ThoughtsTokenCount. Thinking tokens are billed as output, and this client
// asks for a zero thinking budget but falls back to the model's default when the model rejects
// that (see CompleteJSON), so leaving them out would under-report the cost of exactly the models
// where it is largest.
func (c *Client) recordGenerate(
	ctx context.Context, started time.Time, resp *genai.GenerateContentResponse, err error,
) {
	var input, output int64

	if resp != nil && resp.UsageMetadata != nil {
		input = int64(resp.UsageMetadata.PromptTokenCount)
		output = int64(resp.UsageMetadata.CandidatesTokenCount) + int64(resp.UsageMetadata.ThoughtsTokenCount)
	}

	c.recordUsage(ctx, llm.OperationChat, started, input, output, err)
}

// recordUsage reports one call. Split out so every provider entry point records the same shape,
// and so a nil recorder is checked in exactly one place.
func (c *Client) recordUsage(
	ctx context.Context, operation llm.Operation, started time.Time, inputTokens, outputTokens int64, err error,
) {
	llm.Record(ctx, c.usage, operation, c.provider, c.model,
		started, inputTokens, outputTokens, errorTypeOf(err))
}

// errorTypeOf maps an error to the BOUNDED error.type attribute. Only the status extraction is
// Google-specific; the rest of the classification is shared so the two clients cannot disagree on
// what "timeout" means.
func errorTypeOf(err error) string {
	return llm.ClassifyError(err, func(err error) (int, bool) {
		var apiErr genai.APIError
		if errors.As(err, &apiErr) {
			return apiErr.Code, true
		}

		return 0, false
	})
}
