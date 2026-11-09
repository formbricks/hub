package workers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/service"
)

type gateProbeClient struct {
	err error
}

func (c *gateProbeClient) CreateEmbedding(context.Context, string) ([]float32, error) {
	vector := make([]float32, models.EmbeddingVectorDimensions)
	vector[0] = 1

	return vector, c.err
}

func (c *gateProbeClient) CreateEmbeddingForQuery(ctx context.Context, text string) ([]float32, error) {
	return c.CreateEmbedding(ctx, text)
}

type blockingJobClient struct {
	started chan struct{}
	release chan struct{}
}

func (c *blockingJobClient) CreateEmbedding(_ context.Context, _ string) ([]float32, error) {
	close(c.started)
	<-c.release

	return make([]float32, models.EmbeddingVectorDimensions), nil
}

func (c *blockingJobClient) CreateEmbeddingForQuery(ctx context.Context, text string) ([]float32, error) {
	return c.CreateEmbedding(ctx, text)
}

func TestEmbeddingQueuesSnoozeBeforeProviderWork(t *testing.T) {
	preflight := service.NewFailedEmbeddingPreflight(service.PreflightAuth)
	store := &mockEmbeddingService{record: textRecord("customer response")}
	feedback := NewFeedbackEmbeddingWorker(store, nil, "", nil)
	feedback.SetPreflight(preflight, 30*time.Second)
	err := feedback.Work(context.Background(), embeddingJob())

	var snooze *river.JobSnoozeError
	require.ErrorAs(t, err, &snooze)
	assert.Equal(t, 30*time.Second, snooze.Duration)

	reconcile := &EmbeddingReconcileWorker{preflight: preflight, snooze: 30 * time.Second}
	err = reconcile.Work(context.Background(), &river.Job[service.EmbeddingReconcileArgs]{})
	require.ErrorAs(t, err, &snooze)
	assert.Equal(t, 30*time.Second, snooze.Duration)
}

func TestEmbeddingCleanupRunsDuringProviderOutage(t *testing.T) {
	store := &mockEmbeddingService{record: textRecord("")}
	worker := NewFeedbackEmbeddingWorker(store, nil, "", nil)
	worker.SetPreflight(service.NewFailedEmbeddingPreflight(service.PreflightAuth), 30*time.Second)
	require.NoError(t, worker.Work(context.Background(), embeddingJob()))
	assert.Equal(t, 1, store.setCalls)
	assert.True(t, store.setEmbeddingNil)
}

func TestEmbeddingPreflightDegradationLetsInFlightJobFinish(t *testing.T) {
	probeClient := &gateProbeClient{}
	preflight := service.NewEmbeddingPreflight(probeClient, 1, time.Second, time.Minute)
	require.Equal(t, service.PreflightReady, preflight.Probe(context.Background()))

	jobClient := &blockingJobClient{started: make(chan struct{}), release: make(chan struct{})}
	store := &mockEmbeddingService{record: textRecord("customer response")}
	worker := NewFeedbackEmbeddingWorker(store, jobClient, "", nil)
	worker.SetPreflight(preflight, 30*time.Second)

	finished := make(chan error, 1)
	go func() { finished <- worker.Work(context.Background(), embeddingJob()) }()

	select {
	case <-jobClient.started:
	case <-time.After(time.Second):
		t.Fatal("embedding job did not reach provider")
	}

	probeClient.err = huberrors.NewTerminalProviderError(huberrors.TerminalReasonLength, errors.New("synthetic input rejected"))

	require.Equal(t, service.PreflightInput, preflight.Probe(context.Background()))
	assert.False(t, preflight.Ready())

	err := worker.Work(context.Background(), embeddingJob())

	var snooze *river.JobSnoozeError
	require.ErrorAs(t, err, &snooze)
	close(jobClient.release)
	require.NoError(t, <-finished)
	assert.Equal(t, 1, store.setCalls)
}
