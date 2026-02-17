// Package observability provides OpenTelemetry metrics and tracing for the hub API.
package observability

import (
	"github.com/formbricks/hub/internal/datatypes"
)

// Metric names (OpenTelemetry).
const (
	MetricNameEventsDiscarded         = "hub_events_discarded_total"
	MetricNameFanOutDuration          = "hub_message_publisher_fan_out_duration_seconds"
	MetricNameEventChannelDepth       = "hub_event_channel_depth"
	MetricNameRiverQueueDepth         = "hub_river_queue_depth"
	MetricNameWebhookJobsEnqueued     = "hub_webhook_jobs_enqueued_total"
	MetricNameWebhookProviderErrors   = "hub_webhook_provider_errors_total"
	MetricNameWebhookDeliveries       = "hub_webhook_deliveries_total"
	MetricNameWebhookDisabled         = "hub_webhook_disabled_total"
	MetricNameWebhookDispatchErrors   = "hub_webhook_dispatch_errors_total"
	MetricNameWebhookDeliveryDuration = "hub_webhook_delivery_duration_seconds"
)

// Attribute keys.
const (
	AttrEventType = "event_type"
	AttrReason    = "reason"
	AttrStatus    = "status"
)

// AllowedEventTypes returns event type strings allowed for metric attributes (bounded cardinality).
func AllowedEventTypes() []string {
	return datatypes.GetAllEventTypes()
}

// allowedProviderReasons for hub_webhook_provider_errors_total (bounded cardinality).
var allowedProviderReasons = map[string]bool{
	"list_failed":    true,
	"enqueue_failed": true,
}

// allowedDeliveryStatuses for hub_webhook_deliveries_total and hub_webhook_delivery_duration_seconds.
var allowedDeliveryStatuses = map[string]bool{
	"success":      true,
	"retry":        true,
	"failed_final": true,
}

// allowedDisabledReasons for hub_webhook_disabled_total.
var allowedDisabledReasons = map[string]bool{
	"410_gone":     true,
	"max_attempts": true,
}

// allowedDispatchReasons for hub_webhook_dispatch_errors_total.
var allowedDispatchReasons = map[string]bool{
	"get_webhook_failed": true,
}

// AllowedProviderReason returns true if reason is an allowed provider error reason.
func AllowedProviderReason(reason string) bool { return allowedProviderReasons[reason] }

// AllowedDeliveryStatus returns true if status is an allowed delivery status.
func AllowedDeliveryStatus(status string) bool { return allowedDeliveryStatuses[status] }

// AllowedDisabledReason returns true if reason is an allowed disabled reason.
func AllowedDisabledReason(reason string) bool { return allowedDisabledReasons[reason] }

// AllowedDispatchReason returns true if reason is an allowed dispatch error reason.
func AllowedDispatchReason(reason string) bool { return allowedDispatchReasons[reason] }

// NormalizeEventType returns eventType if allowed, otherwise "unknown".
func NormalizeEventType(eventType string) string {
	if datatypes.IsValidEventType(eventType) {
		return eventType
	}

	return "unknown"
}

// NormalizeReason returns reason if allowed(reason), otherwise "other".
func NormalizeReason(reason string, allowed func(string) bool) string {
	if allowed(reason) {
		return reason
	}

	return "other"
}

// NormalizeStatus returns status if in allowed delivery statuses, otherwise "other".
func NormalizeStatus(status string) string {
	if AllowedDeliveryStatus(status) {
		return status
	}

	return "other"
}
