package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/formbricks/hub/internal/api/handlers"
	"github.com/formbricks/hub/internal/api/middleware"
	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/repository"
	"github.com/formbricks/hub/internal/service"
	"github.com/formbricks/hub/pkg/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupConnectorInstanceTestServer creates a test HTTP server with connector instance routes
func setupConnectorInstanceTestServer(t *testing.T) (*httptest.Server, func()) {
	ctx := context.Background()

	// Set test API key in environment
	t.Setenv("API_KEY", testAPIKey)

	// Load configuration
	cfg, err := config.Load()
	require.NoError(t, err, "Failed to load configuration")

	// Initialize database connection
	db, err := database.NewPostgresPool(ctx, cfg.DatabaseURL)
	require.NoError(t, err, "Failed to connect to database")

	// Initialize repository, service, and handler layers
	connectorInstanceRepo := repository.NewConnectorInstanceRepository(db)
	connectorInstanceService := service.NewConnectorInstanceService(connectorInstanceRepo, cfg)
	connectorInstanceHandler := handlers.NewConnectorInstanceHandler(connectorInstanceService)

	// Set up protected endpoints
	protectedMux := http.NewServeMux()
	protectedMux.HandleFunc("POST /v1/connector-instances", connectorInstanceHandler.Create)
	protectedMux.HandleFunc("GET /v1/connector-instances", connectorInstanceHandler.List)
	protectedMux.HandleFunc("GET /v1/connector-instances/{id}", connectorInstanceHandler.Get)
	protectedMux.HandleFunc("PATCH /v1/connector-instances/{id}", connectorInstanceHandler.Update)
	protectedMux.HandleFunc("DELETE /v1/connector-instances/{id}", connectorInstanceHandler.Delete)
	protectedMux.HandleFunc("POST /v1/connector-instances/{id}/start", connectorInstanceHandler.Start)
	protectedMux.HandleFunc("POST /v1/connector-instances/{id}/stop", connectorInstanceHandler.Stop)

	var protectedHandler http.Handler = protectedMux
	protectedHandler = middleware.Auth(cfg.APIKey)(protectedHandler)

	// Create test server
	server := httptest.NewServer(protectedHandler)

	// Cleanup function
	cleanup := func() {
		server.Close()
		// Clean up test data
		_, _ = db.Exec(ctx, "DELETE FROM connector_instances")
		db.Close()
	}

	return server, cleanup
}

func TestCreateConnectorInstance(t *testing.T) {
	server, cleanup := setupConnectorInstanceTestServer(t)
	defer cleanup()

	t.Run("creates connector instance successfully", func(t *testing.T) {
		reqBody := map[string]interface{}{
			"name":        "formbricks",
			"instance_id": "test-instance-1",
			"type":        "polling",
			"config": map[string]interface{}{
				"api_key":   "test-key",
				"survey_id": "test-survey",
			},
		}

		body, err := json.Marshal(reqBody)
		require.NoError(t, err)

		req, err := http.NewRequest("POST", server.URL+"/v1/connector-instances", bytes.NewBuffer(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusCreated, resp.StatusCode)

		var instance models.ConnectorInstance
		err = json.NewDecoder(resp.Body).Decode(&instance)
		require.NoError(t, err)
		assert.Equal(t, "formbricks", instance.Name)
		assert.Equal(t, "test-instance-1", instance.InstanceID)
		assert.Equal(t, "polling", instance.Type)
	})

	t.Run("fails on duplicate name+instance_id", func(t *testing.T) {
		reqBody := map[string]interface{}{
			"name":        "formbricks",
			"instance_id": "test-instance-1",
			"type":        "polling",
			"config": map[string]interface{}{
				"api_key": "test-key",
			},
		}

		body, err := json.Marshal(reqBody)
		require.NoError(t, err)

		req, err := http.NewRequest("POST", server.URL+"/v1/connector-instances", bytes.NewBuffer(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})
}

func TestStartConnectorInstance(t *testing.T) {
	server, cleanup := setupConnectorInstanceTestServer(t)
	defer cleanup()

	// Create an instance first
	reqBody := map[string]interface{}{
		"name":        "formbricks",
		"instance_id": "test-instance-start",
		"type":        "polling",
		"config": map[string]interface{}{
			"api_key":   "test-key",
			"survey_id": "test-survey",
		},
		"running": false,
	}

	body, err := json.Marshal(reqBody)
	require.NoError(t, err)

	req, err := http.NewRequest("POST", server.URL+"/v1/connector-instances", bytes.NewBuffer(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testAPIKey)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	var instance models.ConnectorInstance
	err = json.NewDecoder(resp.Body).Decode(&instance)
	require.NoError(t, err)

	t.Run("starts connector instance", func(t *testing.T) {
		req, err := http.NewRequest("POST", server.URL+"/v1/connector-instances/"+instance.ID.String()+"/start", nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var updated models.ConnectorInstance
		err = json.NewDecoder(resp.Body).Decode(&updated)
		require.NoError(t, err)
		assert.True(t, updated.Running)
		assert.Nil(t, updated.Error) // Error should be cleared
	})
}

func TestStopConnectorInstance(t *testing.T) {
	server, cleanup := setupConnectorInstanceTestServer(t)
	defer cleanup()

	// Create and start an instance
	reqBody := map[string]interface{}{
		"name":        "formbricks",
		"instance_id": "test-instance-stop",
		"type":        "polling",
		"config": map[string]interface{}{
			"api_key":   "test-key",
			"survey_id": "test-survey",
		},
		"running": true,
	}

	body, err := json.Marshal(reqBody)
	require.NoError(t, err)

	req, err := http.NewRequest("POST", server.URL+"/v1/connector-instances", bytes.NewBuffer(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testAPIKey)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	var instance models.ConnectorInstance
	err = json.NewDecoder(resp.Body).Decode(&instance)
	require.NoError(t, err)

	t.Run("stops connector instance", func(t *testing.T) {
		req, err := http.NewRequest("POST", server.URL+"/v1/connector-instances/"+instance.ID.String()+"/stop", nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var updated models.ConnectorInstance
		err = json.NewDecoder(resp.Body).Decode(&updated)
		require.NoError(t, err)
		assert.False(t, updated.Running)
	})
}

func TestListConnectorInstances(t *testing.T) {
	server, cleanup := setupConnectorInstanceTestServer(t)
	defer cleanup()

	// Create multiple instances
	for i := 0; i < 3; i++ {
		reqBody := map[string]interface{}{
			"name":        "formbricks",
			"instance_id": "test-instance-" + string(rune(i)),
			"type":        "polling",
			"config": map[string]interface{}{
				"api_key": "test-key",
			},
		}

		body, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest("POST", server.URL+"/v1/connector-instances", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+testAPIKey)
		_, _ = http.DefaultClient.Do(req)
	}

	t.Run("lists connector instances", func(t *testing.T) {
		req, err := http.NewRequest("GET", server.URL+"/v1/connector-instances", nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var response models.ListConnectorInstancesResponse
		err = json.NewDecoder(resp.Body).Decode(&response)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(response.Data), 3)
	})

	t.Run("filters by type", func(t *testing.T) {
		req, err := http.NewRequest("GET", server.URL+"/v1/connector-instances?type=polling", nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var response models.ListConnectorInstancesResponse
		err = json.NewDecoder(resp.Body).Decode(&response)
		require.NoError(t, err)
		for _, instance := range response.Data {
			assert.Equal(t, "polling", instance.Type)
		}
	})
}
