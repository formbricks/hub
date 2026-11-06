package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRetryingEmbeddingClientRecoversWithoutRestart(t *testing.T) {
	underlying := &preflightClient{vector: testPreflightVector()}
	attempts := 0
	client := &RetryingEmbeddingClient{create: func(context.Context) (EmbeddingClient, error) {
		attempts++
		if attempts == 1 {
			return nil, errors.New("temporary credential discovery failure")
		}

		return underlying, nil
	}}
	preflight := NewEmbeddingPreflight(client, 1, time.Second, time.Minute)
	assert.Equal(t, PreflightAuth, preflight.Probe(context.Background()))
	assert.False(t, preflight.Ready())
	assert.Equal(t, PreflightReady, preflight.Probe(context.Background()))
	assert.True(t, preflight.Ready())

	_, err := client.CreateEmbedding(context.Background(), "record")
	require.NoError(t, err)
	assert.Equal(t, 2, attempts)
}
