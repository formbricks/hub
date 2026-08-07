package models

// Query-side types for listing feedback records: the filter set accepted by
// GET /v1/feedback-records and /count, plus the sort control that orders the result.

import (
	"time"
)

// SortField is the column a feedback record listing is ordered by (ENG-2059).
//
// Every member must name a column that is both NOT NULL and IMMUTABLE after insert:
//   - NOT NULL, because the keyset predicate has no NULL handling — `col < $t` is NULL for a NULL
//     row, which would drop it from every page after the first.
//   - immutable, because a mutable sort key lets a row move across the cursor between pages and be
//     silently skipped. This is why updated_at is deliberately absent: the enrichment workers
//     (SetTranslation, SetSentiment, writeEmotions) and every PATCH bump it, so `sort=updated_at`
//     would lose rows under ordinary traffic. A change-feed needs an append-only sequence, not a
//     sort parameter.
//
// Adding a member requires a case in the repository's resolveListOrdering (token -> SQL column)
// and in FeedbackRecord.SortValue (token -> record field). Both switches are exhaustive-linted, so
// a half-done addition fails lint instead of shipping cursors that bound the wrong column.
type SortField string

// Valid SortField values.
const (
	SortFieldCollectedAt SortField = "collected_at"
	SortFieldCreatedAt   SortField = "created_at"
)

// SortOrder is the direction of a feedback record listing.
type SortOrder string

// Valid SortOrder values.
const (
	SortOrderAsc  SortOrder = "asc"
	SortOrderDesc SortOrder = "desc"
)

// Defaults reproducing the ordering the list endpoint had before sort was configurable, so a
// request that omits both parameters generates the same SQL it always has.
const (
	DefaultSortField = SortFieldCollectedAt
	DefaultSortOrder = SortOrderDesc
)

// MaxFilterValues caps how many values one repeatable *string* filter may carry. The enum filters
// cap at their own label-set cardinality instead, since more entries than that can only be
// duplicates.
//
// go-playground/form imposes no useful bound of its own on a repeated query parameter, so these
// caps are the only thing between a caller and an arbitrarily long IN-list. The remaining exposure
// is bounded by net/http's MaxHeaderBytes, since values are materialized while decoding and
// rejected afterwards.
//
// Struct tags cannot reference a constant, so every cap below is written out as a literal.
// TestFilterValueCapsMatchTheirSets reflects over the tags and enforces that they agree with this
// constant and with the enum sets, which is what keeps adding a label from silently making a
// legitimate request a 400. Keep the OpenAPI maxItems for these parameters in step by hand.
const MaxFilterValues = 100

// ListFeedbackRecordsFilters represents filters for listing feedback records. It is shared
// verbatim by GET /v1/feedback-records and GET /v1/feedback-records/count, so every filter applies
// identically to both and a count always describes the list it is counting.
//
// Multi-value filters are plain slices rather than pointers to slices: the form decoder leaves the
// field nil when the parameter is absent, so nil is already the "unset" state and a *[]T would add
// a second, meaningless one. Single-value filters stay pointers for the mirror-image reason — a
// bare string cannot distinguish "absent" from "empty".
//
// TenantID is deliberately NOT repeatable. It is the tenant isolation boundary; every other filter
// narrows within it.
type ListFeedbackRecordsFilters struct {
	TenantID *string `form:"tenant_id" validate:"required,no_null_bytes,min=1"`

	// Repeatable identity filters. A single occurrence behaves exactly as it did when these were
	// scalars: the repository emits `col = ANY($n)` over a one-element array, which the planner
	// treats as the same ScalarArrayOpExpr an equality produces.
	SubmissionID []string    `form:"submission_id"  validate:"omitempty,max=100,dive,no_null_bytes,max=255"`
	SourceType   []string    `form:"source_type"    validate:"omitempty,max=100,dive,no_null_bytes,max=255"`
	SourceID     []string    `form:"source_id"      validate:"omitempty,max=100,dive,no_null_bytes,max=255"`
	SourceName   []string    `form:"source_name"    validate:"omitempty,max=100,dive,no_null_bytes,max=255"`
	FieldID      []string    `form:"field_id"       validate:"omitempty,max=100,dive,no_null_bytes,max=255"`
	FieldGroupID []string    `form:"field_group_id" validate:"omitempty,max=100,dive,no_null_bytes,max=255"`
	ValueID      []string    `form:"value_id"       validate:"omitempty,max=100,dive,no_null_bytes,max=255"`
	UserID       []string    `form:"user_id"        validate:"omitempty,max=100,dive,no_null_bytes,max=255"`
	Language     []string    `form:"language"       validate:"omitempty,max=100,dive,no_null_bytes,max=10"`
	FieldType    []FieldType `form:"field_type"     validate:"omitempty,max=9,dive,field_type"`

	// collected_at bounds. The bare names predate created_at filtering and are kept for
	// compatibility; both are inclusive.
	Since *time.Time `form:"since" validate:"omitempty"`
	Until *time.Time `form:"until" validate:"omitempty"`

	// created_at bounds. Distinct from Since/Until because the two columns diverge on a
	// historical re-import: old collected_at, today's created_at.
	CreatedSince *time.Time `form:"created_since" validate:"omitempty"`
	CreatedUntil *time.Time `form:"created_until" validate:"omitempty"`

	// Inclusive value bounds. Like every range filter here, these exclude rows whose column is
	// NULL — value_number_min=0 selects only records that carry a number at all.
	ValueNumberMin *float64   `form:"value_number_min" validate:"omitempty"`
	ValueNumberMax *float64   `form:"value_number_max" validate:"omitempty"`
	ValueDateMin   *time.Time `form:"value_date_min"   validate:"omitempty"`
	ValueDateMax   *time.Time `form:"value_date_max"   validate:"omitempty"`

	// Enrichment filters. The repeatable enum filters are validated twice on purpose: the decoder
	// parses and deduplicates the plain repeated form (?sentiment=a&sentiment=b) for a good error
	// message, and these dive tags re-check the decoded elements. The tags are what actually holds
	// the invariant — the decoder's custom type funcs are skipped entirely for the indexed form
	// (?sentiment[0]=a), which would otherwise assign the raw string unvalidated. The max caps
	// bound that same path, where dedupe does not apply.
	Sentiment []SentimentValue `form:"sentiment" validate:"omitempty,max=6,dive,sentiment"`
	Emotions  []EmotionValue   `form:"emotions"  validate:"omitempty,max=6,dive,emotion"`

	// Presence filters are tri-state: nil leaves the column unconstrained, true means IS NOT NULL,
	// false means IS NULL. A plain bool would collapse "absent" into "false".
	HasSentiment   *bool `form:"has_sentiment"   validate:"omitempty"`
	HasEmotions    *bool `form:"has_emotions"    validate:"omitempty"`
	HasTranslation *bool `form:"has_translation" validate:"omitempty"`

	// Bounds mirror SentimentScoreMin/SentimentScoreMax and the
	// feedback_records_sentiment_score_range DB CHECK; keep the three in sync.
	SentimentScoreMin *float64 `form:"sentiment_score_min" validate:"omitempty,gte=-1,lte=1"`
	SentimentScoreMax *float64 `form:"sentiment_score_max" validate:"omitempty,gte=-1,lte=1"`

	Sort  SortField `form:"sort"  validate:"omitempty,oneof=collected_at created_at"`
	Order SortOrder `form:"order" validate:"omitempty,oneof=asc desc"`

	Limit  int    `form:"limit"  validate:"omitempty,min=1,max=1000"`
	Cursor string `form:"cursor" validate:"omitempty"` // keyset; omit for first page, use next_cursor for next
}

// InvertedRangeFilter names a min/max filter pair that was supplied in the wrong order.
type InvertedRangeFilter struct {
	// StructField is the Go field name of the lower bound, which validator.StructLevel.ReportError
	// needs to locate the field.
	StructField string
	// MinParam is the form name of the lower bound — the parameter invalid_params reports.
	MinParam string
	// MaxParam is the form name of the upper bound, quoted in the reason.
	MaxParam string
	// MinValue is the lower bound's value, passed through to ReportError.
	MinValue any
}

// InvertedRanges reports every min/max pair whose lower bound exceeds its upper bound.
//
// Such a range can only ever match zero rows, so it is a client bug — a swapped pair of
// parameters, a date picker that allowed an end before a start. A 400 naming the pair beats an
// empty page, which a caller reads as "there is no such feedback".
//
// Every range filter MUST appear here; a new *_min/*_since pair that is not listed silently loses
// the check, which TestInvertedRanges_CoversEveryRangeFilter guards against by reflecting over the
// struct's form tags.
func (f *ListFeedbackRecordsFilters) InvertedRanges() []InvertedRangeFilter {
	inverted := make([]InvertedRangeFilter, 0, 5) //nolint:mnd // capacity hint: the five pairs below

	appendInvertedTimeRange(&inverted, f.Since, f.Until, "Since", "since", "until")
	appendInvertedTimeRange(&inverted, f.CreatedSince, f.CreatedUntil, "CreatedSince", "created_since", "created_until")
	appendInvertedTimeRange(&inverted, f.ValueDateMin, f.ValueDateMax, "ValueDateMin", "value_date_min", "value_date_max")
	appendInvertedFloatRange(
		&inverted, f.ValueNumberMin, f.ValueNumberMax, "ValueNumberMin", "value_number_min", "value_number_max",
	)
	appendInvertedFloatRange(
		&inverted, f.SentimentScoreMin, f.SentimentScoreMax,
		"SentimentScoreMin", "sentiment_score_min", "sentiment_score_max",
	)

	if len(inverted) == 0 {
		return nil
	}

	return inverted
}

// appendInvertedTimeRange records a timestamp pair whose lower bound is after its upper bound.
// A pair with either side absent is unbounded on that side and cannot be inverted.
func appendInvertedTimeRange(
	inverted *[]InvertedRangeFilter, lower, upper *time.Time, structField, minParam, maxParam string,
) {
	if lower == nil || upper == nil || !lower.After(*upper) {
		return
	}

	*inverted = append(*inverted, InvertedRangeFilter{
		StructField: structField, MinParam: minParam, MaxParam: maxParam, MinValue: *lower,
	})
}

// appendInvertedFloatRange records a numeric pair whose lower bound exceeds its upper bound.
func appendInvertedFloatRange(
	inverted *[]InvertedRangeFilter, lower, upper *float64, structField, minParam, maxParam string,
) {
	if lower == nil || upper == nil || *lower <= *upper {
		return
	}

	*inverted = append(*inverted, InvertedRangeFilter{
		StructField: structField, MinParam: minParam, MaxParam: maxParam, MinValue: *lower,
	})
}
