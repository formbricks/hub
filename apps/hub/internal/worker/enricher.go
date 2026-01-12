// Package worker provides background job processing for AI enrichment and embedding generation.
// The Enricher polls the job queue and processes jobs concurrently using a configurable
// number of worker goroutines.
package worker

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/formbricks/hub/apps/hub/internal/embedding"
	"github.com/formbricks/hub/apps/hub/internal/enrichment"
	"github.com/formbricks/hub/apps/hub/internal/ent"
	"github.com/formbricks/hub/apps/hub/internal/models"
	"github.com/formbricks/hub/apps/hub/internal/queue"
	"github.com/formbricks/hub/apps/hub/internal/webhook"
	"github.com/google/uuid"
)

// Enricher processes enrichment and embedding jobs from the queue
type Enricher struct {
	queue         queue.Queue
	enrichmentSvc *enrichment.Service
	embeddingSvc  *embedding.Service
	db            *ent.Client
	dispatcher    *webhook.Dispatcher
	workers       int
	pollInterval  time.Duration
	logger        *slog.Logger
	stopChan      chan struct{}
	doneChan      chan struct{}
}

// NewEnricher creates a new Enricher worker pool
func NewEnricher(
	q queue.Queue,
	enrichmentService *enrichment.Service,
	embeddingService *embedding.Service,
	db *ent.Client,
	dispatcher *webhook.Dispatcher,
	workers int,
	pollInterval time.Duration,
	logger *slog.Logger,
) *Enricher {
	return &Enricher{
		queue:         q,
		enrichmentSvc: enrichmentService,
		embeddingSvc:  embeddingService,
		db:            db,
		dispatcher:    dispatcher,
		workers:       workers,
		pollInterval:  pollInterval,
		logger:        logger,
		stopChan:      make(chan struct{}),
		doneChan:      make(chan struct{}),
	}
}

// Start begins processing jobs from the queue with the configured number of workers
func (e *Enricher) Start(ctx context.Context) {
	e.logger.Info("starting enrichment worker pool",
		"workers", e.workers,
		"poll_interval", e.pollInterval)

	// Start worker goroutines
	for i := 0; i < e.workers; i++ {
		go e.worker(ctx, i+1)
	}

	// Wait for context cancellation or stop signal
	select {
	case <-ctx.Done():
		e.logger.Info("enrichment workers shutting down...")
	case <-e.stopChan:
		e.logger.Info("enrichment workers stopped")
	}

	close(e.doneChan)
}

// Stop gracefully stops all workers
func (e *Enricher) Stop() {
	close(e.stopChan)
	<-e.doneChan
}

// worker is a single worker goroutine that polls for and processes jobs
func (e *Enricher) worker(ctx context.Context, workerID int) {
	ticker := time.NewTicker(e.pollInterval)
	defer ticker.Stop()

	e.logger.Debug("worker started", "worker_id", workerID)

	for {
		select {
		case <-ctx.Done():
			e.logger.Debug("worker stopping", "worker_id", workerID)
			return
		case <-e.stopChan:
			e.logger.Debug("worker stopping", "worker_id", workerID)
			return
		case <-ticker.C:
			// Poll for a job
			job, err := e.queue.Dequeue(ctx)
			if err != nil {
				e.logger.Error("failed to dequeue job",
					"worker_id", workerID,
					"error", err)
				continue
			}

			// No jobs available
			if job == nil {
				continue
			}

			// Process the job
			e.processJob(ctx, workerID, job)
		}
	}
}

// processJob handles processing for a single job (enrichment or embedding)
func (e *Enricher) processJob(ctx context.Context, workerID int, job *queue.EnrichmentJob) {
	switch job.JobType {
	case queue.JobTypeEnrichment:
		e.processEnrichmentJob(ctx, workerID, job)
	case queue.JobTypeEmbedding:
		e.processEmbeddingJob(ctx, workerID, job)
	default:
		e.logger.Error("unknown job type",
			"worker_id", workerID,
			"job_id", job.ID,
			"job_type", job.JobType)
		_ = e.queue.MarkFailed(ctx, job.ID, fmt.Errorf("unknown job type: %s", job.JobType))
	}
}

// processEnrichmentJob handles sentiment/emotion/topics enrichment
func (e *Enricher) processEnrichmentJob(ctx context.Context, workerID int, job *queue.EnrichmentJob) {
	e.logger.Info("processing enrichment job",
		"worker_id", workerID,
		"job_id", job.ID,
		"feedback_record_id", job.FeedbackRecordID)

	// Skip if enrichment service is not available
	if e.enrichmentSvc == nil {
		e.logger.Warn("enrichment service not configured, skipping job",
			"worker_id", workerID,
			"job_id", job.ID)
		// Mark as complete since there's no work to do
		_ = e.queue.MarkComplete(ctx, job.ID)
		return
	}

	// Enrich the text
	result, err := e.enrichmentSvc.EnrichText(ctx, job.Text)
	if err != nil {
		e.logger.Warn("enrichment failed",
			"worker_id", workerID,
			"job_id", job.ID,
			"error", err)

		// Mark job as failed
		if markErr := e.queue.MarkFailed(ctx, job.ID, err); markErr != nil {
			e.logger.Error("failed to mark job as failed",
				"job_id", job.ID,
				"error", markErr)
		}
		return
	}

	// Update feedback record with enrichment results
	frID, err := uuid.Parse(job.FeedbackRecordID)
	if err != nil {
		e.logger.Error("invalid feedback record ID",
			"feedback_record_id", job.FeedbackRecordID,
			"error", err)
		_ = e.queue.MarkFailed(ctx, job.ID, err)
		return
	}

	err = e.db.FeedbackRecord.
		UpdateOneID(frID).
		SetSentiment(result.Sentiment).
		SetSentimentScore(result.SentimentScore).
		SetEmotion(result.Emotion).
		SetTopics(result.Topics).
		Exec(ctx)

	if err != nil {
		e.logger.Error("failed to update feedback record with enrichment",
			"worker_id", workerID,
			"feedback_record_id", job.FeedbackRecordID,
			"error", err)

		if markErr := e.queue.MarkFailed(ctx, job.ID, err); markErr != nil {
			e.logger.Error("failed to mark job as failed",
				"job_id", job.ID,
				"error", markErr)
		}
		return
	}

	// Fetch the complete enriched feedback record record
	enrichedFR, err := e.db.FeedbackRecord.Get(ctx, frID)
	if err != nil {
		e.logger.Error("failed to fetch enriched feedback record",
			"worker_id", workerID,
			"feedback_record_id", job.FeedbackRecordID,
			"error", err)
		// Still mark job as complete since enrichment was saved
		_ = e.queue.MarkComplete(ctx, job.ID)
		return
	}

	// Convert to domain model for webhook
	enrichedModel := models.FromEnt(enrichedFR)

	// Dispatch feedback_record.enriched webhook
	e.dispatcher.DispatchAsync(webhook.EventFeedbackRecordEnriched, enrichedModel)

	// Mark job as complete
	if err := e.queue.MarkComplete(ctx, job.ID); err != nil {
		e.logger.Error("failed to mark job as complete",
			"job_id", job.ID,
			"error", err)
		return
	}

	e.logger.Info("enrichment completed successfully",
		"worker_id", workerID,
		"job_id", job.ID,
		"feedback_record_id", job.FeedbackRecordID,
		"sentiment", result.Sentiment)
}

// processEmbeddingJob handles vector embedding generation
func (e *Enricher) processEmbeddingJob(ctx context.Context, workerID int, job *queue.EnrichmentJob) {
	e.logger.Info("processing embedding job",
		"worker_id", workerID,
		"job_id", job.ID,
		"feedback_record_id", job.FeedbackRecordID)

	// Skip if embedding service is not available
	if e.embeddingSvc == nil {
		e.logger.Warn("embedding service not configured, skipping job",
			"worker_id", workerID,
			"job_id", job.ID)
		// Mark as complete since there's no work to do
		_ = e.queue.MarkComplete(ctx, job.ID)
		return
	}

	// Generate the embedding
	vector, err := e.embeddingSvc.GenerateEmbedding(ctx, job.Text)
	if err != nil {
		e.logger.Warn("embedding generation failed",
			"worker_id", workerID,
			"job_id", job.ID,
			"error", err)

		if markErr := e.queue.MarkFailed(ctx, job.ID, err); markErr != nil {
			e.logger.Error("failed to mark job as failed",
				"job_id", job.ID,
				"error", markErr)
		}
		return
	}

	// Update feedback record with embedding vector
	frID, err := uuid.Parse(job.FeedbackRecordID)
	if err != nil {
		e.logger.Error("invalid feedback record ID",
			"feedback_record_id", job.FeedbackRecordID,
			"error", err)
		_ = e.queue.MarkFailed(ctx, job.ID, err)
		return
	}

	err = e.db.FeedbackRecord.
		UpdateOneID(frID).
		SetEmbedding(vector).
		SetEmbeddingModel(e.embeddingSvc.Model()).
		Exec(ctx)

	if err != nil {
		e.logger.Error("failed to update feedback record with embedding",
			"worker_id", workerID,
			"feedback_record_id", job.FeedbackRecordID,
			"error", err)

		if markErr := e.queue.MarkFailed(ctx, job.ID, err); markErr != nil {
			e.logger.Error("failed to mark job as failed",
				"job_id", job.ID,
				"error", markErr)
		}
		return
	}

	// Mark job as complete
	if err := e.queue.MarkComplete(ctx, job.ID); err != nil {
		e.logger.Error("failed to mark job as complete",
			"job_id", job.ID,
			"error", err)
		return
	}

	e.logger.Info("embedding completed successfully",
		"worker_id", workerID,
		"job_id", job.ID,
		"feedback_record_id", job.FeedbackRecordID,
		"model", e.embeddingSvc.Model())
}
