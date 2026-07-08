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

// ErrInvalidFieldType is returned when a field type string is invalid (err113).
var ErrInvalidFieldType = errors.New("invalid field type")

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

// ListFeedbackRecordsFilters represents filters for listing feedback records.
type ListFeedbackRecordsFilters struct {
	TenantID     *string    `form:"tenant_id"      validate:"required,no_null_bytes,min=1"`
	SubmissionID *string    `form:"submission_id"  validate:"omitempty,no_null_bytes"`
	SourceType   *string    `form:"source_type"    validate:"omitempty,no_null_bytes"`
	SourceID     *string    `form:"source_id"      validate:"omitempty,no_null_bytes"`
	FieldID      *string    `form:"field_id"       validate:"omitempty,no_null_bytes"`
	FieldGroupID *string    `form:"field_group_id" validate:"omitempty,no_null_bytes"`
	FieldType    *FieldType `form:"field_type"     validate:"omitempty,field_type"`
	ValueID      *string    `form:"value_id"       validate:"omitempty,no_null_bytes"`
	UserID       *string    `form:"user_id"        validate:"omitempty,no_null_bytes"`
	Since        *time.Time `form:"since"          validate:"omitempty"`
	Until        *time.Time `form:"until"          validate:"omitempty"`
	Limit        int        `form:"limit"          validate:"omitempty,min=1,max=1000"`
	Cursor       string     `form:"cursor"         validate:"omitempty"` // keyset; omit for first page, use next_cursor for next
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

// DeletedFeedbackRecordsByTenant groups deleted feedback record IDs by tenant.
type DeletedFeedbackRecordsByTenant struct {
	TenantID string
	IDs      []uuid.UUID
}
