package models

import (
	"encoding/json"
	"testing"
)

// TestOptionalUnmarshalJSONTriState pins the three RFC 7396 states Optional must
// distinguish when decoded as a struct member.
func TestOptionalUnmarshalJSONTriState(t *testing.T) {
	type doc struct {
		Field Optional[string] `json:"field"`
	}

	t.Run("absent member stays not-present", func(t *testing.T) {
		var decoded doc
		if err := json.Unmarshal([]byte(`{}`), &decoded); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}

		if decoded.Field.Present || decoded.Field.Value != nil {
			t.Fatalf("absent: Present=%v Value=%v, want false/nil", decoded.Field.Present, decoded.Field.Value)
		}
	})

	t.Run("explicit null is present with nil value", func(t *testing.T) {
		var decoded doc
		if err := json.Unmarshal([]byte(`{"field":null}`), &decoded); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}

		if !decoded.Field.Present || decoded.Field.Value != nil {
			t.Fatalf("null: Present=%v Value=%v, want true/nil", decoded.Field.Present, decoded.Field.Value)
		}
	})

	t.Run("value is present with the decoded value", func(t *testing.T) {
		var decoded doc
		if err := json.Unmarshal([]byte(`{"field":"de-DE"}`), &decoded); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}

		if !decoded.Field.Present || decoded.Field.Value == nil || *decoded.Field.Value != "de-DE" {
			t.Fatalf("value: Present=%v Value=%v, want true/de-DE", decoded.Field.Present, decoded.Field.Value)
		}
	})

	t.Run("wrong type errors", func(t *testing.T) {
		var decoded doc
		if err := json.Unmarshal([]byte(`{"field":123}`), &decoded); err == nil {
			t.Fatal("expected an error decoding a number into Optional[string]")
		}
	})
}
