package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/formbricks/hub/internal/datatypes"
	"github.com/formbricks/hub/internal/models"
	"github.com/hashicorp/golang-lru/v2/expirable"
	standardwebhooks "github.com/standard-webhooks/standard-webhooks/libraries/go"
)

// WebhookCacheConfig holds configuration for webhook caching
type WebhookCacheConfig struct {
	Enabled bool          // Enable/disable caching
	Size    int           // Max cache entries
	TTL     time.Duration // Cache TTL
}

// WebhookDeliveryService implements MessagePublisher for webhook delivery
type WebhookDeliveryService struct {
	repo         WebhooksRepository
	httpClient   *http.Client
	cache        *expirable.LRU[datatypes.EventType, []models.Webhook]
	cacheKeys    map[datatypes.EventType]bool
	cacheKeysMu  sync.RWMutex
	cacheEnabled bool
}

// NewWebhookDeliveryService creates a new webhook delivery service
func NewWebhookDeliveryService(repo WebhooksRepository, cacheConfig *WebhookCacheConfig) *WebhookDeliveryService {
	service := &WebhookDeliveryService{
		repo:         repo,
		httpClient:   &http.Client{Timeout: 10 * time.Second},
		cacheKeys:    make(map[datatypes.EventType]bool),
		cacheEnabled: cacheConfig != nil && cacheConfig.Enabled,
	}

	// Create cache only if enabled
	if service.cacheEnabled {
		service.cache = expirable.NewLRU[datatypes.EventType, []models.Webhook](
			cacheConfig.Size,
			nil, // onEvicted callback
			cacheConfig.TTL,
		)
	}

	return service
}

// PublishEvent publishes a single event to all enabled webhooks
func (s *WebhookDeliveryService) PublishEvent(ctx context.Context, event Event) error {
	// Use database-level filtering instead of fetching all and filtering in Go
	// This leverages the GIN index on event_types for efficient queries
	webhooks, err := s.getWebhooksForEventType(ctx, event.Type)
	if err != nil {
		eventTypeStr := event.Type.String()
		slog.Error("Failed to list enabled webhooks for event type",
			"event_type", eventTypeStr,
			"error", err,
		)
		return fmt.Errorf("failed to list enabled webhooks: %w", err)
	}

	if len(webhooks) == 0 {
		return nil // No webhooks to send to
	}

	// Convert event to webhook payload (marshal once)
	payload, err := s.eventToPayload(event)
	if err != nil {
		slog.Error("Failed to convert event to payload",
			"event_type", event.Type.String(),
			"error", err,
		)
		return fmt.Errorf("failed to convert event to payload: %w", err)
	}

	// Marshal JSON once, reuse bytes for all webhooks
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		slog.Error("Failed to marshal webhook payload",
			"event_type", event.Type.String(),
			"error", err,
		)
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	// Send to webhooks in parallel
	// Note: Consider adding concurrency control (semaphore/worker pool) for high-volume scenarios
	for _, webhook := range webhooks {
		go func(w models.Webhook) {
			if err := s.sendWebhookWithJSON(ctx, w, payloadJSON); err != nil {
				slog.Warn("Failed to send webhook",
					"webhook_id", w.ID,
					"url", w.URL,
					"event_type", event.Type.String(),
					"error", err,
				)
			}
		}(webhook)
	}

	return nil
}

// getWebhooksForEventType gets webhooks from cache or database
func (s *WebhookDeliveryService) getWebhooksForEventType(ctx context.Context, eventType datatypes.EventType) ([]models.Webhook, error) {
	// Check cache first (if enabled)
	if s.cacheEnabled {
		if cached, ok := s.cache.Get(eventType); ok {
			return cached, nil // Return cached data
		}
	}

	// Cache miss or disabled - fetch from database
	// Convert EventType enum to string for database query
	eventTypeStr := eventType.String()
	webhooks, err := s.repo.ListEnabledForEventType(ctx, eventTypeStr)
	if err != nil {
		slog.Error("Failed to list enabled webhooks for event type",
			"event_type", eventTypeStr,
			"error", err,
		)
		return nil, err
	}

	// Update cache (if enabled)
	if s.cacheEnabled {
		s.cache.Add(eventType, webhooks)

		// Track key for invalidation
		s.cacheKeysMu.Lock()
		s.cacheKeys[eventType] = true
		s.cacheKeysMu.Unlock()
	}

	return webhooks, nil
}

// InvalidateCache clears the cache (call when webhooks are created/updated/deleted)
func (s *WebhookDeliveryService) InvalidateCache() {
	if !s.cacheEnabled {
		return // No-op if caching is disabled
	}

	s.cacheKeysMu.Lock()
	defer s.cacheKeysMu.Unlock()

	// Remove all tracked keys
	for key := range s.cacheKeys {
		s.cache.Remove(key)
	}
	s.cacheKeys = make(map[datatypes.EventType]bool)
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

// eventToPayload converts an Event to a WebhookPayload.
// Supports multiple event data types (FeedbackRecord, Webhook, etc.).
func (s *WebhookDeliveryService) eventToPayload(event Event) (*WebhookPayload, error) {
	payload := &WebhookPayload{
		Type:      event.Type.String(), // Convert EventType enum to string
		Timestamp: time.Unix(event.Timestamp, 0),
		Data:      event.Data, // interface{} - can be FeedbackRecord, Webhook, etc.
	}

	if len(event.ChangedFields) > 0 {
		payload.ChangedFields = event.ChangedFields
	}

	// Note: No error logging here as this is a simple conversion that shouldn't fail
	// If conversion fails, it's a programming error, not a runtime error
	return payload, nil
}

// sendWebhookWithJSON sends a webhook using pre-marshaled JSON bytes
func (s *WebhookDeliveryService) sendWebhookWithJSON(ctx context.Context, webhook models.Webhook, payloadJSON []byte) error {
	// Create Standard Webhooks instance with signing key
	wh, err := standardwebhooks.NewWebhook(webhook.SigningKey)
	if err != nil {
		slog.Error("Failed to create webhook signer",
			"webhook_id", webhook.ID,
			"error", err,
		)
		return fmt.Errorf("failed to create webhook signer: %w", err)
	}

	// Generate webhook ID and timestamp
	webhookID := fmt.Sprintf("%s-%d", webhook.ID.String(), time.Now().UnixNano())
	timestamp := time.Now()

	// Sign the payload (using pre-marshaled JSON)
	signature, err := wh.Sign(webhookID, timestamp, payloadJSON)
	if err != nil {
		slog.Error("Failed to sign webhook payload",
			"webhook_id", webhook.ID,
			"error", err,
		)
		return fmt.Errorf("failed to sign webhook: %w", err)
	}

	// Create HTTP request with pre-marshaled JSON
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhook.URL, bytes.NewReader(payloadJSON))
	if err != nil {
		slog.Error("Failed to create HTTP request for webhook",
			"webhook_id", webhook.ID,
			"url", webhook.URL,
			"error", err,
		)
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
		slog.Warn("Failed to send HTTP request to webhook",
			"webhook_id", webhook.ID,
			"url", webhook.URL,
			"error", err,
		)
		return fmt.Errorf("failed to send webhook: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			slog.Warn("Failed to close webhook response body",
				"webhook_id", webhook.ID,
				"error", closeErr,
			)
		}
	}()

	// Check response status
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		slog.Warn("Webhook returned non-2xx status",
			"webhook_id", webhook.ID,
			"url", webhook.URL,
			"status_code", resp.StatusCode,
		)
		return fmt.Errorf("webhook returned non-2xx status: %d", resp.StatusCode)
	}

	slog.Debug("Webhook sent successfully",
		"webhook_id", webhook.ID,
		"url", webhook.URL,
		"status_code", resp.StatusCode,
	)

	return nil
}
