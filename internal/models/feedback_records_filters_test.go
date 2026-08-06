package models

import (
	"errors"
	"reflect"
	"slices"
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
