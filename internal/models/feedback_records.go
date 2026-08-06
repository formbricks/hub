// Package models defines request/response and domain types for feedback records.
package models

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Sentinel errors for the enum filters parsed out of query parameters (err113). Each has a
// matching Invalid*Error carrying the rejected value, so a handler can report which parameter was
// wrong without string-matching the message.
var (
	// ErrInvalidFieldType is returned when a field type string is invalid.
	ErrInvalidFieldType = errors.New("invalid field type")
	// ErrInvalidSentimentValue is returned when a sentiment label string is invalid.
	ErrInvalidSentimentValue = errors.New("invalid sentiment")
	// ErrInvalidEmotionValue is returned when an emotion label string is invalid.
	ErrInvalidEmotionValue = errors.New("invalid emotion")
)

// FieldType represents the type of feedback field.
type FieldType string

// Valid FieldType constants for feedback fields (NPS, CSAT, CES, rating, etc.).
const (
	FieldTypeText        FieldType = "text"
	FieldTypeCategorical FieldType = "categorical"
	FieldTypeNPS         FieldType = "nps"
	FieldTypeCSAT        FieldType = "csat"
	FieldTypeCES         FieldType = "ces"
	FieldTypeRating      FieldType = "rating"
	FieldTypeNumber      FieldType = "number"
	FieldTypeBoolean     FieldType = "boolean"
	FieldTypeDate        FieldType = "date"
)

var validFieldTypeValues = []FieldType{
	FieldTypeText,
	FieldTypeCategorical,
	FieldTypeNPS,
	FieldTypeCSAT,
	FieldTypeCES,
	FieldTypeRating,
	FieldTypeNumber,
	FieldTypeBoolean,
	FieldTypeDate,
}

var validFieldTypeValueNames = func() []string {
	values := make([]string, 0, len(validFieldTypeValues))
	for _, fieldType := range validFieldTypeValues {
		values = append(values, string(fieldType))
	}

	return values
}()

var validFieldTypeValuesString = strings.Join(validFieldTypeValueNames, ", ")

// ValidFieldTypes contains all valid field type values (set membership).
var ValidFieldTypes = func() map[FieldType]struct{} {
	values := make(map[FieldType]struct{}, len(validFieldTypeValues))
	for _, fieldType := range validFieldTypeValues {
		values[fieldType] = struct{}{}
	}

	return values
}()

// InvalidFieldTypeError describes a rejected field_type enum value.
type InvalidFieldTypeError struct {
	Value string
}

// Error implements the error interface.
func (e *InvalidFieldTypeError) Error() string {
	if e == nil {
		return ErrInvalidFieldType.Error()
	}

	return fmt.Sprintf("%s: %s", ErrInvalidFieldType, e.Value)
}

// Unwrap allows errors.Is(err, ErrInvalidFieldType).
func (e *InvalidFieldTypeError) Unwrap() error {
	return ErrInvalidFieldType
}

// ValidFieldTypeValues returns valid field_type values in API documentation order.
func ValidFieldTypeValues() []string {
	return append([]string(nil), validFieldTypeValueNames...)
}

// ValidFieldTypeValuesString returns a comma-separated list of valid field_type values.
func ValidFieldTypeValuesString() string {
	return validFieldTypeValuesString
}

// IsValid returns true if the FieldType is valid.
func (ft *FieldType) IsValid() bool {
	if ft == nil {
		return false
	}

	_, valid := ValidFieldTypes[*ft]

	return valid
}

// ParseFieldType parses a string to FieldType, returns error if invalid.
func ParseFieldType(s string) (FieldType, error) {
	ft := FieldType(s)
	if _, valid := ValidFieldTypes[ft]; !valid {
		return "", &InvalidFieldTypeError{Value: s}
	}

	return ft, nil
}

// UnmarshalJSON implements json.Unmarshaler to validate field type during JSON unmarshaling.
func (ft *FieldType) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("unmarshal field type: %w", err)
	}

	parsed, err := ParseFieldType(s)
	if err != nil {
		return err
	}

	*ft = parsed

	return nil
}

// parseEnumValues parses the repeated occurrences of one enum query parameter into deduplicated
// labels, preserving first-seen order.
//
// Empty values are skipped, so `?field_type=` keeps behaving as an omitted filter — which is what
// the single-value form has always done, and what a `dive` validate tag would have silently
// turned into a 400. Deduplicating here means a repeated label cannot grow the bound array, so a
// query string cannot inflate the IN-list past the enum's own cardinality.
//
// The nil it returns on "nothing parsed" is a TYPED nil slice, not a bare nil: the form decoder
// assigns a custom type func's result with reflect.Value.Set, which panics on the zero Value that
// reflect.ValueOf(nil) produces.
func parseEnumValues[T ~string](vals []string, parse func(string) (T, error)) ([]T, error) {
	parsed := make([]T, 0, len(vals))
	seen := make(map[T]struct{}, len(vals))

	for _, raw := range vals {
		if raw == "" {
			continue
		}

		value, err := parse(raw)
		if err != nil {
			return nil, err
		}

		if _, duplicate := seen[value]; duplicate {
			continue
		}

		seen[value] = struct{}{}
		parsed = append(parsed, value)
	}

	if len(parsed) == 0 {
		return nil, nil
	}

	return parsed, nil
}

// ParseFieldTypes parses repeated ?field_type= values into deduplicated field types. See
// parseEnumValues for the empty-value and dedupe semantics.
func ParseFieldTypes(vals []string) ([]FieldType, error) {
	return parseEnumValues(vals, ParseFieldType)
}

// SentimentValue is the discrete sentiment label produced by the sentiment-enrichment
// worker (ENG-1529). It is server-generated and persisted only after enrichment. Keep this
// set in sync with the feedback_records_sentiment_valid DB CHECK and the OpenAPI enum.
type SentimentValue string

// Valid SentimentValue labels: the five ordinal levels plus a distinct "mixed".
const (
	SentimentVeryNegative SentimentValue = "very_negative"
	SentimentNegative     SentimentValue = "negative"
	SentimentNeutral      SentimentValue = "neutral"
	SentimentPositive     SentimentValue = "positive"
	SentimentVeryPositive SentimentValue = "very_positive"
	SentimentMixed        SentimentValue = "mixed"
)

// Sentiment score bounds (inclusive): a signed polarity from -1 (very_negative) to +1
// (very_positive), with 0 for neutral/mixed — the conventional sentiment-polarity range (cf.
// Google Cloud NL, TextBlob, VADER). They match the feedback_records_sentiment_score_range DB
// CHECK and bound the score the sentiment worker persists (the classifier output is clamped).
const (
	SentimentScoreMin = -1.0
	SentimentScoreMax = 1.0
)

// sentimentValues lists every valid SentimentValue in ordinal order (very_negative ..
// very_positive) followed by the distinct "mixed". It is the single in-Go source of the label
// set: validSentimentValues (membership) and the sentiment structured-output enum both derive
// from it, so the order is stable. Keep it in sync with the feedback_records_sentiment_valid DB
// CHECK and the OpenAPI enum. Unexported so the canonical ordering cannot be mutated; expose it
// via SentimentValues().
var sentimentValues = []SentimentValue{
	SentimentVeryNegative,
	SentimentNegative,
	SentimentNeutral,
	SentimentPositive,
	SentimentVeryPositive,
	SentimentMixed,
}

// SentimentValues returns the valid SentimentValue labels in ordinal order. It returns a fresh
// copy each call so callers cannot mutate the canonical set out from under validSentimentValues
// / IsValid / the structured-output enum.
func SentimentValues() []SentimentValue {
	return slices.Clone(sentimentValues)
}

// validSentimentValues backs IsValid (set membership), derived from sentimentValues.
var validSentimentValues = func() map[SentimentValue]struct{} {
	set := make(map[SentimentValue]struct{}, len(sentimentValues))
	for _, value := range sentimentValues {
		set[value] = struct{}{}
	}

	return set
}()

// IsValid reports whether s is a known sentiment label. The sentiment worker validates
// with this before persisting; reads rely on the DB CHECK for the same guarantee.
func (s SentimentValue) IsValid() bool {
	_, ok := validSentimentValues[s]

	return ok
}

// validSentimentValuesString lists the labels for error messages, in the ordinal order of
// sentimentValues so the message reads very_negative .. very_positive, mixed.
var validSentimentValuesString = func() string {
	names := make([]string, 0, len(sentimentValues))
	for _, value := range sentimentValues {
		names = append(names, string(value))
	}

	return strings.Join(names, ", ")
}()

// ValidSentimentValuesString returns a comma-separated list of valid sentiment values.
func ValidSentimentValuesString() string {
	return validSentimentValuesString
}

// InvalidSentimentValueError describes a rejected sentiment enum value.
type InvalidSentimentValueError struct {
	Value string
}

// Error implements the error interface.
func (e *InvalidSentimentValueError) Error() string {
	if e == nil {
		return ErrInvalidSentimentValue.Error()
	}

	return fmt.Sprintf("%s: %s", ErrInvalidSentimentValue, e.Value)
}

// Unwrap allows errors.Is(err, ErrInvalidSentimentValue).
func (e *InvalidSentimentValueError) Unwrap() error {
	return ErrInvalidSentimentValue
}

// ParseSentimentValue parses a string to SentimentValue, returns error if invalid.
func ParseSentimentValue(s string) (SentimentValue, error) {
	value := SentimentValue(s)
	if !value.IsValid() {
		return "", &InvalidSentimentValueError{Value: s}
	}

	return value, nil
}

// ParseSentimentValues parses repeated ?sentiment= values into deduplicated labels. See
// parseEnumValues for the empty-value and dedupe semantics.
func ParseSentimentValues(vals []string) ([]SentimentValue, error) {
	return parseEnumValues(vals, ParseSentimentValue)
}

// EmotionValue is a single emotion label produced by the emotion-enrichment worker (ENG-1573).
// Emotions are multi-label — a record carries zero or more — server-generated and persisted only
// after enrichment. Keep this set in sync with the feedback_records_emotions_valid DB CHECK and
// the OpenAPI enum.
type EmotionValue string

// Valid EmotionValue labels: the six basic emotions (Ekman). "Mixed" is not a label — it is two
// or more of these present at once.
const (
	EmotionJoy      EmotionValue = "joy"
	EmotionAnger    EmotionValue = "anger"
	EmotionSadness  EmotionValue = "sadness"
	EmotionFear     EmotionValue = "fear"
	EmotionSurprise EmotionValue = "surprise"
	EmotionDisgust  EmotionValue = "disgust"
)

// emotionValues lists every valid EmotionValue. It is the single in-Go source of the label set:
// validEmotionValues (membership) and the emotions structured-output enum both derive from it, so
// the order is stable. Keep it in sync with the feedback_records_emotions_valid DB CHECK and the
// OpenAPI enum. Unexported so the canonical ordering cannot be mutated; expose it via
// EmotionValues().
var emotionValues = []EmotionValue{
	EmotionJoy,
	EmotionAnger,
	EmotionSadness,
	EmotionFear,
	EmotionSurprise,
	EmotionDisgust,
}

// EmotionValues returns the valid EmotionValue labels in canonical order. It returns a fresh copy
// each call so callers cannot mutate the canonical set out from under validEmotionValues / IsValid
// / the structured-output enum.
func EmotionValues() []EmotionValue {
	return slices.Clone(emotionValues)
}

// validEmotionValues backs IsValid (set membership), derived from emotionValues.
var validEmotionValues = func() map[EmotionValue]struct{} {
	set := make(map[EmotionValue]struct{}, len(emotionValues))
	for _, value := range emotionValues {
		set[value] = struct{}{}
	}

	return set
}()

// IsValid reports whether e is a known emotion label. The emotion worker validates with this
// before persisting; reads rely on the DB CHECK for the same guarantee.
func (e EmotionValue) IsValid() bool {
	_, ok := validEmotionValues[e]

	return ok
}

// validEmotionValuesString lists the labels for error messages, in the canonical order of
// emotionValues.
var validEmotionValuesString = func() string {
	names := make([]string, 0, len(emotionValues))
	for _, value := range emotionValues {
		names = append(names, string(value))
	}

	return strings.Join(names, ", ")
}()

// ValidEmotionValuesString returns a comma-separated list of valid emotion values.
func ValidEmotionValuesString() string {
	return validEmotionValuesString
}

// InvalidEmotionValueError describes a rejected emotion enum value.
type InvalidEmotionValueError struct {
	Value string
}

// Error implements the error interface.
func (e *InvalidEmotionValueError) Error() string {
	if e == nil {
		return ErrInvalidEmotionValue.Error()
	}

	return fmt.Sprintf("%s: %s", ErrInvalidEmotionValue, e.Value)
}

// Unwrap allows errors.Is(err, ErrInvalidEmotionValue).
func (e *InvalidEmotionValueError) Unwrap() error {
	return ErrInvalidEmotionValue
}

// ParseEmotionValue parses a string to EmotionValue, returns error if invalid.
func ParseEmotionValue(s string) (EmotionValue, error) {
	value := EmotionValue(s)
	if !value.IsValid() {
		return "", &InvalidEmotionValueError{Value: s}
	}

	return value, nil
}

// ParseEmotionValues parses repeated ?emotions= values into deduplicated labels. See
// parseEnumValues for the empty-value and dedupe semantics.
func ParseEmotionValues(vals []string) ([]EmotionValue, error) {
	return parseEnumValues(vals, ParseEmotionValue)
}

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

// FeedbackRecord represents a single feedback record.
type FeedbackRecord struct {
	ID              uuid.UUID `json:"id"`
	CollectedAt     time.Time `json:"collected_at"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	SourceType      string    `json:"source_type"`
	SourceID        *string   `json:"source_id,omitempty"`
	SourceName      *string   `json:"source_name,omitempty"`
	FieldID         string    `json:"field_id"`
	FieldLabel      *string   `json:"field_label,omitempty"`
	FieldType       FieldType `json:"field_type"`
	FieldGroupID    *string   `json:"field_group_id,omitempty"`
	FieldGroupLabel *string   `json:"field_group_label,omitempty"`
	ValueText       *string   `json:"value_text,omitempty"`
	// ValueID is the source system's stable id for a selected option (e.g. a survey choice id),
	// stored alongside ValueText's display label so selected-choice answers keep a durable identity
	// across label edits and languages. Opaque to Hub; nil for free-text/non-choice answers.
	ValueID      *string         `json:"value_id,omitempty"`
	ValueNumber  *float64        `json:"value_number,omitempty"`
	ValueBoolean *bool           `json:"value_boolean,omitempty"`
	ValueDate    *time.Time      `json:"value_date,omitempty"`
	Metadata     json.RawMessage `json:"metadata,omitempty"`
	Language     *string         `json:"language,omitempty"`
	UserID       *string         `json:"user_id,omitempty"`
	TenantID     string          `json:"tenant_id"`
	SubmissionID string          `json:"submission_id"` // mandatory; never null
	// Language-enrichment outputs (ENG-1255): server-generated, read-only. NULL until
	// the record is translated into the tenant's configured target language.
	ValueTextTranslated *string `json:"value_text_translated,omitempty"`
	TranslationLangKey  *string `json:"translation_lang_key,omitempty"`
	// Sentiment-enrichment outputs (ENG-1529): server-generated, read-only. NULL until
	// the record is enriched (or sentiment is disabled / the record is ineligible).
	Sentiment      *SentimentValue `json:"sentiment,omitempty"`
	SentimentScore *float64        `json:"sentiment_score,omitempty"`
	// Emotion-enrichment output (ENG-1573): server-generated, read-only, multi-label. NULL until
	// the record is enriched (or emotions is disabled / the record is ineligible / no emotion was
	// detected). Never an empty array — absence is NULL.
	Emotions *[]EmotionValue `json:"emotions,omitempty"`
}

// IsTextField reports whether this record is an open-text field — the eligibility gate the text
// enrichments (sentiment, translation, and emotions) share.
func (r *FeedbackRecord) IsTextField() bool {
	return r.FieldType == FieldTypeText
}

// HasOpenText reports whether this record carries non-empty open text to enrich (value_text is
// present and not just whitespace).
func (r *FeedbackRecord) HasOpenText() bool {
	return r.ValueText != nil && strings.TrimSpace(*r.ValueText) != ""
}

// SortValue returns the timestamp this record is ordered by under field, so a next-page cursor
// bounds the same column the keyset predicate will compare. It is the read-side counterpart of the
// repository's resolveListOrdering; the two must stay in step or a cursor silently bounds the
// wrong column. Unknown fields fall back to the default sort rather than a zero time, which would
// restart pagination from the beginning of the range.
func (r *FeedbackRecord) SortValue(field SortField) time.Time {
	switch field {
	case SortFieldCreatedAt:
		return r.CreatedAt
	case SortFieldCollectedAt:
		return r.CollectedAt
	default:
		return r.CollectedAt
	}
}

// CreateFeedbackRecordRequest represents the request to create a feedback record.
type CreateFeedbackRecordRequest struct {
	CollectedAt     *time.Time      `json:"collected_at,omitempty"`
	SourceType      string          `json:"source_type"                 validate:"required,no_null_bytes,min=1,max=255"`
	SourceID        *string         `json:"source_id,omitempty"         validate:"omitempty,no_null_bytes,max=255"`
	SourceName      *string         `json:"source_name,omitempty"       validate:"omitempty,no_null_bytes,max=255"`
	FieldID         string          `json:"field_id"                    validate:"required,no_null_bytes,min=1,max=255"`
	FieldLabel      *string         `json:"field_label,omitempty"       validate:"omitempty,no_null_bytes,max=2048"`
	FieldType       FieldType       `json:"field_type"                  validate:"required,field_type"`
	FieldGroupID    *string         `json:"field_group_id,omitempty"    validate:"omitempty,no_null_bytes,max=255"`
	FieldGroupLabel *string         `json:"field_group_label,omitempty" validate:"omitempty,no_null_bytes,max=2048"`
	ValueText       *string         `json:"value_text,omitempty"        validate:"omitempty,no_null_bytes,max=30000"`
	ValueID         *string         `json:"value_id,omitempty"          validate:"omitempty,no_null_bytes,max=255"`
	ValueNumber     *float64        `json:"value_number,omitempty"`
	ValueBoolean    *bool           `json:"value_boolean,omitempty"`
	ValueDate       *time.Time      `json:"value_date,omitempty"`
	Metadata        json.RawMessage `json:"metadata,omitempty"`
	Language        *string         `json:"language,omitempty"          validate:"omitempty,no_null_bytes,max=10"`
	UserID          *string         `json:"user_id,omitempty"           validate:"omitempty,no_null_bytes,max=255"`
	TenantID        string          `json:"tenant_id"                   validate:"required,no_null_bytes,max=255"`
	SubmissionID    string          `json:"submission_id"               validate:"required,no_null_bytes,min=1,max=255"`
}

// TranslationBackfillTarget is a feedback record that needs (re)translation to its
// tenant's currently-configured target language, returned by the backfill query.
type TranslationBackfillTarget struct {
	FeedbackRecordID uuid.UUID
	TargetLang       string
}

// UpdateFeedbackRecordRequest represents the request to update a feedback record
// Only value fields, metadata, language, and user_id can be updated.
type UpdateFeedbackRecordRequest struct {
	ValueText    *string         `json:"value_text,omitempty"    validate:"omitempty,no_null_bytes,max=30000"`
	ValueID      *string         `json:"value_id,omitempty"      validate:"omitempty,no_null_bytes,max=255"`
	ValueNumber  *float64        `json:"value_number,omitempty"`
	ValueBoolean *bool           `json:"value_boolean,omitempty"`
	ValueDate    *time.Time      `json:"value_date,omitempty"`
	Metadata     json.RawMessage `json:"metadata,omitempty"`
	Language     *string         `json:"language,omitempty"      validate:"omitempty,no_null_bytes,max=10"`
	UserID       *string         `json:"user_id,omitempty"       validate:"omitempty,no_null_bytes,max=255"`
}

// FieldsChangedFrom returns the names of fields that are set in the update request AND differ
// from old's current values. Unlike ChangedFields (presence only), this is comparison-based, so
// an idempotent re-send — an integration re-PATCHing the same value_text every sync — produces
// an empty result and fires no update event, instead of re-triggering webhooks and re-running
// every LLM enrichment on unchanged content.
func (r *UpdateFeedbackRecordRequest) FieldsChangedFrom(old *FeedbackRecord) []string {
	var fields []string

	if r.ValueText != nil && !stringPtrEqual(old.ValueText, r.ValueText) {
		fields = append(fields, "value_text")
	}

	if r.ValueID != nil && !stringPtrEqual(old.ValueID, r.ValueID) {
		fields = append(fields, "value_id")
	}

	if r.ValueNumber != nil && (old.ValueNumber == nil || *old.ValueNumber != *r.ValueNumber) {
		fields = append(fields, "value_number")
	}

	if r.ValueBoolean != nil && (old.ValueBoolean == nil || *old.ValueBoolean != *r.ValueBoolean) {
		fields = append(fields, "value_boolean")
	}

	if r.ValueDate != nil && (old.ValueDate == nil || !old.ValueDate.Equal(*r.ValueDate)) {
		fields = append(fields, "value_date")
	}

	// Byte-level comparison: a re-serialized metadata object (key order, whitespace) counts as
	// a change — conservative toward firing the event.
	if r.Metadata != nil && !bytes.Equal(old.Metadata, r.Metadata) {
		fields = append(fields, "metadata")
	}

	if r.Language != nil && !stringPtrEqual(old.Language, r.Language) {
		fields = append(fields, "language")
	}

	if r.UserID != nil && !stringPtrEqual(old.UserID, r.UserID) {
		fields = append(fields, "user_id")
	}

	return fields
}

// stringPtrEqual reports whether two optional strings hold the same value (nil equals only nil).
func stringPtrEqual(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}

	return *a == *b
}

// ChangedFields returns the names of fields that are set (non-nil) in the update request.
func (r *UpdateFeedbackRecordRequest) ChangedFields() []string {
	var fields []string
	if r.ValueText != nil {
		fields = append(fields, "value_text")
	}

	if r.ValueID != nil {
		fields = append(fields, "value_id")
	}

	if r.ValueNumber != nil {
		fields = append(fields, "value_number")
	}

	if r.ValueBoolean != nil {
		fields = append(fields, "value_boolean")
	}

	if r.ValueDate != nil {
		fields = append(fields, "value_date")
	}

	if r.Metadata != nil {
		fields = append(fields, "metadata")
	}

	if r.Language != nil {
		fields = append(fields, "language")
	}

	if r.UserID != nil {
		fields = append(fields, "user_id")
	}

	return fields
}

// MaxFilterValues caps how many values one repeatable filter may carry.
//
// go-playground/form imposes no bound of its own on a repeated query parameter — its maxArraySize
// only guards the indexed `x[0]=` form — so this tag is the only thing between a caller and an
// arbitrarily long IN-list. The remaining exposure is bounded by net/http's MaxHeaderBytes, since
// the values are materialized while decoding and rejected afterwards.
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
	SubmissionID []string    `form:"submission_id"  validate:"omitempty,max=100,dive,no_null_bytes"`
	SourceType   []string    `form:"source_type"    validate:"omitempty,max=100,dive,no_null_bytes,max=255"`
	SourceID     []string    `form:"source_id"      validate:"omitempty,max=100,dive,no_null_bytes,max=255"`
	SourceName   []string    `form:"source_name"    validate:"omitempty,max=100,dive,no_null_bytes,max=255"`
	FieldID      []string    `form:"field_id"       validate:"omitempty,max=100,dive,no_null_bytes,max=255"`
	FieldGroupID []string    `form:"field_group_id" validate:"omitempty,max=100,dive,no_null_bytes,max=255"`
	ValueID      []string    `form:"value_id"       validate:"omitempty,max=100,dive,no_null_bytes,max=255"`
	UserID       []string    `form:"user_id"        validate:"omitempty,max=100,dive,no_null_bytes,max=255"`
	Language     []string    `form:"language"       validate:"omitempty,max=100,dive,no_null_bytes,max=10"`
	FieldType    []FieldType `form:"field_type"     validate:"omitempty"`

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

	// Enrichment filters. The enum slices are parsed to valid labels at decode time and
	// deduplicated there, so they can hold neither an unknown value nor more entries than the
	// label set has members.
	Sentiment []SentimentValue `form:"sentiment" validate:"omitempty"`
	Emotions  []EmotionValue   `form:"emotions"  validate:"omitempty"`

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
	inverted := make([]InvertedRangeFilter, 0, 4) //nolint:mnd // capacity hint: the four pairs below

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

// ListFeedbackRecordsResponse represents the response for listing feedback records.
type ListFeedbackRecordsResponse struct {
	Data       []FeedbackRecord `json:"data"`
	Limit      int              `json:"limit"`
	NextCursor string           `json:"next_cursor,omitempty"` // present when there may be more results
}

// DeleteFeedbackRecordsByUserFilters represents query parameters for deleting feedback records by user.
type DeleteFeedbackRecordsByUserFilters struct {
	UserID   string  `form:"user_id"   validate:"required,no_null_bytes,min=1,max=255"`
	TenantID *string `form:"tenant_id" validate:"omitempty,no_null_bytes,min=1,max=255"`
}

// DeleteFeedbackRecordsByUserResponse represents the response for deleting feedback records by user.
type DeleteFeedbackRecordsByUserResponse struct {
	DeletedCount int64  `json:"deleted_count"`
	Message      string `json:"message"`
}

// CountFeedbackRecordsResponse represents the response for counting feedback records.
type CountFeedbackRecordsResponse struct {
	Count int64 `json:"count"`
}

// DeletedFeedbackRecordsByTenant groups deleted feedback record IDs by tenant.
type DeletedFeedbackRecordsByTenant struct {
	TenantID string
	IDs      []uuid.UUID
}
