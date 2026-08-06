package models

import (
	"errors"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestParseEnumValues_Semantics locks the three behaviors the repeatable enum filters rely on:
// first-seen order is preserved, repeats collapse, and an empty value is a no-op rather than a
// rejected label. The last one is the reason these parse at decode time instead of via a `dive`
// validate tag — `?field_type=` has always meant "no field_type filter", and a dive tag would
// silently turn it into a 400.
func TestParseEnumValues_Semantics(t *testing.T) {
	t.Run("preserves first-seen order", func(t *testing.T) {
		got, err := ParseSentimentValues([]string{"positive", "very_negative", "neutral"})
		if err != nil {
			t.Fatalf("ParseSentimentValues() error = %v, want nil", err)
		}

		want := []SentimentValue{SentimentPositive, SentimentVeryNegative, SentimentNeutral}
		if !slices.Equal(got, want) {
			t.Fatalf("ParseSentimentValues() = %v, want %v", got, want)
		}
	})

	t.Run("deduplicates repeats", func(t *testing.T) {
		got, err := ParseEmotionValues([]string{"joy", "anger", "joy"})
		if err != nil {
			t.Fatalf("ParseEmotionValues() error = %v, want nil", err)
		}

		want := []EmotionValue{EmotionJoy, EmotionAnger}
		if !slices.Equal(got, want) {
			t.Fatalf("ParseEmotionValues() = %v, want %v (a repeated label must not grow the bound array)", got, want)
		}
	})

	t.Run("empty values yield no filter", func(t *testing.T) {
		got, err := ParseFieldTypes([]string{""})
		if err != nil {
			t.Fatalf("ParseFieldTypes() error = %v, want nil (?field_type= must stay a no-op filter)", err)
		}

		if got != nil {
			t.Fatalf("ParseFieldTypes() = %v, want nil", got)
		}
	})

	t.Run("no values yield no filter", func(t *testing.T) {
		got, err := ParseSentimentValues(nil)
		if err != nil || got != nil {
			t.Fatalf("ParseSentimentValues(nil) = %v, %v, want nil, nil", got, err)
		}
	})
}

// TestParseEnumValues_ReturnsTypedNil guards the decoder trap behind these helpers: the form
// decoder assigns a custom type func's result with reflect.Value.Set, and reflect.ValueOf(nil)
// yields the zero Value, which panics on Set. A bare `return nil, nil` in parseEnumValues would
// compile, pass every other test here, and then panic on the first `?sentiment=` request.
func TestParseEnumValues_ReturnsTypedNil(t *testing.T) {
	sentiments, err := ParseSentimentValues([]string{""})
	if err != nil {
		t.Fatalf("ParseSentimentValues() error = %v, want nil", err)
	}

	var boxed any = sentiments

	value := reflect.ValueOf(boxed)
	if !value.IsValid() {
		t.Fatal("reflect.ValueOf(result) is the zero Value; the decoder will panic on Set")
	}

	if value.Kind() != reflect.Slice {
		t.Fatalf("reflect.ValueOf(result).Kind() = %v, want %v", value.Kind(), reflect.Slice)
	}
}

// TestParseEnumValues_RejectsUnknown verifies each parser reports a typed error carrying the
// rejected value and unwrapping to its sentinel, which is what lets the query-decode layer name
// the offending parameter instead of falling back to a generic "is invalid".
func TestParseEnumValues_RejectsUnknown(t *testing.T) {
	tests := []struct {
		name     string
		parse    func([]string) error
		sentinel error
	}{
		{
			name: "sentiment",
			parse: func(v []string) error {
				_, err := ParseSentimentValues(v)

				return err
			},
			sentinel: ErrInvalidSentimentValue,
		},
		{
			name: "emotions",
			parse: func(v []string) error {
				_, err := ParseEmotionValues(v)

				return err
			},
			sentinel: ErrInvalidEmotionValue,
		},
		{
			name: "field_type",
			parse: func(v []string) error {
				_, err := ParseFieldTypes(v)

				return err
			},
			sentinel: ErrInvalidFieldType,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.parse([]string{"hostile"})
			if err == nil {
				t.Fatal("parse() error = nil, want an invalid-value error")
			}

			if !errors.Is(err, tt.sentinel) {
				t.Fatalf("parse() error = %v, want it to unwrap to %v", err, tt.sentinel)
			}
		})
	}
}

// TestParseEnumValues_RejectsUnknownAmongValid verifies a bad label is rejected even when it
// arrives after good ones — the parser must not stop at the first success.
func TestParseEnumValues_RejectsUnknownAmongValid(t *testing.T) {
	if _, err := ParseSentimentValues([]string{"positive", "hostile"}); err == nil {
		t.Fatal("ParseSentimentValues() error = nil, want the trailing invalid label to be rejected")
	}
}

// TestValidEnumValuesStrings pins the human-readable label lists used in 400 reasons, including
// their order: sentiment reads in ordinal order with mixed last, emotions in canonical order.
func TestValidEnumValuesStrings(t *testing.T) {
	wantSentiment := "very_negative, negative, neutral, positive, very_positive, mixed"
	if got := ValidSentimentValuesString(); got != wantSentiment {
		t.Fatalf("ValidSentimentValuesString() = %q, want %q", got, wantSentiment)
	}

	wantEmotion := "joy, anger, sadness, fear, surprise, disgust"
	if got := ValidEmotionValuesString(); got != wantEmotion {
		t.Fatalf("ValidEmotionValuesString() = %q, want %q", got, wantEmotion)
	}
}

// TestFeedbackRecord_SortValue verifies the cursor bounds the column the listing is sorted by.
// If this drifts from the repository's resolveListOrdering, a next-page cursor carries a
// created_at value that the keyset predicate compares against collected_at (or vice versa), which
// silently skips and repeats rows instead of failing.
func TestFeedbackRecord_SortValue(t *testing.T) {
	collected := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	created := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	record := &FeedbackRecord{CollectedAt: collected, CreatedAt: created}

	tests := []struct {
		name  string
		field SortField
		want  time.Time
	}{
		{name: "collected_at", field: SortFieldCollectedAt, want: collected},
		{name: "created_at", field: SortFieldCreatedAt, want: created},
		{name: "unknown falls back to the default sort", field: SortField("nonsense"), want: collected},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := record.SortValue(tt.field); !got.Equal(tt.want) {
				t.Fatalf("SortValue(%q) = %v, want %v", tt.field, got, tt.want)
			}
		})
	}
}

// TestDefaultSort pins the defaults to the ordering the endpoint had before sort was configurable.
// Changing either silently reorders every existing caller's first page.
func TestDefaultSort(t *testing.T) {
	if DefaultSortField != SortFieldCollectedAt || DefaultSortOrder != SortOrderDesc {
		t.Fatalf("defaults = (%q, %q), want (collected_at, desc)", DefaultSortField, DefaultSortOrder)
	}
}

// TestInvertedRanges_ReportsEachInvertedPair verifies every range pair is checked, and that the
// error is attributed to the lower bound so invalid_params names one correctable parameter.
func TestInvertedRanges_ReportsEachInvertedPair(t *testing.T) {
	filters := invertedFilters()

	got := make(map[string]string, len(filters.InvertedRanges()))
	for _, inverted := range filters.InvertedRanges() {
		got[inverted.MinParam] = inverted.MaxParam
	}

	want := map[string]string{
		"since":               "until",
		"created_since":       "created_until",
		"value_date_min":      "value_date_max",
		"value_number_min":    "value_number_max",
		"sentiment_score_min": "sentiment_score_max",
	}

	if len(got) != len(want) {
		t.Fatalf("InvertedRanges() reported %v, want %v", got, want)
	}

	for minParam, maxParam := range want {
		if got[minParam] != maxParam {
			t.Fatalf("InvertedRanges() paired %q with %q, want %q", minParam, got[minParam], maxParam)
		}
	}
}

// TestInvertedRanges_CoversEveryRangeFilter is the guard that stops the next range filter from
// silently skipping validation. It reflects over the struct's form tags rather than restating the
// list, so adding a *_min or *_since parameter without wiring it into InvertedRanges fails here
// instead of shipping a filter that returns a confusing empty page when inverted.
func TestInvertedRanges_CoversEveryRangeFilter(t *testing.T) {
	checked := make(map[string]bool)
	for _, inverted := range invertedFilters().InvertedRanges() {
		checked[inverted.MinParam] = true
	}

	for field := range reflect.TypeFor[ListFeedbackRecordsFilters]().Fields() {
		tag, _, _ := strings.Cut(field.Tag.Get("form"), ",")

		// Lower bounds are spelled either `<thing>_min` or `[<thing>_]since`.
		if !strings.HasSuffix(tag, "_min") && !strings.HasSuffix(tag, "since") {
			continue
		}

		if !checked[tag] {
			t.Fatalf("filter %q is a range lower bound but InvertedRanges() does not check it", tag)
		}
	}
}

// TestInvertedRanges_IgnoresValidAndPartialRanges verifies a well-ordered range, an equal pair
// (inclusive bounds, so it selects exactly the boundary), and a half-open range all pass.
func TestInvertedRanges_IgnoresValidAndPartialRanges(t *testing.T) {
	early := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	late := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	low, high := -0.5, 0.5

	tests := []struct {
		name    string
		filters ListFeedbackRecordsFilters
	}{
		{name: "empty", filters: ListFeedbackRecordsFilters{}},
		{name: "ordered", filters: ListFeedbackRecordsFilters{Since: &early, Until: &late}},
		{name: "equal bounds", filters: ListFeedbackRecordsFilters{Since: &early, Until: &early}},
		{name: "lower bound only", filters: ListFeedbackRecordsFilters{SentimentScoreMin: &high}},
		{name: "upper bound only", filters: ListFeedbackRecordsFilters{SentimentScoreMax: &low}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.filters.InvertedRanges(); got != nil {
				t.Fatalf("InvertedRanges() = %v, want nil", got)
			}
		})
	}
}

// invertedFilters returns a filter set in which every range pair is supplied backwards.
func invertedFilters() *ListFeedbackRecordsFilters {
	early := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	late := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	low, high := -0.5, 0.5

	return &ListFeedbackRecordsFilters{
		Since: &late, Until: &early,
		CreatedSince: &late, CreatedUntil: &early,
		ValueDateMin: &late, ValueDateMax: &early,
		ValueNumberMin: &high, ValueNumberMax: &low,
		SentimentScoreMin: &high, SentimentScoreMax: &low,
	}
}

// TestInvalidEnumValueErrors covers the message and nil-receiver behaviour of the two new error
// types, mirroring InvalidFieldTypeError. The message carries the rejected value for logs; the
// query-decode layer deliberately reports a canned reason instead, so the value never reaches the
// response body.
func TestInvalidEnumValueErrors(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		nilErr   error
		wantIn   string
		sentinel error
	}{
		{
			name:     "sentiment",
			err:      &InvalidSentimentValueError{Value: "hostile"},
			nilErr:   (*InvalidSentimentValueError)(nil),
			wantIn:   "hostile",
			sentinel: ErrInvalidSentimentValue,
		},
		{
			name:     "emotion",
			err:      &InvalidEmotionValueError{Value: "ennui"},
			nilErr:   (*InvalidEmotionValueError)(nil),
			wantIn:   "ennui",
			sentinel: ErrInvalidEmotionValue,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(tt.err.Error(), tt.wantIn) {
				t.Fatalf("Error() = %q, want it to carry the rejected value %q", tt.err.Error(), tt.wantIn)
			}

			if !errors.Is(tt.err, tt.sentinel) {
				t.Fatalf("errors.Is(%v, %v) = false, want true", tt.err, tt.sentinel)
			}

			// A nil typed error must not panic — Error() is reached through fmt verbs in logs.
			if got := tt.nilErr.Error(); got != tt.sentinel.Error() {
				t.Fatalf("nil receiver Error() = %q, want %q", got, tt.sentinel.Error())
			}
		})
	}
}

// TestFilterValueCapsMatchTheirSets ties every repeatable filter's `max=` tag to the thing it is
// supposed to mirror, because a struct tag cannot reference a constant and so drifts silently.
//
// The enum caps are the failure that motivated this: adding a seventh emotion would leave
// `max=6` in place, and a request legitimately asking for all seven would start returning 400 with
// no test going red. The string caps just pin MaxFilterValues as the single documented number.
func TestFilterValueCapsMatchTheirSets(t *testing.T) {
	enumCaps := map[string]int{
		"field_type": len(ValidFieldTypeValues()),
		"sentiment":  len(SentimentValues()),
		"emotions":   len(EmotionValues()),
	}

	seen := map[string]bool{}

	for field := range reflect.TypeFor[ListFeedbackRecordsFilters]().Fields() {
		if field.Type.Kind() != reflect.Slice {
			continue
		}

		name, _, _ := strings.Cut(field.Tag.Get("form"), ",")

		limit, ok := maxTagValue(t, field.Tag.Get("validate"))
		if !ok {
			t.Fatalf("repeatable filter %q has no max= cap; an unbounded IN-list is the one thing these tags exist to prevent", name)
		}

		seen[name] = true

		want := MaxFilterValues
		if enumCap, isEnum := enumCaps[name]; isEnum {
			want = enumCap
		}

		if limit != want {
			t.Fatalf("filter %q caps at %d, want %d", name, limit, want)
		}
	}

	for name := range enumCaps {
		if !seen[name] {
			t.Fatalf("enum filter %q is no longer a slice field; this test is checking nothing for it", name)
		}
	}
}

// maxTagValue returns the slice-level `max=N` from a validate tag — the portion before `dive`,
// since anything after it constrains elements rather than length.
func maxTagValue(t *testing.T, tag string) (int, bool) {
	t.Helper()

	sliceLevel, _, _ := strings.Cut(tag, ",dive")

	for part := range strings.SplitSeq(sliceLevel, ",") {
		raw, found := strings.CutPrefix(part, "max=")
		if !found {
			continue
		}

		value, err := strconv.Atoi(raw)
		if err != nil {
			t.Fatalf("unparseable max tag %q", part)
		}

		return value, true
	}

	return 0, false
}
