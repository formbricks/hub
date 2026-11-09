package main

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/service"
)

type lifecycleRiver struct {
	started chan struct{}
	stopped chan struct{}
	start   sync.Once
	stop    sync.Once
}

func newLifecycleRiver() *lifecycleRiver {
	return &lifecycleRiver{started: make(chan struct{}), stopped: make(chan struct{})}
}

func (r *lifecycleRiver) Start(context.Context) error {
	r.start.Do(func() { close(r.started) })

	return nil
}

func (r *lifecycleRiver) Stop(context.Context) error {
	r.stop.Do(func() { close(r.stopped) })

	return nil
}

func (r *lifecycleRiver) StopAndCancel(ctx context.Context) error { return r.Stop(ctx) }
func (r *lifecycleRiver) Stopped() <-chan struct{}                { return r.stopped }
func (r *lifecycleRiver) ID() string                              { return "test-river" }

type lifecycleEmbeddingClient struct{ healthy atomic.Bool }

func (c *lifecycleEmbeddingClient) CreateEmbedding(context.Context, string) ([]float32, error) {
	if !c.healthy.Load() {
		return nil, huberrors.NewRateLimitError(time.Second, errors.New("synthetic provider failure"))
	}

	vector := make([]float32, models.EmbeddingVectorDimensions)
	vector[0] = 1

	return vector, nil
}

func (c *lifecycleEmbeddingClient) CreateEmbeddingForQuery(ctx context.Context, input string) ([]float32, error) {
	return c.CreateEmbedding(ctx, input)
}

func TestWorkerAppRunStartsEmbeddingRiverAfterPreflightAndStopsBoth(t *testing.T) {
	t.Setenv("HUB_EMBEDDING_READINESS_FILE", t.TempDir()+"/ready")

	core, embedding := newLifecycleRiver(), newLifecycleRiver()
	provider := &lifecycleEmbeddingClient{}
	preflight := service.NewEmbeddingPreflight(provider, 1, time.Second, 40*time.Millisecond)
	app := &WorkerApp{
		cfg: &config.Config{
			Embedding: config.EmbeddingConfig{PreflightInterval: config.DurationSec(10 * time.Millisecond)},
			Server:    config.ServerConfig{ShutdownTimeout: config.DurationSec(time.Second)},
		},
		river: core, embeddingRiver: embedding, preflight: preflight,
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()

	select {
	case <-core.started:
	case <-time.After(time.Second):
		t.Fatal("core queues did not start")
	}

	select {
	case <-embedding.started:
		t.Fatal("embedding queues started before a successful preflight")
	default:
	}

	provider.healthy.Store(true)

	select {
	case <-embedding.started:
	case <-time.After(time.Second):
		t.Fatal("embedding queues did not start after provider recovery")
	}

	assert.True(t, checkEmbeddingReadiness())
	provider.healthy.Store(false)
	require.Eventually(t, func() bool { return !preflight.Ready() }, time.Second, 10*time.Millisecond)
	assert.Equal(t, service.PreflightRateLimited, preflight.LastFailureReason())
	cancel()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("worker did not stop")
	}

	assert.False(t, checkEmbeddingReadiness())
	assert.True(t, app.embeddingStarted.Load())
}

func TestWorkerAppRunStartsEmbeddingsWithoutWritableReadinessFile(t *testing.T) {
	tests := []struct {
		name string
		path func(*testing.T) string
	}{
		{"probe disabled", func(*testing.T) string { return "" }},
		{"probe file unwritable", func(t *testing.T) string {
			return filepath.Join(t.TempDir(), "missing", "ready")
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("HUB_EMBEDDING_READINESS_FILE", test.path(t))

			core, embedding := newLifecycleRiver(), newLifecycleRiver()
			provider := &lifecycleEmbeddingClient{}
			provider.healthy.Store(true)
			preflight := service.NewEmbeddingPreflight(provider, 1, time.Second, time.Minute)
			app := &WorkerApp{
				cfg: &config.Config{
					Embedding: config.EmbeddingConfig{PreflightInterval: config.DurationSec(time.Second)},
					Server:    config.ServerConfig{ShutdownTimeout: config.DurationSec(time.Second)},
				},
				river: core, embeddingRiver: embedding, preflight: preflight,
			}
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)

			done := make(chan error, 1)
			go func() { done <- app.Run(ctx) }()

			select {
			case <-embedding.started:
			case <-time.After(time.Second):
				t.Fatal("healthy embedding queues did not start")
			}

			assert.True(t, preflight.Ready())
			assert.False(t, checkEmbeddingReadiness())
			cancel()

			select {
			case err := <-done:
				require.NoError(t, err)
			case <-time.After(time.Second):
				t.Fatal("worker did not stop")
			}
		})
	}
}

func TestWorkerAppRunReportsUnexpectedCoreStopWithoutEmbeddings(t *testing.T) {
	t.Setenv("HUB_EMBEDDING_READINESS_FILE", t.TempDir()+"/ready")

	core := newLifecycleRiver()
	app := &WorkerApp{cfg: &config.Config{Server: config.ServerConfig{
		ShutdownTimeout: config.DurationSec(time.Second),
	}}, river: core}

	done := make(chan error, 1)
	go func() { done <- app.Run(context.Background()) }()

	<-core.started
	require.NoError(t, core.Stop(context.Background()))

	select {
	case err := <-done:
		require.ErrorIs(t, err, errCoreRiverStopped)
	case <-time.After(time.Second):
		t.Fatal("unexpected River stop was not reported")
	}
}

func TestEmbeddingPreflightTimingValid(t *testing.T) {
	cfg := &config.Config{Embedding: config.EmbeddingConfig{
		PreflightInterval:   config.DurationSec(time.Minute),
		PreflightSuccessTTL: config.DurationSec(2 * time.Minute),
	}}
	assert.True(t, embeddingPreflightTimingValid(cfg))
	cfg.Embedding.PreflightSuccessTTL = cfg.Embedding.PreflightInterval
	assert.False(t, embeddingPreflightTimingValid(cfg))
	cfg.Embedding.PreflightSuccessTTL = config.DurationSec(30 * time.Second)
	assert.False(t, embeddingPreflightTimingValid(cfg))
}

func TestEmbeddingReconcileConfigured(t *testing.T) {
	base := func() *config.Config {
		return &config.Config{
			Embedding: config.EmbeddingConfig{
				ReconcileEnabled: true,
				Provider:         "openai",
				Model:            "embedding-model",
			},
			Taxonomy: config.TaxonomyConfig{ServiceURL: "https://taxonomy.example.com"},
		}
	}

	t.Run("translation is not required because taxonomy input falls back to source text", func(t *testing.T) {
		assert.True(t, embeddingReconcileConfigured(base()))
	})

	t.Run("explicit opt in is required", func(t *testing.T) {
		cfg := base()
		cfg.Embedding.ReconcileEnabled = false
		assert.False(t, embeddingReconcileConfigured(cfg))
	})

	t.Run("embedding model is required", func(t *testing.T) {
		cfg := base()
		cfg.Embedding.Model = ""
		assert.False(t, embeddingReconcileConfigured(cfg))
	})

	t.Run("taxonomy is not required: the search embedding is repaired on its own", func(t *testing.T) {
		cfg := base()
		cfg.Taxonomy.ServiceURL = ""
		assert.True(t, embeddingReconcileConfigured(cfg))
	})
}

func TestEmbeddingReconcileTargets(t *testing.T) {
	cfg := &config.Config{Taxonomy: config.TaxonomyConfig{ServiceURL: "https://taxonomy.example.com"}}

	t.Run("raw always, taxonomy when configured", func(t *testing.T) {
		assert.Equal(t, []service.EmbeddingReconcileTarget{
			{Model: "raw", InputKind: models.EmbeddingInputKindRaw},
			{Model: "taxonomy:raw:translated-v1", InputKind: models.EmbeddingInputKindTaxonomyTranslated},
		}, embeddingReconcileTargets(cfg, "raw", "taxonomy:raw:translated-v1"))
	})

	t.Run("no taxonomy service, no taxonomy vectors", func(t *testing.T) {
		noTaxonomy := &config.Config{Taxonomy: config.TaxonomyConfig{ServiceToken: "configured-token"}}
		assert.Equal(t, []service.EmbeddingReconcileTarget{
			{Model: "raw", InputKind: models.EmbeddingInputKindRaw},
		}, embeddingReconcileTargets(noTaxonomy, "raw", "taxonomy:raw:translated-v1"))
	})
}

// TestReconcileSweepSchedulesKeepsBothSweeps is the guard for the failure mode this file's two
// independent sweeps invite.
//
// They are appended to one slice. Assign instead of append -- or resolve a merge conflict by
// keeping one side, which is exactly what nearly happened when main's embedding reconciler landed
// alongside the enrichment one -- and a sweep silently stops being scheduled. Nothing fails: it
// builds, the suite is green, and the only symptom is that coverage quietly stops converging.
// Asserting both are present is the only thing that turns that into a test failure.
func TestReconcileSweepSchedulesKeepsBothSweeps(t *testing.T) {
	cfg := &config.Config{}
	cfg.EnrichmentReconcile.IntervalSeconds = 120
	cfg.Embedding.ReconcileInterval = config.DurationSec(90 * time.Second)

	t.Run("both enabled", func(t *testing.T) {
		schedules := reconcileSweepSchedules(cfg, true, true)

		require.Len(t, schedules, 2, "both sweeps must be scheduled; one missing means it never runs")

		queues := make([]string, 0, len(schedules))
		for _, schedule := range schedules {
			queues = append(queues, schedule.opts.Queue)

			assert.Equal(t, 1, schedule.opts.MaxAttempts,
				"a failed sweep is retried by the next tick, not by River holding the unique key")
			assert.True(t, schedule.opts.UniqueOpts.ByArgs)
			assert.Equal(t, service.InFlightUniqueStates(), schedule.opts.UniqueOpts.ByState,
				"completed must stay out of the set or the first sweep is the only one that runs")
		}

		assert.ElementsMatch(t,
			[]string{service.EnrichmentReconcileQueueName, service.EmbeddingReconcileQueueName}, queues)

		assert.Equal(t, 120*time.Second, schedules[0].interval)
		assert.Equal(t, 90*time.Second, schedules[1].interval)

		// The constructed jobs are opaque -- river.PeriodicJob keeps its constructor unexported --
		// so the count is all that can be asserted on the other side of the conversion. It is
		// still worth asserting: it is what catches the conversion dropping one.
		assert.Len(t, reconcilePeriodicJobs(cfg, true, true), 2)
	})

	t.Run("each can be disabled independently", func(t *testing.T) {
		onlyEnrichment := reconcileSweepSchedules(cfg, true, false)
		require.Len(t, onlyEnrichment, 1)
		assert.Equal(t, service.EnrichmentReconcileQueueName, onlyEnrichment[0].opts.Queue)

		onlyEmbedding := reconcileSweepSchedules(cfg, false, true)
		require.Len(t, onlyEmbedding, 1)
		assert.Equal(t, service.EmbeddingReconcileQueueName, onlyEmbedding[0].opts.Queue)

		assert.Empty(t, reconcileSweepSchedules(cfg, false, false))
	})
}
