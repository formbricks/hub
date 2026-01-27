package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	apperrors "github.com/formbricks/hub/internal/errors"
	"github.com/formbricks/hub/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ConnectorInstanceRepository defines the interface for connector instance data access
type ConnectorInstanceRepository interface {
	Create(ctx context.Context, req *models.CreateConnectorInstanceRequest) (*models.ConnectorInstance, error)
	GetByID(ctx context.Context, id uuid.UUID) (*models.ConnectorInstance, error)
	GetByNameAndInstanceID(ctx context.Context, name, instanceID string) (*models.ConnectorInstance, error)
	List(ctx context.Context, filters *models.ListConnectorInstancesFilters) ([]models.ConnectorInstance, error)
	Count(ctx context.Context, filters *models.ListConnectorInstancesFilters) (int64, error)
	Update(ctx context.Context, id uuid.UUID, req *models.UpdateConnectorInstanceRequest) (*models.ConnectorInstance, error)
	Delete(ctx context.Context, id uuid.UUID) error
	UpdateState(ctx context.Context, id uuid.UUID, state json.RawMessage) error
	SetRunning(ctx context.Context, id uuid.UUID, running bool) error
	SetRunningWithError(ctx context.Context, id uuid.UUID, running bool, errorMsg *string) error
	CountRunningByType(ctx context.Context, connectorType string) (int, error)
	ListRunningByType(ctx context.Context, connectorType string) ([]models.ConnectorInstance, error)
}

// connectorInstanceRepository implements ConnectorInstanceRepository
type connectorInstanceRepository struct {
	db *pgxpool.Pool
}

// NewConnectorInstanceRepository creates a new connector instance repository
func NewConnectorInstanceRepository(db *pgxpool.Pool) ConnectorInstanceRepository {
	return &connectorInstanceRepository{db: db}
}

// Create inserts a new connector instance
func (r *connectorInstanceRepository) Create(ctx context.Context, req *models.CreateConnectorInstanceRequest) (*models.ConnectorInstance, error) {
	running := true // Default to running
	if req.Running != nil {
		running = *req.Running
	}

	query := `
		INSERT INTO connector_instances (
			name, instance_id, type, config, state, running
		)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, name, instance_id, type, config, state, running, error, created_at, updated_at
	`

	var instance models.ConnectorInstance
	stateJSON := []byte("{}") // Default empty state

	err := r.db.QueryRow(ctx, query,
		req.Name, req.InstanceID, req.Type, req.Config, stateJSON, running,
	).Scan(
		&instance.ID, &instance.Name, &instance.InstanceID, &instance.Type,
		&instance.Config, &instance.State, &instance.Running, &instance.Error,
		&instance.CreatedAt, &instance.UpdatedAt,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" { // Unique violation
			slog.Error("Failed to create connector instance: duplicate name/instance_id",
				"name", req.Name,
				"instance_id", req.InstanceID,
				"error", err,
			)
			return nil, apperrors.NewValidationError("name", fmt.Sprintf("connector instance with name '%s' and instance_id '%s' already exists", req.Name, req.InstanceID))
		}
		slog.Error("Failed to create connector instance",
			"name", req.Name,
			"instance_id", req.InstanceID,
			"type", req.Type,
			"error", err,
		)
		return nil, fmt.Errorf("failed to create connector instance: %w", err)
	}

	slog.Info("Created connector instance",
		"id", instance.ID,
		"name", instance.Name,
		"instance_id", instance.InstanceID,
		"type", instance.Type,
	)

	return &instance, nil
}

// GetByID retrieves a single connector instance by ID
func (r *connectorInstanceRepository) GetByID(ctx context.Context, id uuid.UUID) (*models.ConnectorInstance, error) {
	query := `
		SELECT id, name, instance_id, type, config, state, running, error, created_at, updated_at
		FROM connector_instances
		WHERE id = $1
	`

	var instance models.ConnectorInstance
	err := r.db.QueryRow(ctx, query, id).Scan(
		&instance.ID, &instance.Name, &instance.InstanceID, &instance.Type,
		&instance.Config, &instance.State, &instance.Running, &instance.Error,
		&instance.CreatedAt, &instance.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, apperrors.NewNotFoundError("connector instance", "connector instance not found")
		}
		return nil, fmt.Errorf("failed to get connector instance: %w", err)
	}

	return &instance, nil
}

// GetByNameAndInstanceID retrieves a connector instance by name and instance_id
func (r *connectorInstanceRepository) GetByNameAndInstanceID(ctx context.Context, name, instanceID string) (*models.ConnectorInstance, error) {
	query := `
		SELECT id, name, instance_id, type, config, state, running, error, created_at, updated_at
		FROM connector_instances
		WHERE name = $1 AND instance_id = $2
	`

	var instance models.ConnectorInstance
	err := r.db.QueryRow(ctx, query, name, instanceID).Scan(
		&instance.ID, &instance.Name, &instance.InstanceID, &instance.Type,
		&instance.Config, &instance.State, &instance.Running, &instance.Error,
		&instance.CreatedAt, &instance.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, apperrors.NewNotFoundError("connector instance", "connector instance not found")
		}
		return nil, fmt.Errorf("failed to get connector instance: %w", err)
	}

	return &instance, nil
}

// buildConnectorInstanceFilterConditions builds WHERE clause conditions for connector instance filters
func buildConnectorInstanceFilterConditions(filters *models.ListConnectorInstancesFilters) (string, []interface{}) {
	var conditions []string
	var args []interface{}
	argCount := 1

	if filters.Type != nil {
		conditions = append(conditions, fmt.Sprintf("type = $%d", argCount))
		args = append(args, *filters.Type)
		argCount++
	}

	if filters.Running != nil {
		conditions = append(conditions, fmt.Sprintf("running = $%d", argCount))
		args = append(args, *filters.Running)
		argCount++
	}

	if filters.Name != nil {
		conditions = append(conditions, fmt.Sprintf("name = $%d", argCount))
		args = append(args, *filters.Name)
		// argCount would be incremented here if more filters were added
	}

	whereClause := ""
	if len(conditions) > 0 {
		whereClause = " WHERE " + strings.Join(conditions, " AND ")
	}

	return whereClause, args
}

// List retrieves connector instances with optional filters
func (r *connectorInstanceRepository) List(ctx context.Context, filters *models.ListConnectorInstancesFilters) ([]models.ConnectorInstance, error) {
	query := `
		SELECT id, name, instance_id, type, config, state, running, error, created_at, updated_at
		FROM connector_instances
	`

	whereClause, args := buildConnectorInstanceFilterConditions(filters)
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
		return nil, fmt.Errorf("failed to list connector instances: %w", err)
	}
	defer rows.Close()

	instances := []models.ConnectorInstance{}
	for rows.Next() {
		var instance models.ConnectorInstance
		err := rows.Scan(
			&instance.ID, &instance.Name, &instance.InstanceID, &instance.Type,
			&instance.Config, &instance.State, &instance.Running, &instance.Error,
			&instance.CreatedAt, &instance.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan connector instance: %w", err)
		}
		instances = append(instances, instance)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating connector instances: %w", err)
	}

	return instances, nil
}

// Count returns the total count of connector instances matching the filters
func (r *connectorInstanceRepository) Count(ctx context.Context, filters *models.ListConnectorInstancesFilters) (int64, error) {
	query := `SELECT COUNT(*) FROM connector_instances`

	whereClause, args := buildConnectorInstanceFilterConditions(filters)
	query += whereClause

	var count int64
	err := r.db.QueryRow(ctx, query, args...).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count connector instances: %w", err)
	}

	return count, nil
}

// Update updates an existing connector instance
func (r *connectorInstanceRepository) Update(ctx context.Context, id uuid.UUID, req *models.UpdateConnectorInstanceRequest) (*models.ConnectorInstance, error) {
	var updates []string
	var args []interface{}
	argCount := 1

	if req.Config != nil {
		updates = append(updates, fmt.Sprintf("config = $%d", argCount))
		args = append(args, req.Config)
		argCount++
	}

	if req.State != nil {
		updates = append(updates, fmt.Sprintf("state = $%d", argCount))
		args = append(args, req.State)
		argCount++
	}

	if req.Running != nil {
		updates = append(updates, fmt.Sprintf("running = $%d", argCount))
		args = append(args, *req.Running)
		argCount++
	}

	if req.Error != nil {
		updates = append(updates, fmt.Sprintf("error = $%d", argCount))
		args = append(args, *req.Error)
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
		UPDATE connector_instances
		SET %s
		WHERE id = $%d
		RETURNING id, name, instance_id, type, config, state, running, error, created_at, updated_at
	`, strings.Join(updates, ", "), argCount)

	var instance models.ConnectorInstance
	err := r.db.QueryRow(ctx, query, args...).Scan(
		&instance.ID, &instance.Name, &instance.InstanceID, &instance.Type,
		&instance.Config, &instance.State, &instance.Running, &instance.Error,
		&instance.CreatedAt, &instance.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, apperrors.NewNotFoundError("connector instance", "connector instance not found")
		}
		return nil, fmt.Errorf("failed to update connector instance: %w", err)
	}

	return &instance, nil
}

// Delete removes a connector instance
func (r *connectorInstanceRepository) Delete(ctx context.Context, id uuid.UUID) error {
	query := `DELETE FROM connector_instances WHERE id = $1`

	result, err := r.db.Exec(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete connector instance: %w", err)
	}

	if result.RowsAffected() == 0 {
		return apperrors.NewNotFoundError("connector instance", "connector instance not found")
	}

	return nil
}

// UpdateState updates only the state field of a connector instance
func (r *connectorInstanceRepository) UpdateState(ctx context.Context, id uuid.UUID, state json.RawMessage) error {
	query := `
		UPDATE connector_instances
		SET state = $1, updated_at = $2
		WHERE id = $3
	`

	result, err := r.db.Exec(ctx, query, state, time.Now(), id)
	if err != nil {
		return fmt.Errorf("failed to update connector instance state: %w", err)
	}

	if result.RowsAffected() == 0 {
		return apperrors.NewNotFoundError("connector instance", "connector instance not found")
	}

	return nil
}

// SetRunning updates the running state of a connector instance
func (r *connectorInstanceRepository) SetRunning(ctx context.Context, id uuid.UUID, running bool) error {
	query := `
		UPDATE connector_instances
		SET running = $1, updated_at = $2
		WHERE id = $3
	`

	result, err := r.db.Exec(ctx, query, running, time.Now(), id)
	if err != nil {
		return fmt.Errorf("failed to set connector instance running state: %w", err)
	}

	if result.RowsAffected() == 0 {
		return apperrors.NewNotFoundError("connector instance", "connector instance not found")
	}

	return nil
}

// SetRunningWithError updates both the running state and error field atomically
func (r *connectorInstanceRepository) SetRunningWithError(ctx context.Context, id uuid.UUID, running bool, errorMsg *string) error {
	query := `
		UPDATE connector_instances
		SET running = $1, error = $2, updated_at = $3
		WHERE id = $4
	`

	result, err := r.db.Exec(ctx, query, running, errorMsg, time.Now(), id)
	if err != nil {
		return fmt.Errorf("failed to set connector instance running state and error: %w", err)
	}

	if result.RowsAffected() == 0 {
		return apperrors.NewNotFoundError("connector instance", "connector instance not found")
	}

	return nil
}

// CountRunningByType counts the number of running instances for a given connector type
func (r *connectorInstanceRepository) CountRunningByType(ctx context.Context, connectorType string) (int, error) {
	query := `
		SELECT COUNT(*)
		FROM connector_instances
		WHERE type = $1 AND running = true
	`

	var count int
	err := r.db.QueryRow(ctx, query, connectorType).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count running connector instances by type: %w", err)
	}

	return count, nil
}

// ListRunningByType lists running instances for a given connector type, sorted by created_at ASC
func (r *connectorInstanceRepository) ListRunningByType(ctx context.Context, connectorType string) ([]models.ConnectorInstance, error) {
	query := `
		SELECT id, name, instance_id, type, config, state, running, error, created_at, updated_at
		FROM connector_instances
		WHERE type = $1 AND running = true
		ORDER BY created_at ASC
	`

	rows, err := r.db.Query(ctx, query, connectorType)
	if err != nil {
		return nil, fmt.Errorf("failed to list running connector instances by type: %w", err)
	}
	defer rows.Close()

	instances := []models.ConnectorInstance{}
	for rows.Next() {
		var instance models.ConnectorInstance
		err := rows.Scan(
			&instance.ID, &instance.Name, &instance.InstanceID, &instance.Type,
			&instance.Config, &instance.State, &instance.Running, &instance.Error,
			&instance.CreatedAt, &instance.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan connector instance: %w", err)
		}
		instances = append(instances, instance)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating connector instances: %w", err)
	}

	return instances, nil
}
