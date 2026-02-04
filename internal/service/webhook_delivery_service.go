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
	"github.com/hashicorp/go-retryablehttp"
	"github.com/hashicorp/golang-lru/v2/expirable"
	standardwebhooks "github.com/standard-webhooks/standard-webhooks/libraries/go"
	"golang.org/x/sync/singleflight"
)

// WebhookCacheConfig holds configuration for webhook caching and delivery
type WebhookCacheConfig struct {
	Enabled       bool          // Enable/disable caching
	Size          int           // Max cache entries
	TTL           time.Duration // Cache TTL
	MaxConcurrent int           // Max concurrent outbound HTTP calls (0 = 100)
}

// WebhookDeliveryService implements eventPublisher for webhook delivery
type WebhookDeliveryService struct {
	repo         WebhooksRepository
	httpClient   *http.Client
	cache        *expirable.LRU[datatypes.EventType, []models.Webhook]
	cacheKeys    map[datatypes.EventType]bool
	cacheKeysMu  sync.RWMutex
	cacheEnabled bool
	sfGroup      singleflight.Group
	sem          chan struct{} // semaphore to bound concurrent outbound HTTP calls
}

// NewWebhookDeliveryService creates a new webhook delivery service
func NewWebhookDeliveryService(repo WebhooksRepository, cacheConfig *WebhookCacheConfig) *WebhookDeliveryService {
	retryClient := retryablehttp.NewClient()
	retryClient.HTTPClient.Timeout = 15 * time.Second
	retryClient.RetryMax = 3
	retryClient.Logger = nil // disable retryablehttp's default logger; we log at delivery layer
	// Do not follow redirects (Standard Webhooks: treat 3xx as failure; consumer should update webhook URL)
	retryClient.HTTPClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	// Tune connection pool for concurrent delivery
	if t, ok := retryClient.HTTPClient.Transport.(*http.Transport); ok {
		t.MaxIdleConns = 100
		t.MaxIdleConnsPerHost = 20
	}

	maxConcurrent := 100
	if cacheConfig != nil && cacheConfig.MaxConcurrent > 0 {
		maxConcurrent = cacheConfig.MaxConcurrent
	}

	service := &WebhookDeliveryService{
		repo:         repo,
		httpClient:   retryClient.StandardClient(),
		cacheKeys:    make(map[datatypes.EventType]bool),
		cacheEnabled: cacheConfig != nil && cacheConfig.Enabled,
		sem:          make(chan struct{}, maxConcurrent),
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

// PublishEvent publishes a single event to all enabled webhooks (implements eventPublisher)
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

	messageID := event.ID.String()

	for _, webhook := range webhooks {
		s.sem <- struct{}{} // acquire (blocks if at cap)
		go func(w models.Webhook) {
			defer func() { <-s.sem }() // release
			start := time.Now()
			err := s.sendWebhookWithJSON(ctx, w, payloadJSON, messageID)
			duration := time.Since(start)
			if err != nil {
				slog.Warn("Failed to send webhook",
					"message_id", messageID,
					"webhook_id", w.ID,
					"url", w.URL,
					"event_type", event.Type.String(),
					"error", err,
					"duration_ms", duration.Milliseconds(),
				)
				return
			}
			slog.Debug("Webhook sent successfully",
				"message_id", messageID,
				"webhook_id", w.ID,
				"url", w.URL,
				"event_type", event.Type.String(),
				"duration_ms", duration.Milliseconds(),
			)
		}(webhook)
	}
}

// deliveryListLimit matches repository cap; used for truncation warning
const deliveryListLimit = 1000

// getWebhooksForEventType gets webhooks from cache or database (with singleflight on cache miss)
func (s *WebhookDeliveryService) getWebhooksForEventType(ctx context.Context, eventType datatypes.EventType) ([]models.Webhook, error) {
	if s.cacheEnabled {
		if cached, ok := s.cache.Get(eventType); ok {
			return cached, nil
		}
	}

	eventTypeStr := eventType.String()
	v, err, _ := s.sfGroup.Do(eventTypeStr, func() (any, error) {
		webhooks, err := s.repo.ListEnabledForEventType(ctx, eventTypeStr)
		if err != nil {
			return nil, err
		}
		// Cache inside singleflight so only one goroutine writes (cache may not be concurrent-safe)
		if s.cacheEnabled {
			s.cache.Add(eventType, webhooks)
			s.cacheKeysMu.Lock()
			s.cacheKeys[eventType] = true
			s.cacheKeysMu.Unlock()
		}
		return webhooks, nil
	})
	if err != nil {
		slog.Error("Failed to list enabled webhooks for event type",
			"event_type", eventTypeStr,
			"error", err,
		)
		return nil, err
	}
	webhooks := v.([]models.Webhook)

	if len(webhooks) >= deliveryListLimit {
		slog.Warn("Webhook list may be truncated (reached limit)",
			"event_type", eventTypeStr,
			"limit", deliveryListLimit,
		)
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
		ID:        event.ID,
		Type:      event.Type.String(),
		Timestamp: time.Unix(event.Timestamp, 0),
		Data:      event.Data,
	}

	if len(event.ChangedFields) > 0 {
		payload.ChangedFields = event.ChangedFields
	}

	return payload, nil
}

// sendWebhookWithJSON sends a webhook using pre-marshaled JSON bytes.
// messageID is a stable identifier for this event (same across all endpoints and retries) for consumer idempotency.
func (s *WebhookDeliveryService) sendWebhookWithJSON(ctx context.Context, webhook models.Webhook, payloadJSON []byte, messageID string) error {
	wh, err := standardwebhooks.NewWebhook(webhook.SigningKey)
	if err != nil {
		return fmt.Errorf("failed to create webhook signer: %w", err)
	}

	timestamp := time.Now()

	signature, err := wh.Sign(messageID, timestamp, payloadJSON)
	if err != nil {
		return fmt.Errorf("failed to sign webhook: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhook.URL, bytes.NewReader(payloadJSON))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(standardwebhooks.HeaderWebhookID, messageID)
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

	if resp.StatusCode == http.StatusGone {
		// Standard Webhooks: sender should disable the endpoint and stop sending (RFC 7231 410 Gone)
		enabled := false
		_, err := s.repo.Update(ctx, webhook.ID, &models.UpdateWebhookRequest{Enabled: &enabled})
		if err != nil {
			slog.Error("Failed to disable webhook after 410 Gone",
				"webhook_id", webhook.ID,
				"url", webhook.URL,
				"error", err,
			)
		} else {
			slog.Info("Webhook disabled after 410 Gone (endpoint no longer accepts delivery)",
				"webhook_id", webhook.ID,
				"url", webhook.URL,
			)
		}
		s.InvalidateCache()
		return fmt.Errorf("webhook returned 410 Gone (endpoint disabled): %s", webhook.URL)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned non-2xx status: %d", resp.StatusCode)
	}

	return nil
}
