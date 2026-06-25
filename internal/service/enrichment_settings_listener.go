package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/riverqueue/river"
)

// enrichmentBackfillHandler enqueues the per-tenant backfill that one enrichment needs when
// its triggering setting changes.
type enrichmentBackfillHandler func(ctx context.Context, tenantID string) error

// EnrichmentSettingsListener implements SettingsChangeListener by dispatching each changed
// setting key to the enrichment backfill it triggers. The dispatch table is built from what
// the deployment has enabled (e.g. translation), so a disabled enrichment registers no
// handler and an unknown/irrelevant key is ignored.
//
// It deliberately does not use the webhook MessagePublisher: this is an internal,
// best-effort side-effect, not a customer-facing event. Enqueue errors are logged and
// swallowed — the global backfill command remains the guaranteed recovery path.
//
// Adding a future setting is one map entry (e.g. a "sentiment_enabled" → sentiment backfill
// handler); TenantSettingsService does not change.
type EnrichmentSettingsListener struct {
	handlers map[string]enrichmentBackfillHandler
}

// NewTranslationSettingsListener builds a listener that, on a target_language change,
// enqueues a per-tenant translation backfill job (TenantTranslationBackfillArgs) via
// inserter. queueName/maxAttempts configure that fan-out job.
func NewTranslationSettingsListener(
	inserter RiverJobInserter, queueName string, maxAttempts int,
) *EnrichmentSettingsListener {
	return &EnrichmentSettingsListener{
		handlers: map[string]enrichmentBackfillHandler{
			settingKeyTargetLanguage: func(ctx context.Context, tenantID string) error {
				_, err := inserter.Insert(ctx, TenantTranslationBackfillArgs{TenantID: tenantID}, &river.InsertOpts{
					Queue:       queueName,
					MaxAttempts: maxAttempts,
					// Unique by TenantID across in-flight states (no ByPeriod): coalesce
					// rapid changes into one backfill, but let a change after the previous
					// one completed re-trigger.
					UniqueOpts: river.UniqueOpts{ByArgs: true},
				})
				if err != nil {
					return fmt.Errorf("enqueue tenant translation backfill: %w", err)
				}

				return nil
			},
		},
	}
}

// OnSettingsChanged dispatches each changed key to its enrichment backfill handler, if one
// is registered. Errors are logged and swallowed (the settings write already succeeded).
func (l *EnrichmentSettingsListener) OnSettingsChanged(ctx context.Context, tenantID string, changedKeys []string) {
	for _, key := range changedKeys {
		handler, ok := l.handlers[key]
		if !ok {
			continue
		}

		if err := handler(ctx, tenantID); err != nil {
			slog.Error("enrichment settings listener: backfill enqueue failed",
				"tenant_id", tenantID, "setting", key, "error", err)
		}
	}
}

var _ SettingsChangeListener = (*EnrichmentSettingsListener)(nil)
