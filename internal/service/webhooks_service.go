package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"

	"github.com/google/uuid"

	"github.com/formbricks/hub/internal/datatypes"
	"github.com/formbricks/hub/internal/models"
)

// WebhooksRepository defines the interface for webhooks data access.
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

// WebhooksService handles business logic for webhooks.
type WebhooksService struct {
	repo      WebhooksRepository
	publisher MessagePublisher
}

// NewWebhooksService creates a new webhooks service.
func NewWebhooksService(repo WebhooksRepository, publisher MessagePublisher) *WebhooksService {
	return &WebhooksService{
		repo:      repo,
		publisher: publisher,
	}
}

// CreateWebhook creates a new webhook.
func (s *WebhooksService) CreateWebhook(ctx context.Context, req *models.CreateWebhookRequest) (*models.Webhook, error) {
	if req.SigningKey == "" {
		key, err := generateSigningKey()
		if err != nil {
			return nil, fmt.Errorf("generate signing key: %w", err)
		}
		req.SigningKey = key
	}

	webhook, err := s.repo.Create(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("create webhook: %w", err)
	}

	s.publisher.PublishEvent(ctx, datatypes.WebhookCreated, *webhook)

	return webhook, nil
}

// generateSigningKey generates a cryptographically secure signing key
// in the format expected by Standard Webhooks: "whsec_" + base64(32 random bytes).
func generateSigningKey() (string, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return "", fmt.Errorf("rand read: %w", err)
	}
	return "whsec_" + base64.StdEncoding.EncodeToString(key), nil
}

// GetWebhook retrieves a single webhook by ID.
func (s *WebhooksService) GetWebhook(ctx context.Context, id uuid.UUID) (*models.Webhook, error) {
	webhook, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get webhook: %w", err)
	}
	return webhook, nil
}

// ListWebhooks retrieves a list of webhooks with optional filters.
func (s *WebhooksService) ListWebhooks(ctx context.Context, filters *models.ListWebhooksFilters) (*models.ListWebhooksResponse, error) {
	if filters.Limit <= 0 {
		filters.Limit = 100
	}

	webhooks, err := s.repo.List(ctx, filters)
	if err != nil {
		return nil, fmt.Errorf("list webhooks: %w", err)
	}

	total, err := s.repo.Count(ctx, filters)
	if err != nil {
		return nil, fmt.Errorf("count webhooks: %w", err)
	}

	return &models.ListWebhooksResponse{
		Data:   webhooks,
		Total:  total,
		Limit:  filters.Limit,
		Offset: filters.Offset,
	}, nil
}

// UpdateWebhook updates an existing webhook.
func (s *WebhooksService) UpdateWebhook(ctx context.Context, id uuid.UUID, req *models.UpdateWebhookRequest) (*models.Webhook, error) {
	webhook, err := s.repo.Update(ctx, id, req)
	if err != nil {
		return nil, fmt.Errorf("update webhook: %w", err)
	}

	s.publisher.PublishEventWithChangedFields(ctx, datatypes.WebhookUpdated, *webhook, s.getChangedFields(req))

	return webhook, nil
}

// getChangedFields extracts which fields were changed from the update request.
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
	if req.TenantID != nil {
		fields = append(fields, "tenant_id")
	}
	if req.EventTypes != nil {
		fields = append(fields, "event_types")
	}
	return fields
}

// DeleteWebhook deletes a webhook by ID.
// Publishes WebhookDeleted with data = [id] (array of deleted IDs) for consistency with feedback record deletes.
func (s *WebhooksService) DeleteWebhook(ctx context.Context, id uuid.UUID) error {
	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("delete webhook: %w", err)
	}
	s.publisher.PublishEvent(ctx, datatypes.WebhookDeleted, []uuid.UUID{id})
	return nil
}
