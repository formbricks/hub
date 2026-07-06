package service

import (
	"context"
	"log/slog"
	"slices"

	"github.com/google/uuid"
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

// enrichmentProviderConfig configures an enrichmentProvider: the shared deps plus the per-type
// hooks that actually vary between enrichments — which changed fields re-enqueue on update,
// record eligibility, the per-tenant gate, the dedupe hash, and the job args.
type enrichmentProviderConfig struct {
	name        string // enrichment name, used only in log messages ("sentiment", ...)
	inserter    RiverJobInserter
	resolver    TenantSettingsReader // required when gated is true; otherwise unused
	metrics     enqueueMetrics       // may be nil when metrics are disabled
	queueName   string
	maxAttempts int

	// triggers are the changed fields that re-enqueue on an update event.
	triggers []string
	// eligible reports whether a record is eligible at all (e.g. a text field). nil ⇒ always eligible.
	eligible func(record *models.FeedbackRecord) bool
	// hasContent reports whether a create event carries content to enrich (skip empty creates). Required.
	hasContent func(record *models.FeedbackRecord) bool
	// gated reports whether this enrichment reads per-tenant settings before enqueue. When true the
	// provider resolves the tenant's settings and passes them to buildArgs; when false buildArgs
	// receives nil (no settings read). A gated config must supply a resolver.
	gated bool
	// failOpenOnSettingsError makes a settings-read error enqueue the job anyway (buildArgs receives
	// nil settings) instead of dropping it, so a transient settings/cache outage cannot permanently
	// lose enrichment. Only safe when buildArgs is nil-safe and the worker re-checks the gate
	// (sentiment, emotions); translation derives its target language from settings, so it needs them
	// to build args and stays fail-closed (its backfill recovers dropped work).
	failOpenOnSettingsError bool
	// buildArgs decides whether to enqueue and builds the River job payload from the record and
	// (when gated) the tenant's resolved settings. Returning ok=false skips the event — the
	// enrichment is disabled for the tenant, or has no resolvable target. Folding the per-tenant
	// gate, the dedupe hash, and the args into one hook lets a settings-derived value (e.g.
	// translation's target language) be resolved exactly once.
	buildArgs func(record *models.FeedbackRecord, settings *models.TenantSettings, eventID uuid.UUID) (river.JobArgs, bool)
}

// enrichmentProvider implements eventPublisher by enqueueing one enrichment job per eligible
// feedback-record event, driven by an enrichmentProviderConfig. It is the shared enqueue path
// behind the per-type providers (sentiment, emotions, and translation; embedding keeps its own) — the
// per-type differences live entirely in the config hooks. Failures are logged and swallowed so
// they never block ingestion.
type enrichmentProvider struct {
	cfg enrichmentProviderConfig
}

// newEnrichmentProvider builds a provider from cfg, validating it fail-fast.
func newEnrichmentProvider(cfg enrichmentProviderConfig) *enrichmentProvider {
	cfg.validate()

	return &enrichmentProvider{cfg: cfg}
}

// validate panics on a misconfigured provider — a missing required hook, or a gated enrichment
// with no resolver. These are wiring bugs that would otherwise nil-panic only on the first
// eligible event, far from their cause; providers are built at startup, so failing here surfaces
// them immediately.
func (cfg enrichmentProviderConfig) validate() {
	switch {
	case cfg.hasContent == nil:
		panic("enrichment provider " + cfg.name + ": hasContent hook is required")
	case cfg.buildArgs == nil:
		panic("enrichment provider " + cfg.name + ": buildArgs hook is required")
	case cfg.gated && cfg.resolver == nil:
		panic("enrichment provider " + cfg.name + ": resolver is required when the enrichment is gated")
	case cfg.failOpenOnSettingsError && !cfg.gated:
		panic("enrichment provider " + cfg.name + ": failOpenOnSettingsError requires a gated enrichment")
	}
}

// PublishEvent enqueues an enrichment job for an eligible create/update event.
func (p *enrichmentProvider) PublishEvent(ctx context.Context, event Event) {
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

	// Resolve per-tenant settings when gated, so buildArgs can apply the gate and derive per-tenant
	// args. Read after the cheap eligibility checks so non-eligible events never hit the settings
	// store.
	var settings *models.TenantSettings

	if cfg.gated {
		resolved, err := cfg.resolver.GetSettings(ctx, record.TenantID)
		if err != nil {
			if cfg.metrics != nil {
				cfg.metrics.RecordProviderError(ctx, "settings_read_failed")
			}

			if !cfg.failOpenOnSettingsError {
				slog.Error(cfg.name+": resolve tenant settings failed",
					"event_id", event.ID, "feedback_record_id", record.ID, "error", err)

				return
			}

			// Fail open: enqueue anyway with nil settings; the worker re-checks the gate, so a
			// transient settings/cache outage defers work to the worker rather than dropping it.
			slog.Warn(cfg.name+": resolve tenant settings failed, enqueuing anyway (worker re-checks the gate)",
				"event_id", event.ID, "feedback_record_id", record.ID, "error", err)
		} else {
			settings = resolved
		}
	}

	// buildArgs is the per-tenant gate and the payload builder in one: enqueue=false means skip
	// (the enrichment is disabled for the tenant, or has no resolvable target). Note that a gated
	// enrichment which is now disabled skips even the empty-content clear above — a stale result is
	// left until the enrichment is re-enabled (this preserves the pre-refactor gate-before-enqueue
	// behavior).
	args, enqueue := cfg.buildArgs(record, settings, event.ID)
	if !enqueue {
		slog.Debug(cfg.name+": skip, not enabled for tenant", "feedback_record_id", record.ID)

		return
	}

	// Deliberately no UniqueOpts: River's default unique states include `completed` (which
	// cannot be removed), so hash-dedupe silently swallowed legitimate re-enrichment whenever
	// content transitioned away and back within the epoch-aligned dedupe bucket (A→cleared→A
	// left the enrichment NULL; A→""→B→"" kept B's result on an empty record). Update events
	// fire only for fields that actually changed (FieldsChangedFrom), so every event here is a
	// real content transition that must re-enrich — idempotent client re-sends never reach this
	// path in the first place.
	opts := &river.InsertOpts{
		Queue:       cfg.queueName,
		MaxAttempts: cfg.maxAttempts,
	}

	if _, err := cfg.inserter.Insert(ctx, args, opts); err != nil {
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
		if slices.Contains(changed, f) {
			return true
		}
	}

	return false
}
