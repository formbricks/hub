package repository

import (
	"fmt"
	"strings"
	"time"

	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
)

// sqlColumn is a bare SQL identifier that is safe to interpolate into a query.
//
// It is a distinct type rather than a string so that a request value cannot reach the SQL text by
// accident: every call site below passes an untyped constant, which converts implicitly, while
// passing a `string` variable is a compile error that has to be silenced with an explicit,
// greppable conversion. The same reasoning applies to sqlOperator.
type sqlColumn string

// Columns of feedback_records that filters may constrain.
const (
	colTenantID           sqlColumn = "tenant_id"
	colSubmissionID       sqlColumn = "submission_id"
	colSourceType         sqlColumn = "source_type"
	colSourceID           sqlColumn = "source_id"
	colSourceName         sqlColumn = "source_name"
	colFieldID            sqlColumn = "field_id"
	colFieldGroupID       sqlColumn = "field_group_id"
	colFieldType          sqlColumn = "field_type"
	colValueID            sqlColumn = "value_id"
	colUserID             sqlColumn = "user_id"
	colLanguage           sqlColumn = "language"
	colCollectedAt        sqlColumn = "collected_at"
	colCreatedAt          sqlColumn = "created_at"
	colValueDate          sqlColumn = "value_date"
	colValueNumber        sqlColumn = "value_number"
	colSentiment          sqlColumn = "sentiment"
	colSentimentScore     sqlColumn = "sentiment_score"
	colEmotions           sqlColumn = "emotions"
	colTranslationLangKey sqlColumn = "translation_lang_key"
)

// fieldTypeEnum is the PostgreSQL ENUM backing feedback_records.field_type (migration 001).
const fieldTypeEnum = "field_type_enum"

// sqlOperator is a comparison operator, kept a distinct type for the same reason as sqlColumn.
type sqlOperator string

// Comparison operators used by the filter builder.
const (
	opEqual    sqlOperator = "="
	opAtLeast  sqlOperator = ">="
	opAtMost   sqlOperator = "<="
	opOverlaps sqlOperator = "&&"
)

// filterConditions accumulates SQL WHERE fragments together with the arguments they bind.
//
// Every add* method reserves its bind slot and formats its placeholder in one call, so $N always
// equals that argument's 1-based position no matter what order conditions are added in. This makes
// mechanical the invariant that used to be a convention: a new filter cannot format a placeholder
// without appending its argument, so the numbering cannot desync.
type filterConditions struct {
	conditions []string
	args       []any
}

// placeholder appends arg and returns its 1-based bind position.
func (f *filterConditions) placeholder(arg any) int {
	f.args = append(f.args, arg)

	return len(f.args)
}

// addRaw appends a condition that binds no argument (IS NULL / IS NOT NULL).
func (f *filterConditions) addRaw(condition string) {
	f.conditions = append(f.conditions, condition)
}

// addComparison appends `column <operator> $N`.
func (f *filterConditions) addComparison(column sqlColumn, operator sqlOperator, arg any) {
	f.conditions = append(f.conditions,
		fmt.Sprintf("%s %s $%d", column, operator, f.placeholder(arg)))
}

// addAnyOf appends `column = ANY($N)` — the multi-value form of `column = $N`. pgx binds the
// []string as a text array, so one placeholder covers every value, and a one-element slice
// produces the same plan and the same rows the scalar equality did.
func (f *filterConditions) addAnyOf(column sqlColumn, values []string) {
	if len(values) == 0 {
		return
	}

	f.conditions = append(f.conditions,
		fmt.Sprintf("%s = ANY($%d)", column, f.placeholder(values)))
}

// addAnyOfEnum appends `column = ANY($N::text[]::<pgEnum>[])`.
//
// field_type is a PostgreSQL ENUM and pgx has no codec for field_type_enum[]; binding a []string
// directly would rely on its reflection fallback resolving to _text, which works by coincidence
// rather than contract. The inner ::text[] pins the parameter to an OID pgx knows and lets
// Postgres cast the array to the enum. The left side stays the bare column, so the existing
// (tenant_id, field_type) index remains usable.
func (f *filterConditions) addAnyOfEnum(column sqlColumn, pgEnum string, values []string) {
	if len(values) == 0 {
		return
	}

	f.conditions = append(f.conditions,
		fmt.Sprintf("%s = ANY($%d::text[]::%s[])", column, f.placeholder(values), pgEnum))
}

// addNullState renders the presence test for a nullable column; nil leaves it unconstrained.
func (f *filterConditions) addNullState(column sqlColumn, has *bool) {
	if has == nil {
		return
	}

	if *has {
		f.addRaw(string(column) + " IS NOT NULL")

		return
	}

	f.addRaw(string(column) + " IS NULL")
}

// where renders the accumulated conditions, including the leading " WHERE ".
func (f *filterConditions) where() string {
	if len(f.conditions) == 0 {
		return ""
	}

	return " WHERE " + strings.Join(f.conditions, " AND ")
}

// stringsOf converts a slice of a named string type to []string.
//
// Every multi-value filter binds the same Go type this way: pgx maps []string to the built-in
// text-array codec, whereas a slice of a named string type has no registered reflect type and
// falls through to a wrapper that cannot encode into an OID pgx does not know. scanFeedbackRecord
// reads emotions through []string for the mirror-image reason.
func stringsOf[T ~string](values []T) []string {
	if len(values) == 0 {
		return nil
	}

	out := make([]string, len(values))
	for i, value := range values {
		out[i] = string(value)
	}

	return out
}

// buildFilterConditions builds the WHERE clause and arguments for a feedback-record listing.
// Returns the clause including its " WHERE " prefix, plus the args in placeholder order.
//
// It fails closed on a missing tenant. The HTTP layer marks tenant_id required, but this function
// is also reachable from workers and tests that construct filters directly in Go, and a nil tenant
// would otherwise emit a query spanning every tenant — silently, and looking entirely normal.
func buildFilterConditions(filters *models.ListFeedbackRecordsFilters) (whereClause string, args []any, err error) {
	if filters == nil || filters.TenantID == nil || strings.TrimSpace(*filters.TenantID) == "" {
		return "", nil, huberrors.NewValidationError("tenant_id", "tenant_id is required to list feedback records")
	}

	conds := &filterConditions{}

	appendIdentityConditions(conds, filters)
	appendTimeConditions(conds, filters)
	appendValueConditions(conds, filters)
	appendEnrichmentConditions(conds, filters)

	return conds.where(), conds.args, nil
}

// appendIdentityConditions adds the tenant scope and the repeatable identity filters.
//
// The order matches what these emitted as single-value filters, so a request that uses only the
// pre-existing parameters still binds them at the same placeholder positions.
func appendIdentityConditions(conds *filterConditions, filters *models.ListFeedbackRecordsFilters) {
	conds.addComparison(colTenantID, opEqual, *filters.TenantID)
	conds.addAnyOf(colSubmissionID, filters.SubmissionID)
	conds.addAnyOf(colSourceType, filters.SourceType)
	conds.addAnyOf(colSourceID, filters.SourceID)
	conds.addAnyOf(colFieldID, filters.FieldID)
	conds.addAnyOf(colFieldGroupID, filters.FieldGroupID)
	conds.addAnyOfEnum(colFieldType, fieldTypeEnum, stringsOf(filters.FieldType))
	conds.addAnyOf(colValueID, filters.ValueID)
	conds.addAnyOf(colUserID, filters.UserID)
	conds.addAnyOf(colSourceName, filters.SourceName)
	conds.addAnyOf(colLanguage, filters.Language)
}

// appendTimeConditions adds the inclusive timestamp ranges. collected_at is when the feedback was
// given; created_at is when Hub stored it. They diverge on a historical re-import, which is why
// each has its own pair.
func appendTimeConditions(conds *filterConditions, filters *models.ListFeedbackRecordsFilters) {
	addTimeRange(conds, colCollectedAt, filters.Since, filters.Until)
	addTimeRange(conds, colCreatedAt, filters.CreatedSince, filters.CreatedUntil)
	addTimeRange(conds, colValueDate, filters.ValueDateMin, filters.ValueDateMax)
}

// appendValueConditions adds the inclusive numeric range over the answer value.
func appendValueConditions(conds *filterConditions, filters *models.ListFeedbackRecordsFilters) {
	addFloatRange(conds, colValueNumber, filters.ValueNumberMin, filters.ValueNumberMax)
}

// appendEnrichmentConditions adds the sentiment, emotion and translation filters.
func appendEnrichmentConditions(conds *filterConditions, filters *models.ListFeedbackRecordsFilters) {
	conds.addAnyOf(colSentiment, stringsOf(filters.Sentiment))
	addFloatRange(conds, colSentimentScore, filters.SentimentScoreMin, filters.SentimentScoreMax)

	// ANY, not ALL: `&&` (overlap) matches a record carrying at least one of the requested
	// emotions, which is how two selected filter chips read. `@>` (containment) would mean
	// "carries all of them" — a different and much rarer question. `&&` is also strict, so the
	// planner can prove emotions IS NOT NULL and still use the partial GIN index.
	if emotions := stringsOf(filters.Emotions); len(emotions) > 0 {
		conds.addComparison(colEmotions, opOverlaps, emotions)
	}

	conds.addNullState(colSentiment, filters.HasSentiment)
	conds.addNullState(colEmotions, filters.HasEmotions)
	conds.addNullState(colTranslationLangKey, filters.HasTranslation)
}

// addTimeRange adds the inclusive bounds present on a timestamp column. Rows whose column is NULL
// are excluded by SQL comparison semantics, which is the documented behavior of every range filter
// on this endpoint.
func addTimeRange(conds *filterConditions, column sqlColumn, lower, upper *time.Time) {
	if lower != nil {
		conds.addComparison(column, opAtLeast, *lower)
	}

	if upper != nil {
		conds.addComparison(column, opAtMost, *upper)
	}
}

// addFloatRange adds the inclusive bounds present on a numeric column.
func addFloatRange(conds *filterConditions, column sqlColumn, lower, upper *float64) {
	if lower != nil {
		conds.addComparison(column, opAtLeast, *lower)
	}

	if upper != nil {
		conds.addComparison(column, opAtMost, *upper)
	}
}
