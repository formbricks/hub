package service

import "context"

// SettingsChangeListener is notified after a tenant's settings are successfully written,
// with the setting keys the write touched. It lets enrichment side-effects (e.g.
// re-translation) react to a settings change without TenantSettingsService depending on any
// enrichment concern — the service depends only on this translation-free port.
//
// Implementations do the reaction themselves (a fast, durable enqueue) and own their error
// handling: the method returns nothing because the settings write has already committed and
// the side-effect must never fail it.
type SettingsChangeListener interface {
	OnSettingsChanged(ctx context.Context, tenantID string, changedKeys []string)
}
