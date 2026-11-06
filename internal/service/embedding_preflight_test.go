package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	openaisdk "github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"

	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/openai"
)

type preflightClient struct {
	vector      []float32
	err         error
	batchCalls  int
	singleCalls int
}

func testPreflightVector() []float32 {
	vector := make([]float32, models.EmbeddingVectorDimensions)
	vector[0] = 1

	return vector
}

func (c *preflightClient) CreateEmbedding(_ context.Context, _ string) ([]float32, error) {
	c.singleCalls++

	return c.vector, c.err
}

func (c *preflightClient) CreateEmbeddingForQuery(ctx context.Context, input string) ([]float32, error) {
	return c.CreateEmbedding(ctx, input)
}

func (c *preflightClient) CreateEmbeddings(_ context.Context, _ []string) ([][]float32, error) {
	c.batchCalls++
	if c.err != nil {
		return nil, c.err
	}

	return [][]float32{c.vector, c.vector}, nil
}

func TestEmbeddingPreflightUsesExactEndpointAndModel(t *testing.T) {
	var requests int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++

		assert.Equal(t, "/custom/v1/embeddings", r.URL.Path)
		assert.Equal(t, "Bearer synthetic-key", r.Header.Get("Authorization"))

		var request struct {
			Model      string `json:"model"`
			Input      string `json:"input"`
			Dimensions int    `json:"dimensions"`
		}
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		assert.Equal(t, "configured-model", request.Model)
		assert.Equal(t, embeddingPreflightInput, request.Input)
		assert.Equal(t, models.EmbeddingVectorDimensions, request.Dimensions)
		w.Header().Set("Content-Type", "application/json")

		vector := make([]float64, models.EmbeddingVectorDimensions)
		vector[0] = 1
		assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"object": "list", "model": request.Model,
			"data":  []any{map[string]any{"object": "embedding", "index": 0, "embedding": vector}},
			"usage": map[string]any{"prompt_tokens": 1, "total_tokens": 1},
		}))
	}))
	t.Cleanup(server.Close)
	client := openai.NewClient("synthetic-key", openai.WithBaseURL(server.URL+"/custom/v1"),
		openai.WithModel("configured-model"))
	preflight := NewEmbeddingPreflight(client, 1, time.Second, time.Minute)
	require.Equal(t, PreflightReady, preflight.Probe(context.Background()))
	assert.True(t, preflight.Ready())
	assert.Equal(t, 1, requests)
}

func TestEmbeddingPreflightRejectsResponseFromWrongModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"object": "list", "model": "other-model",
			"data": []any{map[string]any{
				"object": "embedding", "index": 0,
				"embedding": make([]float64, models.EmbeddingVectorDimensions),
			}},
			"usage": map[string]any{"prompt_tokens": 1, "total_tokens": 1},
		}))
	}))
	t.Cleanup(server.Close)
	client := openai.NewClient("synthetic-key", openai.WithBaseURL(server.URL),
		openai.WithModel("configured-model"))
	preflight := NewEmbeddingPreflight(client, 1, time.Second, time.Minute)
	assert.Equal(t, PreflightModel, preflight.Probe(context.Background()))
	assert.False(t, preflight.Ready())
}

func TestEmbeddingPreflightClassifiesProviderHTTPResponses(t *testing.T) {
	cases := []struct {
		name   string
		status int
		code   string
		reason EmbeddingPreflightReason
	}{
		{"authentication", http.StatusUnauthorized, "invalid_api_key", PreflightAuth},
		{"permission", http.StatusForbidden, "permission_denied", PreflightPermission},
		{"model", http.StatusNotFound, "model_not_found", PreflightModel},
		{"endpoint", http.StatusNotFound, "not_found", PreflightEndpoint},
		{"input", http.StatusBadRequest, "invalid_input", PreflightInput},
		{"rate limit", http.StatusTooManyRequests, "rate_limit_exceeded", PreflightRateLimited},
		{"server error", http.StatusServiceUnavailable, "unavailable", PreflightProvider},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(test.status)
				assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{
					"error": map[string]any{"code": test.code, "message": test.code},
				}))
			}))
			t.Cleanup(server.Close)
			client := openai.NewClient("synthetic-key", openai.WithBaseURL(server.URL),
				openai.WithModel("configured-model"))
			preflight := NewEmbeddingPreflight(client, 1, time.Second, time.Minute)
			assert.Equal(t, test.reason, preflight.Probe(context.Background()))
			assert.False(t, preflight.Ready())
		})
	}
}

func TestEmbeddingPreflightClassifiesUnavailableEndpointAndTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	client := openai.NewClient("synthetic-key", openai.WithBaseURL(server.URL),
		openai.WithModel("configured-model"))
	preflight := NewEmbeddingPreflight(client, 1, 20*time.Millisecond, time.Minute)
	assert.Equal(t, PreflightTimeout, preflight.Probe(context.Background()))
	server.Close()
	assert.Equal(t, PreflightEndpoint, preflight.Probe(context.Background()))
}

func TestEmbeddingPreflightBatchAndExpiry(t *testing.T) {
	client := &preflightClient{vector: testPreflightVector()}
	preflight := NewEmbeddingPreflight(client, 4, time.Second, 20*time.Millisecond)
	require.Equal(t, PreflightReady, preflight.Probe(context.Background()))
	assert.Equal(t, 1, client.batchCalls)
	assert.Zero(t, client.singleCalls)
	client.err = huberrors.NewRateLimitError(time.Second, errors.New("secret customer input"))

	assert.Equal(t, PreflightRateLimited, preflight.Probe(context.Background()))
	assert.True(t, preflight.Ready(), "one transient failure retains the bounded last success")
	time.Sleep(25 * time.Millisecond)
	assert.False(t, preflight.Ready())
	assert.Equal(t, PreflightRateLimited, preflight.Reason())
	assert.NotContains(t, string(preflight.Reason()), "secret")

	client.err = nil

	assert.Equal(t, PreflightReady, preflight.Probe(context.Background()))
	assert.True(t, preflight.Ready())
}

func TestEmbeddingPreflightRejectsDimensionAndMissingBatch(t *testing.T) {
	client := &preflightClient{vector: make([]float32, models.EmbeddingVectorDimensions-1)}
	preflight := NewEmbeddingPreflight(client, 1, time.Second, time.Minute)
	assert.Equal(t, PreflightDimension, preflight.Probe(context.Background()))
	assert.False(t, preflight.Ready())
	preflight = NewEmbeddingPreflight(singleOnlyPreflightClient{client: client}, 2, time.Second, time.Minute)
	assert.Equal(t, PreflightInvalidConfig, preflight.Probe(context.Background()))
	assert.Zero(t, client.batchCalls)
}

func TestEmbeddingPreflightRejectsInvalidVectorValues(t *testing.T) {
	client := &preflightClient{vector: make([]float32, models.EmbeddingVectorDimensions)}
	preflight := NewEmbeddingPreflight(client, 1, time.Second, time.Minute)
	assert.Equal(t, PreflightProvider, preflight.Probe(context.Background()), "zero vector is unusable for cosine search")
	assert.False(t, preflight.Ready())
}

type singleOnlyPreflightClient struct{ client *preflightClient }

func (c singleOnlyPreflightClient) CreateEmbedding(ctx context.Context, input string) ([]float32, error) {
	return c.client.CreateEmbedding(ctx, input)
}

func (c singleOnlyPreflightClient) CreateEmbeddingForQuery(ctx context.Context, input string) ([]float32, error) {
	return c.client.CreateEmbeddingForQuery(ctx, input)
}

func TestClassifyEmbeddingPreflightError(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		reason EmbeddingPreflightReason
	}{
		{"auth", &openaisdk.Error{StatusCode: 401}, PreflightAuth},
		{"permission", genai.APIError{Code: 403, Status: "PERMISSION_DENIED"}, PreflightPermission},
		{"model", &openaisdk.Error{StatusCode: 404, Code: "model_not_found"}, PreflightModel},
		{"endpoint", &openaisdk.Error{StatusCode: 404}, PreflightEndpoint},
		{"dimension", &openaisdk.Error{StatusCode: 422, Message: "unsupported dimensions"}, PreflightDimension},
		{"input", huberrors.NewTerminalProviderError(huberrors.TerminalReasonLength, errors.New("secret input")), PreflightInput},
		{"rate limit", huberrors.NewRateLimitError(time.Second, errors.New("overloaded")), PreflightRateLimited},
		{"timeout", context.DeadlineExceeded, PreflightTimeout},
		{"server error", &openaisdk.Error{StatusCode: 503}, PreflightProvider},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) { assert.Equal(t, test.reason, ClassifyEmbeddingPreflightError(test.err)) })
	}
}
