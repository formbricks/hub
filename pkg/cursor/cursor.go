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

// ErrCursorSortMismatch is returned when a cursor was issued for a different (sort, order) than
// the request presenting it.
//
// It is deliberately a standalone sentinel rather than a wrapper around ErrInvalidCursor: the
// response layer tests errors.Is(err, ErrInvalidCursor) first, so wrapping would make a mismatch
// render as "malformed cursor" and hand the client the wrong recovery advice.
var ErrCursorSortMismatch = errors.New("cursor does not match the requested sort order")

// Key is the position of the last row of a page, plus the ordering that produced it.
//
// Sort and Order are empty for cursors issued before sort control existed and for endpoints with
// a single fixed ordering (webhooks). Empty means "unspecified": Match accepts it against any
// ordering, which is what keeps already-issued cursors working across a deploy.
type Key struct {
	Timestamp time.Time
	ID        uuid.UUID
	Sort      string
	Order     string
}

// listCursorPayload is the wire form of a cursor.
//
// It carries a POSITION and nothing else. Never add tenancy, filters, or any other authorization
// input: the cursor is unsigned base64 JSON that a client can trivially forge, and it is safe
// today only because forging one lets a caller start from an arbitrary offset inside a query that
// is already scoped by the request. Encoding tenant_id here to save re-passing it would turn this
// blob into a cross-tenant read primitive.
//
// S and O are omitempty and declared after T and I, so a Key with no recorded ordering marshals
// byte-identically to the pre-sort-control format. TestEncode_LegacyWireFormat pins that.
type listCursorPayload struct {
	T string `json:"t"`           // RFC3339 value of the column the listing is sorted by
	I string `json:"i"`           // entity ID (UUID string)
	S string `json:"s,omitempty"` // sort column it was issued for ("" = unspecified/legacy)
	O string `json:"o,omitempty"` // sort direction it was issued for ("" = unspecified/legacy)
}

// EncodeKey encodes a keyset cursor, embedding the ordering when the caller records one.
func EncodeKey(key Key) (string, error) {
	payload := listCursorPayload{
		T: key.Timestamp.UTC().Format(time.RFC3339Nano),
		I: key.ID.String(),
		S: key.Sort,
		O: key.Order,
	}

	b, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode list cursor: %w", err)
	}

	return base64.URLEncoding.EncodeToString(b), nil
}

// DecodeKey parses a keyset cursor. Every malformed input — empty, bad base64, bad JSON,
// unparseable timestamp, unparseable UUID — collapses to ErrInvalidCursor.
//
// It does NOT check the ordering against the request; callers do that with Key.Match, so a
// malformed cursor and a mismatched one stay distinguishable in the response.
func DecodeKey(cursor string) (Key, error) {
	if cursor == "" {
		return Key{}, ErrInvalidCursor
	}

	raw, err := base64.URLEncoding.DecodeString(cursor)
	if err != nil {
		return Key{}, ErrInvalidCursor
	}

	var p listCursorPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return Key{}, ErrInvalidCursor
	}

	timestamp, err := time.Parse(time.RFC3339Nano, p.T)
	if err != nil {
		return Key{}, ErrInvalidCursor
	}

	id, err := uuid.Parse(p.I)
	if err != nil {
		return Key{}, ErrInvalidCursor
	}

	return Key{Timestamp: timestamp, ID: id, Sort: p.S, Order: p.O}, nil
}

// Match reports whether this cursor may continue a listing ordered by (sort, order).
//
// A cursor's timestamp bounds one specific column in one specific direction. Presented against a
// different ordering it still produces a valid, index-served query and a plausible-looking page —
// just not the continuation of the previous one, with an arbitrary set of rows skipped and another
// repeated, and no way for the client to notice. So it is refused rather than served.
//
// A cursor with no recorded ordering matches anything, which is what keeps legacy cursors valid.
func (k Key) Match(sort, order string) error {
	if k.Sort == "" && k.Order == "" {
		return nil
	}

	if k.Sort != sort || k.Order != order {
		return ErrCursorSortMismatch
	}

	return nil
}

// Encode encodes a list cursor with no recorded ordering, for endpoints with a single fixed order
// (webhooks). Sort-aware callers use EncodeKey. The wire format is unchanged.
func Encode(ts time.Time, id uuid.UUID) (string, error) {
	return EncodeKey(Key{Timestamp: ts, ID: id})
}

// Decode parses a list cursor and returns (timestamp, id), ignoring any recorded ordering.
// Returns ErrInvalidCursor if the cursor is malformed.
func Decode(cursor string) (time.Time, uuid.UUID, error) {
	key, err := DecodeKey(cursor)
	if err != nil {
		return time.Time{}, uuid.Nil, err
	}

	return key.Timestamp, key.ID, nil
}
