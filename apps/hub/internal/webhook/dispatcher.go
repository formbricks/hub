// Package webhook provides reliable webhook event delivery using a worker pool pattern.
// It dispatches events to configured URLs with retry logic and exponential backoff.
// The worker pool prevents goroutine leaks and provides graceful shutdown capabilities.
package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

const (
	// defaultWorkerCount is the default number of webhook worker goroutines
	defaultWorkerCount = 10
	// defaultQueueSize is the default size of the job queue buffer
	defaultQueueSize = 100
	// defaultHTTPTimeout is the default timeout for HTTP requests
	defaultHTTPTimeout = 5 * time.Second
	// maxRetries is the maximum number of retry attempts for webhook delivery
	maxRetries = 3
	// retryBaseDelay is the base delay for exponential backoff
	retryBaseDelay = 1 * time.Second
)

// EventType represents the type of webhook event
type EventType string

const (
	EventFeedbackRecordCreated  EventType = "feedback_record.created"
	EventFeedbackRecordUpdated  EventType = "feedback_record.updated"
	EventFeedbackRecordDeleted  EventType = "feedback_record.deleted"
	EventFeedbackRecordEnriched EventType = "feedback_record.enriched"
)

// Event represents a webhook event payload
type Event struct {
	Event     EventType   `json:"event"`
	Timestamp time.Time   `json:"timestamp"`
	Data      interface{} `json:"data"`
}

// webhookJob represents a single webhook delivery job
type webhookJob struct {
	url       string
	payload   []byte
	eventType EventType
	ctx       context.Context
}

// Dispatcher handles webhook dispatching with a worker pool to prevent goroutine leaks
type Dispatcher struct {
	urls        []string
	client      *http.Client
	logger      *slog.Logger
	jobQueue    chan webhookJob
	wg          sync.WaitGroup
	ctx         context.Context
	cancel      context.CancelFunc
	workerCount int
}

// NewDispatcher creates a new webhook dispatcher with a worker pool using default settings
func NewDispatcher(urls []string, logger *slog.Logger) *Dispatcher {
	return NewDispatcherWithPool(urls, defaultWorkerCount, defaultQueueSize, logger)
}

// NewDispatcherWithPool creates a new webhook dispatcher with custom worker pool settings
func NewDispatcherWithPool(urls []string, workerCount, queueSize int, logger *slog.Logger) *Dispatcher {
	ctx, cancel := context.WithCancel(context.Background())

	d := &Dispatcher{
		urls: urls,
		client: &http.Client{
			Timeout: defaultHTTPTimeout,
		},
		logger:      logger,
		jobQueue:    make(chan webhookJob, queueSize),
		ctx:         ctx,
		cancel:      cancel,
		workerCount: workerCount,
	}

	// Start worker pool
	d.startWorkers()

	logger.Info("webhook dispatcher initialized",
		"urls", urls,
		"workers", workerCount,
		"queue_size", queueSize)

	return d
}

// startWorkers initializes the worker pool
func (d *Dispatcher) startWorkers() {
	for i := 0; i < d.workerCount; i++ {
		d.wg.Add(1)
		go d.worker(i)
	}
}

// worker processes webhook jobs from the queue
func (d *Dispatcher) worker(id int) {
	defer d.wg.Done()

	d.logger.Debug("webhook worker started", "worker_id", id)

	for {
		select {
		case job, ok := <-d.jobQueue:
			if !ok {
				// Channel closed, worker should exit
				d.logger.Debug("webhook worker shutting down", "worker_id", id)
				return
			}

			// Process the webhook job
			d.sendWithRetry(job.ctx, job.url, job.payload, job.eventType)

		case <-d.ctx.Done():
			// Context cancelled, worker should exit
			d.logger.Debug("webhook worker cancelled", "worker_id", id)
			return
		}
	}
}

// Shutdown gracefully shuts down the dispatcher, waiting for pending jobs to complete
func (d *Dispatcher) Shutdown(timeout time.Duration) error {
	d.logger.Info("shutting down webhook dispatcher", "timeout", timeout)

	// Stop accepting new jobs
	close(d.jobQueue)

	// Wait for workers to finish with timeout
	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		d.logger.Info("webhook dispatcher shut down successfully")
		return nil
	case <-time.After(timeout):
		d.cancel() // Force cancel remaining jobs
		d.logger.Warn("webhook dispatcher shutdown timed out, forcing cancellation")
		return fmt.Errorf("shutdown timed out after %v", timeout)
	}
}

// Dispatch sends a webhook event to all configured URLs using the worker pool
func (d *Dispatcher) Dispatch(ctx context.Context, eventType EventType, data interface{}) {
	if len(d.urls) == 0 {
		return
	}

	event := Event{
		Event:     eventType,
		Timestamp: time.Now(),
		Data:      data,
	}

	payload, err := json.Marshal(event)
	if err != nil {
		d.logger.Error("failed to marshal webhook event",
			"event", eventType,
			"error", err)
		return
	}

	// Enqueue jobs for each URL (non-blocking with buffered channel)
	for _, url := range d.urls {
		job := webhookJob{
			url:       url,
			payload:   payload,
			eventType: eventType,
			ctx:       ctx,
		}

		select {
		case d.jobQueue <- job:
			// Job enqueued successfully
		default:
			// Queue is full, log warning and drop the job
			d.logger.Warn("webhook queue full, dropping job",
				"url", url,
				"event", eventType,
				"queue_size", cap(d.jobQueue))
		}
	}
}

// sendWithRetry sends a webhook with retry logic
func (d *Dispatcher) sendWithRetry(ctx context.Context, url string, payload []byte, eventType EventType) {
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff
			delay := retryBaseDelay * time.Duration(1<<uint(attempt-1))
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
		if err != nil {
			d.logger.Error("failed to create webhook request",
				"url", url,
				"event", eventType,
				"attempt", attempt+1,
				"error", err)
			continue
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "Formbricks-Hub/1.0")

		resp, err := d.client.Do(req)
		if err != nil {
			d.logger.Warn("failed to send webhook",
				"url", url,
				"event", eventType,
				"attempt", attempt+1,
				"error", err)
			continue
		}

		_ = resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			d.logger.Info("webhook delivered successfully",
				"url", url,
				"event", eventType,
				"status", resp.StatusCode)
			return
		}

		d.logger.Warn("webhook failed with non-2xx status",
			"url", url,
			"event", eventType,
			"status", resp.StatusCode,
			"attempt", attempt+1)
	}

	d.logger.Error("webhook failed after all retries",
		"url", url,
		"event", eventType,
		"attempts", maxRetries)
}

// DispatchAsync is a convenience method that dispatches webhooks asynchronously
// Uses the worker pool internally, so no goroutine leak
func (d *Dispatcher) DispatchAsync(eventType EventType, data interface{}) {
	// Use background context for async operations since:
	// 1. HTTP client has its own timeout (defaultHTTPTimeout = 5s)
	// 2. Workers have retry logic with exponential backoff
	// 3. No need for request-scoped tracing in async webhooks
	// This avoids leaving timeout timers active and is simpler than managing
	// context lifecycle across async boundaries
	d.Dispatch(context.Background(), eventType, data)
}

// String returns a string representation of the event type
func (e EventType) String() string {
	return string(e)
}

// Validate checks if the event type is valid
func (e EventType) Validate() error {
	switch e {
	case EventFeedbackRecordCreated, EventFeedbackRecordUpdated, EventFeedbackRecordDeleted, EventFeedbackRecordEnriched:
		return nil
	default:
		return fmt.Errorf("invalid event type: %s", e)
	}
}
