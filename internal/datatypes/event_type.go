package datatypes

// EventType represents a webhook event type as an enum.
// Use String() to get the string representation for API/database.
type EventType uint16

const (
	// FeedbackRecordCreated represents the "feedback_record.created" event
	FeedbackRecordCreated EventType = iota
	// FeedbackRecordUpdated represents the "feedback_record.updated" event
	FeedbackRecordUpdated
	// FeedbackRecordDeleted represents the "feedback_record.deleted" event
	FeedbackRecordDeleted
	// WebhookCreated represents the "webhook.created" event
	WebhookCreated
	// WebhookUpdated represents the "webhook.updated" event
	WebhookUpdated
	// WebhookDeleted represents the "webhook.deleted" event
	WebhookDeleted
	// Future: Add new event types here
	// ConnectorStarted
	// UserCreated
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
	// Future: Add new mappings here
	// "connector.started": ConnectorStarted,
	// "user.created": UserCreated,
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
