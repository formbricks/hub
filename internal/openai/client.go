// Package openai provides a thin wrapper around the official OpenAI Go SDK for
// embeddings and chat completions (used for translation).
package openai

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	openaisdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"

	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/llm"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/pkg/embeddings"
)

var (
	// ErrEmptyInput is returned when CreateEmbedding is called with empty input.
	ErrEmptyInput = errors.New("openai: input text is empty")
	// ErrInvalidDims is returned when dimensions is not positive.
	ErrInvalidDims = errors.New("openai: embedding dimensions must be positive")
	// ErrNoEmbeddingInResponse is returned when the API response contains no embedding data.
	ErrNoEmbeddingInResponse = errors.New("openai: no embedding in response")
	// ErrDimensionMismatch is returned when the response embedding length does not match configured dimensions.
	ErrDimensionMismatch = errors.New("openai: embedding dimension mismatch")
	// ErrEmbeddingCountMismatch is returned when a batch response does not contain exactly one vector per input.
	ErrEmbeddingCountMismatch = errors.New("openai: embedding response count mismatch")
	// ErrEmbeddingIndexInvalid is returned when a batch response contains an out-of-range or duplicate index.
	ErrEmbeddingIndexInvalid = errors.New("openai: embedding response index invalid")
	// ErrEmbeddingValueInvalid is returned when a response vector contains NaN or infinity.
	ErrEmbeddingValueInvalid = errors.New("openai: embedding value is not finite")
	// ErrNoCompletionInResponse is returned when a chat completion response contains no usable text.
	ErrNoCompletionInResponse = errors.New("openai: no completion in response")
)

// Client calls the OpenAI embeddings API via the official SDK.
type Client struct {
	sdk               openaisdk.Client
	baseURL           string
	dimensions        int
	model             string
	normalize         bool
	disableKeepAlives bool
	// temperatureUnsupported latches once the configured model rejects the temperature
	// parameter (reasoning models do), so later calls omit it instead of failing.
	temperatureUnsupported atomic.Bool
	// usage receives one record per provider call. nil disables recording entirely, which is how
	// the backfill commands and the unit tests opt out.
	usage llm.UsageRecorder
}

// WithUsageRecorder attaches a recorder that receives each call's token counts and duration. The
// provider returns those numbers on every response; without this they are decoded and dropped.
func WithUsageRecorder(recorder llm.UsageRecorder) ClientOption {
	return func(c *Client) {
		c.usage = recorder
	}
}

// NewClient creates an OpenAI embeddings client using the official SDK.
// Embedding dimension is fixed (models.EmbeddingVectorDimensions); WithDimensions is optional for overrides.
func NewClient(apiKey string, opts ...ClientOption) *Client {
	client := &Client{
		dimensions: models.EmbeddingVectorDimensions,
	}

	for _, opt := range opts {
		opt(client)
	}

	sdkOpts := []option.RequestOption{
		option.WithAPIKey(apiKey),
		// Disable the SDK's internal retry loop (default 2 retries sleeping the full,
		// uncapped Retry-After): it races the worker's job deadline, so a throttled call
		// surfaced as context.DeadlineExceeded instead of the 429 — defeating the
		// RateLimitError -> snooze path — and tripled request volume against an already
		// rate-limited provider. River and the rate-limit snooze own all retry policy.
		option.WithMaxRetries(0),
	}
	if client.baseURL != "" {
		sdkOpts = append(sdkOpts, option.WithBaseURL(client.baseURL))
	}

	if client.disableKeepAlives {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.DisableKeepAlives = true
		sdkOpts = append(sdkOpts, option.WithHTTPClient(&http.Client{Transport: transport}))
	}

	client.sdk = openaisdk.NewClient(sdkOpts...)

	return client
}

// CreateEmbedding returns the embedding vector for the given text using the configured model.
// The returned slice length equals the configured dimensions.
func (c *Client) CreateEmbedding(ctx context.Context, input string) ([]float32, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil, ErrEmptyInput
	}

	if c.dimensions <= 0 {
		return nil, ErrInvalidDims
	}

	model := c.model

	started := time.Now()

	resp, err := c.sdk.Embeddings.New(ctx, openaisdk.EmbeddingNewParams{
		Input: openaisdk.EmbeddingNewParamsInputUnion{
			OfString: param.NewOpt(input),
		},
		Model:      model,
		Dimensions: param.NewOpt(int64(c.dimensions)),
	})

	var promptTokens int64

	if resp != nil {
		promptTokens = resp.Usage.PromptTokens
	}

	c.recordUsage(ctx, llm.OperationEmbeddings, started, promptTokens, 0, err)

	if err != nil {
		return nil, wrapOpenAIError("openai embedding", err)
	}

	vectors, err := c.decodeEmbeddingResponse(resp.Data, 1)
	if err != nil {
		return nil, err
	}

	return vectors[0], nil
}

// CreateEmbeddings returns one embedding per input. Results are reordered by the response index so
// callers always receive vectors in input order, even when an OpenAI-compatible provider returns
// data out of order.
func (c *Client) CreateEmbeddings(ctx context.Context, inputs []string) ([][]float32, error) {
	if len(inputs) == 0 {
		return nil, ErrEmptyInput
	}

	trimmed := make([]string, len(inputs))
	for i, input := range inputs {
		trimmed[i] = strings.TrimSpace(input)
		if trimmed[i] == "" {
			return nil, fmt.Errorf("%w: input %d", ErrEmptyInput, i)
		}
	}

	if c.dimensions <= 0 {
		return nil, ErrInvalidDims
	}

	started := time.Now()

	resp, err := c.sdk.Embeddings.New(ctx, openaisdk.EmbeddingNewParams{
		Input: openaisdk.EmbeddingNewParamsInputUnion{
			OfArrayOfStrings: trimmed,
		},
		Model:      c.model,
		Dimensions: param.NewOpt(int64(c.dimensions)),
	})

	var promptTokens int64

	if resp != nil {
		promptTokens = resp.Usage.PromptTokens
	}

	c.recordUsage(ctx, llm.OperationEmbeddings, started, promptTokens, 0, err)

	if err != nil {
		return nil, wrapOpenAIError("openai embeddings batch", err)
	}

	return c.decodeEmbeddingResponse(resp.Data, len(trimmed))
}

func (c *Client) decodeEmbeddingResponse( //nolint:funcorder // shared decoder stays next to the embedding methods
	data []openaisdk.Embedding,
	expected int,
) ([][]float32, error) {
	if len(data) == 0 {
		return nil, ErrNoEmbeddingInResponse
	}

	if len(data) != expected {
		return nil, fmt.Errorf("%w: got %d, want %d", ErrEmbeddingCountMismatch, len(data), expected)
	}

	out := make([][]float32, expected)
	seen := make([]bool, expected)

	for _, item := range data {
		if item.Index < 0 || item.Index >= int64(expected) || seen[item.Index] {
			return nil, fmt.Errorf("%w: %d", ErrEmbeddingIndexInvalid, item.Index)
		}

		if len(item.Embedding) != c.dimensions {
			return nil, fmt.Errorf("%w: index %d got %d, want %d",
				ErrDimensionMismatch, item.Index, len(item.Embedding), c.dimensions)
		}

		vector := make([]float32, len(item.Embedding))
		for i, value := range item.Embedding {
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return nil, fmt.Errorf("%w: index %d dimension %d", ErrEmbeddingValueInvalid, item.Index, i)
			}

			vector[i] = float32(value)
			if math.IsInf(float64(vector[i]), 0) {
				return nil, fmt.Errorf("%w: index %d dimension %d", ErrEmbeddingValueInvalid, item.Index, i)
			}
		}

		if c.normalize {
			embeddings.NormalizeL2(vector)
		}

		out[item.Index] = vector
		seen[item.Index] = true
	}

	return out, nil
}

// CreateEmbeddingForQuery returns an embedding for the given search query. OpenAI's API does not distinguish
// task type; this delegates to CreateEmbedding for compatibility with EmbeddingClient.
func (c *Client) CreateEmbeddingForQuery(ctx context.Context, input string) ([]float32, error) {
	return c.CreateEmbedding(ctx, input)
}

// Translate sends a chat completion with the given system prompt and user text at
// temperature 0 (deterministic) using the configured model, returning the trimmed
// assistant text. It is the low-level call behind the translation enrichment; the
// service layer builds the prompt.
func (c *Client) Translate(ctx context.Context, systemPrompt, userText string) (string, error) {
	userText = strings.TrimSpace(userText)
	if userText == "" {
		return "", ErrEmptyInput
	}

	resp, err := c.createChatCompletion(ctx, openaisdk.ChatCompletionNewParams{
		Model: c.model,
		Messages: []openaisdk.ChatCompletionMessageParamUnion{
			openaisdk.SystemMessage(systemPrompt),
			openaisdk.UserMessage(userText),
		},
	})
	if err != nil {
		return "", wrapChatCompletionError(err)
	}

	return completionText(resp)
}

// CompleteJSON sends a chat completion that must return JSON matching schema,
// at temperature 0 (deterministic) using the configured model, and returns the
// trimmed JSON text. It uses OpenAI Structured Outputs (response_format =
// json_schema, strict) so the model is constrained to the schema; the service
// layer builds the schema and parses/validates the result. It is the low-level
// call behind structured enrichments (e.g. sentiment).
func (c *Client) CompleteJSON(ctx context.Context, systemPrompt, userText string, schema llm.Schema) (string, error) {
	userText = strings.TrimSpace(userText)
	if userText == "" {
		return "", ErrEmptyInput
	}

	resp, err := c.createChatCompletion(ctx, openaisdk.ChatCompletionNewParams{
		Model: c.model,
		Messages: []openaisdk.ChatCompletionMessageParamUnion{
			openaisdk.SystemMessage(systemPrompt),
			openaisdk.UserMessage(userText),
		},
		ResponseFormat: openaisdk.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &openaisdk.ResponseFormatJSONSchemaParam{
				JSONSchema: openaisdk.ResponseFormatJSONSchemaJSONSchemaParam{
					Name:   schema.Name,
					Strict: param.NewOpt(true),
					Schema: schema.JSONSchema(),
				},
			},
		},
	})
	if err != nil {
		return "", wrapChatCompletionError(err)
	}

	return completionText(resp)
}

// createChatCompletion runs a chat completion at temperature 0 (deterministic), falling back to
// the model's default temperature for models that reject the parameter: reasoning models
// (o-series, gpt-5 family) 400 on any non-default temperature, which config validation cannot
// know up front. The first such rejection retries once without the parameter and latches
// temperatureUnsupported, so every later call skips it — self-healing, with no model list to
// maintain.
func (c *Client) createChatCompletion(
	ctx context.Context, params openaisdk.ChatCompletionNewParams,
) (*openaisdk.ChatCompletion, error) {
	started := time.Now()

	resp, err := c.chatCompletionCall(ctx, params)

	var inputTokens, outputTokens int64

	if resp != nil {
		inputTokens, outputTokens = resp.Usage.PromptTokens, resp.Usage.CompletionTokens
	}

	c.recordUsage(ctx, llm.OperationChat, started, inputTokens, outputTokens, err)

	return resp, err
}

// chatCompletionCall is the original body, kept intact so the temperature-retry logic below is
// unchanged; createChatCompletion wraps it purely to time and record the call.
//
// One record covers the whole sequence, including the reasoning-model temperature retry inside:
// the token counts come from the successful request, but the duration spans both. That inflates a
// single call's latency at most once per client per process, since the rejection latches.
func (c *Client) chatCompletionCall(
	ctx context.Context, params openaisdk.ChatCompletionNewParams,
) (*openaisdk.ChatCompletion, error) {
	if !c.temperatureUnsupported.Load() {
		params.Temperature = param.NewOpt(0.0)
	}

	resp, err := c.sdk.Chat.Completions.New(ctx, params)
	if err != nil && isUnsupportedTemperatureError(err) {
		c.temperatureUnsupported.Store(true)

		params.Temperature = param.Opt[float64]{} // omit the parameter entirely

		resp, err = c.sdk.Chat.Completions.New(ctx, params)
	}

	if err != nil {
		// Deliberately unwrapped: the per-call wrapping (and 429 mapping) happens in
		// wrapChatCompletionError at the call sites.
		return nil, err //nolint:wrapcheck // wrapped by the callers via wrapChatCompletionError.
	}

	return resp, nil
}

// isUnsupportedTemperatureError reports whether err is the API rejecting the temperature
// parameter (invalid_request_error with param "temperature", as returned by reasoning models).
func isUnsupportedTemperatureError(err error) bool {
	var apiErr *openaisdk.Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadRequest {
		return false
	}

	return apiErr.Param == "temperature" ||
		strings.Contains(strings.ToLower(apiErr.Message), "temperature")
}

// completionText extracts the single trimmed assistant message from a chat
// completion response, or ErrNoCompletionInResponse when there is no usable text.
// A refusal or abnormal finish reason (e.g. content_filter, length) is included in
// the error — model-generated text, never the user's input — so a persistent
// refusal is diagnosable in logs instead of looking like a provider outage.
func completionText(resp *openaisdk.ChatCompletion) (string, error) {
	if len(resp.Choices) == 0 {
		return "", ErrNoCompletionInResponse
	}

	choice := resp.Choices[0]

	// Checked BEFORE the content, because a length-capped response usually carries partial text
	// rather than none. Returning that would hand back a half-finished translation to be stored as
	// complete, or a truncated JSON object that fails to parse and looks like a transient provider
	// glitch — retried to exhaustion and recorded as transient, when it is permanent for this text
	// at this model. A truncated result is not a result.
	if choice.FinishReason == "length" {
		return "", huberrors.NewTerminalProviderError(huberrors.TerminalReasonLength,
			fmt.Errorf("%w: finish reason %q", ErrNoCompletionInResponse, choice.FinishReason))
	}

	out := strings.TrimSpace(choice.Message.Content)
	if out == "" {
		if refusal := strings.TrimSpace(choice.Message.Refusal); refusal != "" {
			return "", huberrors.NewTerminalProviderError(huberrors.TerminalReasonRefusal,
				fmt.Errorf("%w: model refused: %.256s", ErrNoCompletionInResponse, refusal))
		}

		if choice.FinishReason != "" && choice.FinishReason != "stop" {
			err := fmt.Errorf("%w: finish reason %q", ErrNoCompletionInResponse, choice.FinishReason)

			// content_filter is the only terminal reason reachable here: length returns above,
			// before the content is read, because it usually truncates rather than blanks the
			// output. Everything else — including the tool finishes we never request — stays
			// retryable, since a false terminal abandons a record while a false transient only
			// costs a few calls.
			if choice.FinishReason == "content_filter" {
				return "", huberrors.NewTerminalProviderError(huberrors.TerminalReasonContentFilter, err)
			}

			return "", err
		}

		return "", ErrNoCompletionInResponse
	}

	return out, nil
}

// wrapChatCompletionError wraps a chat-completion SDK error, mapping a 429 to a
// huberrors.RateLimitError (carrying the Retry-After hint) so callers can snooze.
func wrapChatCompletionError(err error) error {
	return wrapOpenAIError("openai chat completion", err)
}

// wrapOpenAIError wraps an SDK error under op, mapping a 429 to a huberrors.RateLimitError
// (carrying the Retry-After hint) so callers can snooze. Shared by the chat-completion and
// embedding call paths — a throttled embedding backfill must snooze, not burn retry attempts.
func wrapOpenAIError(op string, err error) error {
	wrapped := fmt.Errorf("%s: %w", op, err)

	var apiErr *openaisdk.Error
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusTooManyRequests {
		return huberrors.NewRateLimitError(openaiRetryAfter(apiErr), wrapped)
	}

	if isOpenAIInputLengthError(apiErr) {
		return huberrors.NewTerminalProviderError(huberrors.TerminalReasonLength, wrapped)
	}

	return wrapped
}

// isOpenAIInputLengthError recognizes only explicit per-request input/context limit failures.
// Other 4xx responses (auth, model configuration, dimensions, malformed requests) remain
// retryable at the record layer because classifying them as terminal would permanently suppress
// every record after an operator fixes the deployment.
func isOpenAIInputLengthError(apiErr *openaisdk.Error) bool {
	if apiErr == nil || (apiErr.StatusCode != http.StatusBadRequest && apiErr.StatusCode != http.StatusRequestEntityTooLarge) {
		return false
	}

	code := strings.ToLower(strings.TrimSpace(apiErr.Code))
	if code == "context_length_exceeded" || code == "input_too_long" || code == "max_tokens_exceeded" {
		return true
	}

	message := strings.ToLower(apiErr.Message)
	for _, marker := range []string{
		"maximum context length",
		"context length exceeded",
		"input token count exceeds",
		"input is too long",
		"too many tokens",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}

	return false
}

// openaiRetryAfter reads the retry hint from a 429 response: retry-after-ms
// (milliseconds — OpenAI sends it for sub-second waits), then Retry-After as
// delta-seconds (integer or fractional per the SDK's own parser), then Retry-After
// as an HTTP-date. Returns 0 when absent or unparseable (the worker snoozes its
// default). The result is a hint only; the worker clamps it to sane bounds.
func openaiRetryAfter(apiErr *openaisdk.Error) time.Duration {
	if apiErr == nil || apiErr.Response == nil {
		return 0
	}

	if ms, err := strconv.ParseFloat(apiErr.Response.Header.Get("Retry-After-Ms"), 64); err == nil && ms > 0 {
		return time.Duration(ms * float64(time.Millisecond))
	}

	header := apiErr.Response.Header.Get("Retry-After")
	if header == "" {
		return 0
	}

	if secs, err := strconv.ParseFloat(header, 64); err == nil {
		if secs <= 0 {
			return 0
		}

		return time.Duration(secs * float64(time.Second))
	}

	if at, err := http.ParseTime(header); err == nil {
		if wait := time.Until(at); wait > 0 {
			return wait
		}
	}

	return 0
}

// recordUsage reports one call. Split out so every provider entry point records the same shape,
// and so a nil recorder is checked in exactly one place.
func (c *Client) recordUsage(
	ctx context.Context, operation llm.Operation, started time.Time, inputTokens, outputTokens int64, err error,
) {
	llm.Record(ctx, c.usage, operation, llm.ProviderOpenAI, c.model,
		started, inputTokens, outputTokens, errorTypeOf(err))
}

// errorTypeOf maps an error to the BOUNDED error.type attribute. Only the status extraction is
// OpenAI-specific; the rest of the classification is shared so the two clients cannot disagree on
// what "timeout" means.
func errorTypeOf(err error) string {
	return llm.ClassifyError(err, func(err error) (int, bool) {
		var apiErr *openaisdk.Error
		if errors.As(err, &apiErr) {
			return apiErr.StatusCode, true
		}

		return 0, false
	})
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

// WithBaseURL sets a custom OpenAI-compatible base URL (for example a self-hosted embeddings runtime).
func WithBaseURL(baseURL string) ClientOption {
	return func(c *Client) {
		c.baseURL = baseURL
	}
}

// WithDisableKeepAlives creates a new HTTP connection for every provider request. This lets
// Kubernetes balance background embedding batches across all endpoints instead of pinning a
// long-lived worker connection to one pod.
func WithDisableKeepAlives(disable bool) ClientOption {
	return func(c *Client) {
		c.disableKeepAlives = disable
	}
}

// WithNormalize enables L2 normalization of the embedding vector before returning (e.g. before storing or caching).
func WithNormalize(normalize bool) ClientOption {
	return func(c *Client) {
		c.normalize = normalize
	}
}
