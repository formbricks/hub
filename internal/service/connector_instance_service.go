package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/formbricks/hub/internal/config"
	apperrors "github.com/formbricks/hub/internal/errors"
	"github.com/formbricks/hub/internal/models"
	"github.com/google/uuid"
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

// ConnectorInstanceService handles business logic for connector instances
type ConnectorInstanceService struct {
	repo   ConnectorInstanceRepository
	config *config.Config
}

// NewConnectorInstanceService creates a new connector instance service
func NewConnectorInstanceService(repo ConnectorInstanceRepository, cfg *config.Config) *ConnectorInstanceService {
	return &ConnectorInstanceService{
		repo:   repo,
		config: cfg,
	}
}

// getMaxInstancesForType returns the maximum number of instances allowed for a connector type
func (s *ConnectorInstanceService) getMaxInstancesForType(connectorType string) int {
	switch connectorType {
	case "polling":
		return s.config.MaxPollingConnectorInstances
	case "webhook":
		return s.config.MaxWebhookConnectorInstances
	case "output":
		return s.config.MaxOutputConnectorInstances
	case "enrichment":
		return s.config.MaxEnrichmentConnectorInstances
	default:
		return 10 // Default limit
	}
}

// validateConfig validates connector-specific configuration
func (s *ConnectorInstanceService) validateConfig(connectorType string, config []byte) error {
	// Basic validation - can be extended per connector type
	if len(config) == 0 {
		return apperrors.NewValidationError("config", "config is required")
	}

	// TODO: Add connector-specific validation
	// For now, just check that config is valid JSON
	var test interface{}
	if err := json.Unmarshal(config, &test); err != nil {
		return apperrors.NewValidationError("config", fmt.Sprintf("invalid JSON in config: %v", err))
	}

	return nil
}

// CreateConnectorInstance creates a new connector instance
func (s *ConnectorInstanceService) CreateConnectorInstance(ctx context.Context, req *models.CreateConnectorInstanceRequest) (*models.ConnectorInstance, error) {
	// Validate configuration
	if err := s.validateConfig(req.Type, req.Config); err != nil {
		return nil, err
	}

	// Check instance limit if running is true (or default true)
	running := true
	if req.Running != nil {
		running = *req.Running
	}

	if running {
		count, err := s.repo.CountRunningByType(ctx, req.Type)
		if err != nil {
			return nil, fmt.Errorf("failed to check instance limit: %w", err)
		}

		maxInstances := s.getMaxInstancesForType(req.Type)
		if count >= maxInstances {
			slog.Warn("Instance limit exceeded",
				"type", req.Type,
				"current", count,
				"max", maxInstances,
			)
			return nil, apperrors.NewValidationError("type", fmt.Sprintf("Maximum number of enabled %s connector instances (%d) has been reached", req.Type, maxInstances))
		}
	}

	return s.repo.Create(ctx, req)
}

// GetConnectorInstance retrieves a single connector instance by ID
func (s *ConnectorInstanceService) GetConnectorInstance(ctx context.Context, id uuid.UUID) (*models.ConnectorInstance, error) {
	return s.repo.GetByID(ctx, id)
}

// ListConnectorInstances retrieves a list of connector instances with optional filters
func (s *ConnectorInstanceService) ListConnectorInstances(ctx context.Context, filters *models.ListConnectorInstancesFilters) (*models.ListConnectorInstancesResponse, error) {
	// Set default limit if not provided
	if filters.Limit <= 0 {
		filters.Limit = 100 // Default limit
	}

	instances, err := s.repo.List(ctx, filters)
	if err != nil {
		return nil, err
	}

	total, err := s.repo.Count(ctx, filters)
	if err != nil {
		return nil, err
	}

	return &models.ListConnectorInstancesResponse{
		Data:   instances,
		Total:  total,
		Limit:  filters.Limit,
		Offset: filters.Offset,
	}, nil
}

// UpdateConnectorInstance updates an existing connector instance
func (s *ConnectorInstanceService) UpdateConnectorInstance(ctx context.Context, id uuid.UUID, req *models.UpdateConnectorInstanceRequest) (*models.ConnectorInstance, error) {
	// If config is being updated, validate it
	if req.Config != nil {
		// Get existing instance to know the type
		existing, err := s.repo.GetByID(ctx, id)
		if err != nil {
			return nil, err
		}

		if err := s.validateConfig(existing.Type, *req.Config); err != nil {
			return nil, err
		}
	}

	// If running is being set to true, check limit
	if req.Running != nil && *req.Running {
		existing, err := s.repo.GetByID(ctx, id)
		if err != nil {
			return nil, err
		}

		// Only check limit if instance is not already running
		if !existing.Running {
			count, err := s.repo.CountRunningByType(ctx, existing.Type)
			if err != nil {
				return nil, fmt.Errorf("failed to check instance limit: %w", err)
			}

			maxInstances := s.getMaxInstancesForType(existing.Type)
			if count >= maxInstances {
				slog.Warn("Instance limit exceeded on update",
					"type", existing.Type,
					"current", count,
					"max", maxInstances,
				)
				return nil, apperrors.NewValidationError("running", fmt.Sprintf("Maximum number of enabled %s connector instances (%d) has been reached", existing.Type, maxInstances))
			}
		}
	}

	return s.repo.Update(ctx, id, req)
}

// DeleteConnectorInstance deletes a connector instance by ID
func (s *ConnectorInstanceService) DeleteConnectorInstance(ctx context.Context, id uuid.UUID) error {
	return s.repo.Delete(ctx, id)
}

// StartConnectorInstance starts a connector instance
func (s *ConnectorInstanceService) StartConnectorInstance(ctx context.Context, id uuid.UUID) (*models.ConnectorInstance, error) {
	// Get existing instance
	instance, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	// Check instance limit
	count, err := s.repo.CountRunningByType(ctx, instance.Type)
	if err != nil {
		return nil, fmt.Errorf("failed to check instance limit: %w", err)
	}

	maxInstances := s.getMaxInstancesForType(instance.Type)
	if count >= maxInstances {
		slog.Warn("Instance limit exceeded on start",
			"type", instance.Type,
			"current", count,
			"max", maxInstances,
		)
		return nil, apperrors.NewValidationError("running", fmt.Sprintf("Maximum number of enabled %s connector instances (%d) has been reached", instance.Type, maxInstances))
	}

	// Set running = true and clear error
	errorNull := ""
	updateReq := &models.UpdateConnectorInstanceRequest{
		Running: func() *bool { b := true; return &b }(),
		Error:   &errorNull,
	}

	return s.repo.Update(ctx, id, updateReq)
}

// StopConnectorInstance stops a connector instance
func (s *ConnectorInstanceService) StopConnectorInstance(ctx context.Context, id uuid.UUID) (*models.ConnectorInstance, error) {
	// Set running = false (does not clear error)
	updateReq := &models.UpdateConnectorInstanceRequest{
		Running: func() *bool { b := false; return &b }(),
	}

	return s.repo.Update(ctx, id, updateReq)
}
