package repository

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/formbricks/hub/internal/datatypes"
	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
)

// ErrInvalidEventTypeInDB is returned when the database contains an invalid event type string.
var ErrInvalidEventTypeInDB = errors.New("invalid event type in database")

// WebhooksRepository handles data access for webhooks.
type WebhooksRepository struct {
	db *pgxpool.Pool
}

// NewWebhooksRepository creates a new webhooks repository.
func NewWebhooksRepository(db *pgxpool.Pool) *WebhooksRepository {
	return &WebhooksRepository{db: db}
}

// Create inserts a new webhook.
func (r *WebhooksRepository) Create(ctx context.Context, req *models.CreateWebhookRequest) (*models.Webhook, error) {
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	var eventTypes []string
	if len(req.EventTypes) > 0 {
		eventTypes = make([]string, len(req.EventTypes))
		for i, et := range req.EventTypes {
			eventTypes[i] = et.String()
		}
	}

	query := `
		INSERT INTO webhooks (
			url, signing_key, enabled, tenant_id, event_types
		)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, url, signing_key, enabled, tenant_id, created_at, updated_at, event_types
	`

	var (
		webhook      models.Webhook
		dbEventTypes []string
	)

	err := r.db.QueryRow(ctx, query,
		req.URL, req.SigningKey, enabled, req.TenantID, eventTypes,
	).Scan(
		&webhook.ID, &webhook.URL, &webhook.SigningKey, &webhook.Enabled,
		&webhook.TenantID, &webhook.CreatedAt, &webhook.UpdatedAt, &dbEventTypes,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create webhook: %w", err)
	}

	if dbEventTypes != nil {
		webhook.EventTypes = make([]datatypes.EventType, 0, len(dbEventTypes))
		for _, s := range dbEventTypes {
			et, ok := datatypes.ParseEventType(s)
			if !ok {
				return nil, fmt.Errorf("%w: %s", ErrInvalidEventTypeInDB, s)
			}

			webhook.EventTypes = append(webhook.EventTypes, et)
		}
	}

	return &webhook, nil
}

// GetByID retrieves a single webhook by ID.
func (r *WebhooksRepository) GetByID(ctx context.Context, id uuid.UUID) (*models.Webhook, error) {
	query := `
		SELECT id, url, signing_key, enabled, tenant_id, created_at, updated_at, event_types, disabled_reason, disabled_at
		FROM webhooks
		WHERE id = $1
	`

	var (
		webhook      models.Webhook
		dbEventTypes []string
	)

	err := r.db.QueryRow(ctx, query, id).Scan(
		&webhook.ID, &webhook.URL, &webhook.SigningKey, &webhook.Enabled,
		&webhook.TenantID, &webhook.CreatedAt, &webhook.UpdatedAt, &dbEventTypes,
		&webhook.DisabledReason, &webhook.DisabledAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, huberrors.NewNotFoundError("webhook", "webhook not found")
		}

		return nil, fmt.Errorf("failed to get webhook: %w", err)
	}

	if dbEventTypes != nil {
		webhook.EventTypes = make([]datatypes.EventType, 0, len(dbEventTypes))
		for _, s := range dbEventTypes {
			et, ok := datatypes.ParseEventType(s)
			if !ok {
				return nil, fmt.Errorf("%w: %s", ErrInvalidEventTypeInDB, s)
			}

			webhook.EventTypes = append(webhook.EventTypes, et)
		}
	}

	return &webhook, nil
}

// buildWebhookFilterConditions builds WHERE clause conditions and arguments from filters.
func buildWebhookFilterConditions(filters *models.ListWebhooksFilters) (whereClause string, args []any) {
	var conditions []string

	if filters.Enabled != nil {
		conditions = append(conditions, fmt.Sprintf("enabled = $%d", len(args)+1))
		args = append(args, *filters.Enabled)
	}

	if filters.TenantID != nil {
		conditions = append(conditions, fmt.Sprintf("tenant_id = $%d", len(args)+1))
		args = append(args, *filters.TenantID)
	}

	if len(conditions) > 0 {
		whereClause = " WHERE " + strings.Join(conditions, " AND ")
	}

	return whereClause, args
}

// List retrieves webhooks with optional filters.
func (r *WebhooksRepository) List(ctx context.Context, filters *models.ListWebhooksFilters) ([]models.Webhook, error) {
	query := `
		SELECT id, url, signing_key, enabled, tenant_id, created_at, updated_at, event_types, disabled_reason, disabled_at
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
		var (
			webhook      models.Webhook
			dbEventTypes []string
		)

		err := rows.Scan(
			&webhook.ID, &webhook.URL, &webhook.SigningKey, &webhook.Enabled,
			&webhook.TenantID, &webhook.CreatedAt, &webhook.UpdatedAt, &dbEventTypes,
			&webhook.DisabledReason, &webhook.DisabledAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan webhook: %w", err)
		}

		if dbEventTypes != nil {
			webhook.EventTypes = make([]datatypes.EventType, 0, len(dbEventTypes))
			for _, s := range dbEventTypes {
				et, ok := datatypes.ParseEventType(s)
				if !ok {
					return nil, fmt.Errorf("%w: %s", ErrInvalidEventTypeInDB, s)
				}

				webhook.EventTypes = append(webhook.EventTypes, et)
			}
		}

		webhooks = append(webhooks, webhook)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating webhooks: %w", err)
	}

	return webhooks, nil
}

// Count returns the total count of webhooks matching the filters.
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

// Update updates an existing webhook.
func (r *WebhooksRepository) Update(ctx context.Context, id uuid.UUID, req *models.UpdateWebhookRequest) (*models.Webhook, error) {
	var (
		updates []string
		args    []any
	)

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
		// Re-enabling clears disabled state (read-only fields set by the system)
		if *req.Enabled {
			updates = append(updates, "disabled_reason = NULL", "disabled_at = NULL")
		}
	}

	if req.TenantID != nil {
		// Empty string clears tenant_id (store as NULL)
		var val any
		if *req.TenantID == "" {
			val = nil
		} else {
			val = *req.TenantID
		}

		updates = append(updates, fmt.Sprintf("tenant_id = $%d", argCount))
		args = append(args, val)
		argCount++
	}

	if req.EventTypes != nil {
		eventTypes := make([]string, len(*req.EventTypes))
		for i, et := range *req.EventTypes {
			eventTypes[i] = et.String()
		}

		updates = append(updates, fmt.Sprintf("event_types = $%d", argCount))
		args = append(args, eventTypes)
		argCount++
	}

	if req.DisabledReason != nil {
		updates = append(updates, fmt.Sprintf("disabled_reason = $%d", argCount))
		args = append(args, *req.DisabledReason)
		argCount++
	}

	if req.DisabledAt != nil {
		updates = append(updates, fmt.Sprintf("disabled_at = $%d", argCount))
		args = append(args, *req.DisabledAt)
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
		RETURNING id, url, signing_key, enabled, tenant_id, created_at, updated_at, event_types, disabled_reason, disabled_at
	`, strings.Join(updates, ", "), argCount)

	var (
		webhook      models.Webhook
		dbEventTypes []string
	)

	err := r.db.QueryRow(ctx, query, args...).Scan(
		&webhook.ID, &webhook.URL, &webhook.SigningKey, &webhook.Enabled,
		&webhook.TenantID, &webhook.CreatedAt, &webhook.UpdatedAt, &dbEventTypes,
		&webhook.DisabledReason, &webhook.DisabledAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, huberrors.NewNotFoundError("webhook", "webhook not found")
		}

		return nil, fmt.Errorf("failed to update webhook: %w", err)
	}

	if dbEventTypes != nil {
		webhook.EventTypes = make([]datatypes.EventType, 0, len(dbEventTypes))
		for _, s := range dbEventTypes {
			et, ok := datatypes.ParseEventType(s)
			if !ok {
				return nil, fmt.Errorf("%w: %s", ErrInvalidEventTypeInDB, s)
			}

			webhook.EventTypes = append(webhook.EventTypes, et)
		}
	}

	return &webhook, nil
}

// Delete removes a webhook.
func (r *WebhooksRepository) Delete(ctx context.Context, id uuid.UUID) error {
	query := `DELETE FROM webhooks WHERE id = $1`

	result, err := r.db.Exec(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete webhook: %w", err)
	}

	if result.RowsAffected() == 0 {
		return huberrors.NewNotFoundError("webhook", "webhook not found")
	}

	return nil
}

// ListEnabled retrieves all enabled webhooks.
func (r *WebhooksRepository) ListEnabled(ctx context.Context) ([]models.Webhook, error) {
	filters := &models.ListWebhooksFilters{
		Enabled: func() *bool {
			b := true

			return &b
		}(),
	}

	return r.List(ctx, filters)
}

// maxWebhookListLimit caps the number of webhooks returned for delivery to avoid unbounded queries.
const maxWebhookListLimit = 1000

// ListEnabledForEventType retrieves enabled webhooks that should receive a specific event type.
func (r *WebhooksRepository) ListEnabledForEventType(ctx context.Context, eventType string) ([]models.Webhook, error) {
	query := `
		SELECT id, url, signing_key, enabled, tenant_id, created_at, updated_at, event_types, disabled_reason, disabled_at
		FROM webhooks
		WHERE enabled = true
		AND (event_types IS NULL OR event_types = '{}' OR event_types @> ARRAY[$1]::VARCHAR(64)[])
		LIMIT ` + strconv.Itoa(maxWebhookListLimit) + `
	`

	rows, err := r.db.Query(ctx, query, eventType)
	if err != nil {
		return nil, fmt.Errorf("failed to list enabled webhooks for event type: %w", err)
	}
	defer rows.Close()

	webhooks := []models.Webhook{}

	for rows.Next() {
		var (
			webhook      models.Webhook
			dbEventTypes []string
		)

		err := rows.Scan(
			&webhook.ID, &webhook.URL, &webhook.SigningKey, &webhook.Enabled,
			&webhook.TenantID, &webhook.CreatedAt, &webhook.UpdatedAt, &dbEventTypes,
			&webhook.DisabledReason, &webhook.DisabledAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan webhook: %w", err)
		}

		if dbEventTypes != nil {
			webhook.EventTypes = make([]datatypes.EventType, 0, len(dbEventTypes))
			for _, s := range dbEventTypes {
				et, ok := datatypes.ParseEventType(s)
				if !ok {
					return nil, fmt.Errorf("%w: %s", ErrInvalidEventTypeInDB, s)
				}

				webhook.EventTypes = append(webhook.EventTypes, et)
			}
		}

		webhooks = append(webhooks, webhook)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating webhooks: %w", err)
	}

	return webhooks, nil
}
