package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
