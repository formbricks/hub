package workers

import (
	"time"

	"github.com/riverqueue/river"

	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/observability"
	"github.com/formbricks/hub/internal/service"
)

// RiverDeps holds dependencies required to build River workers and queue config.
// When EmbeddingClient is nil, only webhook workers are registered.
type RiverDeps struct {
	// Webhook worker
	WebhooksRepo       webhookDispatchRepo
	WebhookSender      service.WebhookSender
	WebhookHTTPTimeout time.Duration
	WebhookMetrics     observability.WebhookMetrics

	// Embedding worker (optional; if EmbeddingClient is nil, embedding worker is not registered)
	EmbeddingService   feedbackEmbeddingService
	EmbeddingClient    service.EmbeddingClient
	EmbeddingDocPrefix string
	EmbeddingMetrics   observability.EmbeddingMetrics
}

// NewRiverWorkersAndQueues builds River workers and queue config from cfg and deps.
// When deps.EmbeddingClient is nil, only webhook workers are registered and the embeddings queue is not added.
func NewRiverWorkersAndQueues(cfg *config.Config, deps RiverDeps) (*river.Workers, map[string]river.QueueConfig) {
	workers := river.NewWorkers()

	webhookWorker := NewWebhookDispatchWorker(deps.WebhooksRepo, deps.WebhookSender, deps.WebhookHTTPTimeout, deps.WebhookMetrics)
	river.AddWorker(workers, webhookWorker)

	queues := map[string]river.QueueConfig{
		river.QueueDefault: {MaxWorkers: cfg.Webhook.DeliveryMaxConcurrent},
	}

	if deps.EmbeddingClient != nil {
		embeddingWorker := NewFeedbackEmbeddingWorker(deps.EmbeddingService, deps.EmbeddingClient, deps.EmbeddingDocPrefix, deps.EmbeddingMetrics)
		river.AddWorker(workers, embeddingWorker)

		queues[service.EmbeddingsQueueName] = river.QueueConfig{MaxWorkers: cfg.Embedding.MaxConcurrent}
	}

	return workers, queues
}
