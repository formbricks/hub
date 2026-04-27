package googleai

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
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
