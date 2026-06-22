package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/formbricks/hub/internal/models"
)

// countingSettingsReader is a TenantSettingsReader that counts delegate calls.
type countingSettingsReader struct {
	calls    int
	settings *models.TenantSettings
	err      error
}

func (r *countingSettingsReader) GetSettings(_ context.Context, _ string) (*models.TenantSettings, error) {
	r.calls++

	if r.err != nil {
		return nil, r.err
	}

	return r.settings, nil
}

func TestCachedTenantSettings_CachesWithinTTL(t *testing.T) {
	reader := &countingSettingsReader{
		settings: &models.TenantSettings{
			TenantID: "org-1",
			Settings: models.EnrichmentSettings{TargetLanguage: "de-DE"},
		},
	}
	cached := NewCachedTenantSettings(reader, 16, time.Minute, nil)

	for range 3 {
		got, err := cached.GetSettings(context.Background(), "org-1")
		if err != nil {
			t.Fatalf("GetSettings() error = %v", err)
		}

		if got.Settings.TargetLanguage != "de-DE" {
			t.Fatalf("target = %q, want de-DE", got.Settings.TargetLanguage)
		}
	}

	if reader.calls != 1 {
		t.Fatalf("delegate calls = %d, want 1 (subsequent reads served from cache)", reader.calls)
	}
}

func TestCachedTenantSettings_DisabledBypassesCache(t *testing.T) {
	reader := &countingSettingsReader{settings: &models.TenantSettings{TenantID: "org-1"}}
	cached := NewCachedTenantSettings(reader, 0, time.Minute, nil) // size 0 disables caching

	for range 2 {
		if _, err := cached.GetSettings(context.Background(), "org-1"); err != nil {
			t.Fatalf("GetSettings() error = %v", err)
		}
	}

	if reader.calls != 2 {
		t.Fatalf("delegate calls = %d, want 2 (cache disabled)", reader.calls)
	}
}

func TestCachedTenantSettings_DoesNotCacheErrors(t *testing.T) {
	reader := &countingSettingsReader{err: errors.New("boom")}
	cached := NewCachedTenantSettings(reader, 16, time.Minute, nil)

	for range 2 {
		if _, err := cached.GetSettings(context.Background(), "org-1"); err == nil {
			t.Fatal("GetSettings() expected error, got nil")
		}
	}

	if reader.calls != 2 {
		t.Fatalf("delegate calls = %d, want 2 (errors must not be cached)", reader.calls)
	}
}
