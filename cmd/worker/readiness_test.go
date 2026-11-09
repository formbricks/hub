package main

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/service"
)

func TestEmbeddingReadinessFile(t *testing.T) {
	t.Setenv("HUB_EMBEDDING_READINESS_FILE", filepath.Join(t.TempDir(), "ready"))
	require.NoError(t, setEmbeddingReadiness(time.Time{}))
	assert.False(t, checkEmbeddingReadiness())
	require.NoError(t, setEmbeddingReadiness(time.Now().Add(time.Minute)))
	assert.True(t, checkEmbeddingReadiness())
	require.NoError(t, os.WriteFile(embeddingReadinessFile(),
		[]byte(strconv.FormatInt(time.Now().Add(-time.Minute).UnixNano(), 10)), 0o600))
	assert.False(t, checkEmbeddingReadiness(), "an old marker cannot outlive preflight validity")
	require.NoError(t, setEmbeddingReadiness(time.Now().Add(-time.Minute)))
	assert.False(t, checkEmbeddingReadiness())
	require.NoError(t, setEmbeddingReadiness(time.Time{}))
	assert.False(t, checkEmbeddingReadiness())
}

func TestEmbeddingReadinessFileDisabled(t *testing.T) {
	t.Setenv("HUB_EMBEDDING_READINESS_FILE", "")
	assert.Empty(t, embeddingReadinessFile())
	require.NoError(t, setEmbeddingReadiness(time.Now().Add(time.Minute)))
	assert.False(t, checkEmbeddingReadiness())
}

func TestEmbeddingReadinessProbeUsesDotEnv(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("EMBEDDING_PROVIDER", "")
	t.Setenv("EMBEDDING_MODEL", "")
	t.Setenv("HUB_EMBEDDING_READINESS_FILE", filepath.Join(t.TempDir(), "ready"))

	args := os.Args
	os.Args = []string{"hub-worker", "--check-embedding-readiness"}

	t.Cleanup(func() { os.Args = args })
	assert.Equal(t, exitSuccess, run(), "disabled embeddings need no state file")
	require.NoError(t, os.WriteFile(".env", []byte("EMBEDDING_PROVIDER=openai\nEMBEDDING_MODEL=model\n"), 0o600))
	assert.Equal(t, exitFailure, run(), ".env enables embeddings and missing state fails")
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
