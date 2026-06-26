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

// compositeSettingsChangeListener fans a settings change out to several listeners, in order.
// It lets a single TenantSettingsService listener registration drive multiple independent
// reactions — e.g. evict the read cache and enqueue a re-translation backfill.
type compositeSettingsChangeListener struct {
	listeners []SettingsChangeListener
}

// NewCompositeSettingsChangeListener combines listeners into one that notifies each in order.
func NewCompositeSettingsChangeListener(listeners ...SettingsChangeListener) SettingsChangeListener {
	return &compositeSettingsChangeListener{listeners: listeners}
}

func (c *compositeSettingsChangeListener) OnSettingsChanged(
	ctx context.Context, tenantID string, changedKeys []string,
) {
	for _, l := range c.listeners {
		l.OnSettingsChanged(ctx, tenantID, changedKeys)
	}
}
