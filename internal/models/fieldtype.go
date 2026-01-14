package models

// FieldType represents the standardized data types for feedback record fields.
// These types are optimized for analytics and map to specific use cases.
type FieldType string

const (
	// FieldTypeText represents open-ended qualitative feedback.
	// AI enrichment (sentiment, emotion, topics) is automatically applied.
	// Value stored in: value_text
	FieldTypeText FieldType = "text"

	// FieldTypeCategorical represents pre-defined discrete options.
	// Used for single or multiple choice questions.
	// Value stored in: value_text (one row per selection)
	FieldTypeCategorical FieldType = "categorical"

	// FieldTypeNPS represents Net Promoter Score (0-10 scale).
	// Used for "How likely are you to recommend?" questions.
	// Value stored in: value_number
	FieldTypeNPS FieldType = "nps"

	// FieldTypeCSAT represents Customer Satisfaction (typically 1-5 or 1-7 scale).
	// Used for "How satisfied are you?" questions.
	// Value stored in: value_number
	FieldTypeCSAT FieldType = "csat"

	// FieldTypeCES represents Customer Effort Score (typically 1-7 scale).
	// Used for "How easy was it to...?" questions.
	// Value stored in: value_number
	FieldTypeCES FieldType = "ces"

	// FieldTypeRating represents generic rating scales (e.g., star ratings).
	// Used for ordinal scales like 1-5 stars or 1-10 scales.
	// Value stored in: value_number
	FieldTypeRating FieldType = "rating"

	// FieldTypeNumber represents quantitative continuous measurements.
	// Used for counts, amounts, durations, measurements.
	// Value stored in: value_number
	FieldTypeNumber FieldType = "number"

	// FieldTypeBoolean represents binary yes/no responses.
	// Used for true/false, yes/no, on/off questions.
	// Value stored in: value_boolean
	FieldTypeBoolean FieldType = "boolean"

	// FieldTypeDate represents temporal date/datetime values.
	// Used for date/datetime responses and timestamps.
	// Value stored in: value_date
	FieldTypeDate FieldType = "date"
)

// IsValid checks if the FieldType is one of the valid standardized types.
func (f FieldType) IsValid() bool {
	switch f {
	case FieldTypeText, FieldTypeCategorical, FieldTypeNPS,
		FieldTypeCSAT, FieldTypeCES, FieldTypeRating, FieldTypeNumber,
		FieldTypeBoolean, FieldTypeDate:
		return true
	}
	return false
}

// ShouldEnrich returns true if this field type should undergo AI enrichment.
// Currently, only text (open-ended) responses are enriched with sentiment,
// emotion, and topic analysis.
func (f FieldType) ShouldEnrich() bool {
	return f == FieldTypeText
}

// String returns the string representation of the FieldType.
func (f FieldType) String() string {
	return string(f)
}

// AllFieldTypes returns a slice of all valid field types.
func AllFieldTypes() []FieldType {
	return []FieldType{
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
}
