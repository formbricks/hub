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

	// Translation worker (optional; if TranslationClient is nil, translation worker is not registered)
	TranslationService translationWorkerService
	TranslationClient  service.TranslationClient
	TranslationMetrics observability.TranslationMetrics
	// Per-tenant translation backfill worker (registered alongside the translation worker).
	TranslationBackfillService tenantTranslationBackfillService
	TranslationMaxAttempts     int

	// Sentiment worker (optional; if SentimentClient is nil, sentiment worker is not registered)
	SentimentService sentimentWorkerService
	SentimentClient  service.SentimentClient
	SentimentMetrics observability.SentimentMetrics
}

// NewRiverWorkersAndQueues builds River workers and queue config from cfg and deps.
// When deps.EmbeddingClient is nil, only webhook workers are registered and the embeddings queue is not added.
// When placeholderMaxWorkers > 0 (e.g. 1 for insert-only API), all queue MaxWorkers use it; otherwise use cfg.
func NewRiverWorkersAndQueues(
	cfg *config.Config, deps RiverDeps, placeholderMaxWorkers int,
) (*river.Workers, map[string]river.QueueConfig) {
	workers := river.NewWorkers()

	webhookWorker := NewWebhookDispatchWorker(deps.WebhooksRepo, deps.WebhookSender, deps.WebhookHTTPTimeout, deps.WebhookMetrics)
	river.AddWorker(workers, webhookWorker)

	maxDefault := cfg.Webhook.DeliveryMaxConcurrent
	maxEmbedding := cfg.Embedding.MaxConcurrent
	maxTranslation := cfg.Translation.MaxConcurrent
	maxSentiment := cfg.Sentiment.MaxConcurrent

	if placeholderMaxWorkers > 0 {
		maxDefault = placeholderMaxWorkers
		maxEmbedding = placeholderMaxWorkers
		maxTranslation = placeholderMaxWorkers
		maxSentiment = placeholderMaxWorkers
	}

	queues := map[string]river.QueueConfig{
		river.QueueDefault: {MaxWorkers: maxDefault},
	}

	if deps.EmbeddingClient != nil {
		embeddingWorker := NewFeedbackEmbeddingWorker(deps.EmbeddingService, deps.EmbeddingClient, deps.EmbeddingDocPrefix, deps.EmbeddingMetrics)
		river.AddWorker(workers, embeddingWorker)

		queues[service.EmbeddingsQueueName] = river.QueueConfig{MaxWorkers: maxEmbedding}
	}

	if deps.TranslationClient != nil {
		translationWorker := NewFeedbackTranslationWorker(deps.TranslationService, deps.TranslationClient, deps.TranslationMetrics)
		river.AddWorker(workers, translationWorker)

		queues[service.TranslationsQueueName] = river.QueueConfig{MaxWorkers: maxTranslation}

		backfillWorker := NewTenantTranslationBackfillWorker(deps.TranslationBackfillService, deps.TranslationMaxAttempts)
		river.AddWorker(workers, backfillWorker)

		queues[service.TranslationBackfillsQueueName] = river.QueueConfig{MaxWorkers: maxTranslation}
	}

	if deps.SentimentClient != nil {
		sentimentWorker := NewFeedbackSentimentWorker(deps.SentimentService, deps.SentimentClient, deps.SentimentMetrics)
		river.AddWorker(workers, sentimentWorker)

		queues[service.SentimentsQueueName] = river.QueueConfig{MaxWorkers: maxSentiment}
	}

	return workers, queues
}
