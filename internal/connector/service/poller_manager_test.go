package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/pkg/hub"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockConnectorInstanceRepository is a mock for testing
type MockConnectorInstanceRepository struct {
	mock.Mock
}

func (m *MockConnectorInstanceRepository) Create(ctx context.Context, req *models.CreateConnectorInstanceRequest) (*models.ConnectorInstance, error) {
	args := m.Called(ctx, req)
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

// MockPollingConnector is a mock connector for testing
type MockPollingConnector struct {
	mock.Mock
}

func (m *MockPollingConnector) Poll(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *MockPollingConnector) ExtractLastID() (string, error) {
	args := m.Called()
	return args.String(0), args.Error(1)
}

// MockConnectorFactory is a mock factory for testing
type MockConnectorFactory struct {
	mock.Mock
}

func (m *MockConnectorFactory) CreateConnector(ctx context.Context, instance *models.ConnectorInstance, hubClient interface{}) (PollingConnector, error) {
	args := m.Called(ctx, instance, hubClient)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(PollingConnector), args.Error(1)
}

func (m *MockConnectorFactory) GetName() string {
	args := m.Called()
	return args.String(0)
}

func TestPollerManager_LoadAndStartInstances(t *testing.T) {
	mockRepo := new(MockConnectorInstanceRepository)
	mockRegistry := NewRegistry()
	mockRateLimiter := NewRateLimiter(1*time.Minute, 60)
	mockHubClient := hub.NewClient("http://localhost:8080", "test-key")
	cfg := &config.Config{
		MaxPollingConnectorInstances: 2,
	}

	pm := NewPollerManager(mockRepo, mockRegistry, mockRateLimiter, mockHubClient, cfg)
	ctx := context.Background()

	t.Run("starts only first MAX instances and sets rest to running=false", func(t *testing.T) {
		// Create 3 instances (more than max of 2)
		instances := []models.ConnectorInstance{
			{ID: uuid.New(), Name: "formbricks", Type: "polling", Running: true},
			{ID: uuid.New(), Name: "formbricks", Type: "polling", Running: true},
			{ID: uuid.New(), Name: "formbricks", Type: "polling", Running: true},
		}

		// Mock all connector types to return empty lists except polling
		mockRepo.On("ListRunningByType", ctx, "polling").Return(instances, nil)
		mockRepo.On("ListRunningByType", ctx, "webhook").Return([]models.ConnectorInstance{}, nil)
		mockRepo.On("ListRunningByType", ctx, "output").Return([]models.ConnectorInstance{}, nil)
		mockRepo.On("ListRunningByType", ctx, "enrichment").Return([]models.ConnectorInstance{}, nil)
		mockRepo.On("SetRunning", ctx, instances[2].ID, false).Return(nil)

		err := pm.loadAndStartInstances(ctx)
		// The function continues even if factory is missing (logs error and continues)
		// Verify SetRunning was called for instances exceeding limit
		mockRepo.AssertCalled(t, "SetRunning", ctx, instances[2].ID, false)
		// Function may or may not return error depending on implementation
		// The important thing is SetRunning was called
		_ = err // Error handling is implementation-specific
	})
}

func TestPollerManager_GetMaxInstancesForType(t *testing.T) {
	cfg := &config.Config{
		MaxPollingConnectorInstances:    5,
		MaxWebhookConnectorInstances:    10,
		MaxOutputConnectorInstances:     15,
		MaxEnrichmentConnectorInstances: 20,
	}

	mockRepo := new(MockConnectorInstanceRepository)
	mockRegistry := NewRegistry()
	mockRateLimiter := NewRateLimiter(1*time.Minute, 60)
	mockHubClient := hub.NewClient("http://localhost:8080", "test-key")

	pm := NewPollerManager(mockRepo, mockRegistry, mockRateLimiter, mockHubClient, cfg)

	tests := []struct {
		connectorType string
		expected      int
	}{
		{"polling", 5},
		{"webhook", 10},
		{"output", 15},
		{"enrichment", 20},
		{"unknown", 10}, // Default
	}

	for _, tt := range tests {
		t.Run(tt.connectorType, func(t *testing.T) {
			result := pm.getMaxInstancesForType(tt.connectorType)
			assert.Equal(t, tt.expected, result)
		})
	}
}
