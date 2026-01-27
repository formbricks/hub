package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/repository"
	"github.com/formbricks/hub/pkg/hub"
)

// PollerManager manages polling connectors
type PollerManager struct {
	repo         repository.ConnectorInstanceRepository
	registry     *Registry
	rateLimiter  *RateLimiter
	hubClient    *hub.Client
	config       *config.Config
	pollers      map[string]*InstancePoller
	pollersMu    sync.RWMutex
	watchContext context.Context
	watchCancel  context.CancelFunc
}

// InstancePoller manages polling for a single connector instance
type InstancePoller struct {
	instance            *models.ConnectorInstance
	connector           PollingConnector
	cancel              context.CancelFunc
	consecutiveFailures int
	lastFailureTime     time.Time
	failureWindow       []time.Time
}

// NewPollerManager creates a new poller manager
func NewPollerManager(
	repo repository.ConnectorInstanceRepository,
	registry *Registry,
	rateLimiter *RateLimiter,
	hubClient *hub.Client,
	cfg *config.Config,
) *PollerManager {
	watchCtx, watchCancel := context.WithCancel(context.Background())
	return &PollerManager{
		repo:         repo,
		registry:     registry,
		rateLimiter:  rateLimiter,
		hubClient:    hubClient,
		config:       cfg,
		pollers:      make(map[string]*InstancePoller),
		watchContext: watchCtx,
		watchCancel:  watchCancel,
	}
}

// Start starts the poller manager
func (pm *PollerManager) Start(ctx context.Context) error {
	slog.Info("Starting poller manager")

	// Load and start running instances on startup
	if err := pm.loadAndStartInstances(ctx); err != nil {
		return fmt.Errorf("failed to load instances: %w", err)
	}

	// Start watching for changes
	go pm.watchForChanges(ctx)

	return nil
}

// Stop stops the poller manager
func (pm *PollerManager) Stop() {
	slog.Info("Stopping poller manager")
	pm.watchCancel()

	pm.pollersMu.Lock()
	defer pm.pollersMu.Unlock()

	for _, poller := range pm.pollers {
		if poller.cancel != nil {
			poller.cancel()
		}
	}
	pm.pollers = make(map[string]*InstancePoller)
}

// loadAndStartInstances loads running instances and starts them (respecting limits)
func (pm *PollerManager) loadAndStartInstances(ctx context.Context) error {
	// For each connector type, load running instances sorted by created_at
	connectorTypes := []string{"polling", "webhook", "output", "enrichment"}

	for _, connectorType := range connectorTypes {
		instances, err := pm.repo.ListRunningByType(ctx, connectorType)
		if err != nil {
			slog.Error("Failed to load running instances",
				"type", connectorType,
				"error", err,
			)
			continue
		}

		maxInstances := pm.getMaxInstancesForType(connectorType)
		slog.Info("Loading running instances",
			"type", connectorType,
			"total", len(instances),
			"max", maxInstances,
		)

		// Start only the first MAX instances (oldest first, already sorted by created_at ASC)
		started := 0
		for i := range instances {
			instance := &instances[i]
			if i >= maxInstances {
				// Set running=false for instances that exceed the limit
				if err := pm.repo.SetRunning(ctx, instance.ID, false); err != nil {
					slog.Error("Failed to set running=false for instance",
						"instance_id", instance.ID,
						"error", err,
					)
				} else {
					slog.Info("Set running=false for instance exceeding limit",
						"instance_id", instance.ID,
						"type", connectorType,
					)
				}
				continue
			}

			// Start instance with staggered delay
			delay := time.Duration(started) * (time.Minute / time.Duration(maxInstances))
			if err := pm.startInstance(ctx, instance, delay); err != nil {
				slog.Error("Failed to start instance",
					"instance_id", instance.ID,
					"error", err,
				)
				continue
			}
			started++
		}
	}

	return nil
}

// startInstance starts a connector instance
func (pm *PollerManager) startInstance(ctx context.Context, instance *models.ConnectorInstance, startupDelay time.Duration) error {
	pm.pollersMu.Lock()
	defer pm.pollersMu.Unlock()

	// Check if already running
	if _, exists := pm.pollers[instance.ID.String()]; exists {
		return nil
	}

	// Create connector from registry
	connector, err := pm.registry.CreateConnector(ctx, instance, pm.hubClient)
	if err != nil {
		return fmt.Errorf("failed to create connector: %w", err)
	}

	// Create context for this instance
	instanceCtx, cancel := context.WithCancel(ctx)

	poller := &InstancePoller{
		instance:            instance,
		connector:           connector,
		cancel:              cancel,
		consecutiveFailures: 0,
		failureWindow:       make([]time.Time, 0),
	}

	pm.pollers[instance.ID.String()] = poller

	// Start polling goroutine with staggered delay
	go pm.runPoller(instanceCtx, poller, startupDelay)

	slog.Info("Started connector instance",
		"instance_id", instance.ID,
		"name", instance.Name,
		"type", instance.Type,
		"startup_delay", startupDelay,
	)

	return nil
}

// stopInstance stops a connector instance
func (pm *PollerManager) stopInstance(instanceID string) {
	pm.pollersMu.Lock()
	defer pm.pollersMu.Unlock()

	poller, exists := pm.pollers[instanceID]
	if !exists {
		return
	}

	if poller.cancel != nil {
		poller.cancel()
	}

	delete(pm.pollers, instanceID)

	slog.Info("Stopped connector instance", "instance_id", instanceID)
}

// runPoller runs the polling loop for an instance
func (pm *PollerManager) runPoller(ctx context.Context, poller *InstancePoller, startupDelay time.Duration) {
	// Wait for startup delay
	if startupDelay > 0 {
		select {
		case <-ctx.Done():
			return
		case <-time.After(startupDelay):
		}
	}

	// Parse poll interval from config or use default
	pollInterval := 1 * time.Hour
	if poller.instance.Config != nil {
		var configMap map[string]interface{}
		if err := json.Unmarshal(poller.instance.Config, &configMap); err == nil {
			if pollIntervalStr, ok := configMap["poll_interval"].(string); ok {
				if parsed, err := time.ParseDuration(pollIntervalStr); err == nil {
					pollInterval = parsed
				}
			}
		}
	}

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	// Poll immediately on startup (after delay)
	if err := pm.executePoll(ctx, poller); err != nil {
		slog.Error("Initial poll failed",
			"instance_id", poller.instance.ID,
			"error", err,
		)
	}

	// Then poll on schedule
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := pm.executePoll(ctx, poller); err != nil {
				slog.Error("Poll failed",
					"instance_id", poller.instance.ID,
					"error", err,
				)
			}
		}
	}
}

// executePoll executes a single poll with rate limiting and error handling
func (pm *PollerManager) executePoll(ctx context.Context, poller *InstancePoller) error {
	instanceID := poller.instance.ID.String()

	// Check rate limiting
	canPoll, waitTime := pm.rateLimiter.CanPoll(instanceID)
	if !canPoll {
		slog.Debug("Rate limit exceeded, skipping poll",
			"instance_id", instanceID,
			"wait_time", waitTime,
		)
		return nil
	}

	// Execute poll
	err := poller.connector.Poll(ctx)
	pm.rateLimiter.RecordPoll(instanceID)

	if err != nil {
		pm.handlePollError(ctx, poller, err)
		return err
	}

	// Reset failure tracking on success
	poller.consecutiveFailures = 0
	poller.failureWindow = make([]time.Time, 0)

	// Update last_id in state if connector supports it
	if lastID, err := poller.connector.ExtractLastID(); err == nil && lastID != "" {
		state := map[string]interface{}{
			"last_id": lastID,
		}
		stateJSON, _ := json.Marshal(state)
		if err := pm.repo.UpdateState(ctx, poller.instance.ID, stateJSON); err != nil {
			slog.Warn("Failed to update state",
				"instance_id", instanceID,
				"error", err,
			)
		}
	}

	return nil
}

// handlePollError handles poll errors and detects critical conditions
func (pm *PollerManager) handlePollError(ctx context.Context, poller *InstancePoller, err error) {
	now := time.Now()
	poller.consecutiveFailures++
	poller.lastFailureTime = now
	poller.failureWindow = append(poller.failureWindow, now)

	// Clean up old failures from window (keep last hour)
	oneHourAgo := now.Add(-1 * time.Hour)
	filtered := make([]time.Time, 0)
	for _, ts := range poller.failureWindow {
		if ts.After(oneHourAgo) {
			filtered = append(filtered, ts)
		}
	}
	poller.failureWindow = filtered

	// Check for critical error conditions
	isCritical := false
	errorMsg := ""

	// Consecutive failures (e.g., 5+)
	if poller.consecutiveFailures >= 5 {
		isCritical = true
		errorMsg = fmt.Sprintf("Critical: %d consecutive poll failures", poller.consecutiveFailures)
	}

	// Failures in time window (e.g., 10 in 1 hour)
	if len(poller.failureWindow) >= 10 {
		isCritical = true
		errorMsg = fmt.Sprintf("Critical: %d failures in the last hour", len(poller.failureWindow))
	}

	// Specific error types (auth errors, invalid config, etc.)
	if err != nil {
		errStr := err.Error()
		if contains(errStr, "authentication") || contains(errStr, "unauthorized") || contains(errStr, "invalid config") {
			isCritical = true
			errorMsg = fmt.Sprintf("Critical: %s", errStr)
		}
	}

	if isCritical {
		slog.Error("Critical error detected, stopping instance",
			"instance_id", poller.instance.ID,
			"error", errorMsg,
		)

		// Set running=false and error=message
		errorMsgPtr := &errorMsg
		if err := pm.repo.SetRunningWithError(ctx, poller.instance.ID, false, errorMsgPtr); err != nil {
			slog.Error("Failed to set error state",
				"instance_id", poller.instance.ID,
				"error", err,
			)
		}

		// Stop the poller
		pm.stopInstance(poller.instance.ID.String())
	}
}

// watchForChanges watches for connector instance changes in the database
func (pm *PollerManager) watchForChanges(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second) // Poll every 10 seconds
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pm.checkForChanges(ctx)
		}
	}
}

// checkForChanges checks for running state changes and starts/stops instances accordingly
func (pm *PollerManager) checkForChanges(ctx context.Context) {
	// Get all running instances from database
	running := true
	filters := &models.ListConnectorInstancesFilters{
		Running: &running,
	}

	instances, err := pm.repo.List(ctx, filters)
	if err != nil {
		slog.Error("Failed to check for changes", "error", err)
		return
	}

	// Build set of running instance IDs
	runningIDs := make(map[string]bool)
	for _, instance := range instances {
		runningIDs[instance.ID.String()] = true

		// Check if instance is not yet started
		pm.pollersMu.RLock()
		_, exists := pm.pollers[instance.ID.String()]
		pm.pollersMu.RUnlock()

		if !exists {
			// Start immediately (no staggered delay for manual starts)
			instancePtr := &instance
			if err := pm.startInstance(ctx, instancePtr, 0); err != nil {
				slog.Error("Failed to start instance on change",
					"instance_id", instancePtr.ID,
					"error", err,
				)
			}
		}
	}

	// Stop pollers that are no longer running
	pm.pollersMu.Lock()
	for instanceID := range pm.pollers {
		if !runningIDs[instanceID] {
			pm.stopInstance(instanceID)
		}
	}
	pm.pollersMu.Unlock()
}

// getMaxInstancesForType returns the maximum number of instances allowed for a connector type
func (pm *PollerManager) getMaxInstancesForType(connectorType string) int {
	switch connectorType {
	case "polling":
		return pm.config.MaxPollingConnectorInstances
	case "webhook":
		return pm.config.MaxWebhookConnectorInstances
	case "output":
		return pm.config.MaxOutputConnectorInstances
	case "enrichment":
		return pm.config.MaxEnrichmentConnectorInstances
	default:
		return 10
	}
}

// contains checks if a string contains a substring (case-insensitive)
func contains(s, substr string) bool {
	return len(s) >= len(substr) &&
		(len(s) == 0 && len(substr) == 0 ||
			strings.Contains(strings.ToLower(s), strings.ToLower(substr)))
}
