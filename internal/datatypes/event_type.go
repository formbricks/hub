// Package datatypes defines shared types for events (e.g. webhook event types).
package datatypes

import (
	"errors"
	"fmt"
)

// Event type validation errors (sentinels for err113).
var (
	ErrEventTypeTooLong   = errors.New("event type exceeds max length")
	ErrInvalidEventType   = errors.New("invalid event type")
	ErrDuplicateEventType = errors.New("duplicate event type")
)

// maxEventTypeLen is the maximum allowed length for an event type string.
const maxEventTypeLen = 64

// EventType represents a webhook event type as an enum.
// Use String() to get the string representation for API/database.
type EventType uint16

// Event type constants; string form is given in eventTypeMap.
const (
	FeedbackRecordCreated EventType = iota
	FeedbackRecordUpdated
	FeedbackRecordDeleted
	WebhookCreated
	WebhookUpdated
	WebhookDeleted
)

// eventTypeMap maps string representations to EventType enums.
// This is the single source of truth for valid event type strings.
var eventTypeMap = map[string]EventType{
	"feedback_record.created": FeedbackRecordCreated,
	"feedback_record.updated": FeedbackRecordUpdated,
	"feedback_record.deleted": FeedbackRecordDeleted,
	"webhook.created":         WebhookCreated,
	"webhook.updated":         WebhookUpdated,
	"webhook.deleted":         WebhookDeleted,
}

// reverseEventTypeMap maps EventType enums to string representations.
// Built at init time from eventTypeMap for O(1) lookups.
var reverseEventTypeMap map[EventType]string

func init() {
	// Build reverse map from eventTypeMap for efficient String() lookups
	reverseEventTypeMap = make(map[EventType]string, len(eventTypeMap))
	for str, eventType := range eventTypeMap {
		reverseEventTypeMap[eventType] = str
	}
}

// String returns the string representation of an EventType.
// Implements fmt.Stringer interface.
// Returns empty string for invalid event types.
func (et EventType) String() string {
	str, ok := reverseEventTypeMap[et]
	if !ok {
		return "" // Invalid event type
	}

	return str
}

// ParseEventType converts a string to an EventType enum.
// Returns the EventType and true if valid, or 0 and false if invalid.
func ParseEventType(s string) (EventType, bool) {
	et, ok := eventTypeMap[s]

	return et, ok
}

// GetAllEventTypes returns all valid event type strings (for validation).
// The order is not guaranteed (map iteration order).
func GetAllEventTypes() []string {
	types := make([]string, 0, len(eventTypeMap))
	for k := range eventTypeMap {
		types = append(types, k)
	}

	return types
}

// IsValidEventType checks if an event type string is valid.
func IsValidEventType(eventType string) bool {
	_, ok := eventTypeMap[eventType]

	return ok
}

// ParseEventTypes converts a slice of strings to []EventType.
// Returns an error if any string is invalid, exceeds maxEventTypeLen chars, or is duplicated.
func ParseEventTypes(ss []string) ([]EventType, error) {
	if len(ss) == 0 {
		return nil, nil
	}

	out := make([]EventType, 0, len(ss))

	seen := make(map[string]bool, len(ss))
	for _, s := range ss {
		if len(s) > maxEventTypeLen {
			return nil, fmt.Errorf("%w: %d characters: %s", ErrEventTypeTooLong, maxEventTypeLen, s)
		}

		if !IsValidEventType(s) {
			return nil, fmt.Errorf("%w: %s", ErrInvalidEventType, s)
		}

		if seen[s] {
			return nil, fmt.Errorf("%w: %s", ErrDuplicateEventType, s)
		}

		seen[s] = true
		et, _ := ParseEventType(s)
		out = append(out, et)
	}

	return out, nil
}

// EventTypeStrings returns the string slice for a []EventType (for JSON marshaling).
func EventTypeStrings(types []EventType) []string {
	if len(types) == 0 {
		return nil
	}

	out := make([]string, len(types))
	for i, et := range types {
		out[i] = et.String()
	}

	return out
}
