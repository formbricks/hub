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

	// MetricNameEmbeddingJobsEnqueued and related embedding pipeline metrics.
	MetricNameEmbeddingJobsEnqueued   = "hub_embedding_jobs_enqueued_total"
	MetricNameEmbeddingProviderErrors = "hub_embedding_provider_errors_total"
	MetricNameEmbeddingOutcomes       = "hub_embedding_outcomes_total"
	MetricNameEmbeddingWorkerErrors   = "hub_embedding_worker_errors_total"
	MetricNameEmbeddingDuration       = "hub_embedding_duration_seconds"

	// MetricNameTranslationJobsEnqueued and related translation pipeline metrics.
	MetricNameTranslationJobsEnqueued   = "hub_translation_jobs_enqueued_total"
	MetricNameTranslationProviderErrors = "hub_translation_provider_errors_total"
	MetricNameTranslationOutcomes       = "hub_translation_outcomes_total"
	MetricNameTranslationWorkerErrors   = "hub_translation_worker_errors_total"
	MetricNameTranslationDuration       = "hub_translation_duration_seconds"

	// MetricNameSentimentJobsEnqueued and related sentiment pipeline metrics.
	MetricNameSentimentJobsEnqueued   = "hub_sentiment_jobs_enqueued_total"
	MetricNameSentimentProviderErrors = "hub_sentiment_provider_errors_total"
	MetricNameSentimentOutcomes       = "hub_sentiment_outcomes_total"
	MetricNameSentimentWorkerErrors   = "hub_sentiment_worker_errors_total"
	MetricNameSentimentDuration       = "hub_sentiment_duration_seconds"

	// MetricNameEmotionsJobsEnqueued and related emotion pipeline metrics.
	MetricNameEmotionsJobsEnqueued   = "hub_emotions_jobs_enqueued_total"
	MetricNameEmotionsProviderErrors = "hub_emotions_provider_errors_total"
	MetricNameEmotionsOutcomes       = "hub_emotions_outcomes_total"
	MetricNameEmotionsWorkerErrors   = "hub_emotions_worker_errors_total"
	MetricNameEmotionsDuration       = "hub_emotions_duration_seconds"

	MetricNameCacheHits   = "hub_cache_hits_total"
	MetricNameCacheMisses = "hub_cache_misses_total"
)

// Attribute keys.
const (
	AttrEventType = "event_type"
	AttrReason    = "reason"
	AttrStatus    = "status"
	// AttrQueue labels the River queue-depth gauge; values come from the poller's fixed queue
	// set, so cardinality is bounded.
	AttrQueue = "queue"
)

// AllowedEventTypes returns event type strings allowed for metric attributes (bounded cardinality).
func AllowedEventTypes() []string {
	return datatypes.GetAllEventTypes()
}

// allowedProviderReasons for hub_webhook_provider_errors_total (bounded cardinality).
var allowedProviderReasons = map[string]bool{
	"list_failed":       true,
	"enqueue_failed":    true,
	"missing_tenant_id": true,
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
	"missing_tenant_id":  true,
	"tenant_mismatch":    true,
}

// allowedEmbeddingProviderReasons for hub_embedding_provider_errors_total.
var allowedEmbeddingProviderReasons = map[string]bool{
	"enqueue_failed": true,
	"invalid_data":   true,
}

// allowedEmbeddingOutcomeStatuses for hub_embedding_outcomes_total and hub_embedding_duration_seconds.
var allowedEmbeddingOutcomeStatuses = map[string]bool{
	"success":      true,
	"retry":        true,
	"failed_final": true,
	"skipped":      true,
}

// allowedEmbeddingWorkerReasons for hub_embedding_worker_errors_total.
var allowedEmbeddingWorkerReasons = map[string]bool{
	"embedding_api_failed":  true,
	"get_record_failed":     true,
	"update_failed":         true,
	"tenant_write_conflict": true,
	"rate_limited":          true,
	"superseded":            true,
}

// AllowedEmbeddingProviderReason returns true if reason is allowed for embedding provider errors.
func AllowedEmbeddingProviderReason(reason string) bool {
	return allowedEmbeddingProviderReasons[reason]
}

// AllowedEmbeddingOutcomeStatus returns true if status is allowed for embedding outcomes.
func AllowedEmbeddingOutcomeStatus(status string) bool {
	return allowedEmbeddingOutcomeStatuses[status]
}

// AllowedEmbeddingWorkerReason returns true if reason is allowed for embedding worker errors.
func AllowedEmbeddingWorkerReason(reason string) bool { return allowedEmbeddingWorkerReasons[reason] }

// allowedTranslationProviderReasons for hub_translation_provider_errors_total.
var allowedTranslationProviderReasons = map[string]bool{
	"settings_read_failed": true,
	"enqueue_failed":       true,
}

// allowedTranslationOutcomeStatuses for hub_translation_outcomes_total and hub_translation_duration_seconds.
var allowedTranslationOutcomeStatuses = map[string]bool{
	"success":      true,
	"retry":        true,
	"failed_final": true,
	"skipped":      true,
}

// allowedTranslationWorkerReasons for hub_translation_worker_errors_total.
var allowedTranslationWorkerReasons = map[string]bool{
	"translation_api_failed": true,
	"get_record_failed":      true,
	"update_failed":          true,
	"tenant_write_conflict":  true,
	"rate_limited":           true,
	// A stale-target write skipped by the supersession guard: benign, but kept under its own
	// label (not folded into "other") so target churn / cache staleness stays observable.
	"superseded": true,
}

// AllowedTranslationProviderReason returns true if reason is allowed for translation provider errors.
func AllowedTranslationProviderReason(reason string) bool {
	return allowedTranslationProviderReasons[reason]
}

// AllowedTranslationOutcomeStatus returns true if status is allowed for translation outcomes.
func AllowedTranslationOutcomeStatus(status string) bool {
	return allowedTranslationOutcomeStatuses[status]
}

// AllowedTranslationWorkerReason returns true if reason is allowed for translation worker errors.
func AllowedTranslationWorkerReason(reason string) bool {
	return allowedTranslationWorkerReasons[reason]
}

// allowedSentimentProviderReasons for hub_sentiment_provider_errors_total.
var allowedSentimentProviderReasons = map[string]bool{
	"settings_read_failed": true,
	"enqueue_failed":       true,
}

// allowedSentimentOutcomeStatuses for hub_sentiment_outcomes_total and hub_sentiment_duration_seconds.
var allowedSentimentOutcomeStatuses = map[string]bool{
	"success":      true,
	"retry":        true,
	"failed_final": true,
	"skipped":      true,
}

// allowedSentimentWorkerReasons for hub_sentiment_worker_errors_total.
var allowedSentimentWorkerReasons = map[string]bool{
	"sentiment_api_failed":  true,
	"get_record_failed":     true,
	"settings_read_failed":  true,
	"update_failed":         true,
	"tenant_write_conflict": true,
	"rate_limited":          true,
}

// AllowedSentimentProviderReason returns true if reason is allowed for sentiment provider errors.
func AllowedSentimentProviderReason(reason string) bool {
	return allowedSentimentProviderReasons[reason]
}

// AllowedSentimentOutcomeStatus returns true if status is allowed for sentiment outcomes.
func AllowedSentimentOutcomeStatus(status string) bool {
	return allowedSentimentOutcomeStatuses[status]
}

// AllowedSentimentWorkerReason returns true if reason is allowed for sentiment worker errors.
func AllowedSentimentWorkerReason(reason string) bool {
	return allowedSentimentWorkerReasons[reason]
}

// allowedEmotionsProviderReasons for hub_emotions_provider_errors_total.
var allowedEmotionsProviderReasons = map[string]bool{
	"settings_read_failed": true,
	"enqueue_failed":       true,
}

// allowedEmotionsOutcomeStatuses for hub_emotions_outcomes_total and hub_emotions_duration_seconds.
var allowedEmotionsOutcomeStatuses = map[string]bool{
	"success":      true,
	"retry":        true,
	"failed_final": true,
	"skipped":      true,
}

// allowedEmotionsWorkerReasons for hub_emotions_worker_errors_total.
var allowedEmotionsWorkerReasons = map[string]bool{
	"emotions_api_failed":   true,
	"get_record_failed":     true,
	"update_failed":         true,
	"tenant_write_conflict": true,
	"rate_limited":          true,
}

// AllowedEmotionsProviderReason returns true if reason is allowed for emotion provider errors.
func AllowedEmotionsProviderReason(reason string) bool {
	return allowedEmotionsProviderReasons[reason]
}

// AllowedEmotionsOutcomeStatus returns true if status is allowed for emotion outcomes.
func AllowedEmotionsOutcomeStatus(status string) bool {
	return allowedEmotionsOutcomeStatuses[status]
}

// AllowedEmotionsWorkerReason returns true if reason is allowed for emotion worker errors.
func AllowedEmotionsWorkerReason(reason string) bool {
	return allowedEmotionsWorkerReasons[reason]
}

// AllowedProviderReason returns true if reason is an allowed provider error reason.
func AllowedProviderReason(reason string) bool { return allowedProviderReasons[reason] }

// AllowedDeliveryStatus returns true if status is an allowed delivery status.
func AllowedDeliveryStatus(status string) bool { return allowedDeliveryStatuses[status] }

// AllowedDisabledReason returns true if reason is an allowed disabled reason.
func AllowedDisabledReason(reason string) bool { return allowedDisabledReasons[reason] }

// AllowedDispatchReason returns true if reason is an allowed dispatch error reason.
func AllowedDispatchReason(reason string) bool { return allowedDispatchReasons[reason] }

// allowedCacheNames for hub_cache_hits_total / hub_cache_misses_total (bounded cardinality).
var allowedCacheNames = map[string]bool{
	"search_query_embedding": true,
	"tenant_settings":        true,
}

// AllowedCacheName returns true if name is an allowed cache name for metrics.
func AllowedCacheName(name string) bool { return allowedCacheNames[name] }

// NormalizeCacheName returns name if allowed, otherwise "other".
func NormalizeCacheName(name string) string {
	if AllowedCacheName(name) {
		return name
	}

	return "other"
}

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
