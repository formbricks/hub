package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The enrichment names, shared by the failure markers, the pending-set queries and the job kinds.
// They are values in a CHECK constraint (migration 022) and a metric label, so they are declared
// once here rather than spelled out at each use.
const (
	EnrichmentSentiment   = "sentiment"
	EnrichmentEmotions    = "emotions"
	EnrichmentTranslation = "translation"
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
func pendingEnrichmentSelect(enrichment, gate, notDone, targetColumn, limitParam string) string {
	return `
	SELECT fr.id, fr.tenant_id, ` + targetColumn + `
	FROM feedback_records fr
	LEFT JOIN tenant_settings ts ON ts.tenant_id = fr.tenant_id
	LEFT JOIN feedback_record_enrichment_failures f
		ON f.feedback_record_id = fr.id AND f.enrichment = '` + enrichment + `' AND f.terminal
	WHERE ` + enrichmentEligibleText + `
		AND ` + gate + `
		AND ` + notDone + `
		AND f.feedback_record_id IS NULL
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
		EnrichmentSentiment, enrichmentSentimentOn, enrichmentSentimentNotDone, `''`, `$1`)
	pendingEmotionsSQL = pendingEnrichmentSelect(
		EnrichmentEmotions, enrichmentEmotionsOn, enrichmentEmotionsNotDone, `''`, `$1`)
	pendingTranslationSQL = pendingEnrichmentSelect(
		EnrichmentTranslation, enrichmentEffectiveTarget+` <> ''`, enrichmentTranslationNotDone,
		enrichmentEffectiveTarget, `$2`)
)

// pendingQueryFor maps an enrichment to its query and the arguments that query expects. An unknown
// name is a programming error rather than input, so it fails loudly instead of quietly selecting
// nothing.
func pendingQueryFor(enrichment, defaultLang string, limit int) (string, []any, error) {
	switch enrichment {
	case EnrichmentSentiment:
		return pendingSentimentSQL, []any{limit}, nil
	case EnrichmentEmotions:
		return pendingEmotionsSQL, []any{limit}, nil
	case EnrichmentTranslation:
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
