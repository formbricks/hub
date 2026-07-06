// Package openai provides a thin wrapper around the official OpenAI Go SDK for
// embeddings and chat completions (used for translation).
package openai

import (
	"context"
	"errors"
	"fmt"
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
	// ErrNoCompletionInResponse is returned when a chat completion response contains no usable text.
	ErrNoCompletionInResponse = errors.New("openai: no completion in response")
)

// Client calls the OpenAI embeddings API via the official SDK.
type Client struct {
	sdk        openaisdk.Client
	baseURL    string
	dimensions int
	model      string
	normalize  bool
	// temperatureUnsupported latches once the configured model rejects the temperature
	// parameter (reasoning models do), so later calls omit it instead of failing.
	temperatureUnsupported atomic.Bool
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

// WithNormalize enables L2 normalization of the embedding vector before returning (e.g. before storing or caching).
func WithNormalize(normalize bool) ClientOption {
	return func(c *Client) {
		c.normalize = normalize
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

	resp, err := c.sdk.Embeddings.New(ctx, openaisdk.EmbeddingNewParams{
		Input: openaisdk.EmbeddingNewParamsInputUnion{
			OfString: param.NewOpt(input),
		},
		Model:      model,
		Dimensions: param.NewOpt(int64(c.dimensions)),
	})
	if err != nil {
		return nil, wrapOpenAIError("openai embedding", err)
	}

	if len(resp.Data) == 0 {
		return nil, ErrNoEmbeddingInResponse
	}

	emb := resp.Data[0].Embedding
	if len(emb) != c.dimensions {
		return nil, fmt.Errorf("%w: got %d, want %d", ErrDimensionMismatch, len(emb), c.dimensions)
	}

	// SDK returns float64; convert to float32 so we match EmbeddingClient and the Google SDK (which already returns
	// float32). Precision loss (64→32, and later 32→16 in the DB driver for halfvec) is acceptable for embeddings;
	// similarity results are unchanged in practice.
	out := make([]float32, len(emb))
	for i := range emb {
		out[i] = float32(emb[i])
	}

	if c.normalize {
		embeddings.NormalizeL2(out)
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

	out := strings.TrimSpace(choice.Message.Content)
	if out == "" {
		if refusal := strings.TrimSpace(choice.Message.Refusal); refusal != "" {
			return "", fmt.Errorf("%w: model refused: %.256s", ErrNoCompletionInResponse, refusal)
		}

		if choice.FinishReason != "" && choice.FinishReason != "stop" {
			return "", fmt.Errorf("%w: finish reason %q", ErrNoCompletionInResponse, choice.FinishReason)
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

	return wrapped
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
