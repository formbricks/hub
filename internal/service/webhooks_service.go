package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"

	"github.com/google/uuid"
	standardwebhooks "github.com/standard-webhooks/standard-webhooks/libraries/go"

	"github.com/formbricks/hub/internal/datatypes"
	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
)

// WebhooksRepository is the port for webhooks data access. Implementations:
// - repository.DBWebhooksRepository (DB); in production wrapped by NewCachingWebhooksRepository for cache.
type WebhooksRepository interface {
	Create(ctx context.Context, req *models.CreateWebhookRequest) (*models.Webhook, error)
	GetByID(ctx context.Context, id uuid.UUID) (*models.WebhookResponse, error)
	GetByIDInternal(ctx context.Context, id uuid.UUID) (*models.Webhook, error)
	List(ctx context.Context, filters *models.ListWebhooksFilters) ([]models.WebhookResponse, error)
	ListInternal(ctx context.Context, filters *models.ListWebhooksFilters) ([]models.Webhook, error)
	Count(ctx context.Context, filters *models.ListWebhooksFilters) (int64, error)
	Update(ctx context.Context, id uuid.UUID, req *models.UpdateWebhookRequest) (*models.Webhook, error)
	Delete(ctx context.Context, id uuid.UUID) error
	ListEnabled(ctx context.Context) ([]models.Webhook, error)
	ListEnabledForEventType(ctx context.Context, eventType string) ([]models.Webhook, error)
}

// WebhooksService handles business logic for webhooks.
type WebhooksService struct {
	repo        WebhooksRepository
	publisher   MessagePublisher
	maxWebhooks int
}

// NewWebhooksService creates a new webhooks service.
func NewWebhooksService(repo WebhooksRepository, publisher MessagePublisher, maxWebhooks int) *WebhooksService {
	return &WebhooksService{
		repo:        repo,
		publisher:   publisher,
		maxWebhooks: maxWebhooks,
	}
}

// CreateWebhook creates a new webhook.
func (s *WebhooksService) CreateWebhook(ctx context.Context, req *models.CreateWebhookRequest) (*models.Webhook, error) {
	count, err := s.repo.Count(ctx, &models.ListWebhooksFilters{})
	if err != nil {
		return nil, fmt.Errorf("count webhooks: %w", err)
	}

	if count >= int64(s.maxWebhooks) {
		return nil, huberrors.NewLimitExceededError(fmt.Sprintf("webhook limit reached (max %d)", s.maxWebhooks))
	}

	if req.SigningKey == "" {
		key, err := generateSigningKey()
		if err != nil {
			return nil, fmt.Errorf("generate signing key: %w", err)
		}

		req.SigningKey = key
	} else {
		if err := validateSigningKey(req.SigningKey); err != nil {
			return nil, err
		}
	}

	webhook, err := s.repo.Create(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("create webhook: %w", err)
	}

	s.publisher.PublishEvent(ctx, datatypes.WebhookCreated, models.ToWebhookResponse(webhook))

	return webhook, nil
}

// validateSigningKey checks that the key is valid for Standard Webhooks (base64-decodable, correct prefix/length).
// Returns a ValidationError if the key is malformed so the client gets a 400 with a clear message.
func validateSigningKey(key string) error {
	_, err := standardwebhooks.NewWebhook(key)
	if err != nil {
		msg := "invalid for Standard Webhooks: must be base64-decodable with correct prefix and length (e.g. whsec_...): " + err.Error()

		return huberrors.NewValidationError("signing_key", msg)
	}

	return nil
}

// SigningKeySize is the number of random bytes for Standard Webhooks signing keys.
const SigningKeySize = 32

// generateSigningKey generates a cryptographically secure signing key
// in the format expected by Standard Webhooks: "whsec_" + base64(32 random bytes).
func generateSigningKey() (string, error) {
	key := make([]byte, SigningKeySize)
	if _, err := rand.Read(key); err != nil {
		return "", fmt.Errorf("rand read: %w", err)
	}

	return "whsec_" + base64.StdEncoding.EncodeToString(key), nil
}

// GetWebhookInternal retrieves a single webhook by ID including internal fields.
func (s *WebhooksService) GetWebhookInternal(ctx context.Context, id uuid.UUID) (*models.Webhook, error) {
	webhook, err := s.repo.GetByIDInternal(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get webhook: %w", err)
	}

	return webhook, nil
}

// GetWebhook retrieves a single webhook by ID for public API responses.
func (s *WebhooksService) GetWebhook(ctx context.Context, id uuid.UUID) (*models.WebhookResponse, error) {
	webhook, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get webhook public: %w", err)
	}

	return webhook, nil
}

// ListWebhooksInternal retrieves a list of webhooks with optional filters including internal fields.
func (s *WebhooksService) ListWebhooksInternal(
	ctx context.Context, filters *models.ListWebhooksFilters,
) (*models.ListWebhooksResponse, error) {
	if filters.Limit <= 0 {
		filters.Limit = 100
	}

	webhooks, err := s.repo.ListInternal(ctx, filters)
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

// ListWebhooks retrieves webhooks for public API responses (without secrets).
func (s *WebhooksService) ListWebhooks(
	ctx context.Context, filters *models.ListWebhooksFilters,
) (*models.ListWebhooksPublicResponse, error) {
	if filters.Limit <= 0 {
		filters.Limit = 100
	}

	webhooks, err := s.repo.List(ctx, filters)
	if err != nil {
		return nil, fmt.Errorf("list webhooks public: %w", err)
	}

	total, err := s.repo.Count(ctx, filters)
	if err != nil {
		return nil, fmt.Errorf("count webhooks: %w", err)
	}

	return &models.ListWebhooksPublicResponse{
		Data:   webhooks,
		Total:  total,
		Limit:  filters.Limit,
		Offset: filters.Offset,
	}, nil
}

// UpdateWebhook updates an existing webhook.
func (s *WebhooksService) UpdateWebhook(ctx context.Context, id uuid.UUID, req *models.UpdateWebhookRequest) (*models.Webhook, error) {
	if req.SigningKey != nil {
		if err := validateSigningKey(*req.SigningKey); err != nil {
			return nil, err
		}
	}

	webhook, err := s.repo.Update(ctx, id, req)
	if err != nil {
		return nil, fmt.Errorf("update webhook: %w", err)
	}

	s.publisher.PublishEventWithChangedFields(ctx, datatypes.WebhookUpdated, models.ToWebhookResponse(webhook), req.ChangedFields())

	return webhook, nil
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
