package main

import (
	"log/slog"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/config"
)

// River must log through the handler observability.SetupLogging installs (ENG-2485). With no
// Logger, River falls back to a private text logger at WARN, which filters out the INFO lines it
// writes for every failed attempt, so a failing job left no trace outside river_job.errors. That
// River really emits those lines through this logger is pinned against a real database in
// tests/river_logging_test.go; this pins that hub-worker hands River the logger at all.
func TestNewRiverConfigUsesProcessLogger(t *testing.T) {
	previous := slog.Default()

	t.Cleanup(func() { slog.SetDefault(previous) })

	// A pointer, so the identity check below can only match this exact handler.
	handler := &markerHandler{Handler: slog.DiscardHandler}
	slog.SetDefault(slog.New(handler))

	riverCfg := newRiverConfig(&config.Config{}, river.NewWorkers(), nil, nil)

	require.NotNil(t, riverCfg.Logger, "a nil Logger makes River fall back to its own WARN-level logger")
	assert.Same(t, handler, riverCfg.Logger.Handler(),
		"River must write through the handler installed by observability.SetupLogging")
}

// newRiverConfig was extracted from NewWorkerApp; these pin the settings it carried over unchanged.
func TestNewRiverConfigMapsRiverSettings(t *testing.T) {
	riverWorkers := river.NewWorkers()
	queues := map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 2}}
	periodic := []*river.PeriodicJob{
		river.NewPeriodicJob(river.PeriodicInterval(time.Minute),
			func() (river.JobArgs, *river.InsertOpts) { return nil, nil }, nil),
	}

	t.Run("configured values are applied", func(t *testing.T) {
		cfg := &config.Config{}
		cfg.River.JobTimeoutSec = config.DurationSec(90 * time.Second)
		cfg.River.RescueStuckJobsAfterSec = config.DurationSec(10 * time.Minute)
		cfg.River.CompletedJobRetentionSec = 3600
		cfg.River.ClientID = "worker-a"

		riverCfg := newRiverConfig(cfg, riverWorkers, queues, periodic)

		assert.Same(t, riverWorkers, riverCfg.Workers)
		assert.Equal(t, queues, riverCfg.Queues)
		assert.Equal(t, periodic, riverCfg.PeriodicJobs)
		assert.Equal(t, 90*time.Second, riverCfg.JobTimeout)
		assert.Equal(t, 10*time.Minute, riverCfg.RescueStuckJobsAfter)
		assert.Equal(t, time.Hour, riverCfg.CompletedJobRetentionPeriod)
		assert.Equal(t, "worker-a", riverCfg.ID)
	})

	t.Run("zero values leave River's defaults in place", func(t *testing.T) {
		riverCfg := newRiverConfig(&config.Config{}, riverWorkers, queues, nil)

		assert.Zero(t, riverCfg.JobTimeout)
		assert.Zero(t, riverCfg.RescueStuckJobsAfter)
		assert.Empty(t, riverCfg.ID)
		// River reads a zero retention as "use the default" (24h), the same as the env default.
		assert.Zero(t, riverCfg.CompletedJobRetentionPeriod)
	})

	t.Run("negative retention disables deletion", func(t *testing.T) {
		cfg := &config.Config{}
		cfg.River.CompletedJobRetentionSec = -1

		assert.Equal(t, time.Duration(-1), newRiverConfig(cfg, riverWorkers, queues, nil).CompletedJobRetentionPeriod)
	})
}

// markerHandler is a distinct handler instance for asserting which handler River was given.
type markerHandler struct {
	slog.Handler
}
