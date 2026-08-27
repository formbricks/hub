package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/formbricks/hub/internal/models"
)

// ErrUnknownEnrichment is returned for an enrichment name with no pending-set query. It marks a
// wiring mistake, not bad input — every caller is internal.
var ErrUnknownEnrichment = errors.New("unknown enrichment")

// EnrichmentReconcileRepository finds the records an enrichment still owes work on.
//
// It is the level-triggered half of enrichment coverage: the event path enqueues a job when a
// record changes, and this finds anything that path missed — a record created while the provider
// was unconfigured, a job lost to a crash, a transient failure that used up its retries. The
// event path makes enrichment fast; this is what makes it eventually complete.
type EnrichmentReconcileRepository struct {
	db *pgxpool.Pool
}

// NewEnrichmentReconcileRepository creates an enrichment reconcile repository.
func NewEnrichmentReconcileRepository(db *pgxpool.Pool) *EnrichmentReconcileRepository {
	return &EnrichmentReconcileRepository{db: db}
}

// PendingEnrichmentTarget is one record owed an enrichment.
type PendingEnrichmentTarget struct {
	ID       uuid.UUID
	TenantID string
	// TargetLang is the tenant's effective translation target, empty for sentiment and emotions.
	// Carried here because the translation job args need it and resolving it per record later
	// would mean a settings read per record.
	TargetLang string
}

// pendingEnrichmentSelect builds the pending-set query for one enrichment.
//
// Every predicate is REUSED from the status query rather than rewritten. That is the point: the
// endpoint reports `eligible - done - failed` as work that is scheduled, and this is what
// schedules it. If the two definitions of "pending" could drift, the endpoint would report a
// remainder the reconciler never picks up — which is the exact failure this whole feature exists
// to remove. Note in particular that these are NOT the predicates the one-off backfill commands
// use: those trim only spaces, while the status query trims the full ASCII whitespace set, and
// agreeing with the status query is what matters here.
//
// Terminal failures are excluded. Without that the reconciler re-enqueues a content-filtered
// record on every tick forever, at a provider call each time, and never terminates.
//
// Newest first. A backlog drains from the top of the feedback table, so the records someone is
// most likely to be looking at fill in first and the long tail drains underneath.
// The River job kinds the three enrichments run under, spelled here rather than imported: the
// repository layer cannot depend on service, and a kind is as stable a name as the enrichment
// itself (both live in CHECK constraints and job rows). A parity test in internal/service asserts
// these match the args types' Kind() so they cannot drift silently.
// Exported so the parity test in internal/service (which owns the args types) can assert they
// match Kind() — the repository cannot import service to derive them.
const (
	FeedbackSentimentJobKind   = "feedback_sentiment"
	FeedbackEmotionsJobKind    = "feedback_emotions"
	FeedbackTranslationJobKind = "feedback_translation"
)

// pendingInFlightStates are the river_job states in which a record's enrichment is already being
// handled, matching service.InFlightUniqueStates. A record with such a job is NOT pending: it is
// scheduled, and re-enqueueing it would spend a second provider call on the same text.
const pendingInFlightStates = `('available', 'pending', 'retryable', 'running', 'scheduled')`

func pendingEnrichmentSelect(enrichment, jobKind, gate, notDone, targetColumn, limitParam string) string {
	return `
	SELECT fr.id, fr.tenant_id, ` + targetColumn + `
	FROM feedback_records fr
	LEFT JOIN tenant_settings ts ON ts.tenant_id = fr.tenant_id
	LEFT JOIN feedback_record_enrichment_failures f
		ON f.feedback_record_id = fr.id AND f.enrichment = '` + enrichment + `' AND f.terminal
	LEFT JOIN (
		-- Records whose enrichment already has a job in flight, on ANY queue. This is what actually
		-- keeps the sweep from double-enqueueing: River's ByArgs uniqueness cannot do it, because
		-- the event path deliberately inserts with no unique options (its jobs carry no unique key
		-- to collide with) and the other backfill lanes hash their args differently. It also keeps
		-- an in-backoff head from starving the tail — a record whose job is waiting out a retry is
		-- excluded here, so the LIMIT reaches past it to work that can genuinely be enqueued.
		--
		-- Cheap by construction: the in-flight states are bounded by queue depth (thousands), the
		-- scan of them is served by River's own (state, queue, ...) index, and the join is one hash
		-- build over that small set.
		SELECT (j.args->>'feedback_record_id') AS record_id
		FROM river_job j
		WHERE j.kind = '` + jobKind + `' AND j.state IN ` + pendingInFlightStates + `
	) inflight ON inflight.record_id = fr.id::text
	WHERE ` + enrichmentEligibleText + `
		AND ` + gate + `
		AND ` + notDone + `
		AND f.feedback_record_id IS NULL
		AND inflight.record_id IS NULL
	ORDER BY fr.collected_at DESC, fr.id DESC
	LIMIT ` + limitParam
}

// The three pending-set queries, each declaring only the parameters it actually uses.
//
// Sentiment and emotions have no target language, so passing one would leave $1 unreferenced and
// Postgres rejects a statement whose parameter type it cannot infer ("could not determine data
// type of parameter $1"). Their limit is therefore $1 and translation's is $2, and pendingArgsFor
// builds the matching argument list.
var (
	pendingSentimentSQL = pendingEnrichmentSelect(
		models.EnrichmentNameSentiment, FeedbackSentimentJobKind,
		enrichmentSentimentOn, enrichmentSentimentNotDone, `''`, `$1`)
	pendingEmotionsSQL = pendingEnrichmentSelect(
		models.EnrichmentNameEmotions, FeedbackEmotionsJobKind,
		enrichmentEmotionsOn, enrichmentEmotionsNotDone, `''`, `$1`)
	pendingTranslationSQL = pendingEnrichmentSelect(
		models.EnrichmentNameTranslation, FeedbackTranslationJobKind,
		enrichmentEffectiveTarget+` <> ''`, enrichmentTranslationNotDone,
		enrichmentEffectiveTarget, `$2`)
)

// pendingQueryFor maps an enrichment to its query and the arguments that query expects. An unknown
// name is a programming error rather than input, so it fails loudly instead of quietly selecting
// nothing.
func pendingQueryFor(enrichment, defaultLang string, limit int) (string, []any, error) {
	switch enrichment {
	case models.EnrichmentNameSentiment:
		return pendingSentimentSQL, []any{limit}, nil
	case models.EnrichmentNameEmotions:
		return pendingEmotionsSQL, []any{limit}, nil
	case models.EnrichmentNameTranslation:
		return pendingTranslationSQL, []any{defaultLang, limit}, nil
	default:
		return "", nil, fmt.Errorf("%w: %q", ErrUnknownEnrichment, enrichment)
	}
}

// ListPendingEnrichment returns at most limit records still owed the named enrichment, newest
// first, across all tenants.
//
// Cross-tenant by design: a provider outage is deployment-wide, so the work it stranded is too.
// The records carry their own tenant through to the job, and the worker re-checks the per-tenant
// gate before doing anything, so a globally-scoped sweep cannot enrich a tenant that has the
// enrichment switched off.
//
// defaultLang is the deployment translation fallback; "" disables it, leaving only tenants with a
// target of their own eligible for translation.
func (r *EnrichmentReconcileRepository) ListPendingEnrichment(
	ctx context.Context, enrichment, defaultLang string, limit int,
) ([]PendingEnrichmentTarget, error) {
	query, args, err := pendingQueryFor(enrichment, defaultLang, limit)
	if err != nil {
		return nil, err
	}

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query pending %s: %w", enrichment, err)
	}
	defer rows.Close()

	var targets []PendingEnrichmentTarget

	for rows.Next() {
		var target PendingEnrichmentTarget
		if scanErr := rows.Scan(&target.ID, &target.TenantID, &target.TargetLang); scanErr != nil {
			return nil, fmt.Errorf("scan pending %s: %w", enrichment, scanErr)
		}

		targets = append(targets, target)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read pending %s: %w", enrichment, err)
	}

	return targets, nil
}

// countRunnableByQueueSQL counts jobs that are queued or about to be, per queue.
//
// "Runnable" is the set that has not finished and is not being retried into the future:
// available, pending, running and scheduled. Retryable is deliberately EXCLUDED — a job waiting
// out its backoff will run again on its own, so counting it would have the reconciler treat a
// queue as full while nothing is draining, and a stuck backoff would stall the sweep indefinitely.
const countRunnableByQueueSQL = `
	SELECT queue, count(*)
	FROM river_job
	WHERE queue = ANY($1)
	  AND state IN ('available', 'pending', 'running', 'scheduled')
	GROUP BY queue`

// CountRunnableByQueue returns the current depth of each named queue. Queues with nothing runnable
// are absent from the map rather than zero, so callers must treat a missing key as empty.
func (r *EnrichmentReconcileRepository) CountRunnableByQueue(
	ctx context.Context, queues []string,
) (map[string]int64, error) {
	rows, err := r.db.Query(ctx, countRunnableByQueueSQL, queues)
	if err != nil {
		return nil, fmt.Errorf("count runnable jobs by queue: %w", err)
	}

	defer rows.Close()

	depths := make(map[string]int64, len(queues))

	for rows.Next() {
		var (
			queue string
			count int64
		)

		if scanErr := rows.Scan(&queue, &count); scanErr != nil {
			return nil, fmt.Errorf("scan queue depth: %w", scanErr)
		}

		depths[queue] = count
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read queue depths: %w", err)
	}

	return depths, nil
}
