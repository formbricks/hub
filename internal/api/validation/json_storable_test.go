package validation

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/models"
)

func TestIsStorableJSON(t *testing.T) {
	// Every rejected case here was confirmed against Postgres 18 by casting the same text to
	// jsonb; every accepted case casts cleanly.
	tests := []struct {
		name     string
		raw      string
		storable bool
	}{
		{name: "empty", raw: "", storable: true},
		{name: "plain object", raw: `{"source":"link","country":"PT"}`, storable: true},
		{name: "nested and arrays", raw: `{"a":{"b":["c",1,true,null]}}`, storable: true},
		{name: "non-object scalar", raw: `42`, storable: true},

		{name: "null byte in a value", raw: `{"note":"bad\u0000value"}`, storable: false},
		{name: "null byte in a key", raw: `{"ba\u0000d":"v"}`, storable: false},
		{name: "null byte in a nested object key", raw: `{"a":{"b\u0000c":1}}`, storable: false},
		{name: "null byte nested in an array", raw: `{"tags":["ok","b\u0000d"]}`, storable: false},

		{name: "lone high surrogate", raw: `{"note":"bad\ud83dvalue"}`, storable: false},
		{name: "lone low surrogate", raw: `{"note":"bad\ude00value"}`, storable: false},
		{name: "high surrogate at end of input", raw: `{"note":"bad\ud83d"}`, storable: false},
		{name: "high surrogate followed by a plain character", raw: `{"note":"\ud83dA"}`, storable: false},
		{name: "valid surrogate pair", raw: `{"note":"ok\ud83d\ude00"}`, storable: true},
		{name: "valid pair uppercase hex", raw: `{"note":"ok\uD83D\uDE00"}`, storable: true},
		{name: "two valid pairs back to back", raw: `{"note":"\ud83d\ude00\ud83d\ude01"}`, storable: true},

		// An escaped backslash is not the start of an escape. Without consuming its second byte the
		// scanner would read the following "u0000" as a NUL escape and reject a legitimate value.
		{name: "escaped backslash before u0000 text", raw: `{"note":"path\\u0000notanescape"}`, storable: true},
		{name: "escaped backslash then a real null escape", raw: `{"note":"a\\\u0000"}`, storable: false},
		{name: "other escapes are ignored", raw: `{"note":"line\nquote\"tab\t"}`, storable: true},

		{name: "malformed short escape", raw: `{"note":"\u00"}`, storable: false},
		{name: "malformed non-hex escape", raw: `{"note":"\uZZZZ"}`, storable: false},
		{name: "trailing backslash", raw: `{"note":"a\`, storable: false},

		// Emoji as real UTF-8 rather than escapes: valid, and the common case for survey text.
		{name: "literal multi-byte utf8", raw: `{"note":"feedback 😀"}`, storable: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.storable, IsStorableJSON([]byte(tt.raw)))
		})
	}
}

func TestIsStorableJSONRejectsInvalidUTF8(t *testing.T) {
	// A surrogate encoded directly as CESU-8 rather than as a \u escape. Postgres rejects it as an
	// encoding error, so the scan has to catch it before the escape walk.
	raw := []byte(`{"note":"`)
	raw = append(raw, 0xed, 0xa0, 0xbd) // U+D83D encoded directly, which UTF-8 forbids
	raw = append(raw, `"}`...)

	assert.False(t, IsStorableJSON(raw))
}

// The tag is what actually protects the endpoint, so drive it through ValidateStruct rather than
// calling the predicate directly.
func TestStorableJSONTagOnRequestStruct(t *testing.T) {
	type request struct {
		Metadata json.RawMessage `json:"metadata,omitempty" validate:"omitempty,storable_json"`
	}

	t.Run("accepts storable metadata", func(t *testing.T) {
		require.NoError(t, ValidateStruct(request{Metadata: json.RawMessage(`{"source":"link"}`)}))
	})

	t.Run("accepts an absent metadata field", func(t *testing.T) {
		require.NoError(t, ValidateStruct(request{}))
	})

	t.Run("rejects a null byte with a self-correcting reason", func(t *testing.T) {
		err := ValidateStruct(request{Metadata: json.RawMessage(`{"note":"bad\u0000value"}`)})

		require.Error(t, err)
		require.ErrorIs(t, err, ErrValidationFailed)
		assert.Contains(t, err.Error(), "metadata must contain valid UTF-8 and no NULL bytes or unpaired UTF-16 surrogates")
	})

	t.Run("rejects an unpaired surrogate", func(t *testing.T) {
		err := ValidateStruct(request{Metadata: json.RawMessage(`{"note":"bad\ud83dvalue"}`)})

		require.Error(t, err)
		require.ErrorIs(t, err, ErrValidationFailed)
	})
}

// The tag is deliberately inert on non-raw-JSON fields, mirroring no_null_bytes, so misapplying it
// cannot start rejecting unrelated input.
func TestStorableJSONTagIgnoresNonRawJSONFields(t *testing.T) {
	type request struct {
		Name string `json:"name" validate:"omitempty,storable_json"`
	}

	require.NoError(t, ValidateStruct(request{Name: "anything at all"}))
}

// The tests above pin the tag's behavior on a local struct; these pin that the tag is actually
// PRESENT on the two request structs the endpoint decodes into. Reverting only the models hunk of
// the ENG-2745 fix — the part that protects the endpoint — left the whole suite green before this.
func TestRealRequestStructsCarryStorableJSONTag(t *testing.T) {
	badMetadata := json.RawMessage(`{"note":"bad\u0000value"}`)

	t.Run("CreateFeedbackRecordRequest rejects unstorable metadata", func(t *testing.T) {
		err := ValidateStruct(models.CreateFeedbackRecordRequest{
			SourceType:   "survey",
			FieldID:      "q1",
			FieldType:    models.FieldTypeText,
			TenantID:     "tenant-1",
			SubmissionID: "s1",
			Metadata:     badMetadata,
		})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "metadata must contain valid UTF-8 and no NULL bytes or unpaired UTF-16 surrogates")
	})

	t.Run("UpdateFeedbackRecordRequest rejects unstorable metadata", func(t *testing.T) {
		err := ValidateStruct(models.UpdateFeedbackRecordRequest{Metadata: badMetadata})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "metadata must contain valid UTF-8 and no NULL bytes or unpaired UTF-16 surrogates")
	})

	t.Run("both accept storable metadata", func(t *testing.T) {
		fine := json.RawMessage(`{"source":"link"}`)
		require.NoError(t, ValidateStruct(models.CreateFeedbackRecordRequest{
			SourceType:   "survey",
			FieldID:      "q1",
			FieldType:    models.FieldTypeText,
			TenantID:     "tenant-1",
			SubmissionID: "s1",
			Metadata:     fine,
		}))
		require.NoError(t, ValidateStruct(models.UpdateFeedbackRecordRequest{Metadata: fine}))
	})
}
