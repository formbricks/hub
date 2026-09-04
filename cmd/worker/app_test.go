package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/service"
)

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

	t.Run("taxonomy integration is required", func(t *testing.T) {
		cfg := base()
		cfg.Taxonomy.ServiceURL = ""
		assert.False(t, embeddingReconcileConfigured(cfg))
	})

	t.Run("taxonomy token without a service URL is not configured", func(t *testing.T) {
		cfg := base()
		cfg.Taxonomy.ServiceURL = ""
		cfg.Taxonomy.ServiceToken = "configured-token"
		assert.False(t, embeddingReconcileConfigured(cfg))
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
