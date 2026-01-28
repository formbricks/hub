package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/models"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockConnectorInstanceRepository is a mock implementation of ConnectorInstanceRepository
type MockConnectorInstanceRepository struct {
	mock.Mock
}

func (m *MockConnectorInstanceRepository) Create(ctx context.Context, req *models.CreateConnectorInstanceRequest) (*models.ConnectorInstance, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.ConnectorInstance), args.Error(1)
}

func (m *MockConnectorInstanceRepository) GetByID(ctx context.Context, id uuid.UUID) (*models.ConnectorInstance, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.ConnectorInstance), args.Error(1)
}

func (m *MockConnectorInstanceRepository) GetByNameAndInstanceID(ctx context.Context, name, instanceID string) (*models.ConnectorInstance, error) {
	args := m.Called(ctx, name, instanceID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.ConnectorInstance), args.Error(1)
}

func (m *MockConnectorInstanceRepository) List(ctx context.Context, filters *models.ListConnectorInstancesFilters) ([]models.ConnectorInstance, error) {
	args := m.Called(ctx, filters)
	return args.Get(0).([]models.ConnectorInstance), args.Error(1)
}

func (m *MockConnectorInstanceRepository) Count(ctx context.Context, filters *models.ListConnectorInstancesFilters) (int64, error) {
	args := m.Called(ctx, filters)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockConnectorInstanceRepository) Update(ctx context.Context, id uuid.UUID, req *models.UpdateConnectorInstanceRequest) (*models.ConnectorInstance, error) {
	args := m.Called(ctx, id, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.ConnectorInstance), args.Error(1)
}

func (m *MockConnectorInstanceRepository) Delete(ctx context.Context, id uuid.UUID) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *MockConnectorInstanceRepository) UpdateState(ctx context.Context, id uuid.UUID, state json.RawMessage) error {
	args := m.Called(ctx, id, state)
	return args.Error(0)
}

func (m *MockConnectorInstanceRepository) SetRunning(ctx context.Context, id uuid.UUID, running bool) error {
	args := m.Called(ctx, id, running)
	return args.Error(0)
}

func (m *MockConnectorInstanceRepository) SetRunningWithError(ctx context.Context, id uuid.UUID, running bool, errorMsg *string) error {
	args := m.Called(ctx, id, running, errorMsg)
	return args.Error(0)
}

func (m *MockConnectorInstanceRepository) CountRunningByType(ctx context.Context, connectorType string) (int, error) {
	args := m.Called(ctx, connectorType)
	return args.Int(0), args.Error(1)
}

func (m *MockConnectorInstanceRepository) ListRunningByType(ctx context.Context, connectorType string) ([]models.ConnectorInstance, error) {
	args := m.Called(ctx, connectorType)
	return args.Get(0).([]models.ConnectorInstance), args.Error(1)
}

func setupTestService(t *testing.T) (*ConnectorInstanceService, *MockConnectorInstanceRepository, *config.Config) {
	mockRepo := new(MockConnectorInstanceRepository)
	cfg := &config.Config{
		MaxPollingConnectorInstances:    10,
		MaxWebhookConnectorInstances:    10,
		MaxOutputConnectorInstances:     10,
		MaxEnrichmentConnectorInstances: 10,
	}
	service := NewConnectorInstanceService(mockRepo, cfg)
	return service, mockRepo, cfg
}

func TestConnectorInstanceService_CreateConnectorInstance(t *testing.T) {
	service, mockRepo, cfg := setupTestService(t)
	ctx := context.Background()

	t.Run("creates instance when limit not exceeded", func(t *testing.T) {
		configJSON := json.RawMessage(`{"api_key": "test-key"}`)
		req := &models.CreateConnectorInstanceRequest{
			Name:       "formbricks",
			InstanceID: "test-instance",
			Type:       "polling",
			Config:     configJSON,
		}

		instance := &models.ConnectorInstance{
			ID:         uuid.New(),
			Name:       "formbricks",
			InstanceID: "test-instance",
			Type:       "polling",
			Config:     configJSON,
			Running:    true,
		}

		mockRepo.On("CountRunningByType", ctx, "polling").Return(5, nil)
		mockRepo.On("Create", ctx, req).Return(instance, nil)

		result, err := service.CreateConnectorInstance(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, instance.ID, result.ID)
		mockRepo.AssertExpectations(t)
	})

	t.Run("fails when limit exceeded", func(t *testing.T) {
		// Reset mock to clear expectations from previous test case
		mockRepo.ExpectedCalls = nil
		mockRepo.Calls = nil

		configJSON := json.RawMessage(`{"api_key": "test-key"}`)
		req := &models.CreateConnectorInstanceRequest{
			Name:       "formbricks",
			InstanceID: "test-instance-2",
			Type:       "polling",
			Config:     configJSON,
			Running:    func() *bool { b := true; return &b }(), // Explicitly set to running
		}

		mockRepo.On("CountRunningByType", ctx, "polling").Return(cfg.MaxPollingConnectorInstances, nil)

		_, err := service.CreateConnectorInstance(ctx, req)
		assert.Error(t, err)
		// Verify CountRunningByType was called but Create was not
		mockRepo.AssertCalled(t, "CountRunningByType", ctx, "polling")
		// Create should not be called when limit is exceeded
		mockRepo.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
	})

	t.Run("validates config JSON", func(t *testing.T) {
		invalidConfig := json.RawMessage(`invalid json`)
		req := &models.CreateConnectorInstanceRequest{
			Name:       "formbricks",
			InstanceID: "test-instance",
			Type:       "polling",
			Config:     invalidConfig,
		}

		_, err := service.CreateConnectorInstance(ctx, req)
		assert.Error(t, err)
	})
}

func TestConnectorInstanceService_StartConnectorInstance(t *testing.T) {
	service, mockRepo, cfg := setupTestService(t)
	ctx := context.Background()

	t.Run("starts instance when limit not exceeded", func(t *testing.T) {
		instanceID := uuid.New()
		instance := &models.ConnectorInstance{
			ID:      instanceID,
			Type:    "polling",
			Running: false,
		}

		updatedInstance := &models.ConnectorInstance{
			ID:      instanceID,
			Type:    "polling",
			Running: true,
			Error:   nil,
		}

		mockRepo.On("GetByID", ctx, instanceID).Return(instance, nil)
		mockRepo.On("CountRunningByType", ctx, "polling").Return(5, nil)
		mockRepo.On("Update", ctx, instanceID, mock.AnythingOfType("*models.UpdateConnectorInstanceRequest")).Return(updatedInstance, nil)

		result, err := service.StartConnectorInstance(ctx, instanceID)
		require.NoError(t, err)
		assert.True(t, result.Running)
		assert.Nil(t, result.Error)
		mockRepo.AssertExpectations(t)
	})

	t.Run("fails when limit exceeded", func(t *testing.T) {
		instanceID := uuid.New()
		instance := &models.ConnectorInstance{
			ID:      instanceID,
			Type:    "polling",
			Running: false,
		}

		mockRepo.On("GetByID", ctx, instanceID).Return(instance, nil)
		mockRepo.On("CountRunningByType", ctx, "polling").Return(cfg.MaxPollingConnectorInstances, nil)

		_, err := service.StartConnectorInstance(ctx, instanceID)
		assert.Error(t, err)
		mockRepo.AssertNotCalled(t, "Update")
	})
}

func TestConnectorInstanceService_StopConnectorInstance(t *testing.T) {
	service, mockRepo, _ := setupTestService(t)
	ctx := context.Background()

	t.Run("stops instance", func(t *testing.T) {
		instanceID := uuid.New()
		updatedInstance := &models.ConnectorInstance{
			ID:      instanceID,
			Running: false,
		}

		mockRepo.On("Update", ctx, instanceID, mock.AnythingOfType("*models.UpdateConnectorInstanceRequest")).Return(updatedInstance, nil)

		result, err := service.StopConnectorInstance(ctx, instanceID)
		require.NoError(t, err)
		assert.False(t, result.Running)
		mockRepo.AssertExpectations(t)
	})
}
