package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	apperrors "github.com/formbricks/hub/internal/errors"
	"github.com/formbricks/hub/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// WebhooksRepository handles data access for webhooks
type WebhooksRepository struct {
	db *pgxpool.Pool
}

// NewWebhooksRepository creates a new webhooks repository
func NewWebhooksRepository(db *pgxpool.Pool) *WebhooksRepository {
	return &WebhooksRepository{db: db}
}

// Create inserts a new webhook
func (r *WebhooksRepository) Create(ctx context.Context, req *models.CreateWebhookRequest) (*models.Webhook, error) {
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	query := `
		INSERT INTO webhooks (
			url, signing_key, enabled, tenant_id
		)
		VALUES ($1, $2, $3, $4)
		RETURNING id, url, signing_key, enabled, tenant_id, created_at, updated_at
	`

	var webhook models.Webhook
	err := r.db.QueryRow(ctx, query,
		req.URL, req.SigningKey, enabled, req.TenantID,
	).Scan(
		&webhook.ID, &webhook.URL, &webhook.SigningKey, &webhook.Enabled,
		&webhook.TenantID, &webhook.CreatedAt, &webhook.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create webhook: %w", err)
	}

	return &webhook, nil
}

// GetByID retrieves a single webhook by ID
func (r *WebhooksRepository) GetByID(ctx context.Context, id uuid.UUID) (*models.Webhook, error) {
	query := `
		SELECT id, url, signing_key, enabled, tenant_id, created_at, updated_at
		FROM webhooks
		WHERE id = $1
	`

	var webhook models.Webhook
	err := r.db.QueryRow(ctx, query, id).Scan(
		&webhook.ID, &webhook.URL, &webhook.SigningKey, &webhook.Enabled,
		&webhook.TenantID, &webhook.CreatedAt, &webhook.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, apperrors.NewNotFoundError("webhook", "webhook not found")
		}
		return nil, fmt.Errorf("failed to get webhook: %w", err)
	}

	return &webhook, nil
}

// buildWebhookFilterConditions builds WHERE clause conditions and arguments from filters
func buildWebhookFilterConditions(filters *models.ListWebhooksFilters) (string, []interface{}) {
	var conditions []string
	var args []interface{}
	argCount := 1

	if filters.Enabled != nil {
		conditions = append(conditions, fmt.Sprintf("enabled = $%d", argCount))
		args = append(args, *filters.Enabled)
		argCount++
	}

	if filters.TenantID != nil {
		conditions = append(conditions, fmt.Sprintf("tenant_id = $%d", argCount))
		args = append(args, *filters.TenantID)
		// argCount is not used after this, but kept for consistency with other filter functions
	}

	whereClause := ""
	if len(conditions) > 0 {
		whereClause = " WHERE " + strings.Join(conditions, " AND ")
	}

	return whereClause, args
}

// List retrieves webhooks with optional filters
func (r *WebhooksRepository) List(ctx context.Context, filters *models.ListWebhooksFilters) ([]models.Webhook, error) {
	query := `
		SELECT id, url, signing_key, enabled, tenant_id, created_at, updated_at
		FROM webhooks
	`

	whereClause, args := buildWebhookFilterConditions(filters)
	query += whereClause
	argCount := len(args) + 1

	query += " ORDER BY created_at DESC"

	if filters.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argCount)
		args = append(args, filters.Limit)
		argCount++
	}

	if filters.Offset > 0 {
		query += fmt.Sprintf(" OFFSET $%d", argCount)
		args = append(args, filters.Offset)
	}

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list webhooks: %w", err)
	}
	defer rows.Close()

	webhooks := []models.Webhook{}
	for rows.Next() {
		var webhook models.Webhook
		err := rows.Scan(
			&webhook.ID, &webhook.URL, &webhook.SigningKey, &webhook.Enabled,
			&webhook.TenantID, &webhook.CreatedAt, &webhook.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan webhook: %w", err)
		}
		webhooks = append(webhooks, webhook)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating webhooks: %w", err)
	}

	return webhooks, nil
}

// Count returns the total count of webhooks matching the filters
func (r *WebhooksRepository) Count(ctx context.Context, filters *models.ListWebhooksFilters) (int64, error) {
	query := `SELECT COUNT(*) FROM webhooks`

	whereClause, args := buildWebhookFilterConditions(filters)
	query += whereClause

	var count int64
	err := r.db.QueryRow(ctx, query, args...).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count webhooks: %w", err)
	}

	return count, nil
}

// Update updates an existing webhook
func (r *WebhooksRepository) Update(ctx context.Context, id uuid.UUID, req *models.UpdateWebhookRequest) (*models.Webhook, error) {
	var updates []string
	var args []interface{}
	argCount := 1

	if req.URL != nil {
		updates = append(updates, fmt.Sprintf("url = $%d", argCount))
		args = append(args, *req.URL)
		argCount++
	}

	if req.SigningKey != nil {
		updates = append(updates, fmt.Sprintf("signing_key = $%d", argCount))
		args = append(args, *req.SigningKey)
		argCount++
	}

	if req.Enabled != nil {
		updates = append(updates, fmt.Sprintf("enabled = $%d", argCount))
		args = append(args, *req.Enabled)
		argCount++
	}

	if len(updates) == 0 {
		return r.GetByID(ctx, id)
	}

	updates = append(updates, fmt.Sprintf("updated_at = $%d", argCount))
	args = append(args, time.Now())
	argCount++

	args = append(args, id)

	query := fmt.Sprintf(`
		UPDATE webhooks
		SET %s
		WHERE id = $%d
		RETURNING id, url, signing_key, enabled, tenant_id, created_at, updated_at
	`, strings.Join(updates, ", "), argCount)

	var webhook models.Webhook
	err := r.db.QueryRow(ctx, query, args...).Scan(
		&webhook.ID, &webhook.URL, &webhook.SigningKey, &webhook.Enabled,
		&webhook.TenantID, &webhook.CreatedAt, &webhook.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, apperrors.NewNotFoundError("webhook", "webhook not found")
		}
		return nil, fmt.Errorf("failed to update webhook: %w", err)
	}

	return &webhook, nil
}

// Delete removes a webhook
func (r *WebhooksRepository) Delete(ctx context.Context, id uuid.UUID) error {
	query := `DELETE FROM webhooks WHERE id = $1`

	result, err := r.db.Exec(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete webhook: %w", err)
	}

	if result.RowsAffected() == 0 {
		return apperrors.NewNotFoundError("webhook", "webhook not found")
	}

	return nil
}

// ListEnabled retrieves all enabled webhooks
func (r *WebhooksRepository) ListEnabled(ctx context.Context) ([]models.Webhook, error) {
	filters := &models.ListWebhooksFilters{
		Enabled: func() *bool { b := true; return &b }(),
	}
	return r.List(ctx, filters)
}
