// Package worker provides background workers for the Hub API.
package worker

import (
	"context"
	"log/slog"
	"time"
)

// ClassificationRetryService defines the interface for retrying classification.
type ClassificationRetryService interface {
	RetryClassification(ctx context.Context, batchSize int) (int, error)
}

// ClassificationWorker is a background worker that periodically retries
// classification for feedback records that have embeddings but no topic_id.
// This fixes the race condition where feedback is created before topics have embeddings.
type ClassificationWorker struct {
	service   ClassificationRetryService
	interval  time.Duration
	batchSize int
}

// NewClassificationWorker creates a new classification retry worker.
func NewClassificationWorker(service ClassificationRetryService, interval time.Duration, batchSize int) *ClassificationWorker {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	if batchSize <= 0 {
		batchSize = 100
	}

	return &ClassificationWorker{
		service:   service,
		interval:  interval,
		batchSize: batchSize,
	}
}

// Start begins the background worker loop. It runs until the context is cancelled.
func (w *ClassificationWorker) Start(ctx context.Context) {
	slog.Info("classification retry worker started",
		"interval", w.interval,
		"batch_size", w.batchSize,
	)

	// Run immediately on startup
	w.runOnce(ctx)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("classification retry worker stopped")
			return
		case <-ticker.C:
			w.runOnce(ctx)
		}
	}
}

// runOnce executes a single classification retry batch.
func (w *ClassificationWorker) runOnce(ctx context.Context) {
	classified, err := w.service.RetryClassification(ctx, w.batchSize)
	if err != nil {
		slog.Error("classification retry failed", "error", err)
		return
	}

	if classified > 0 {
		slog.Info("classification retry completed",
			"classified", classified,
		)
	} else {
		slog.Debug("classification retry completed, no records to classify")
	}
}
