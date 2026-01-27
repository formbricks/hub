package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/pkg/hub"
)

// ConnectorFactory creates a connector instance from configuration
type ConnectorFactory interface {
	CreateConnector(ctx context.Context, instance *models.ConnectorInstance, hubClient *hub.Client) (PollingConnector, error)
	GetName() string
}

// PollingConnector defines the interface for polling connectors
type PollingConnector interface {
	Poll(ctx context.Context) error
	ExtractLastID() (string, error)
}

// Registry manages connector factories
type Registry struct {
	factories map[string]ConnectorFactory
}

// NewRegistry creates a new connector registry
func NewRegistry() *Registry {
	return &Registry{
		factories: make(map[string]ConnectorFactory),
	}
}

// Register registers a connector factory
func (r *Registry) Register(factory ConnectorFactory) {
	r.factories[factory.GetName()] = factory
	slog.Info("Registered connector factory", "name", factory.GetName())
}

// CreateConnector creates a connector instance from database configuration
func (r *Registry) CreateConnector(ctx context.Context, instance *models.ConnectorInstance, hubClient *hub.Client) (PollingConnector, error) {
	factory, ok := r.factories[instance.Name]
	if !ok {
		return nil, fmt.Errorf("no factory registered for connector name: %s", instance.Name)
	}

	return factory.CreateConnector(ctx, instance, hubClient)
}

// GetRegisteredNames returns all registered connector names
func (r *Registry) GetRegisteredNames() []string {
	names := make([]string, 0, len(r.factories))
	for name := range r.factories {
		names = append(names, name)
	}
	return names
}

// ParseConfig parses JSONB config into a map for easy access
func ParseConfig(config json.RawMessage) (map[string]interface{}, error) {
	var configMap map[string]interface{}
	if err := json.Unmarshal(config, &configMap); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}
	return configMap, nil
}
