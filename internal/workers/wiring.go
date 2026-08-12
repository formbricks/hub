package workers

import (
	"time"

	"github.com/riverqueue/river"

	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/observability"
	"github.com/formbricks/hub/internal/service"
)

// RiverDeps holds dependencies required to build River workers and queue config.
// Each optional group is gated on its client: leave a client nil and neither its worker nor its
// queue is registered.
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
	SentimentService  sentimentWorkerService
	SentimentResolver tenantSettingsReader
	SentimentClient   service.SentimentClient
	SentimentMetrics  observability.SentimentMetrics

	// Emotions worker (optional; if EmotionsClient is nil, emotions worker is not registered)
	EmotionsService  emotionsWorkerService
	EmotionsResolver tenantSettingsReader
	EmotionsClient   service.EmotionsClient
	EmotionsMetrics  observability.EmotionsMetrics

	// Feedback-records purge worker (always registered; the purge is a core tenant operation, not
	// an enrichment, so it has no client to gate on).
	FeedbackRecordsPurgeService feedbackRecordsPurgeService
}

// NewRiverWorkersAndQueues builds River workers and queue config from cfg and deps. Each optional
// worker is registered only when its client is set, and its queue is declared alongside it, so a
// disabled enrichment leaves no queue for jobs to pile up on unworked.
//
// Only hub-worker calls this: the API inserts jobs through an insert-only River client and registers
// nothing (see cmd/api/app.go), so the queue MaxWorkers below are always the real configured
// concurrency.
func NewRiverWorkersAndQueues(
	cfg *config.Config, deps RiverDeps,
) (*river.Workers, map[string]river.QueueConfig) {
	workers := river.NewWorkers()

	webhookWorker := NewWebhookDispatchWorker(deps.WebhooksRepo, deps.WebhookSender, deps.WebhookHTTPTimeout, deps.WebhookMetrics)
	river.AddWorker(workers, webhookWorker)

	maxDefault := cfg.Webhook.DeliveryMaxConcurrent
	maxEmbedding := cfg.Embedding.MaxConcurrent
	maxTranslation := cfg.Translation.MaxConcurrent
	maxSentiment := cfg.Sentiment.MaxConcurrent
	maxEmotions := cfg.Emotions.MaxConcurrent

	purgeWorker := NewFeedbackRecordsPurgeWorker(deps.FeedbackRecordsPurgeService)
	river.AddWorker(workers, purgeWorker)

	queues := map[string]river.QueueConfig{
		river.QueueDefault: {MaxWorkers: maxDefault},
		// Purges run one at a time. Each one is an unbounded delete that holds its tenant's write
		// lock exclusively, so running several concurrently would multiply that load on Postgres for
		// no gain — purges are rare and admin-initiated, and a queued one loses nothing by waiting.
		service.FeedbackRecordsPurgeQueueName: {MaxWorkers: feedbackRecordsPurgeMaxWorkers},
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
		sentimentWorker := NewFeedbackSentimentWorker(
			deps.SentimentService, deps.SentimentResolver, deps.SentimentClient, deps.SentimentMetrics)
		river.AddWorker(workers, sentimentWorker)

		queues[service.SentimentsQueueName] = river.QueueConfig{MaxWorkers: maxSentiment}
	}

	if deps.EmotionsClient != nil {
		emotionsWorker := NewFeedbackEmotionsWorker(
			deps.EmotionsService, deps.EmotionsResolver, deps.EmotionsClient, deps.EmotionsMetrics)
		river.AddWorker(workers, emotionsWorker)

		queues[service.EmotionsQueueName] = river.QueueConfig{MaxWorkers: maxEmotions}
	}

	return workers, queues
}
