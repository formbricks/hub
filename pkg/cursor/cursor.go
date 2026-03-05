// Package cursor provides encode/decode for keyset pagination cursors used by list endpoints.
// List endpoints (feedback records, webhooks) use (time.Time, uuid.UUID) as the keyset;
// search endpoints use a different format (see internal/service/search_cursor.go).
package cursor

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ErrInvalidCursor is returned when the cursor parameter is malformed or invalid.
var ErrInvalidCursor = errors.New("invalid cursor")

type listCursorPayload struct {
	T string `json:"t"` // RFC3339 timestamp (collected_at or created_at)
	I string `json:"i"` // entity ID (UUID string)
}

// Encode encodes a list cursor from the last row's timestamp and ID.
// Used for keyset pagination on ORDER BY timestamp DESC, id ASC.
func Encode(ts time.Time, id uuid.UUID) (string, error) {
	b, err := json.Marshal(listCursorPayload{T: ts.UTC().Format(time.RFC3339Nano), I: id.String()})
	if err != nil {
		return "", fmt.Errorf("encode list cursor: %w", err)
	}

	return base64.URLEncoding.EncodeToString(b), nil
}

// Decode parses a list cursor and returns (timestamp, id).
// Returns ErrInvalidCursor if the cursor is malformed.
func Decode(cursor string) (time.Time, uuid.UUID, error) {
	if cursor == "" {
		return time.Time{}, uuid.Nil, ErrInvalidCursor
	}

	raw, err := base64.URLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, uuid.Nil, ErrInvalidCursor
	}

	var p listCursorPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return time.Time{}, uuid.Nil, ErrInvalidCursor
	}

	timestamp, err := time.Parse(time.RFC3339Nano, p.T)
	if err != nil {
		return time.Time{}, uuid.Nil, ErrInvalidCursor
	}

	id, err := uuid.Parse(p.I)
	if err != nil {
		return time.Time{}, uuid.Nil, ErrInvalidCursor
	}

	return timestamp, id, nil
}
