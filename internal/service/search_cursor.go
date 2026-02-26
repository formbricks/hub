package service

import (
	"encoding/base64"
	"encoding/json"
	"errors"

	"github.com/google/uuid"

	"github.com/formbricks/hub/internal/models"
)

// ErrInvalidCursor is returned when the cursor parameter is malformed or invalid.
var ErrInvalidCursor = errors.New("invalid cursor")

type cursorPayload struct {
	D float64 `json:"d"` // cosine distance (embedding <=> query) of last row
	I string  `json:"i"` // feedback_record_id of last row (UUID string)
}

// EncodeSearchCursor returns an opaque cursor for the next page. distance is the cosine distance
// (e.embedding <=> query) of the last result row; id is that row's feedback_record_id.
func EncodeSearchCursor(distance float64, id uuid.UUID) string {
	b, err := json.Marshal(cursorPayload{D: distance, I: id.String()})
	if err != nil {
		return ""
	}

	return base64.URLEncoding.EncodeToString(b)
}

// DecodeSearchCursor parses an opaque cursor and returns (distance, feedbackRecordID).
// Returns ErrInvalidCursor if the cursor is malformed.
func DecodeSearchCursor(cursor string) (distance float64, feedbackRecordID uuid.UUID, err error) {
	if cursor == "" {
		return 0, uuid.Nil, ErrInvalidCursor
	}

	raw, err := base64.URLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, uuid.Nil, ErrInvalidCursor
	}

	var p cursorPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return 0, uuid.Nil, ErrInvalidCursor
	}

	id, err := uuid.Parse(p.I)
	if err != nil {
		return 0, uuid.Nil, ErrInvalidCursor
	}

	if p.D < 0 || p.D > 2 {
		return 0, uuid.Nil, ErrInvalidCursor
	}

	return p.D, id, nil
}

// SearchResult holds the results and optional next-page cursor from semantic search or similar feedback.
type SearchResult struct {
	Results    []models.FeedbackRecordWithScore
	NextCursor string // non-empty if there may be a next page (len(Results) == requested limit)
}
