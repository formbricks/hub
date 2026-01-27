package service

import (
	"context"

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
}

// WebhooksService handles business logic for webhooks
type WebhooksService struct {
	repo WebhooksRepository
}

// NewWebhooksService creates a new webhooks service
func NewWebhooksService(repo WebhooksRepository) *WebhooksService {
	return &WebhooksService{repo: repo}
}

// CreateWebhook creates a new webhook
func (s *WebhooksService) CreateWebhook(ctx context.Context, req *models.CreateWebhookRequest) (*models.Webhook, error) {
	return s.repo.Create(ctx, req)
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
	return s.repo.Update(ctx, id, req)
}

// DeleteWebhook deletes a webhook by ID
func (s *WebhooksService) DeleteWebhook(ctx context.Context, id uuid.UUID) error {
	return s.repo.Delete(ctx, id)
}
