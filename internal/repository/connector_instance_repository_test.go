package repository

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/pkg/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestDB(t *testing.T) (*pgxpool.Pool, func()) {
	ctx := context.Background()
	t.Setenv("API_KEY", "test-api-key")

	cfg, err := config.Load()
	require.NoError(t, err)

	db, err := database.NewPostgresPool(ctx, cfg.DatabaseURL)
	require.NoError(t, err)

	cleanup := func() {
		// Clean up test data
		_, _ = db.Exec(ctx, "DELETE FROM connector_instances")
		db.Close()
	}

	return db, cleanup
}

func TestConnectorInstanceRepository_Create(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewConnectorInstanceRepository(db)
	ctx := context.Background()

	t.Run("creates connector instance successfully", func(t *testing.T) {
		configJSON := json.RawMessage(`{"api_key": "test-key", "survey_id": "test-survey"}`)
		req := &models.CreateConnectorInstanceRequest{
			Name:       "formbricks",
			InstanceID: "test-instance-1",
			Type:       "polling",
			Config:     configJSON,
		}

		instance, err := repo.Create(ctx, req)
		require.NoError(t, err)
		assert.NotEqual(t, uuid.Nil, instance.ID)
		assert.Equal(t, "formbricks", instance.Name)
		assert.Equal(t, "test-instance-1", instance.InstanceID)
		assert.Equal(t, "polling", instance.Type)
		assert.True(t, instance.Running) // Default to running
		assert.Nil(t, instance.Error)
	})

	t.Run("fails on duplicate name+instance_id", func(t *testing.T) {
		configJSON := json.RawMessage(`{"api_key": "test-key"}`)
		req := &models.CreateConnectorInstanceRequest{
			Name:       "formbricks",
			InstanceID: "test-instance-1",
			Type:       "polling",
			Config:     configJSON,
		}

		_, err := repo.Create(ctx, req)
		assert.Error(t, err)
	})

	t.Run("respects running flag", func(t *testing.T) {
		running := false
		configJSON := json.RawMessage(`{"api_key": "test-key"}`)
		req := &models.CreateConnectorInstanceRequest{
			Name:       "formbricks",
			InstanceID: "test-instance-2",
			Type:       "polling",
			Config:     configJSON,
			Running:    &running,
		}

		instance, err := repo.Create(ctx, req)
		require.NoError(t, err)
		assert.False(t, instance.Running)
	})
}

func TestConnectorInstanceRepository_GetByID(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewConnectorInstanceRepository(db)
	ctx := context.Background()

	// Create test instance
	configJSON := json.RawMessage(`{"api_key": "test-key"}`)
	req := &models.CreateConnectorInstanceRequest{
		Name:       "formbricks",
		InstanceID: "test-instance",
		Type:       "polling",
		Config:     configJSON,
	}
	created, err := repo.Create(ctx, req)
	require.NoError(t, err)

	t.Run("gets instance by ID", func(t *testing.T) {
		instance, err := repo.GetByID(ctx, created.ID)
		require.NoError(t, err)
		assert.Equal(t, created.ID, instance.ID)
		assert.Equal(t, "formbricks", instance.Name)
	})

	t.Run("returns not found for non-existent ID", func(t *testing.T) {
		nonExistentID := uuid.New()
		_, err := repo.GetByID(ctx, nonExistentID)
		assert.Error(t, err)
	})
}

func TestConnectorInstanceRepository_CountRunningByType(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewConnectorInstanceRepository(db)
	ctx := context.Background()

	// Create multiple instances
	configJSON := json.RawMessage(`{"api_key": "test-key"}`)
	for i := 0; i < 3; i++ {
		req := &models.CreateConnectorInstanceRequest{
			Name:       "formbricks",
			InstanceID: "test-instance-" + string(rune(i)),
			Type:       "polling",
			Config:     configJSON,
			Running:    func() *bool { b := true; return &b }(),
		}
		_, err := repo.Create(ctx, req)
		require.NoError(t, err)
	}

	// Create one stopped instance
	stopped := false
	req := &models.CreateConnectorInstanceRequest{
		Name:       "formbricks",
		InstanceID: "test-instance-stopped",
		Type:       "polling",
		Config:     configJSON,
		Running:    &stopped,
	}
	_, err := repo.Create(ctx, req)
	require.NoError(t, err)

	t.Run("counts only running instances", func(t *testing.T) {
		count, err := repo.CountRunningByType(ctx, "polling")
		require.NoError(t, err)
		assert.Equal(t, 3, count)
	})

	t.Run("returns zero for non-existent type", func(t *testing.T) {
		count, err := repo.CountRunningByType(ctx, "nonexistent")
		require.NoError(t, err)
		assert.Equal(t, 0, count)
	})
}

func TestConnectorInstanceRepository_ListRunningByType(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewConnectorInstanceRepository(db)
	ctx := context.Background()

	// Create instances with delays to ensure different created_at
	configJSON := json.RawMessage(`{"api_key": "test-key"}`)
	running := true
	for i := 0; i < 3; i++ {
		req := &models.CreateConnectorInstanceRequest{
			Name:       "formbricks",
			InstanceID: "test-instance-" + string(rune(i)),
			Type:       "polling",
			Config:     configJSON,
			Running:    &running,
		}
		_, err := repo.Create(ctx, req)
		require.NoError(t, err)
		time.Sleep(10 * time.Millisecond) // Ensure different timestamps
	}

	t.Run("lists running instances sorted by created_at ASC", func(t *testing.T) {
		instances, err := repo.ListRunningByType(ctx, "polling")
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(instances), 3)

		// Verify sorted by created_at ASC
		for i := 1; i < len(instances); i++ {
			assert.True(t, instances[i-1].CreatedAt.Before(instances[i].CreatedAt) ||
				instances[i-1].CreatedAt.Equal(instances[i].CreatedAt))
		}
	})
}

func TestConnectorInstanceRepository_SetRunning(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewConnectorInstanceRepository(db)
	ctx := context.Background()

	// Create instance
	configJSON := json.RawMessage(`{"api_key": "test-key"}`)
	req := &models.CreateConnectorInstanceRequest{
		Name:       "formbricks",
		InstanceID: "test-instance",
		Type:       "polling",
		Config:     configJSON,
	}
	instance, err := repo.Create(ctx, req)
	require.NoError(t, err)

	t.Run("sets running state", func(t *testing.T) {
		err := repo.SetRunning(ctx, instance.ID, false)
		require.NoError(t, err)

		updated, err := repo.GetByID(ctx, instance.ID)
		require.NoError(t, err)
		assert.False(t, updated.Running)
	})
}

func TestConnectorInstanceRepository_SetRunningWithError(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewConnectorInstanceRepository(db)
	ctx := context.Background()

	// Create instance
	configJSON := json.RawMessage(`{"api_key": "test-key"}`)
	req := &models.CreateConnectorInstanceRequest{
		Name:       "formbricks",
		InstanceID: "test-instance",
		Type:       "polling",
		Config:     configJSON,
	}
	instance, err := repo.Create(ctx, req)
	require.NoError(t, err)

	t.Run("sets running and error atomically", func(t *testing.T) {
		errorMsg := "Critical error occurred"
		err := repo.SetRunningWithError(ctx, instance.ID, false, &errorMsg)
		require.NoError(t, err)

		updated, err := repo.GetByID(ctx, instance.ID)
		require.NoError(t, err)
		assert.False(t, updated.Running)
		assert.NotNil(t, updated.Error)
		assert.Equal(t, "Critical error occurred", *updated.Error)
	})

	t.Run("clears error when error is nil", func(t *testing.T) {
		err := repo.SetRunningWithError(ctx, instance.ID, true, nil)
		require.NoError(t, err)

		updated, err := repo.GetByID(ctx, instance.ID)
		require.NoError(t, err)
		assert.True(t, updated.Running)
		assert.Nil(t, updated.Error)
	})
}

func TestConnectorInstanceRepository_UpdateState(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewConnectorInstanceRepository(db)
	ctx := context.Background()

	// Create instance
	configJSON := json.RawMessage(`{"api_key": "test-key"}`)
	req := &models.CreateConnectorInstanceRequest{
		Name:       "formbricks",
		InstanceID: "test-instance",
		Type:       "polling",
		Config:     configJSON,
	}
	instance, err := repo.Create(ctx, req)
	require.NoError(t, err)

	t.Run("updates state", func(t *testing.T) {
		stateJSON := json.RawMessage(`{"last_id": "test-id-123"}`)
		err := repo.UpdateState(ctx, instance.ID, stateJSON)
		require.NoError(t, err)

		updated, err := repo.GetByID(ctx, instance.ID)
		require.NoError(t, err)
		assert.NotNil(t, updated.State)
	})
}
