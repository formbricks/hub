package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/riverqueue/river"

	"github.com/formbricks/hub/internal/datatypes"
	"github.com/formbricks/hub/internal/models"
)

// enqueueMetrics is the metrics surface the enqueue path needs: the two type-agnostic counters
// every enrichment metrics interface exposes. SentimentMetrics / TranslationMetrics /
// EmbeddingMetrics all satisfy it, so a per-type metrics value passes straight through (a nil
// interface stays nil, so the caller's disabled-metrics case is preserved).
type enqueueMetrics interface {
	RecordJobsEnqueued(ctx context.Context, count int64)
	RecordProviderError(ctx context.Context, reason string)
}

// enrichmentProviderConfig configures an EnrichmentProvider: the shared deps plus the per-type
// hooks that actually vary between enrichments — which changed fields re-enqueue on update,
// record eligibility, the per-tenant gate, the dedupe hash, and the job args.
type enrichmentProviderConfig struct {
	name           string // enrichment name, used only in log messages ("sentiment", ...)
	inserter       RiverJobInserter
	resolver       TenantSettingsReader // required iff enabled != nil; otherwise unused
	metrics        enqueueMetrics       // may be nil when metrics are disabled
	queueName      string
	maxAttempts    int
	uniqueByPeriod time.Duration

	// triggers are the changed fields that re-enqueue on an update event.
	triggers []string
	// eligible reports whether a record is eligible at all (e.g. a text field). nil ⇒ always eligible.
	eligible func(record *models.FeedbackRecord) bool
	// hasContent reports whether a create event carries content to enrich (skip empty creates).
	hasContent func(record *models.FeedbackRecord) bool
	// enabled reports whether the tenant has this enrichment on. nil ⇒ no per-tenant gate (no settings read).
	enabled func(settings models.EnrichmentSettings) bool
	// contentHash is the dedupe key for a record's content.
	contentHash func(record *models.FeedbackRecord) string
	// newArgs builds the River job payload for a record and its content hash.
	newArgs func(record *models.FeedbackRecord, hash string) river.JobArgs
}

// EnrichmentProvider implements eventPublisher by enqueueing one enrichment job per eligible
// feedback-record event, driven by an enrichmentProviderConfig. It is the shared enqueue path
// behind the per-type providers (sentiment today; translation and embedding to follow) — the
// per-type differences live entirely in the config hooks. Failures are logged and swallowed so
// they never block ingestion.
type EnrichmentProvider struct {
	cfg enrichmentProviderConfig
}

// NewEnrichmentProvider builds a provider from cfg. It fails fast when a per-tenant gate (enabled)
// is configured without a resolver to read it — a wiring bug that would otherwise nil-panic only
// on the first eligible event, far from its cause.
func NewEnrichmentProvider(cfg enrichmentProviderConfig) *EnrichmentProvider {
	if cfg.enabled != nil && cfg.resolver == nil {
		panic("enrichment provider " + cfg.name + ": resolver is required when a per-tenant gate is configured")
	}

	return &EnrichmentProvider{cfg: cfg}
}

// PublishEvent enqueues an enrichment job for an eligible create/update event.
func (p *EnrichmentProvider) PublishEvent(ctx context.Context, event Event) {
	cfg := p.cfg

	// Trigger gate: on update, re-enqueue only when a triggering field changed; otherwise the
	// event must be a create.
	if event.Type == datatypes.FeedbackRecordUpdated {
		if !changedAny(event.ChangedFields, cfg.triggers) {
			return
		}
	} else if event.Type != datatypes.FeedbackRecordCreated {
		return
	}

	record, ok := event.Data.(*models.FeedbackRecord)
	if !ok {
		slog.Debug(cfg.name+": skip, event data is not *FeedbackRecord", "event_id", event.ID)

		return
	}

	if cfg.eligible != nil && !cfg.eligible(record) {
		slog.Debug(cfg.name+": skip, record not eligible", "feedback_record_id", record.ID)

		return
	}

	// On create, only enqueue when there is content to enrich. On update, enqueue even when the
	// content is now empty so the worker can clear a stale result.
	if event.Type == datatypes.FeedbackRecordCreated && !cfg.hasContent(record) {
		slog.Debug(cfg.name+": skip, no content on create", "feedback_record_id", record.ID)

		return
	}

	// Per-tenant gate (when this enrichment has one): skip tenants that turned it off. Read after
	// the cheap eligibility checks so non-eligible events never hit the settings store.
	if cfg.enabled != nil {
		settings, err := cfg.resolver.GetSettings(ctx, record.TenantID)
		if err != nil {
			if cfg.metrics != nil {
				cfg.metrics.RecordProviderError(ctx, "settings_read_failed")
			}

			slog.Error(cfg.name+": resolve tenant settings failed",
				"event_id", event.ID, "feedback_record_id", record.ID, "error", err)

			return
		}

		if !cfg.enabled(settings.Settings) {
			slog.Debug(cfg.name+": skip, disabled for tenant", "feedback_record_id", record.ID)

			return
		}
	}

	opts := &river.InsertOpts{
		Queue:       cfg.queueName,
		MaxAttempts: cfg.maxAttempts,
		UniqueOpts:  river.UniqueOpts{ByArgs: true, ByPeriod: cfg.uniqueByPeriod},
	}

	if _, err := cfg.inserter.Insert(ctx, cfg.newArgs(record, cfg.contentHash(record)), opts); err != nil {
		if cfg.metrics != nil {
			cfg.metrics.RecordProviderError(ctx, "enqueue_failed")
		}

		slog.Error(cfg.name+": enqueue failed",
			"event_id", event.ID, "feedback_record_id", record.ID, "error", err)

		return
	}

	slog.Info(cfg.name+": job enqueued", "event_id", event.ID, "feedback_record_id", record.ID)

	if cfg.metrics != nil {
		cfg.metrics.RecordJobsEnqueued(ctx, 1)
	}
}

// changedAny reports whether any of fields appears in changed.
func changedAny(changed, fields []string) bool {
	for _, f := range fields {
		if contains(changed, f) {
			return true
		}
	}

	return false
}
