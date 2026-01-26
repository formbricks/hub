package formbricks

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	"github.com/formbricks/hub/internal/service"
	"github.com/formbricks/hub/pkg/formbricks"
)

// WebhookConnector represents a Formbricks webhook connector
type WebhookConnector struct {
	feedbackService *service.FeedbackRecordsService
}

// WebhookConfig holds configuration for the Formbricks webhook connector
type WebhookConfig struct {
	FeedbackService *service.FeedbackRecordsService
}

// NewWebhookConnector creates a new Formbricks webhook connector
func NewWebhookConnector(cfg WebhookConfig) *WebhookConnector {
	return &WebhookConnector{
		feedbackService: cfg.FeedbackService,
	}
}

// HandleWebhook processes incoming webhook payload from Formbricks
// Implements connector.WebhookInputConnector interface
func (c *WebhookConnector) HandleWebhook(ctx context.Context, payload []byte) error {
	slog.Debug("Processing Formbricks webhook")

	// Parse the webhook payload
	var event formbricks.WebhookEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		slog.Error("Failed to parse Formbricks webhook payload",
			"error", err,
			"payload_length", len(payload),
		)
		return fmt.Errorf("failed to parse webhook payload: %w", err)
	}

	slog.Info("Received Formbricks webhook event",
		"event_type", event.Event,
		"webhook_id", event.WebhookID,
		"response_id", event.Data.ID,
		"survey_id", event.Data.SurveyID,
	)

	// Process based on event type
	switch event.Event {
	case formbricks.EventResponseCreated, formbricks.EventResponseUpdated, formbricks.EventResponseFinished:
		return c.processResponse(ctx, event)
	default:
		slog.Warn("Unknown Formbricks webhook event type",
			"event_type", event.Event,
			"webhook_id", event.WebhookID,
		)
		// Don't return error for unknown events - just log and acknowledge
		return nil
	}
}

// processResponse handles response-related webhook events
func (c *WebhookConnector) processResponse(ctx context.Context, event formbricks.WebhookEvent) error {
	response := event.Data

	// For responseCreated events, the response might not be finished yet
	// We still want to process it to capture partial responses
	slog.Debug("Processing Formbricks response",
		"response_id", response.ID,
		"finished", response.Finished,
		"event_type", event.Event,
	)

	// Transform the webhook response to feedback records
	feedbackRecords := TransformWebhookToFeedbackRecords(event)

	if len(feedbackRecords) == 0 {
		slog.Info("No feedback records to create from webhook",
			"response_id", response.ID,
			"data_fields", len(response.Data),
		)
		return nil
	}

	// Create feedback records using the service
	var lastErr error
	successCount := 0
	for _, recordReq := range feedbackRecords {
		record, err := c.feedbackService.CreateFeedbackRecord(ctx, recordReq)
		if err != nil {
			slog.Error("Failed to create feedback record from webhook",
				"error", err,
				"response_id", response.ID,
				"field_id", recordReq.FieldID,
			)
			lastErr = err
			continue
		}
		successCount++
		slog.Debug("Created feedback record from webhook",
			"record_id", record.ID,
			"response_id", response.ID,
			"field_id", recordReq.FieldID,
		)
	}

	slog.Info("Processed Formbricks webhook",
		"response_id", response.ID,
		"records_created", successCount,
		"records_failed", len(feedbackRecords)-successCount,
	)

	// Return the last error if any record failed
	// This allows the webhook sender to know something went wrong
	return lastErr
}

// NewWebhookConnectorIfConfigured creates a Formbricks webhook connector if FORMBRICKS_WEBHOOK_API_KEY is set.
// Returns the connector and API key, or nil if not configured.
func NewWebhookConnectorIfConfigured(feedbackService *service.FeedbackRecordsService) (*WebhookConnector, string) {
	apiKey := os.Getenv("FORMBRICKS_WEBHOOK_API_KEY")
	if apiKey == "" {
		slog.Info("Formbricks webhook connector not configured (FORMBRICKS_WEBHOOK_API_KEY required)")
		return nil, ""
	}

	conn := NewWebhookConnector(WebhookConfig{
		FeedbackService: feedbackService,
	})

	return conn, apiKey
}
