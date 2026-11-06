package main

import (
	"path/filepath"
	"testing"

	"github.com/riverqueue/river"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/service"
)

func TestEmbeddingReadinessFile(t *testing.T) {
	t.Setenv("HUB_EMBEDDING_READINESS_FILE", filepath.Join(t.TempDir(), "ready"))
	require.NoError(t, setEmbeddingReadiness(false))
	assert.False(t, checkEmbeddingReadiness())
	require.NoError(t, setEmbeddingReadiness(true))
	assert.True(t, checkEmbeddingReadiness())
	require.NoError(t, setEmbeddingReadiness(false))
	assert.False(t, checkEmbeddingReadiness())
}

func TestSplitEmbeddingQueues(t *testing.T) {
	queues := map[string]river.QueueConfig{
		river.QueueDefault:                   {MaxWorkers: 2},
		service.EmbeddingsQueueName:          {MaxWorkers: 3},
		service.EmbeddingsReconcileQueueName: {MaxWorkers: 1},
		service.EmbeddingReconcileQueueName:  {MaxWorkers: 1},
		service.TranslationsQueueName:        {MaxWorkers: 4},
	}
	core, embedding := splitEmbeddingQueues(queues)
	assert.Len(t, core, 2)
	assert.Len(t, embedding, 3)
	assert.Contains(t, core, river.QueueDefault)
	assert.Contains(t, core, service.TranslationsQueueName)
	assert.NotContains(t, core, service.EmbeddingsQueueName)
	assert.Contains(t, embedding, service.EmbeddingsQueueName)
}
