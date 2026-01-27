package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/pkg/hub"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegistry_Register(t *testing.T) {
	registry := NewRegistry()

	t.Run("registers factory", func(t *testing.T) {
		factory := &FormbricksFactory{}
		registry.Register(factory)

		names := registry.GetRegisteredNames()
		assert.Contains(t, names, "formbricks")
	})
}

func TestRegistry_CreateConnector(t *testing.T) {
	registry := NewRegistry()
	registry.Register(&FormbricksFactory{})

	ctx := context.Background()
	hubClient := hub.NewClient("http://localhost:8080", "test-key")

	t.Run("creates connector from factory", func(t *testing.T) {
		configJSON := json.RawMessage(`{"api_key": "test-key", "survey_id": "test-survey", "base_url": "https://app.formbricks.com"}`)
		instance := &models.ConnectorInstance{
			ID:         uuid.New(),
			Name:       "formbricks",
			InstanceID: "test-instance",
			Type:       "polling",
			Config:     configJSON,
		}

		connector, err := registry.CreateConnector(ctx, instance, hubClient)
		require.NoError(t, err)
		assert.NotNil(t, connector)
	})

	t.Run("fails for unregistered connector", func(t *testing.T) {
		configJSON := json.RawMessage(`{"api_key": "test-key"}`)
		instance := &models.ConnectorInstance{
			ID:         uuid.New(),
			Name:       "nonexistent",
			InstanceID: "test-instance",
			Type:       "polling",
			Config:     configJSON,
		}

		_, err := registry.CreateConnector(ctx, instance, hubClient)
		assert.Error(t, err)
	})
}

func TestParseConfig(t *testing.T) {
	t.Run("parses valid JSON config", func(t *testing.T) {
		configJSON := json.RawMessage(`{"api_key": "test-key", "survey_id": "test-survey"}`)
		configMap, err := ParseConfig(configJSON)
		require.NoError(t, err)
		assert.Equal(t, "test-key", configMap["api_key"])
		assert.Equal(t, "test-survey", configMap["survey_id"])
	})

	t.Run("fails on invalid JSON", func(t *testing.T) {
		invalidConfig := json.RawMessage(`invalid json`)
		_, err := ParseConfig(invalidConfig)
		assert.Error(t, err)
	})
}
