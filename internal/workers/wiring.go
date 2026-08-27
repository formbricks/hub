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

	// ReconcileSweeper runs the level-triggered enrichment sweep. nil leaves the reconciler out
	// entirely — the kill switch, and the shape a deployment with no enrichment provider takes.
	ReconcileSweeper ReconcileSweeper

	// Failures records the durable marker a classify worker writes when it gives up on a record.
	// Shared by the three classify pipelines; nil disables recording, which leaves the API
	// under-reporting failures but changes no enrichment behaviour.
	Failures FailureRecorder
	// FailureMetrics counts permanent give-ups by cause, for whoever watches the deployment
	// rather than a single tenant. nil disables it.
	FailureMetrics observability.EnrichmentFailureMetrics
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
	// The backfill lanes get their own, smaller budget. Reconciled work is by definition not urgent
	// — nobody is watching a record that has been stranded for a week — so it drains in the
	// background at a rate that cannot crowd out a record submitted a moment ago.
	reconcileWorkers := func(configured int) int {
		if configured <= 0 {
			return 1
		}

		return configured
	}

	maxTranslation := cfg.Translation.MaxConcurrent
	maxSentiment := cfg.Sentiment.MaxConcurrent
	maxEmotions := cfg.Emotions.MaxConcurrent

	purgeWorker := NewFeedbackRecordsPurgeWorker(deps.FeedbackRecordsPurgeService)
	river.AddWorker(workers, purgeWorker)

	queues := map[string]river.QueueConfig{
		river.QueueDefault: {MaxWorkers: maxDefault},
		// One purge at a time per replica (River's MaxWorkers is per-client, so N hub-worker replicas
		// can still run N purges — for different tenants, which is fine). Each purge is a long
		// sequence of batched deletes holding its tenant's write lock, so stacking several on one
		// replica would multiply that load on Postgres for no gain: purges are rare and
		// admin-initiated, and a queued one loses nothing by waiting.
		service.FeedbackRecordsPurgeQueueName: {MaxWorkers: feedbackRecordsPurgeMaxWorkers},
	}

	if deps.EmbeddingClient != nil {
		embeddingWorker := NewFeedbackEmbeddingWorker(deps.EmbeddingService, deps.EmbeddingClient, deps.EmbeddingDocPrefix, deps.EmbeddingMetrics)
		river.AddWorker(workers, embeddingWorker)

		queues[service.EmbeddingsQueueName] = river.QueueConfig{MaxWorkers: maxEmbedding}
	}

	if deps.TranslationClient != nil {
		translationWorker := NewFeedbackTranslationWorker(deps.TranslationService, deps.TranslationClient, deps.TranslationMetrics,
			deps.Failures, deps.FailureMetrics)
		river.AddWorker(workers, translationWorker)

		queues[service.TranslationsQueueName] = river.QueueConfig{MaxWorkers: maxTranslation}
		queues[service.TranslationsReconcileQueueName] = river.QueueConfig{MaxWorkers: reconcileWorkers(cfg.Translation.ReconcileMaxConcurrent)}

		backfillWorker := NewTenantTranslationBackfillWorker(deps.TranslationBackfillService, deps.TranslationMaxAttempts)
		river.AddWorker(workers, backfillWorker)

		queues[service.TranslationBackfillsQueueName] = river.QueueConfig{MaxWorkers: maxTranslation}
	}

	if deps.ReconcileSweeper != nil {
		river.AddWorker(workers, NewEnrichmentReconcileWorker(deps.ReconcileSweeper))

		// MaxWorkers 1: one sweep at a time, structurally. The job's uniqueness already collapses
		// overlapping ticks, but a queue that cannot run two makes that true even if the unique
		// options are ever loosened.
		queues[service.EnrichmentReconcileQueueName] = river.QueueConfig{MaxWorkers: 1}
	}

	if deps.SentimentClient != nil {
		sentimentWorker := NewFeedbackSentimentWorker(
			deps.SentimentService, deps.SentimentResolver, deps.SentimentClient, deps.SentimentMetrics,
			deps.Failures, deps.FailureMetrics)
		river.AddWorker(workers, sentimentWorker)

		queues[service.SentimentsQueueName] = river.QueueConfig{MaxWorkers: maxSentiment}
		queues[service.SentimentsReconcileQueueName] = river.QueueConfig{MaxWorkers: reconcileWorkers(cfg.Sentiment.ReconcileMaxConcurrent)}
	}

	if deps.EmotionsClient != nil {
		emotionsWorker := NewFeedbackEmotionsWorker(
			deps.EmotionsService, deps.EmotionsResolver, deps.EmotionsClient, deps.EmotionsMetrics,
			deps.Failures, deps.FailureMetrics)
		river.AddWorker(workers, emotionsWorker)

		queues[service.EmotionsQueueName] = river.QueueConfig{MaxWorkers: maxEmotions}
		queues[service.EmotionsReconcileQueueName] = river.QueueConfig{MaxWorkers: reconcileWorkers(cfg.Emotions.ReconcileMaxConcurrent)}
	}

	return workers, queues
}
