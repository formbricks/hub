package models

// EnrichmentSettings is the typed contents of the tenant_settings.settings JSONB
// column. Every field is optional and falls back to a documented default applied
// by the service when absent. This is the forward-compatible surface for
// tenant-scoped enrichment configuration: add new settings as fields here, no
// schema migration required.
type EnrichmentSettings struct {
	// TargetLanguage is the normalized BCP-47 locale (e.g. "en-US") that language
	// enrichment translates feedback records and topic labels into. An empty
	// string means "not configured"; consumers decide the fallback behavior.
	TargetLanguage string `json:"target_language,omitempty"`
}

// TenantSettings is a tenant's persisted settings. It doubles as the API response
// body for the settings endpoints. tenant_id is the natural key and is never
// shared across tenants.
type TenantSettings struct {
	TenantID string             `json:"tenant_id"`
	Settings EnrichmentSettings `json:"settings"`
}

// UpdateTenantSettingsRequest is the body for PUT /v1/tenants/{tenant_id}/settings.
// The tenant_id comes from the path, never the body, so a request can only ever
// address its own tenant. PUT replaces the full settings object.
type UpdateTenantSettingsRequest struct {
	TargetLanguage string `json:"target_language" validate:"omitempty,no_null_bytes,max=35"`
}
