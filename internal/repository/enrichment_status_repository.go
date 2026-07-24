package repository

import (
	"context"
	"fmt"

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

// enrichmentEligibleText is the data-level eligibility predicate: an open-text field with
// content. It mirrors the backfill eligibility (see classifyBackfillEligibleSQL /
// translationBackfillSelectSQL) but uses the fuller btrim charset E' \t\r\n' so a whitespace-only
// value_text ("\t", "\n") is treated as empty — matching the workers' Go strings.TrimSpace content
// gate (HasOpenText). field_type = 'text' is load-bearing: matrix/multi-choice expansion writes
// value_text on categorical/number rows that are not enrichable.
const enrichmentEligibleText = `fr.field_type = 'text' AND fr.value_text IS NOT NULL AND btrim(fr.value_text, E' \t\r\n') <> ''`

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

// The `fr.field_type = 'text'` predicate in the outer WHERE below is redundant with
// enrichmentEligibleText inside every FILTER (so it can never change a count), but hoisting it out
// lets the planner use idx_feedback_records_tenant_field_type (tenant_id, field_type) to visit only
// text rows instead of scanning every row and discarding non-text inside the FILTERs.

// countEnrichmentStatusSQL counts eligible/done per enrichment for ONE tenant. $1 = deployment
// default target language, $2 = tenant_id. The (tenant_id, field_type) predicate is served by
// idx_feedback_records_tenant_field_type, so cost scales with the tenant's text-record count.
const countEnrichmentStatusSQL = `SELECT ` + enrichmentCountSelect + enrichmentCountFrom + `
	WHERE fr.tenant_id = $2 AND fr.field_type = 'text'`

// countEnrichmentBacklogAggregateSQL is the same SELECT without the tenant filter: it sums
// eligible/done per enrichment across ALL tenants (for the observability gauge). $1 = deployment
// default target language. The field_type = 'text' predicate narrows the scan to text rows; the
// per-tenant enable gates still apply, so a tenant that switched an enrichment off, or has no
// resolvable target, never inflates the backlog.
const countEnrichmentBacklogAggregateSQL = `SELECT ` + enrichmentCountSelect + enrichmentCountFrom + `
	WHERE fr.field_type = 'text'`

// CountEnrichmentStatus returns one tenant's eligible/done counts per enrichment. defaultLang is
// the deployment translation fallback ("" disables the fallback, so only tenants with their own
// target language have eligible translation records). Always scoped to the given tenant_id.
func (r *EnrichmentStatusRepository) CountEnrichmentStatus(
	ctx context.Context, tenantID, defaultLang string,
) (EnrichmentStatusCounts, error) {
	var counts EnrichmentStatusCounts

	// Scan order matches enrichmentCountSelect.
	err := r.db.QueryRow(ctx, countEnrichmentStatusSQL, defaultLang, tenantID).Scan(
		&counts.SentimentEligible, &counts.SentimentDone,
		&counts.EmotionsEligible, &counts.EmotionsDone,
		&counts.TranslationEligible, &counts.TranslationDone,
	)
	if err != nil {
		return EnrichmentStatusCounts{}, fmt.Errorf("count enrichment status: %w", err)
	}

	return counts, nil
}

// CountEnrichmentBacklogAggregate returns eligible/done counts per enrichment summed across all
// tenants. defaultLang is the deployment translation fallback. Used by the observability poller;
// the result carries no tenant dimension.
func (r *EnrichmentStatusRepository) CountEnrichmentBacklogAggregate(
	ctx context.Context, defaultLang string,
) (EnrichmentStatusCounts, error) {
	var counts EnrichmentStatusCounts

	// Scan order matches enrichmentCountSelect.
	err := r.db.QueryRow(ctx, countEnrichmentBacklogAggregateSQL, defaultLang).Scan(
		&counts.SentimentEligible, &counts.SentimentDone,
		&counts.EmotionsEligible, &counts.EmotionsDone,
		&counts.TranslationEligible, &counts.TranslationDone,
	)
	if err != nil {
		return EnrichmentStatusCounts{}, fmt.Errorf("count enrichment backlog aggregate: %w", err)
	}

	return counts, nil
}
