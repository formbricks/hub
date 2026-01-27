package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/formbricks/hub/internal/models"
	standardwebhooks "github.com/standard-webhooks/standard-webhooks/libraries/go"
)

// WebhookDeliveryService implements MessagePublisher for webhook delivery
type WebhookDeliveryService struct {
	repo       WebhooksRepository
	httpClient *http.Client
}

// NewWebhookDeliveryService creates a new webhook delivery service
func NewWebhookDeliveryService(repo WebhooksRepository) *WebhookDeliveryService {
	return &WebhookDeliveryService{
		repo: repo,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// PublishEvent publishes a single event to all enabled webhooks
func (s *WebhookDeliveryService) PublishEvent(ctx context.Context, event Event) error {
	// Get all enabled webhooks
	webhooks, err := s.repo.ListEnabled(ctx)
	if err != nil {
		return fmt.Errorf("failed to list enabled webhooks: %w", err)
	}

	if len(webhooks) == 0 {
		return nil // No webhooks to send to
	}

	// Convert event to webhook payload
	payload, err := s.eventToPayload(event)
	if err != nil {
		return fmt.Errorf("failed to convert event to payload: %w", err)
	}

	// Send to all webhooks in parallel
	for _, webhook := range webhooks {
		go func(w models.Webhook) {
			if err := s.sendWebhook(ctx, w, payload); err != nil {
				slog.Warn("Failed to send webhook",
					"webhook_id", w.ID,
					"url", w.URL,
					"error", err,
				)
			}
		}(webhook)
	}

	return nil
}

// PublishEvents publishes multiple events (currently sends individually, future: batch)
func (s *WebhookDeliveryService) PublishEvents(ctx context.Context, events []Event) error {
	for _, event := range events {
		if err := s.PublishEvent(ctx, event); err != nil {
			// Log error but continue with other events
			slog.Warn("Failed to publish event", "error", err)
		}
	}
	return nil
}

// eventToPayload converts an Event to a FeedbackRecordWebhookPayload
func (s *WebhookDeliveryService) eventToPayload(event Event) (*FeedbackRecordWebhookPayload, error) {
	// Extract FeedbackRecord from event.Data
	record, ok := event.Data.(models.FeedbackRecord)
	if !ok {
		return nil, fmt.Errorf("event data is not a FeedbackRecord")
	}

	payload := &FeedbackRecordWebhookPayload{
		Type:      event.Type,
		Timestamp: time.Unix(event.Timestamp, 0),
		Data:      record,
	}

	if len(event.ChangedFields) > 0 {
		payload.ChangedFields = event.ChangedFields
	}

	return payload, nil
}

// sendWebhook sends a webhook to a single endpoint using Standard Webhooks
func (s *WebhookDeliveryService) sendWebhook(ctx context.Context, webhook models.Webhook, payload *FeedbackRecordWebhookPayload) error {
	// Marshal payload to JSON
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	// Create Standard Webhooks instance with signing key
	wh, err := standardwebhooks.NewWebhook(webhook.SigningKey)
	if err != nil {
		return fmt.Errorf("failed to create webhook signer: %w", err)
	}

	// Generate webhook ID and timestamp
	webhookID := fmt.Sprintf("%s-%d", webhook.ID.String(), time.Now().UnixNano())
	timestamp := time.Now()

	// Sign the payload
	signature, err := wh.Sign(webhookID, timestamp, payloadJSON)
	if err != nil {
		return fmt.Errorf("failed to sign webhook: %w", err)
	}

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhook.URL, bytes.NewReader(payloadJSON))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers according to Standard Webhooks spec
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(standardwebhooks.HeaderWebhookID, webhookID)
	req.Header.Set(standardwebhooks.HeaderWebhookSignature, signature)
	req.Header.Set(standardwebhooks.HeaderWebhookTimestamp, fmt.Sprintf("%d", timestamp.Unix()))

	// Send request
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send webhook: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			slog.Warn("Failed to close response body", "error", closeErr)
		}
	}()

	// Check response status
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned non-2xx status: %d", resp.StatusCode)
	}

	slog.Info("Webhook sent successfully",
		"webhook_id", webhook.ID,
		"url", webhook.URL,
		"status", resp.StatusCode,
	)

	return nil
}
