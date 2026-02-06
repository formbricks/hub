package observability

import "testing"

func Test_normalizeEventType(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"known feedback_record.created", "feedback_record.created", "feedback_record.created"},
		{"known feedback_record.updated", "feedback_record.updated", "feedback_record.updated"},
		{"known feedback_record.deleted", "feedback_record.deleted", "feedback_record.deleted"},
		{"known webhook.created", "webhook.created", "webhook.created"},
		{"known webhook.updated", "webhook.updated", "webhook.updated"},
		{"known webhook.deleted", "webhook.deleted", "webhook.deleted"},
		{"unknown empty", "", "unknown"},
		{"unknown random", "some.other.event", "unknown"},
		{"unknown typo", "feedback_record.creatd", "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeEventType(tt.input)
			if got != tt.expected {
				t.Errorf("normalizeEventType(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func Test_normalizeOutcome(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"success", "success", "success"},
		{"retryable_failure", "retryable_failure", "retryable_failure"},
		{"disabled_410", "disabled_410", "disabled_410"},
		{"disabled_max_retries", "disabled_max_retries", "disabled_max_retries"},
		{"unknown empty", "", "unknown"},
		{"unknown random", "timeout", "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeOutcome(tt.input)
			if got != tt.expected {
				t.Errorf("normalizeOutcome(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func Test_normalizeDisabledReason(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"410_gone", "410_gone", "410_gone"},
		{"max_retries", "max_retries", "max_retries"},
		{"unknown empty", "", "unknown"},
		{"unknown random", "manual", "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeDisabledReason(tt.input)
			if got != tt.expected {
				t.Errorf("normalizeDisabledReason(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}
