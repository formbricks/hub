package googleai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"

	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/llm"
	"github.com/formbricks/hub/internal/llm/llmtest"
)

func TestNewGoogleGeminiClient_emptyProject_returnsError(t *testing.T) {
	ctx := context.Background()

	_, err := NewGoogleGeminiClient(ctx, "", "europe-west3")
	assert.ErrorIs(t, err, ErrGoogleGeminiProjectRequired)
}

func TestNewGoogleGeminiClient_emptyLocation_returnsError(t *testing.T) {
	ctx := context.Background()

	_, err := NewGoogleGeminiClient(ctx, "my-project", "")
	assert.ErrorIs(t, err, ErrGoogleGeminiLocationRequired)
}

func TestClient_CreateEmbedding_emptyInput_returnsErrEmptyInput(t *testing.T) {
	// NewClient (AI Studio) requires valid API key; we test validation via CreateEmbedding.
	// Use a fake key - genai.NewClient may not validate until first API call.
	ctx := context.Background()

	client, err := NewClient(ctx, "test-key-placeholder", WithModel("text-embedding-004"))
	if err != nil {
		t.Skipf("NewClient failed (expected without valid key): %v", err)
	}

	_, err = client.CreateEmbedding(ctx, "")
	assert.ErrorIs(t, err, ErrEmptyInput)
}

func TestClient_CreateEmbedding_whitespaceInput_returnsErrEmptyInput(t *testing.T) {
	ctx := context.Background()

	client, err := NewClient(ctx, "test-key-placeholder", WithModel("text-embedding-004"))
	if err != nil {
		t.Skipf("NewClient failed (expected without valid key): %v", err)
	}

	_, err = client.CreateEmbedding(ctx, "   \t\n  ")
	assert.ErrorIs(t, err, ErrEmptyInput)
}

func TestGenaiRetryAfter(t *testing.T) {
	const retryInfoType = "type.googleapis.com/google.rpc.RetryInfo"

	tests := []struct {
		name    string
		details []map[string]any
		want    time.Duration
	}{
		{
			name:    "retry info with parseable delay",
			details: []map[string]any{{"@type": retryInfoType, "retryDelay": "17s"}},
			want:    17 * time.Second,
		},
		{
			name: "retry info among other details",
			details: []map[string]any{
				{"@type": "type.googleapis.com/google.rpc.QuotaFailure"},
				{"@type": retryInfoType, "retryDelay": "1m30s"},
			},
			want: 90 * time.Second,
		},
		{
			name:    "no retry info detail",
			details: []map[string]any{{"@type": "type.googleapis.com/google.rpc.QuotaFailure"}},
			want:    0,
		},
		{
			name:    "retry info with unparseable delay",
			details: []map[string]any{{"@type": retryInfoType, "retryDelay": "soon"}},
			want:    0,
		},
		{
			name:    "retry info missing delay",
			details: []map[string]any{{"@type": retryInfoType}},
			want:    0,
		},
		{name: "no details", details: nil, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, genaiRetryAfter(genai.APIError{Details: tt.details}))
		})
	}
}

func TestTranslate_RateLimitReturnsRateLimitError(t *testing.T) {
	ctx := context.Background()

	// Drive the real genai SDK against a stub endpoint returning a RESOURCE_EXHAUSTED error
	// with a RetryInfo detail, so the SDK's own error decoding is exercised end to end.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"quota",` +
			`"details":[{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"23s"}]}}`))
	}))
	t.Cleanup(server.Close)

	genaiClient, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:      "test-key",
		Backend:     genai.BackendGeminiAPI,
		HTTPOptions: genai.HTTPOptions{BaseURL: server.URL},
	})
	require.NoError(t, err)

	client := &Client{client: genaiClient, model: "gemini-2.5-flash"}

	_, err = client.Translate(ctx, "system prompt", "hello")
	require.Error(t, err)

	var rateLimited *huberrors.RateLimitError
	require.ErrorAs(t, err, &rateLimited)
	assert.Equal(t, 23*time.Second, rateLimited.RetryAfter, "the RetryInfo retryDelay is honored")
}

var sentimentTestSchema = llm.Schema{
	Name: "sentiment",
	Properties: []llm.Property{
		{Name: "label", Type: llm.TypeString, Description: "polarity", Enum: []string{"negative", "neutral", "positive"}},
		{Name: "score", Type: llm.TypeNumber, Description: "polarity score"},
	},
}

func TestCompleteJSON_SendsResponseSchemaAndReturnsJSON(t *testing.T) {
	ctx := context.Background()

	var body map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"role":"model",` +
			`"parts":[{"text":"  {\"label\":\"positive\",\"score\":1.5}  "}]}}]}`))
	}))
	t.Cleanup(server.Close)

	genaiClient, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:      "test-key",
		Backend:     genai.BackendGeminiAPI,
		HTTPOptions: genai.HTTPOptions{BaseURL: server.URL},
	})
	require.NoError(t, err)

	client := &Client{client: genaiClient, model: "gemini-2.5-flash"}

	out, err := client.CompleteJSON(ctx, "classify", "great product", sentimentTestSchema)
	require.NoError(t, err)
	assert.JSONEq(t, `{"label":"positive","score":1.5}`, out, "the JSON text is returned trimmed")

	// The request carries a JSON response MIME type and a standard JSON Schema (responseJsonSchema,
	// not the OpenAPI-subset responseSchema), enforcing the closed-object contract.
	generationConfig := llmtest.MustMap(t, body["generationConfig"], "generationConfig")
	assert.Equal(t, "application/json", generationConfig["responseMimeType"])
	assert.NotContains(t, generationConfig, "responseSchema", "responseSchema must be omitted when responseJsonSchema is set")

	responseSchema := llmtest.MustMap(t, generationConfig["responseJsonSchema"], "responseJsonSchema")
	assert.Equal(t, false, responseSchema["additionalProperties"], "the object is closed")
	assert.ElementsMatch(t, []any{"label", "score"}, responseSchema["required"], "every property is required")

	properties := llmtest.MustMap(t, responseSchema["properties"], "properties")
	assert.Contains(t, properties, "label")
	assert.Contains(t, properties, "score")
}
