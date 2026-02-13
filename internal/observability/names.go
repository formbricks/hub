// Package observability provides OpenTelemetry metrics and tracing for the hub API.
package observability

import (
	"github.com/formbricks/hub/internal/datatypes"
)

// Metric names (Prometheus / OpenTelemetry).
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

// AllowedProviderReasons for hub_webhook_provider_errors_total.
var AllowedProviderReasons = map[string]bool{
	"list_failed":    true,
	"enqueue_failed": true,
}

// AllowedDeliveryStatuses for hub_webhook_deliveries_total and hub_webhook_delivery_duration_seconds.
var AllowedDeliveryStatuses = map[string]bool{
	"success":      true,
	"retry":        true,
	"failed_final": true,
}

// AllowedDisabledReasons for hub_webhook_disabled_total.
var AllowedDisabledReasons = map[string]bool{
	"410_gone":     true,
	"max_attempts": true,
}

// AllowedDispatchReasons for hub_webhook_dispatch_errors_total.
var AllowedDispatchReasons = map[string]bool{
	"get_webhook_failed": true,
}

// NormalizeEventType returns eventType if allowed, otherwise "unknown".
func NormalizeEventType(eventType string) string {
	if datatypes.IsValidEventType(eventType) {
		return eventType
	}

	return "unknown"
}

// NormalizeReason returns reason if in allowed, otherwise "other".
func NormalizeReason(reason string, allowed map[string]bool) string {
	if allowed[reason] {
		return reason
	}

	return "other"
}

// NormalizeStatus returns status if in AllowedDeliveryStatuses, otherwise "other".
func NormalizeStatus(status string) string {
	if AllowedDeliveryStatuses[status] {
		return status
	}

	return "other"
}
