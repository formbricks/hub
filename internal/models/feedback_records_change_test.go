package models

import (
	"slices"
	"testing"
)

// TestUpdateFeedbackRecordRequest_ChangedFields verifies presence-based change detection includes
// value_id when set and omits it when nil.
func TestUpdateFeedbackRecordRequest_ChangedFields(t *testing.T) {
	valueID := "opt_a"

	t.Run("value_id set is reported", func(t *testing.T) {
		got := (&UpdateFeedbackRecordRequest{ValueID: &valueID}).ChangedFields()
		if !slices.Contains(got, "value_id") {
			t.Fatalf("ChangedFields() = %v, want it to contain value_id", got)
		}
	})

	t.Run("value_id nil is omitted", func(t *testing.T) {
		text := "hi"

		got := (&UpdateFeedbackRecordRequest{ValueText: &text}).ChangedFields()
		if slices.Contains(got, "value_id") {
			t.Fatalf("ChangedFields() = %v, want it to omit value_id", got)
		}
	})
}

// TestUpdateFeedbackRecordRequest_FieldsChangedFrom verifies comparison-based change detection for
// value_id: a differing value is reported, an idempotent re-send of the same value is not.
func TestUpdateFeedbackRecordRequest_FieldsChangedFrom(t *testing.T) {
	oldID := "opt_a"
	newID := "opt_b"

	t.Run("changed value is reported", func(t *testing.T) {
		old := &FeedbackRecord{ValueID: &oldID}

		got := (&UpdateFeedbackRecordRequest{ValueID: &newID}).FieldsChangedFrom(old)
		if !slices.Contains(got, "value_id") {
			t.Fatalf("FieldsChangedFrom() = %v, want it to contain value_id", got)
		}
	})

	t.Run("unchanged value is not reported", func(t *testing.T) {
		old := &FeedbackRecord{ValueID: &oldID}
		sameID := "opt_a"

		got := (&UpdateFeedbackRecordRequest{ValueID: &sameID}).FieldsChangedFrom(old)
		if slices.Contains(got, "value_id") {
			t.Fatalf("FieldsChangedFrom() = %v, want it to omit value_id (idempotent re-send)", got)
		}
	})

	t.Run("newly set from nil is reported", func(t *testing.T) {
		old := &FeedbackRecord{}

		got := (&UpdateFeedbackRecordRequest{ValueID: &newID}).FieldsChangedFrom(old)
		if !slices.Contains(got, "value_id") {
			t.Fatalf("FieldsChangedFrom() = %v, want it to contain value_id", got)
		}
	})
}
