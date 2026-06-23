package googleai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"

	"github.com/formbricks/hub/internal/huberrors"
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
