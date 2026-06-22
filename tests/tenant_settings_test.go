package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/pkg/database"
)

// settingsRequest issues a request to the tenant settings API. When body is empty
// no request body is sent; when withAuth is true the test API key is attached.
func settingsRequest(
	t *testing.T, serverURL, method, tenantID, body string, withAuth bool,
) *http.Response {
	t.Helper()

	var reqBody io.Reader = http.NoBody
	if body != "" {
		reqBody = bytes.NewBufferString(body)
	}

	endpoint := serverURL + "/v1/tenants/" + url.PathEscape(tenantID) + "/settings"

	req, err := http.NewRequestWithContext(context.Background(), method, endpoint, reqBody)
	require.NoError(t, err)

	if withAuth {
		req.Header.Set("Authorization", "Bearer "+testAPIKey)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{}).Do(req)
	require.NoError(t, err)

	return resp
}

// testTenantID returns a unique tenant id for a test so runs never collide.
func testTenantID(suffix string) string {
	return "test-settings-" + suffix + "-" + uuid.NewString()
}

func TestTenantSettings_PutThenGetRoundTrip(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	tenantID := testTenantID("roundtrip")

	putResp := settingsRequest(t, server.URL, http.MethodPut, tenantID, `{"target_language":"en-US"}`, true)
	require.Equal(t, http.StatusOK, putResp.StatusCode)

	var put models.TenantSettings
	require.NoError(t, decodeData(putResp, &put))
	require.NoError(t, putResp.Body.Close())
	assert.Equal(t, tenantID, put.TenantID)
	assert.Equal(t, "en-US", put.Settings.TargetLanguage)

	getResp := settingsRequest(t, server.URL, http.MethodGet, tenantID, "", true)
	require.Equal(t, http.StatusOK, getResp.StatusCode)

	var got models.TenantSettings
	require.NoError(t, decodeData(getResp, &got))
	require.NoError(t, getResp.Body.Close())
	assert.Equal(t, "en-US", got.Settings.TargetLanguage)
}

func TestTenantSettings_GetReturnsDefaultsWhenUnset(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	resp := settingsRequest(t, server.URL, http.MethodGet, testTenantID("unset"), "", true)
	require.Equal(t, http.StatusOK, resp.StatusCode, "unconfigured tenant must return 200, not 404")

	var got models.TenantSettings
	require.NoError(t, decodeData(resp, &got))
	require.NoError(t, resp.Body.Close())
	assert.Empty(t, got.Settings.TargetLanguage, "default target_language must be empty")
}

func TestTenantSettings_NormalizesLocale(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	resp := settingsRequest(t, server.URL, http.MethodPut, testTenantID("normalize"), `{"target_language":"de-de"}`, true)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got models.TenantSettings
	require.NoError(t, decodeData(resp, &got))
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, "de-DE", got.Settings.TargetLanguage, "locale must be normalized to canonical BCP-47")
}

func TestTenantSettings_InvalidLocaleRejected(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	resp := settingsRequest(t, server.URL, http.MethodPut, testTenantID("invalid"), `{"target_language":"not a locale"}`, true)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestTenantSettings_PutRejectsOversizedLanguage(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	// Exceeds the max=35 struct validation bound, rejected before the service runs.
	body := `{"target_language":"` + strings.Repeat("a", 40) + `"}`
	resp := settingsRequest(t, server.URL, http.MethodPut, testTenantID("oversized"), body, true)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestTenantSettings_BlankTenantRejected(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	// A whitespace-only tenant_id in the path normalizes to empty and is rejected.
	resp := settingsRequest(t, server.URL, http.MethodGet, "   ", "", true)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// TestTenantSettings_RejectsTenantIDInBody proves a request cannot address another
// tenant: tenant_id is path-only, and an unexpected body field is rejected.
func TestTenantSettings_RejectsTenantIDInBody(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	resp := settingsRequest(t, server.URL, http.MethodPut, testTenantID("smuggle"),
		`{"tenant_id":"another-tenant","target_language":"en-US"}`, true)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "unknown body field (tenant_id) must be rejected")
}

func TestTenantSettings_RequiresAuth(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	resp := settingsRequest(t, server.URL, http.MethodGet, testTenantID("auth"), "", false)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

// TestTenantSettings_TenantIsolation proves one tenant's settings can neither be
// read by nor overwritten through another tenant.
func TestTenantSettings_TenantIsolation(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	tenantA := testTenantID("iso-a")
	tenantB := testTenantID("iso-b")

	// Configure tenant A.
	respA := settingsRequest(t, server.URL, http.MethodPut, tenantA, `{"target_language":"en-US"}`, true)
	require.Equal(t, http.StatusOK, respA.StatusCode)
	require.NoError(t, respA.Body.Close())

	// Tenant B sees no settings (A's value does not leak).
	respB := settingsRequest(t, server.URL, http.MethodGet, tenantB, "", true)
	require.Equal(t, http.StatusOK, respB.StatusCode)

	var bSettings models.TenantSettings
	require.NoError(t, decodeData(respB, &bSettings))
	require.NoError(t, respB.Body.Close())
	assert.Empty(t, bSettings.Settings.TargetLanguage, "tenant B must not see tenant A's setting")

	// Writing tenant B must not clobber tenant A.
	respBWrite := settingsRequest(t, server.URL, http.MethodPut, tenantB, `{"target_language":"fr-FR"}`, true)
	require.Equal(t, http.StatusOK, respBWrite.StatusCode)
	require.NoError(t, respBWrite.Body.Close())

	respA2 := settingsRequest(t, server.URL, http.MethodGet, tenantA, "", true)
	require.Equal(t, http.StatusOK, respA2.StatusCode)

	var aSettings models.TenantSettings
	require.NoError(t, decodeData(respA2, &aSettings))
	require.NoError(t, respA2.Body.Close())
	assert.Equal(t, "en-US", aSettings.Settings.TargetLanguage, "tenant A's setting must be unchanged by tenant B's write")
}

// TestTenantSettings_PurgeRemovesSettings proves a tenant data purge also removes
// the tenant's settings row.
func TestTenantSettings_PurgeRemovesSettings(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	tenantID := testTenantID("purge")

	putResp := settingsRequest(t, server.URL, http.MethodPut, tenantID, `{"target_language":"en-US"}`, true)
	require.Equal(t, http.StatusOK, putResp.StatusCode)
	require.NoError(t, putResp.Body.Close())

	purgeURL := server.URL + "/v1/tenants/" + url.PathEscape(tenantID) + "/data"
	purgeReq, err := http.NewRequestWithContext(context.Background(), http.MethodDelete, purgeURL, http.NoBody)
	require.NoError(t, err)
	purgeReq.Header.Set("Authorization", "Bearer "+testAPIKey)

	purgeResp, err := (&http.Client{}).Do(purgeReq)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, purgeResp.StatusCode)
	require.NoError(t, purgeResp.Body.Close())

	getResp := settingsRequest(t, server.URL, http.MethodGet, tenantID, "", true)
	require.Equal(t, http.StatusOK, getResp.StatusCode)

	var got models.TenantSettings
	require.NoError(t, decodeData(getResp, &got))
	require.NoError(t, getResp.Body.Close())
	assert.Empty(t, got.Settings.TargetLanguage, "purge must remove the tenant's settings")
}

func TestTenantSettings_PutBodyTooLargeRejected(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	// A body well over the 8 KiB cap is rejected with 413 before it is fully decoded.
	oversized := `{"target_language":"` + strings.Repeat("a", 9000) + `"}`
	resp := settingsRequest(t, server.URL, http.MethodPut, testTenantID("toobig"), oversized, true)
	require.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)

	var problem struct {
		Code string `json:"code"`
	}
	require.NoError(t, decodeData(resp, &problem))
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, "content_too_large", problem.Code, "413 should carry a payload-too-large code, not generic bad_request")
}

func TestTenantSettings_PatchUpdatesField(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	tenantID := testTenantID("patch-update")

	put := settingsRequest(t, server.URL, http.MethodPut, tenantID, `{"target_language":"en-US"}`, true)
	require.Equal(t, http.StatusOK, put.StatusCode)
	require.NoError(t, put.Body.Close())

	patch := settingsRequest(t, server.URL, http.MethodPatch, tenantID, `{"target_language":"de-de"}`, true)
	require.Equal(t, http.StatusOK, patch.StatusCode)

	var got models.TenantSettings
	require.NoError(t, decodeData(patch, &got))
	require.NoError(t, patch.Body.Close())
	assert.Equal(t, "de-DE", got.Settings.TargetLanguage, "PATCH should update and normalize the field")
}

func TestTenantSettings_PatchOmittedFieldLeavesUnchanged(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	tenantID := testTenantID("patch-omit")

	put := settingsRequest(t, server.URL, http.MethodPut, tenantID, `{"target_language":"en-US"}`, true)
	require.Equal(t, http.StatusOK, put.StatusCode)
	require.NoError(t, put.Body.Close())

	// Empty patch: nothing provided, so nothing changes.
	patch := settingsRequest(t, server.URL, http.MethodPatch, tenantID, `{}`, true)
	require.Equal(t, http.StatusOK, patch.StatusCode)
	require.NoError(t, patch.Body.Close())

	get := settingsRequest(t, server.URL, http.MethodGet, tenantID, "", true)
	require.Equal(t, http.StatusOK, get.StatusCode)

	var got models.TenantSettings
	require.NoError(t, decodeData(get, &got))
	require.NoError(t, get.Body.Close())
	assert.Equal(t, "en-US", got.Settings.TargetLanguage, "an empty PATCH must not change existing settings")
}

func TestTenantSettings_PatchEmptyStringClears(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	tenantID := testTenantID("patch-clear")

	put := settingsRequest(t, server.URL, http.MethodPut, tenantID, `{"target_language":"en-US"}`, true)
	require.Equal(t, http.StatusOK, put.StatusCode)
	require.NoError(t, put.Body.Close())

	patch := settingsRequest(t, server.URL, http.MethodPatch, tenantID, `{"target_language":""}`, true)
	require.Equal(t, http.StatusOK, patch.StatusCode)

	var got models.TenantSettings
	require.NoError(t, decodeData(patch, &got))
	require.NoError(t, patch.Body.Close())
	assert.Empty(t, got.Settings.TargetLanguage, "PATCH with an empty string should clear the field")
}

// TestTenantSettings_PatchMergePreservesOtherKeys proves PATCH does a JSONB merge
// (||), not a full replace: a key the typed model does not know about survives.
func TestTenantSettings_PatchMergePreservesOtherKeys(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	ctx := context.Background()
	tenantID := testTenantID("patch-merge")

	cfg, err := config.Load()
	require.NoError(t, err)

	db, err := database.NewPostgresPool(ctx, cfg.Database.URL, database.WithPoolConfig(cfg.Database.PoolConfig()))
	require.NoError(t, err)

	defer db.Close()

	// Seed a row with an extra key the typed API never writes.
	_, err = db.Exec(ctx,
		`INSERT INTO tenant_settings (tenant_id, settings) VALUES ($1, $2::jsonb)`,
		tenantID, `{"target_language":"en-US","experimental":"keep-me"}`)
	require.NoError(t, err)

	patch := settingsRequest(t, server.URL, http.MethodPatch, tenantID, `{"target_language":"de-DE"}`, true)
	require.Equal(t, http.StatusOK, patch.StatusCode)
	require.NoError(t, patch.Body.Close())

	var rawBytes []byte
	require.NoError(t, db.QueryRow(ctx,
		`SELECT settings FROM tenant_settings WHERE tenant_id = $1`, tenantID).Scan(&rawBytes))

	stored := map[string]string{}
	require.NoError(t, json.Unmarshal(rawBytes, &stored))
	assert.Equal(t, "de-DE", stored["target_language"], "PATCH should update the targeted key")
	assert.Equal(t, "keep-me", stored["experimental"], "PATCH must preserve keys it did not touch (merge, not replace)")
}

func TestTenantSettings_PatchBodyTooLargeRejected(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	oversized := `{"target_language":"` + strings.Repeat("a", 9000) + `"}`
	resp := settingsRequest(t, server.URL, http.MethodPatch, testTenantID("patch-toobig"), oversized, true)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
}

// TestTenantSettings_PatchCreatesRowForNewTenant exercises the INSERT branch: a
// PATCH for a tenant with no settings row yet creates it with the patched field.
func TestTenantSettings_PatchCreatesRowForNewTenant(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	tenantID := testTenantID("patch-create")

	patch := settingsRequest(t, server.URL, http.MethodPatch, tenantID, `{"target_language":"fr-FR"}`, true)
	require.Equal(t, http.StatusOK, patch.StatusCode)

	var created models.TenantSettings
	require.NoError(t, decodeData(patch, &created))
	require.NoError(t, patch.Body.Close())
	assert.Equal(t, "fr-FR", created.Settings.TargetLanguage)

	get := settingsRequest(t, server.URL, http.MethodGet, tenantID, "", true)
	require.Equal(t, http.StatusOK, get.StatusCode)

	var got models.TenantSettings
	require.NoError(t, decodeData(get, &got))
	require.NoError(t, get.Body.Close())
	assert.Equal(t, "fr-FR", got.Settings.TargetLanguage, "PATCH on a tenant with no row should create it")
}
