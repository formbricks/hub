package cursor

import (
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

var (
	testID = uuid.MustParse("018e1234-5678-9abc-def0-111111111111")
	testTS = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
)

// TestEncode_LegacyWireFormat is the compatibility lock for the shared cursor package.
//
// Encode is still used by webhooks, which have one fixed ordering and record no sort. Its output
// must stay byte-identical to the pre-sort-control format, which requires that S and O are
// omitempty AND declared after T and I in listCursorPayload. Reordering the struct fields would
// change the JSON key order, change the base64, and silently invalidate every cursor a client is
// currently holding — with no compile error and no other failing test.
func TestEncode_LegacyWireFormat(t *testing.T) {
	want := base64.URLEncoding.EncodeToString(
		[]byte(`{"t":"2026-01-01T00:00:00Z","i":"018e1234-5678-9abc-def0-111111111111"}`),
	)

	got, err := Encode(testTS, testID)
	if err != nil {
		t.Fatalf("Encode() error = %v, want nil", err)
	}

	if got != want {
		t.Fatalf("Encode() = %q, want %q (the legacy wire format must not change)", got, want)
	}
}

// TestEncodeKey_OmitsUnspecifiedOrdering verifies a Key carrying no ordering encodes exactly like
// the legacy two-field cursor, so the sort-aware and fixed-order paths share one wire format.
func TestEncodeKey_OmitsUnspecifiedOrdering(t *testing.T) {
	fromKey, err := EncodeKey(Key{Timestamp: testTS, ID: testID})
	if err != nil {
		t.Fatalf("EncodeKey() error = %v, want nil", err)
	}

	fromLegacy, err := Encode(testTS, testID)
	if err != nil {
		t.Fatalf("Encode() error = %v, want nil", err)
	}

	if fromKey != fromLegacy {
		t.Fatalf("EncodeKey() = %q, Encode() = %q, want identical", fromKey, fromLegacy)
	}
}

// TestEncodeDecodeKey_RoundTrip covers both directions of a rolling deploy: a sort-aware cursor
// round-trips through the new API, and a legacy cursor decodes with an unspecified ordering.
func TestEncodeDecodeKey_RoundTrip(t *testing.T) {
	t.Run("with recorded ordering", func(t *testing.T) {
		want := Key{Timestamp: testTS, ID: testID, Sort: "created_at", Order: "asc"}

		encoded, err := EncodeKey(want)
		if err != nil {
			t.Fatalf("EncodeKey() error = %v, want nil", err)
		}

		got, err := DecodeKey(encoded)
		if err != nil {
			t.Fatalf("DecodeKey() error = %v, want nil", err)
		}

		if !got.Timestamp.Equal(want.Timestamp) || got.ID != want.ID ||
			got.Sort != want.Sort || got.Order != want.Order {
			t.Fatalf("DecodeKey() = %+v, want %+v", got, want)
		}
	})

	t.Run("legacy cursor decodes with unspecified ordering", func(t *testing.T) {
		encoded, err := Encode(testTS, testID)
		if err != nil {
			t.Fatalf("Encode() error = %v, want nil", err)
		}

		got, err := DecodeKey(encoded)
		if err != nil {
			t.Fatalf("DecodeKey() error = %v, want nil", err)
		}

		if got.Sort != "" || got.Order != "" {
			t.Fatalf("DecodeKey() ordering = (%q, %q), want both empty", got.Sort, got.Order)
		}
	})

	t.Run("legacy Decode ignores a recorded ordering", func(t *testing.T) {
		encoded, err := EncodeKey(Key{Timestamp: testTS, ID: testID, Sort: "created_at", Order: "asc"})
		if err != nil {
			t.Fatalf("EncodeKey() error = %v, want nil", err)
		}

		// The old-pod direction of a rolling deploy: a pod that predates sort control must still
		// read the position out of a sort-aware cursor rather than rejecting it.
		timestamp, id, err := Decode(encoded)
		if err != nil {
			t.Fatalf("Decode() error = %v, want nil", err)
		}

		if !timestamp.Equal(testTS) || id != testID {
			t.Fatalf("Decode() = (%v, %v), want (%v, %v)", timestamp, id, testTS, testID)
		}
	})
}

// TestKey_Match verifies the guard that stops a client from carrying a cursor across a change of
// ordering, and that a legacy cursor stays usable under any ordering.
func TestKey_Match(t *testing.T) {
	tests := []struct {
		name    string
		key     Key
		sort    string
		order   string
		wantErr bool
	}{
		{
			name:  "unspecified ordering matches anything",
			key:   Key{Timestamp: testTS, ID: testID},
			sort:  "created_at",
			order: "asc",
		},
		{
			name:  "same ordering matches",
			key:   Key{Timestamp: testTS, ID: testID, Sort: "collected_at", Order: "desc"},
			sort:  "collected_at",
			order: "desc",
		},
		{
			name:    "different sort is refused",
			key:     Key{Timestamp: testTS, ID: testID, Sort: "collected_at", Order: "desc"},
			sort:    "created_at",
			order:   "desc",
			wantErr: true,
		},
		{
			name:    "different order is refused",
			key:     Key{Timestamp: testTS, ID: testID, Sort: "collected_at", Order: "desc"},
			sort:    "collected_at",
			order:   "asc",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.key.Match(tt.sort, tt.order)
			if tt.wantErr {
				if !errors.Is(err, ErrCursorSortMismatch) {
					t.Fatalf("Match() error = %v, want ErrCursorSortMismatch", err)
				}

				return
			}

			if err != nil {
				t.Fatalf("Match() error = %v, want nil", err)
			}
		})
	}
}

// TestErrCursorSortMismatch_IsNotInvalidCursor pins the sentinel separation.
//
// The response layer maps ErrInvalidCursor to "the cursor is malformed, start over" and
// ErrCursorSortMismatch to "keep sort and order unchanged". If a future refactor makes the latter
// wrap the former, the errors.Is check for ErrInvalidCursor starts catching mismatches and clients
// get advice that does not fix their problem.
func TestErrCursorSortMismatch_IsNotInvalidCursor(t *testing.T) {
	if errors.Is(ErrCursorSortMismatch, ErrInvalidCursor) {
		t.Fatal("ErrCursorSortMismatch must not wrap ErrInvalidCursor")
	}

	if errors.Is(ErrInvalidCursor, ErrCursorSortMismatch) {
		t.Fatal("ErrInvalidCursor must not wrap ErrCursorSortMismatch")
	}
}

// TestDecodeKey_MalformedCollapsesToInvalidCursor verifies every failure mode still reports the
// same opaque sentinel, so a malformed cursor never leaks the reason it failed to parse.
func TestDecodeKey_MalformedCollapsesToInvalidCursor(t *testing.T) {
	validJSON := `{"t":"2026-01-01T00:00:00Z","i":"018e1234-5678-9abc-def0-111111111111"}`

	tests := []struct {
		name   string
		cursor string
	}{
		{name: "empty", cursor: ""},
		{name: "bad base64", cursor: "!!!not-base64!!!"},
		{name: "bad json", cursor: base64.URLEncoding.EncodeToString([]byte(`{nope`))},
		{
			name:   "unparseable timestamp",
			cursor: base64.URLEncoding.EncodeToString([]byte(`{"t":"yesterday","i":"018e1234-5678-9abc-def0-111111111111"}`)),
		},
		{
			name:   "unparseable uuid",
			cursor: base64.URLEncoding.EncodeToString([]byte(`{"t":"2026-01-01T00:00:00Z","i":"not-a-uuid"}`)),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := DecodeKey(tt.cursor); !errors.Is(err, ErrInvalidCursor) {
				t.Fatalf("DecodeKey(%q) error = %v, want ErrInvalidCursor", tt.name, err)
			}
		})
	}

	// Sanity check that the fixture above is otherwise well-formed, so the subtests are proving
	// the specific defect they name rather than a typo shared by all of them.
	if _, err := DecodeKey(base64.URLEncoding.EncodeToString([]byte(validJSON))); err != nil {
		t.Fatalf("DecodeKey(valid) error = %v, want nil", err)
	}
}
