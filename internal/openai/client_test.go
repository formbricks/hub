package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/huberrors"
)

type embeddingRequest struct {
	Input      string `json:"input"`
	Model      string `json:"model"`
	Dimensions int    `json:"dimensions"`
}

func newEmbeddingServer(t *testing.T, embedding []float64) (*httptest.Server, *atomic.Int32) {
	t.Helper()

	var hitCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitCount.Add(1)
		assert.Equal(t, "/v1/embeddings", r.URL.Path)
		assert.Equal(t, "Bearer sk-test", r.Header.Get("Authorization"))

		var req embeddingRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request body: %v", err)
			http.Error(w, "invalid request", http.StatusBadRequest)

			return
		}

		assert.Equal(t, "hello world", req.Input)
		assert.Equal(t, "test-model", req.Model)
		assert.Equal(t, 2, req.Dimensions)

		w.Header().Set("Content-Type", "application/json")

		if err := json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"model":  req.Model,
			"data": []map[string]any{
				{
					"object":    "embedding",
					"index":     0,
					"embedding": embedding,
				},
			},
			"usage": map[string]any{
				"prompt_tokens": 1,
				"total_tokens":  1,
			},
		}); err != nil {
			t.Errorf("encode response body: %v", err)
		}
	}))

	t.Cleanup(server.Close)

	return server, &hitCount
}

func TestCreateEmbedding_UsesExplicitBaseURLOverEnvironment(t *testing.T) {
	envServer, envHits := newEmbeddingServer(t, []float64{9, 9})
	explicitServer, explicitHits := newEmbeddingServer(t, []float64{1, 2})

	t.Setenv("OPENAI_BASE_URL", envServer.URL+"/v1")

	client := NewClient("sk-test",
		WithBaseURL(explicitServer.URL+"/v1"),
		WithDimensions(2),
		WithModel("test-model"),
	)

	embedding, err := client.CreateEmbedding(context.Background(), "hello world")
	require.NoError(t, err)
	assert.Equal(t, []float32{1, 2}, embedding)
	assert.Equal(t, int32(0), envHits.Load())
	assert.Equal(t, int32(1), explicitHits.Load())
}

func TestCreateEmbedding_UsesEnvironmentBaseURLWhenExplicitBaseURLIsUnset(t *testing.T) {
	envServer, envHits := newEmbeddingServer(t, []float64{3, 4})

	t.Setenv("OPENAI_BASE_URL", envServer.URL+"/v1")

	client := NewClient("sk-test",
		WithDimensions(2),
		WithModel("test-model"),
	)

	embedding, err := client.CreateEmbedding(context.Background(), "hello world")
	require.NoError(t, err)
	assert.Equal(t, []float32{3, 4}, embedding)
	assert.Equal(t, int32(1), envHits.Load())
}

// newChatCompletionServer drives the real SDK against a stub /v1/chat/completions endpoint so
// the translation error paths exercise the SDK's own response decoding, not a hand-built error.
func newChatCompletionServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/chat/completions", r.URL.Path)
		handler(w, r)
	}))
	t.Cleanup(server.Close)

	return server
}

func TestTranslate_RateLimitReturnsRateLimitError(t *testing.T) {
	// x-should-retry:false stops the SDK's own retry/backoff, so the 429 surfaces immediately.
	server := newChatCompletionServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "12")
		w.Header().Set("X-Should-Retry", "false")
		w.WriteHeader(http.StatusTooManyRequests)
	})

	client := NewClient("sk-test", WithBaseURL(server.URL+"/v1"), WithModel("test-model"))

	_, err := client.Translate(context.Background(), "system prompt", "hello")
	require.Error(t, err)

	var rateLimited *huberrors.RateLimitError
	require.ErrorAs(t, err, &rateLimited)
	assert.Equal(t, 12*time.Second, rateLimited.RetryAfter, "the Retry-After header is honored")
}

func TestTranslate_RateLimitWithoutRetryAfterHeader(t *testing.T) {
	server := newChatCompletionServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Should-Retry", "false")
		w.WriteHeader(http.StatusTooManyRequests)
	})

	client := NewClient("sk-test", WithBaseURL(server.URL+"/v1"), WithModel("test-model"))

	_, err := client.Translate(context.Background(), "system prompt", "hello")

	var rateLimited *huberrors.RateLimitError
	require.ErrorAs(t, err, &rateLimited)
	assert.Equal(t, time.Duration(0), rateLimited.RetryAfter, "no header means no hint, not a parse error")
}

func TestTranslate_NonRateLimitErrorIsNotWrapped(t *testing.T) {
	server := newChatCompletionServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	})

	client := NewClient("sk-test", WithBaseURL(server.URL+"/v1"), WithModel("test-model"))

	_, err := client.Translate(context.Background(), "system prompt", "hello")
	require.Error(t, err)

	var rateLimited *huberrors.RateLimitError
	assert.NotErrorAs(t, err, &rateLimited, "a non-429 error must not be classified as rate-limited")
}

func TestTranslate_Success(t *testing.T) {
	server := newChatCompletionServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if err := json.NewEncoder(w).Encode(map[string]any{
			"id":     "chatcmpl-test",
			"object": "chat.completion",
			"model":  "test-model",
			"choices": []map[string]any{{
				"index":         0,
				"finish_reason": "stop",
				"message":       map[string]any{"role": "assistant", "content": "  Hola mundo  "},
			}},
		}); err != nil {
			t.Errorf("encode response body: %v", err)
		}
	})

	client := NewClient("sk-test", WithBaseURL(server.URL+"/v1"), WithModel("test-model"))

	out, err := client.Translate(context.Background(), "system prompt", "hello")
	require.NoError(t, err)
	assert.Equal(t, "Hola mundo", out, "the assistant message is returned trimmed")
}
