package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	apperrors "github.com/formbricks/hub/internal/errors"
	"github.com/formbricks/hub/internal/models"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockConnectorInstanceService is a mock implementation of ConnectorInstanceService
type MockConnectorInstanceService struct {
	mock.Mock
}

func (m *MockConnectorInstanceService) CreateConnectorInstance(ctx context.Context, req *models.CreateConnectorInstanceRequest) (*models.ConnectorInstance, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.ConnectorInstance), args.Error(1)
}

func (m *MockConnectorInstanceService) GetConnectorInstance(ctx context.Context, id uuid.UUID) (*models.ConnectorInstance, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.ConnectorInstance), args.Error(1)
}

func (m *MockConnectorInstanceService) ListConnectorInstances(ctx context.Context, filters *models.ListConnectorInstancesFilters) (*models.ListConnectorInstancesResponse, error) {
	args := m.Called(ctx, filters)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.ListConnectorInstancesResponse), args.Error(1)
}

func (m *MockConnectorInstanceService) UpdateConnectorInstance(ctx context.Context, id uuid.UUID, req *models.UpdateConnectorInstanceRequest) (*models.ConnectorInstance, error) {
	args := m.Called(ctx, id, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.ConnectorInstance), args.Error(1)
}

func (m *MockConnectorInstanceService) DeleteConnectorInstance(ctx context.Context, id uuid.UUID) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *MockConnectorInstanceService) StartConnectorInstance(ctx context.Context, id uuid.UUID) (*models.ConnectorInstance, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.ConnectorInstance), args.Error(1)
}

func (m *MockConnectorInstanceService) StopConnectorInstance(ctx context.Context, id uuid.UUID) (*models.ConnectorInstance, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.ConnectorInstance), args.Error(1)
}

func TestConnectorInstanceHandler_Create(t *testing.T) {
	mockService := new(MockConnectorInstanceService)
	handler := NewConnectorInstanceHandler(mockService)

	t.Run("creates instance successfully", func(t *testing.T) {
		reqBody := map[string]interface{}{
			"name":        "formbricks",
			"instance_id": "test-instance",
			"type":        "polling",
			"config": map[string]interface{}{
				"api_key": "test-key",
			},
		}

		body, _ := json.Marshal(reqBody)
		req := httptest.NewRequest("POST", "/v1/connector-instances", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		instance := &models.ConnectorInstance{
			ID:         uuid.New(),
			Name:       "formbricks",
			InstanceID: "test-instance",
			Type:       "polling",
		}

		mockService.On("CreateConnectorInstance", req.Context(), mock.AnythingOfType("*models.CreateConnectorInstanceRequest")).Return(instance, nil)

		handler.Create(w, req)

		assert.Equal(t, http.StatusCreated, w.Code)
		mockService.AssertExpectations(t)
	})

	t.Run("returns validation error on limit exceeded", func(t *testing.T) {
		mockService := new(MockConnectorInstanceService)
		handler := NewConnectorInstanceHandler(mockService)

		reqBody := map[string]interface{}{
			"name":        "formbricks",
			"instance_id": "test-instance-2",
			"type":        "polling",
			"config": map[string]interface{}{
				"api_key": "test-key",
			},
			"running": true, // Explicitly set to running to trigger limit check
		}

		body, _ := json.Marshal(reqBody)
		req := httptest.NewRequest("POST", "/v1/connector-instances", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		mockService.On("CreateConnectorInstance", req.Context(), mock.AnythingOfType("*models.CreateConnectorInstanceRequest")).
			Return(nil, apperrors.NewValidationError("type", "limit exceeded"))

		handler.Create(w, req)

		assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
		mockService.AssertExpectations(t)
	})
}

func TestConnectorInstanceHandler_Start(t *testing.T) {
	mockService := new(MockConnectorInstanceService)
	handler := NewConnectorInstanceHandler(mockService)

	t.Run("starts instance successfully", func(t *testing.T) {
		instanceID := uuid.New()
		req := httptest.NewRequest("POST", "/v1/connector-instances/"+instanceID.String()+"/start", nil)
		req.SetPathValue("id", instanceID.String())
		w := httptest.NewRecorder()

		instance := &models.ConnectorInstance{
			ID:      instanceID,
			Running: true,
			Error:   nil,
		}

		mockService.On("StartConnectorInstance", req.Context(), instanceID).Return(instance, nil)

		handler.Start(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		mockService.AssertExpectations(t)
	})

	t.Run("returns not found for non-existent instance", func(t *testing.T) {
		mockService := new(MockConnectorInstanceService)
		handler := NewConnectorInstanceHandler(mockService)

		instanceID := uuid.New()
		req := httptest.NewRequest("POST", "/v1/connector-instances/"+instanceID.String()+"/start", nil)
		req.SetPathValue("id", instanceID.String())
		w := httptest.NewRecorder()

		mockService.On("StartConnectorInstance", req.Context(), instanceID).
			Return(nil, apperrors.NewNotFoundError("connector instance", "not found"))

		handler.Start(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
		mockService.AssertExpectations(t)
	})
}

func TestConnectorInstanceHandler_Stop(t *testing.T) {
	t.Run("stops instance successfully", func(t *testing.T) {
		mockService := new(MockConnectorInstanceService)
		handler := NewConnectorInstanceHandler(mockService)

		instanceID := uuid.New()
		req := httptest.NewRequest("POST", "/v1/connector-instances/"+instanceID.String()+"/stop", nil)
		req.SetPathValue("id", instanceID.String())
		w := httptest.NewRecorder()

		instance := &models.ConnectorInstance{
			ID:      instanceID,
			Running: false,
		}

		mockService.On("StopConnectorInstance", req.Context(), instanceID).Return(instance, nil)

		handler.Stop(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		mockService.AssertExpectations(t)
	})
}
