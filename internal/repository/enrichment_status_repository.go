package repository

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// EnrichmentStatusRepository computes data-derived enrichment progress counts over
// feedback_records. It is a read-only sibling of FeedbackRecordsRepository, kept separate
// so the status/observability concern doesn't grow the primary records repository.
type EnrichmentStatusRepository struct {
	db *pgxpool.Pool
}

// NewEnrichmentStatusRepository creates an enrichment status repository.
func NewEnrichmentStatusRepository(db *pgxpool.Pool) *EnrichmentStatusRepository {
	return &EnrichmentStatusRepository{db: db}
}

// EnrichmentStatusCounts holds the raw per-enrichment eligible/done counts. The counts already
// reflect the per-tenant gates: sentiment/emotions include only tenants with the enrichment
// switched on, translation only records with a resolvable effective target language. The
// deployment-level (provider/model) gate is applied by the caller.
type EnrichmentStatusCounts struct {
	TranslationEligible int64
	TranslationDone     int64
	SentimentEligible   int64
	SentimentDone       int64
	EmotionsEligible    int64
	EmotionsDone        int64
}

// enrichmentEligibleText is the data-level eligibility predicate: an open-text field with content.
// It mirrors the backfill eligibility (classifyBackfillEligibleSQL / translationBackfillSelectSQL),
// trimming the full ASCII whitespace set (space, tab, VT, FF, CR, LF) before the emptiness check.
// This approximates the workers' HasOpenText gate (Go strings.TrimSpace, which additionally strips
// exotic Unicode whitespace such as NBSP U+00A0 or the ideographic space U+3000); a value composed
// ENTIRELY of exotic Unicode whitespace is a rare edge that would read as eligible-but-never-done
// here — an accepted approximation, consistent with the existing backfill queries. field_type =
// 'text' is load-bearing: matrix/multi-choice expansion writes value_text on categorical/number
// rows that are not enrichable.
const enrichmentEligibleText = `fr.field_type = 'text' AND fr.value_text IS NOT NULL AND btrim(fr.value_text, E' \t\n\v\f\r') <> ''`

// enrichmentEffectiveTarget resolves a tenant's effective translation target: its own
// target_language, falling back to the deployment default ($1). An empty result means translation
// is not enabled for the tenant. Mirrors translationBackfillSelectSQL.
// enrichmentSentimentOn / enrichmentEmotionsOn read the tri-state per-directory switch, defaulting
// to enabled when the key is absent — matching EnrichmentSettings.SentimentEnrichmentEnabled /
// EmotionsEnrichmentEnabled (parity is covered by test). $1 is the deployment default target in
// both the per-tenant and aggregate queries.
const (
	enrichmentEffectiveTarget = `COALESCE(NULLIF(ts.settings->>'target_language', ''), $1)`
	enrichmentSentimentOn     = `COALESCE((ts.settings->>'sentiment_enabled')::boolean, true)`
	enrichmentEmotionsOn      = `COALESCE((ts.settings->>'emotions_enabled')::boolean, true)`
)

// enrichmentCountSelect is the shared six-column SELECT list — {sentiment, emotions, translation} ×
// {eligible, done} — used by both the per-tenant and aggregate queries so the predicates can't
// drift between them. Column order must match the EnrichmentStatusCounts scan order below. All
// fragments are static constants (never user input).
const enrichmentCountSelect = `
		COUNT(*) FILTER (WHERE ` + enrichmentEligibleText + ` AND ` + enrichmentSentimentOn + `),
		COUNT(*) FILTER (WHERE ` + enrichmentEligibleText + ` AND ` + enrichmentSentimentOn + ` AND fr.sentiment IS NOT NULL),
		COUNT(*) FILTER (WHERE ` + enrichmentEligibleText + ` AND ` + enrichmentEmotionsOn + `),
		COUNT(*) FILTER (WHERE ` + enrichmentEligibleText + ` AND ` + enrichmentEmotionsOn + ` AND fr.emotions IS NOT NULL),
		COUNT(*) FILTER (WHERE ` + enrichmentEligibleText + ` AND ` + enrichmentEffectiveTarget + ` <> ''),
		COUNT(*) FILTER (
			WHERE ` + enrichmentEligibleText + `
			AND ` + enrichmentEffectiveTarget + ` <> ''
			AND fr.translation_lang_key = ` + enrichmentEffectiveTarget + `)`

const enrichmentCountFrom = `
	FROM feedback_records fr
	LEFT JOIN tenant_settings ts ON ts.tenant_id = fr.tenant_id`

// The `fr.field_type = 'text'` predicate in the outer WHERE of both queries below is redundant
// with enrichmentEligibleText inside every FILTER (so it can never change a count); it is hoisted
// out so the planner can use it as an access-path predicate rather than only as a per-row filter.

// countEnrichmentStatusSQL counts eligible/done per enrichment for ONE tenant. $1 = deployment
// default target language, $2 = tenant_id. The (tenant_id, field_type) pair matches
// idx_feedback_records_tenant_field_type, so cost scales with the tenant's text-record count.
const countEnrichmentStatusSQL = `SELECT ` + enrichmentCountSelect + enrichmentCountFrom + `
	WHERE fr.tenant_id = $2 AND fr.field_type = 'text'`

// countEnrichmentBacklogAggregateSQL is the same SELECT without the tenant filter: it sums
// eligible/done per enrichment across ALL tenants (for the observability gauge). $1 = deployment
// default target language. The per-tenant enable gates still apply, so a tenant that switched an
// enrichment off, or has no resolvable target, never inflates the backlog.
//
// NOTE: unlike the per-tenant query this one canNOT use idx_feedback_records_tenant_field_type --
// tenant_id is that index's leading column, and this query has no tenant predicate -- so Postgres
// plans a sequential scan of feedback_records (confirmed via EXPLAIN). That is deliberate: the
// aggregate must read every text row anyway, so an index scan over most of the table would not be
// cheaper, and a dedicated partial index would add write amplification to the high-throughput
// ingest path we are protecting. The scan is instead bounded by running it infrequently
// (enrichmentBacklogInterval), under a statement timeout, and on ONE replica at a time via the
// advisory lock below.
const countEnrichmentBacklogAggregateSQL = `SELECT ` + enrichmentCountSelect + enrichmentCountFrom + `
	WHERE fr.field_type = 'text'`

// enrichmentBacklogLockKey names the advisory lock that elects a single backlog-poller run across
// API replicas. Hashed with hashtextextended like the other advisory locks in this package.
const enrichmentBacklogLockKey = "hub:enrichment-backlog-poller"

// tryAdvisoryXactLockSQL takes a transaction-scoped advisory lock without blocking; it returns
// false when another session (replica) already holds it. Transaction scope means the lock is
// always released on commit/rollback, so a crashed poller cannot wedge the others.
const tryAdvisoryXactLockSQL = `SELECT pg_try_advisory_xact_lock(hashtextextended($1, 0))`

// CountEnrichmentStatus returns one tenant's eligible/done counts per enrichment. defaultLang is
// the deployment translation fallback ("" disables the fallback, so only tenants with their own
// target language have eligible translation records). Always scoped to the given tenant_id.
func (r *EnrichmentStatusRepository) CountEnrichmentStatus(
	ctx context.Context, tenantID, defaultLang string,
) (EnrichmentStatusCounts, error) {
	return scanEnrichmentCounts(
		r.db.QueryRow(ctx, countEnrichmentStatusSQL, defaultLang, tenantID), "count enrichment status")
}

// CountEnrichmentBacklogAggregate returns eligible/done counts per enrichment summed across all
// tenants. defaultLang is the deployment translation fallback. The result carries no tenant
// dimension. Prefer CountEnrichmentBacklogAggregateIfLeader from the poller so only one replica
// runs the scan.
func (r *EnrichmentStatusRepository) CountEnrichmentBacklogAggregate(
	ctx context.Context, defaultLang string,
) (EnrichmentStatusCounts, error) {
	return scanEnrichmentCounts(
		r.db.QueryRow(ctx, countEnrichmentBacklogAggregateSQL, defaultLang), "count enrichment backlog aggregate")
}

// CountEnrichmentBacklogAggregateIfLeader runs the cross-tenant aggregate only if this process wins
// a non-blocking advisory lock, and reports whether it did. Production runs several API replicas
// per region, and each would otherwise repeat the same full-table scan on every tick and publish
// the same global gauge — multiplying DB load and producing duplicate series that a naive sum would
// over-count. Losing the race is normal, not an error: the winner refreshes the gauge for everyone.
// The lock is transaction-scoped, so it is released even if this process dies mid-scan.
func (r *EnrichmentStatusRepository) CountEnrichmentBacklogAggregateIfLeader(
	ctx context.Context, defaultLang string,
) (EnrichmentStatusCounts, bool, error) {
	backlogTx, err := r.db.Begin(ctx)
	if err != nil {
		return EnrichmentStatusCounts{}, false, fmt.Errorf("begin enrichment backlog tx: %w", err)
	}

	// Read-only work: always roll back (releasing the advisory lock); a rollback after success is
	// equivalent to a commit here and avoids leaking the tx on any early return.
	defer func() { _ = backlogTx.Rollback(ctx) }()

	var acquired bool
	if err := backlogTx.QueryRow(ctx, tryAdvisoryXactLockSQL, enrichmentBacklogLockKey).Scan(&acquired); err != nil {
		return EnrichmentStatusCounts{}, false, fmt.Errorf("try enrichment backlog advisory lock: %w", err)
	}

	if !acquired {
		return EnrichmentStatusCounts{}, false, nil
	}

	counts, err := scanEnrichmentCounts(
		backlogTx.QueryRow(ctx, countEnrichmentBacklogAggregateSQL, defaultLang), "count enrichment backlog aggregate")
	if err != nil {
		return EnrichmentStatusCounts{}, false, err
	}

	return counts, true, nil
}

// scanEnrichmentCounts reads the six-column count row; the scan order matches enrichmentCountSelect.
func scanEnrichmentCounts(row pgx.Row, what string) (EnrichmentStatusCounts, error) {
	var counts EnrichmentStatusCounts

	if err := row.Scan(
		&counts.SentimentEligible, &counts.SentimentDone,
		&counts.EmotionsEligible, &counts.EmotionsDone,
		&counts.TranslationEligible, &counts.TranslationDone,
	); err != nil {
		return EnrichmentStatusCounts{}, fmt.Errorf("%s: %w", what, err)
	}

	return counts, nil
}
