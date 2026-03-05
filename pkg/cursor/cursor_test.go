package cursor

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestEncode_Decode_RoundTrip(t *testing.T) {
	timestamp := time.Date(2025, 3, 5, 12, 0, 0, 123456789, time.UTC)
	id := uuid.MustParse("01234567-89ab-cdef-0123-456789abcdef")

	encoded, err := Encode(timestamp, id)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	if encoded == "" {
		t.Fatal("Encode returned empty string")
	}

	decodedTs, decodedID, err := Decode(encoded)
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}

	if !decodedTs.Equal(timestamp) {
		t.Errorf("Decode timestamp: got %v, want %v", decodedTs, timestamp)
	}

	if decodedID != id {
		t.Errorf("Decode id: got %v, want %v", decodedID, id)
	}
}

func TestEncode_ProducesURLSafeBase64(t *testing.T) {
	timestamp := time.Now().UTC()
	id := uuid.New()

	encoded, err := Encode(timestamp, id)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	// base64.URLEncoding should not produce + or / (uses - and _)
	for _, r := range encoded {
		if r == '+' || r == '/' {
			t.Errorf("Encode produced non-URL-safe base64 character: %q", r)
		}
	}

	// Should be decodable with base64.URLEncoding
	_, err = base64.URLEncoding.DecodeString(encoded)
	if err != nil {
		t.Errorf("Encoded output not valid URL-safe base64: %v", err)
	}
}

func TestDecode_EmptyString(t *testing.T) {
	_, _, err := Decode("")
	if !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("Decode empty string: want ErrInvalidCursor, got %v", err)
	}
}

func TestDecode_InvalidBase64(t *testing.T) {
	_, _, err := Decode("!!!not-valid-base64!!!")
	if !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("Decode invalid base64: want ErrInvalidCursor, got %v", err)
	}
}

func TestDecode_InvalidJSON(t *testing.T) {
	raw := base64.URLEncoding.EncodeToString([]byte("not valid json"))

	_, _, err := Decode(raw)
	if !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("Decode invalid JSON: want ErrInvalidCursor, got %v", err)
	}
}

func TestDecode_InvalidTimestamp(t *testing.T) {
	payload := map[string]string{"t": "not-rfc3339", "i": "01234567-89ab-cdef-0123-456789abcdef"}

	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal payload: %v", err)
	}

	raw := base64.URLEncoding.EncodeToString(b)

	_, _, err = Decode(raw)
	if !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("Decode invalid timestamp: want ErrInvalidCursor, got %v", err)
	}
}

func TestDecode_InvalidUUID(t *testing.T) {
	payload := map[string]string{"t": time.Now().UTC().Format(time.RFC3339Nano), "i": "not-a-uuid"}

	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal payload: %v", err)
	}

	raw := base64.URLEncoding.EncodeToString(b)

	_, _, err = Decode(raw)
	if !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("Decode invalid UUID: want ErrInvalidCursor, got %v", err)
	}
}

func TestDecode_MissingFields(t *testing.T) {
	payload := map[string]string{"t": time.Now().UTC().Format(time.RFC3339Nano)}

	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal payload: %v", err)
	}

	raw := base64.URLEncoding.EncodeToString(b)

	_, _, err = Decode(raw)
	if !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("Decode missing 'i' field: want ErrInvalidCursor, got %v", err)
	}
}
