package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/formbricks/hub/internal/datatypes"
	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
)

// DBWebhooksRepository is the database implementation of webhook data access.
// It satisfies service.WebhooksRepository; in production it is usually wrapped
// by service.NewCachingWebhooksRepository for ListEnabledForEventType and GetByID.
type DBWebhooksRepository struct {
	db *pgxpool.Pool
}

// NewDBWebhooksRepository creates a new webhooks repository that reads and writes to the DB.
func NewDBWebhooksRepository(db *pgxpool.Pool) *DBWebhooksRepository {
	return &DBWebhooksRepository{db: db}
}

// Create inserts a new webhook.
func (r *DBWebhooksRepository) Create(ctx context.Context, req *models.CreateWebhookRequest) (*models.Webhook, error) {
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

	webhook.EventTypes, err = parseDBEventTypes(dbEventTypes)
	if err != nil {
		return nil, err
	}

	return &webhook, nil
}

// GetByIDInternal retrieves a single webhook by ID including internal fields.
func (r *DBWebhooksRepository) GetByIDInternal(ctx context.Context, id uuid.UUID) (*models.Webhook, error) {
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

	webhook.EventTypes, err = parseDBEventTypes(dbEventTypes)
	if err != nil {
		return nil, err
	}

	return &webhook, nil
}

// GetByID retrieves a single webhook by ID without internal secrets.
func (r *DBWebhooksRepository) GetByID(ctx context.Context, id uuid.UUID) (*models.WebhookResponse, error) {
	query := `
		SELECT id, url, enabled, tenant_id, created_at, updated_at, event_types, disabled_reason, disabled_at
		FROM webhooks
		WHERE id = $1
	`

	var (
		webhook      models.WebhookResponse
		dbEventTypes []string
	)

	err := r.db.QueryRow(ctx, query, id).Scan(
		&webhook.ID, &webhook.URL, &webhook.Enabled,
		&webhook.TenantID, &webhook.CreatedAt, &webhook.UpdatedAt, &dbEventTypes,
		&webhook.DisabledReason, &webhook.DisabledAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, huberrors.NewNotFoundError("webhook", "webhook not found")
		}

		return nil, fmt.Errorf("failed to get public webhook: %w", err)
	}

	webhook.EventTypes, err = parseDBEventTypes(dbEventTypes)
	if err != nil {
		return nil, err
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

// ListInternal retrieves webhooks with optional filters including internal fields.
func (r *DBWebhooksRepository) ListInternal(ctx context.Context, filters *models.ListWebhooksFilters) ([]models.Webhook, error) {
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

		webhook.EventTypes, err = parseDBEventTypes(dbEventTypes)
		if err != nil {
			return nil, err
		}

		webhooks = append(webhooks, webhook)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating webhooks: %w", err)
	}

	return webhooks, nil
}

// List retrieves webhooks with optional filters, without internal secrets.
func (r *DBWebhooksRepository) List(ctx context.Context, filters *models.ListWebhooksFilters) ([]models.WebhookResponse, error) {
	query := `
		SELECT id, url, enabled, tenant_id, created_at, updated_at, event_types, disabled_reason, disabled_at
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
		return nil, fmt.Errorf("failed to list public webhooks: %w", err)
	}
	defer rows.Close()

	webhooks := []models.WebhookResponse{}

	for rows.Next() {
		var (
			webhook      models.WebhookResponse
			dbEventTypes []string
		)

		err := rows.Scan(
			&webhook.ID, &webhook.URL, &webhook.Enabled,
			&webhook.TenantID, &webhook.CreatedAt, &webhook.UpdatedAt, &dbEventTypes,
			&webhook.DisabledReason, &webhook.DisabledAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan public webhook: %w", err)
		}

		webhook.EventTypes, err = parseDBEventTypes(dbEventTypes)
		if err != nil {
			return nil, err
		}

		webhooks = append(webhooks, webhook)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating public webhooks: %w", err)
	}

	return webhooks, nil
}

// Count returns the total count of webhooks matching the filters.
func (r *DBWebhooksRepository) Count(ctx context.Context, filters *models.ListWebhooksFilters) (int64, error) {
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
func (r *DBWebhooksRepository) Update(ctx context.Context, id uuid.UUID, req *models.UpdateWebhookRequest) (*models.Webhook, error) {
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
		return r.GetByIDInternal(ctx, id)
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

	webhook.EventTypes, err = parseDBEventTypes(dbEventTypes)
	if err != nil {
		return nil, err
	}

	return &webhook, nil
}

// Delete removes a webhook.
func (r *DBWebhooksRepository) Delete(ctx context.Context, id uuid.UUID) error {
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

// parseDBEventTypes converts a DB string slice to []datatypes.EventType. Returns (nil, nil) for nil input.
func parseDBEventTypes(ss []string) ([]datatypes.EventType, error) {
	if ss == nil {
		return nil, nil
	}

	out := make([]datatypes.EventType, 0, len(ss))
	for _, s := range ss {
		et, ok := datatypes.ParseEventType(s)
		if !ok {
			return nil, fmt.Errorf("%w: %s", datatypes.ErrInvalidEventType, s)
		}

		out = append(out, et)
	}

	return out, nil
}

// ListEnabled retrieves all enabled webhooks.
func (r *DBWebhooksRepository) ListEnabled(ctx context.Context) ([]models.Webhook, error) {
	filters := &models.ListWebhooksFilters{
		Enabled: func() *bool {
			b := true

			return &b
		}(),
	}

	return r.ListInternal(ctx, filters)
}

// ListEnabledForEventType retrieves all enabled webhooks that should receive a specific event type.
// Order is deterministic (ORDER BY id) so delivery behavior is consistent.
func (r *DBWebhooksRepository) ListEnabledForEventType(ctx context.Context, eventType string) ([]models.Webhook, error) {
	query := `
		SELECT id, url, signing_key, enabled, tenant_id, created_at, updated_at, event_types, disabled_reason, disabled_at
		FROM webhooks
		WHERE enabled = true
		AND (event_types IS NULL OR event_types = '{}' OR event_types @> ARRAY[$1]::VARCHAR(64)[])
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

		webhook.EventTypes, err = parseDBEventTypes(dbEventTypes)
		if err != nil {
			return nil, err
		}

		webhooks = append(webhooks, webhook)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating webhooks: %w", err)
	}

	return webhooks, nil
}
