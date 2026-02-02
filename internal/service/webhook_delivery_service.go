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
			nil,
			cacheConfig.TTL,
		)
	}

	return service
}

// PublishEvent publishes a single event to all enabled webhooks (implements MessagePublisher)
func (s *WebhookDeliveryService) PublishEvent(ctx context.Context, event Event) {
	webhooks, err := s.getWebhooksForEventType(ctx, event.Type)
	if err != nil {
		slog.Error("Failed to list enabled webhooks for event type",
			"event_type", event.Type.String(),
			"error", err,
		)
		return
	}

	if len(webhooks) == 0 {
		return
	}

	payload, err := s.eventToPayload(event)
	if err != nil {
		slog.Error("Failed to convert event to payload",
			"event_type", event.Type.String(),
			"error", err,
		)
		return
	}

	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		slog.Error("Failed to marshal webhook payload",
			"event_type", event.Type.String(),
			"error", err,
		)
		return
	}

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
}

// getWebhooksForEventType gets webhooks from cache or database
func (s *WebhookDeliveryService) getWebhooksForEventType(ctx context.Context, eventType datatypes.EventType) ([]models.Webhook, error) {
	if s.cacheEnabled {
		if cached, ok := s.cache.Get(eventType); ok {
			return cached, nil
		}
	}

	eventTypeStr := eventType.String()
	webhooks, err := s.repo.ListEnabledForEventType(ctx, eventTypeStr)
	if err != nil {
		slog.Error("Failed to list enabled webhooks for event type",
			"event_type", eventTypeStr,
			"error", err,
		)
		return nil, err
	}

	if s.cacheEnabled {
		s.cache.Add(eventType, webhooks)
		s.cacheKeysMu.Lock()
		s.cacheKeys[eventType] = true
		s.cacheKeysMu.Unlock()
	}

	return webhooks, nil
}

// InvalidateCache clears the cache (call when webhooks are created/updated/deleted)
func (s *WebhookDeliveryService) InvalidateCache() {
	if !s.cacheEnabled {
		return
	}

	s.cacheKeysMu.Lock()
	defer s.cacheKeysMu.Unlock()

	for key := range s.cacheKeys {
		s.cache.Remove(key)
	}
	s.cacheKeys = make(map[datatypes.EventType]bool)
}

// eventToPayload converts an Event to a WebhookPayload
func (s *WebhookDeliveryService) eventToPayload(event Event) (*WebhookPayload, error) {
	payload := &WebhookPayload{
		Type:      event.Type.String(),
		Timestamp: time.Unix(event.Timestamp, 0),
		Data:      event.Data,
	}

	if len(event.ChangedFields) > 0 {
		payload.ChangedFields = event.ChangedFields
	}

	return payload, nil
}

// sendWebhookWithJSON sends a webhook using pre-marshaled JSON bytes
func (s *WebhookDeliveryService) sendWebhookWithJSON(ctx context.Context, webhook models.Webhook, payloadJSON []byte) error {
	wh, err := standardwebhooks.NewWebhook(webhook.SigningKey)
	if err != nil {
		return fmt.Errorf("failed to create webhook signer: %w", err)
	}

	webhookID := fmt.Sprintf("%s-%d", webhook.ID.String(), time.Now().UnixNano())
	timestamp := time.Now()

	signature, err := wh.Sign(webhookID, timestamp, payloadJSON)
	if err != nil {
		return fmt.Errorf("failed to sign webhook: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhook.URL, bytes.NewReader(payloadJSON))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(standardwebhooks.HeaderWebhookID, webhookID)
	req.Header.Set(standardwebhooks.HeaderWebhookSignature, signature)
	req.Header.Set(standardwebhooks.HeaderWebhookTimestamp, fmt.Sprintf("%d", timestamp.Unix()))

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send webhook: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			slog.Warn("Failed to close webhook response body", "webhook_id", webhook.ID, "error", closeErr)
		}
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned non-2xx status: %d", resp.StatusCode)
	}

	slog.Debug("Webhook sent successfully",
		"webhook_id", webhook.ID,
		"url", webhook.URL,
		"status_code", resp.StatusCode,
	)

	return nil
}
