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

// PatchTenantSettingsRequest is the body for PATCH /v1/tenants/{tenant_id}/settings.
// PATCH is an RFC 7396 JSON Merge Patch, not a full replace: for each member, a
// concrete value sets that setting, an explicit JSON null removes it, and an
// omitted member is left unchanged. Optional captures those three states (a plain
// pointer cannot tell absent from null). Values are validated and normalized by
// the service. tenant_id comes from the path, never the body.
type PatchTenantSettingsRequest struct {
	TargetLanguage Optional[string] `json:"target_language"`
}
