package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"log/slog"
	"time"

	"github.com/formbricks/hub/internal/datatypes"
	"github.com/formbricks/hub/internal/models"
	"github.com/google/uuid"
)

// WebhooksRepository defines the interface for webhooks data access
type WebhooksRepository interface {
	Create(ctx context.Context, req *models.CreateWebhookRequest) (*models.Webhook, error)
	GetByID(ctx context.Context, id uuid.UUID) (*models.Webhook, error)
	List(ctx context.Context, filters *models.ListWebhooksFilters) ([]models.Webhook, error)
	Count(ctx context.Context, filters *models.ListWebhooksFilters) (int64, error)
	Update(ctx context.Context, id uuid.UUID, req *models.UpdateWebhookRequest) (*models.Webhook, error)
	Delete(ctx context.Context, id uuid.UUID) error
	ListEnabled(ctx context.Context) ([]models.Webhook, error)
	ListEnabledForEventType(ctx context.Context, eventType string) ([]models.Webhook, error)
}

// WebhooksService handles business logic for webhooks
type WebhooksService struct {
	repo      WebhooksRepository
	publisher MessagePublisher
}

// NewWebhooksService creates a new webhooks service
func NewWebhooksService(repo WebhooksRepository, publisher MessagePublisher) *WebhooksService {
	return &WebhooksService{
		repo:      repo,
		publisher: publisher,
	}
}

// CreateWebhook creates a new webhook
func (s *WebhooksService) CreateWebhook(ctx context.Context, req *models.CreateWebhookRequest) (*models.Webhook, error) {
	// Auto-generate signing key if not provided
	if req.SigningKey == "" {
		key, err := generateSigningKey()
		if err != nil {
			return nil, err
		}
		req.SigningKey = key
	}

	webhook, err := s.repo.Create(ctx, req)
	if err != nil {
		return nil, err
	}

	// Publish event asynchronously
	go func() {
		event := Event{
			Type:      datatypes.WebhookCreated,
			Timestamp: time.Now().Unix(),
			Data:      *webhook,
		}
		if err := s.publisher.PublishEvent(context.Background(), event); err != nil {
			// Error is already logged by MessagePublisherManager, but log here for visibility
			slog.Debug("Failed to publish webhook created event",
				"webhook_id", webhook.ID,
				"error", err,
			)
		}
	}()

	return webhook, nil
}

// generateSigningKey generates a cryptographically secure signing key
// in the format expected by Standard Webhooks: "whsec_" + base64(32 random bytes)
func generateSigningKey() (string, error) {
	// Generate 32 random bytes
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return "", err
	}
	// Encode as base64 and prefix with "whsec_"
	return "whsec_" + base64.StdEncoding.EncodeToString(key), nil
}

// GetWebhook retrieves a single webhook by ID
func (s *WebhooksService) GetWebhook(ctx context.Context, id uuid.UUID) (*models.Webhook, error) {
	return s.repo.GetByID(ctx, id)
}

// ListWebhooks retrieves a list of webhooks with optional filters
func (s *WebhooksService) ListWebhooks(ctx context.Context, filters *models.ListWebhooksFilters) (*models.ListWebhooksResponse, error) {
	// Set default limit if not provided
	if filters.Limit <= 0 {
		filters.Limit = 100 // Default limit
	}

	webhooks, err := s.repo.List(ctx, filters)
	if err != nil {
		return nil, err
	}

	total, err := s.repo.Count(ctx, filters)
	if err != nil {
		return nil, err
	}

	return &models.ListWebhooksResponse{
		Data:   webhooks,
		Total:  total,
		Limit:  filters.Limit,
		Offset: filters.Offset,
	}, nil
}

// UpdateWebhook updates an existing webhook
func (s *WebhooksService) UpdateWebhook(ctx context.Context, id uuid.UUID, req *models.UpdateWebhookRequest) (*models.Webhook, error) {
	// Track changed fields
	changedFields := s.getChangedFields(req)

	webhook, err := s.repo.Update(ctx, id, req)
	if err != nil {
		return nil, err
	}

	// Publish event asynchronously
	go func() {
		event := Event{
			Type:          datatypes.WebhookUpdated,
			Timestamp:     time.Now().Unix(),
			Data:          *webhook,
			ChangedFields: changedFields,
		}
		if err := s.publisher.PublishEvent(context.Background(), event); err != nil {
			// Error is already logged by MessagePublisherManager, but log here for visibility
			slog.Debug("Failed to publish webhook updated event",
				"webhook_id", webhook.ID,
				"error", err,
			)
		}
	}()

	return webhook, nil
}

// getChangedFields extracts which fields were changed from the update request
func (s *WebhooksService) getChangedFields(req *models.UpdateWebhookRequest) []string {
	var fields []string
	if req.URL != nil {
		fields = append(fields, "url")
	}
	if req.SigningKey != nil {
		fields = append(fields, "signing_key")
	}
	if req.Enabled != nil {
		fields = append(fields, "enabled")
	}
	if req.EventTypes != nil {
		fields = append(fields, "event_types")
	}
	return fields
}

// DeleteWebhook deletes a webhook by ID
func (s *WebhooksService) DeleteWebhook(ctx context.Context, id uuid.UUID) error {
	// Get webhook before deletion for event payload
	webhook, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}

	// Delete the webhook
	if err := s.repo.Delete(ctx, id); err != nil {
		return err
	}

	// Publish event asynchronously
	go func() {
		event := Event{
			Type:      datatypes.WebhookDeleted,
			Timestamp: time.Now().Unix(),
			Data:      *webhook,
		}
		if err := s.publisher.PublishEvent(context.Background(), event); err != nil {
			// Error is already logged by MessagePublisherManager, but log here for visibility
			slog.Debug("Failed to publish webhook deleted event",
				"webhook_id", webhook.ID,
				"error", err,
			)
		}
	}()

	return nil
}
