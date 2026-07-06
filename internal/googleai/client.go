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

	resp, err := c.client.Models.GenerateContent(ctx, c.model, genai.Text(userText), &genai.GenerateContentConfig{
		Temperature:       &temperature,
		SystemInstruction: genai.NewContentFromText(systemPrompt, genai.RoleUser),
	})
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

	resp, err := c.client.Models.GenerateContent(ctx, c.model, genai.Text(userText), config)
	if err != nil && isUnsupportedThinkingBudgetError(err) {
		c.thinkingBudgetUnsupported.Store(true)

		config.ThinkingConfig = nil

		resp, err = c.client.Models.GenerateContent(ctx, c.model, genai.Text(userText), config)
	}

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
	out := strings.TrimSpace(resp.Text())
	if out == "" {
		return "", fmt.Errorf("%w%s", ErrNoCompletionInResponse, emptyResponseDetail(resp))
	}

	return out, nil
}

// emptyResponseDetail renders the block/finish metadata explaining an empty response
// (": blocked: SAFETY", ": finish reason: MAX_TOKENS"), or "" when none is present.
func emptyResponseDetail(resp *genai.GenerateContentResponse) string {
	if resp.PromptFeedback != nil && resp.PromptFeedback.BlockReason != "" {
		detail := fmt.Sprintf(": blocked: %s", resp.PromptFeedback.BlockReason)
		if resp.PromptFeedback.BlockReasonMessage != "" {
			detail += " (" + resp.PromptFeedback.BlockReasonMessage + ")"
		}

		return detail
	}

	if len(resp.Candidates) > 0 {
		candidate := resp.Candidates[0]
		if candidate.FinishReason != "" && candidate.FinishReason != genai.FinishReasonStop {
			return fmt.Sprintf(": finish reason: %s", candidate.FinishReason)
		}
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

	return wrapped
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

	resp, err := c.client.Models.EmbedContent(ctx, c.model, contents, &genai.EmbedContentConfig{
		TaskType:             taskType,
		OutputDimensionality: &dimInt32,
	})
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
