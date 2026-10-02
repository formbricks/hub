package main

import (
	"context"
	"log"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/observability"
)

// River must log through the handler observability.SetupLogging installs (ENG-2485). With no
// Logger, River falls back to a private text logger at WARN, which filters out the INFO lines it
// writes for every failed attempt, so a failing job left no trace outside river_job.errors. That
// River really emits those lines through its logger is pinned against a real database in
// tests/river_logging_test.go; this pins that hub-worker hands River a logger that reaches the
// process handler, and that the logger caps a storm of identical lines.
func TestNewRiverConfigLogsThroughProcessHandlerWithSampling(t *testing.T) {
	handler := &countingHandler{}
	setDefaultLogger(t, slog.New(handler))

	riverCfg, riverLogs := newRiverConfig(&config.Config{}, river.NewWorkers(), nil, nil)
	require.NotNil(t, riverCfg.Logger, "a nil Logger makes River fall back to its own WARN-level logger")

	riverCfg.Logger.Info("JobExecutor: Job errored; retrying", "job_kind", "feedback_embedding")
	require.Equal(t, 1, handler.count(), "River's lines must reach the handler installed by SetupLogging")

	// A provider outage: every attempt of one job kind fails at once. The loop takes microseconds,
	// well inside one sampling window.
	const storm = 1000
	for range storm - 1 {
		riverCfg.Logger.Info("JobExecutor: Job errored; retrying", "job_kind", "feedback_embedding")
	}

	want := riverLogSampling.First + (storm-riverLogSampling.First)/riverLogSampling.Thereafter
	assert.Equal(t, want, handler.count(), "a storm of identical River lines must be sampled")

	// Another job kind has its own budget, so one failing kind cannot hide another's failures.
	riverCfg.Logger.Info("JobExecutor: Job errored; retrying", "job_kind", "webhook_dispatch")
	assert.Equal(t, want+1, handler.count())

	// River names the kind "kind", not "job_kind", on its unhandled-kind, panic and stuck-job lines.
	// Those need a budget per kind too.
	for range storm {
		riverCfg.Logger.Error("JobExecutor: Unhandled job kind", "kind", "kind_a")
	}

	afterStorm := handler.count()

	riverCfg.Logger.Error("JobExecutor: Unhandled job kind", "kind", "kind_b")
	assert.Equal(t, afterStorm+1, handler.count(), "a storm from one kind must not hide another kind's errors")

	// Shutdown flushes the returned handler: one summary for each storm's dropped lines.
	require.NoError(t, riverLogs.Flush(context.Background()))
	assert.Equal(t, afterStorm+3, handler.count())
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

		riverCfg, _ := newRiverConfig(cfg, riverWorkers, queues, periodic)

		assert.Same(t, riverWorkers, riverCfg.Workers)
		assert.Equal(t, queues, riverCfg.Queues)
		assert.Equal(t, periodic, riverCfg.PeriodicJobs)
		assert.Equal(t, 90*time.Second, riverCfg.JobTimeout)
		assert.Equal(t, 10*time.Minute, riverCfg.RescueStuckJobsAfter)
		assert.Equal(t, time.Hour, riverCfg.CompletedJobRetentionPeriod)
		assert.Equal(t, "worker-a", riverCfg.ID)
	})

	t.Run("zero values leave River's defaults in place", func(t *testing.T) {
		riverCfg, _ := newRiverConfig(&config.Config{}, riverWorkers, queues, nil)

		assert.Zero(t, riverCfg.JobTimeout)
		assert.Zero(t, riverCfg.RescueStuckJobsAfter)
		assert.Empty(t, riverCfg.ID)
		// River reads a zero retention as "use the default" (24h), the same as the env default.
		assert.Zero(t, riverCfg.CompletedJobRetentionPeriod)
	})

	t.Run("negative retention disables deletion", func(t *testing.T) {
		cfg := &config.Config{}
		cfg.River.CompletedJobRetentionSec = -1

		assert.Equal(t, time.Duration(-1), riverConfigOnly(newRiverConfig(cfg, riverWorkers, queues, nil)).CompletedJobRetentionPeriod)
	})
}

// riverConfigOnly drops newRiverConfig's sampling handler, for tests that only read the config.
func riverConfigOnly(riverCfg *river.Config, _ *observability.SamplingHandler) *river.Config {
	return riverCfg
}

// countingHandler counts the records that reach it.
type countingHandler struct {
	mu sync.Mutex
	n  int
}

func (h *countingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *countingHandler) WithAttrs([]slog.Attr) slog.Handler       { return h }
func (h *countingHandler) WithGroup(string) slog.Handler            { return h }

func (h *countingHandler) Handle(context.Context, slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.n++

	return nil
}

func (h *countingHandler) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()

	return h.n
}

// setDefaultLogger installs logger as the slog default for the rest of the test. slog.SetDefault
// also points the standard log package at the new handler, and putting the old slog default back
// does not undo that, so log's output and flags are restored as well; otherwise every later log
// line in this test binary would go to the discarded handler.
func setDefaultLogger(t *testing.T, logger *slog.Logger) {
	t.Helper()

	previous, writer, flags := slog.Default(), log.Writer(), log.Flags()

	t.Cleanup(func() {
		slog.SetDefault(previous)
		log.SetOutput(writer)
		log.SetFlags(flags)
	})

	slog.SetDefault(logger)
}
