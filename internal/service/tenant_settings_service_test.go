package service

import (
	"context"
	"errors"
	"testing"

	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
)

type mockTenantSettingsRepo struct {
	getResult       *models.TenantSettings
	getFound        bool
	getErr          error
	getCalled       bool
	getTenantID     string
	upsertResult    *models.TenantSettings
	upsertErr       error
	upsertCalled    bool
	upsertTenantID  string
	upsertSettings  models.EnrichmentSettings
	patchErr        error
	patchCalled     bool
	patchTenantID   string
	patchSet        models.EnrichmentSettings
	patchRemoveKeys []string
}

func (m *mockTenantSettingsRepo) Get(_ context.Context, tenantID string) (*models.TenantSettings, bool, error) {
	m.getCalled = true
	m.getTenantID = tenantID

	return m.getResult, m.getFound, m.getErr
}

func (m *mockTenantSettingsRepo) Upsert(
	_ context.Context, tenantID string, settings models.EnrichmentSettings,
) (*models.TenantSettings, error) {
	m.upsertCalled = true
	m.upsertTenantID = tenantID
	m.upsertSettings = settings

	if m.upsertErr != nil {
		return nil, m.upsertErr
	}

	if m.upsertResult != nil {
		return m.upsertResult, nil
	}

	return &models.TenantSettings{TenantID: tenantID, Settings: settings}, nil
}

func (m *mockTenantSettingsRepo) Patch(
	_ context.Context, tenantID string, set models.EnrichmentSettings, removeKeys []string,
) (*models.TenantSettings, error) {
	m.patchCalled = true
	m.patchTenantID = tenantID
	m.patchSet = set
	m.patchRemoveKeys = removeKeys

	if m.patchErr != nil {
		return nil, m.patchErr
	}

	// Echo a plausible stored result: the set value, cleared if it was removed.
	settings := models.EnrichmentSettings{TargetLanguage: set.TargetLanguage}

	for _, key := range removeKeys {
		if key == "target_language" {
			settings.TargetLanguage = ""
		}
	}

	return &models.TenantSettings{TenantID: tenantID, Settings: settings}, nil
}

func TestTenantSettingsService_GetSettings_NoRowReturnsDefaults(t *testing.T) {
	repo := &mockTenantSettingsRepo{getResult: nil}
	svc := NewTenantSettingsService(repo)

	settings, err := svc.GetSettings(context.Background(), " org-123 ")
	if err != nil {
		t.Fatalf("GetSettings() error = %v", err)
	}

	if repo.getTenantID != "org-123" {
		t.Fatalf("repo.Get tenant = %q, want org-123 (trimmed)", repo.getTenantID)
	}

	if settings.TenantID != "org-123" {
		t.Fatalf("settings.TenantID = %q, want org-123", settings.TenantID)
	}

	if settings.Settings.TargetLanguage != "" {
		t.Fatalf("default TargetLanguage = %q, want empty", settings.Settings.TargetLanguage)
	}
}

func TestTenantSettingsService_GetSettings_ReturnsStored(t *testing.T) {
	stored := &models.TenantSettings{
		TenantID: "org-123",
		Settings: models.EnrichmentSettings{TargetLanguage: "en-US"},
	}
	repo := &mockTenantSettingsRepo{getResult: stored, getFound: true}
	svc := NewTenantSettingsService(repo)

	settings, err := svc.GetSettings(context.Background(), "org-123")
	if err != nil {
		t.Fatalf("GetSettings() error = %v", err)
	}

	if settings.Settings.TargetLanguage != "en-US" {
		t.Fatalf("TargetLanguage = %q, want en-US", settings.Settings.TargetLanguage)
	}
}

func TestTenantSettingsService_GetSettings_BlankTenantIsRejected(t *testing.T) {
	repo := &mockTenantSettingsRepo{}
	svc := NewTenantSettingsService(repo)

	_, err := svc.GetSettings(context.Background(), "   ")
	if !errors.Is(err, huberrors.ErrValidation) {
		t.Fatalf("GetSettings() error = %v, want validation error", err)
	}

	if repo.getCalled {
		t.Fatal("repo.Get called despite invalid tenant")
	}
}

func TestTenantSettingsService_GetSettings_RepoErrorPropagates(t *testing.T) {
	sentinel := errors.New("boom")
	repo := &mockTenantSettingsRepo{getErr: sentinel}
	svc := NewTenantSettingsService(repo)

	_, err := svc.GetSettings(context.Background(), "org-123")
	if !errors.Is(err, sentinel) {
		t.Fatalf("GetSettings() error = %v, want wrapped sentinel", err)
	}
}

func TestTenantSettingsService_UpdateSettings_NormalizesLanguageAndTenant(t *testing.T) {
	repo := &mockTenantSettingsRepo{}
	svc := NewTenantSettingsService(repo)

	settings, err := svc.UpdateSettings(
		context.Background(), " org-123 ",
		&models.UpdateTenantSettingsRequest{TargetLanguage: "en-us"},
	)
	if err != nil {
		t.Fatalf("UpdateSettings() error = %v", err)
	}

	if repo.upsertTenantID != "org-123" {
		t.Fatalf("repo.Upsert tenant = %q, want org-123", repo.upsertTenantID)
	}

	if repo.upsertSettings.TargetLanguage != "en-US" {
		t.Fatalf("repo.Upsert TargetLanguage = %q, want en-US", repo.upsertSettings.TargetLanguage)
	}

	if settings.Settings.TargetLanguage != "en-US" {
		t.Fatalf("returned TargetLanguage = %q, want en-US", settings.Settings.TargetLanguage)
	}
}

func TestTenantSettingsService_UpdateSettings_EmptyLanguageAllowed(t *testing.T) {
	repo := &mockTenantSettingsRepo{}
	svc := NewTenantSettingsService(repo)

	_, err := svc.UpdateSettings(
		context.Background(), "org-123",
		&models.UpdateTenantSettingsRequest{TargetLanguage: ""},
	)
	if err != nil {
		t.Fatalf("UpdateSettings() error = %v", err)
	}

	if !repo.upsertCalled {
		t.Fatal("repo.Upsert not called")
	}

	if repo.upsertSettings.TargetLanguage != "" {
		t.Fatalf("TargetLanguage = %q, want empty", repo.upsertSettings.TargetLanguage)
	}
}

func TestTenantSettingsService_UpdateSettings_InvalidLanguageRejected(t *testing.T) {
	repo := &mockTenantSettingsRepo{}
	svc := NewTenantSettingsService(repo)

	_, err := svc.UpdateSettings(
		context.Background(), "org-123",
		&models.UpdateTenantSettingsRequest{TargetLanguage: "not a locale"},
	)
	if !errors.Is(err, huberrors.ErrValidation) {
		t.Fatalf("UpdateSettings() error = %v, want validation error", err)
	}

	if repo.upsertCalled {
		t.Fatal("repo.Upsert called despite invalid locale")
	}
}

func TestTenantSettingsService_UpdateSettings_BlankTenantRejected(t *testing.T) {
	repo := &mockTenantSettingsRepo{}
	svc := NewTenantSettingsService(repo)

	_, err := svc.UpdateSettings(
		context.Background(), "  ",
		&models.UpdateTenantSettingsRequest{TargetLanguage: "en-US"},
	)
	if !errors.Is(err, huberrors.ErrValidation) {
		t.Fatalf("UpdateSettings() error = %v, want validation error", err)
	}

	if repo.upsertCalled {
		t.Fatal("repo.Upsert called despite invalid tenant")
	}
}

func TestTenantSettingsService_UpdateSettings_RepoConflictPropagates(t *testing.T) {
	repo := &mockTenantSettingsRepo{upsertErr: huberrors.NewTenantWriteConflictError("purge in progress")}
	svc := NewTenantSettingsService(repo)

	_, err := svc.UpdateSettings(
		context.Background(), "org-123",
		&models.UpdateTenantSettingsRequest{TargetLanguage: "en-US"},
	)
	if !errors.Is(err, huberrors.ErrTenantWriteConflict) {
		t.Fatalf("UpdateSettings() error = %v, want tenant write conflict", err)
	}
}

func TestNormalizeTargetLanguage(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "empty stays empty", input: "", want: ""},
		{name: "whitespace trims to empty", input: "   ", want: ""},
		{name: "lowercase canonicalized", input: "en-us", want: "en-US"},
		{name: "already canonical", input: "de-DE", want: "de-DE"},
		{name: "language only", input: "en", want: "en"},
		{name: "surrounding whitespace", input: "  fr-FR  ", want: "fr-FR"},
		{name: "invalid locale", input: "not a locale", wantErr: true},
		{name: "garbage", input: "@@@", wantErr: true},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := normalizeTargetLanguage(testCase.input)
			if testCase.wantErr {
				if !errors.Is(err, huberrors.ErrValidation) {
					t.Fatalf("normalizeTargetLanguage(%q) error = %v, want validation error", testCase.input, err)
				}

				return
			}

			if err != nil {
				t.Fatalf("normalizeTargetLanguage(%q) error = %v", testCase.input, err)
			}

			if got != testCase.want {
				t.Fatalf("normalizeTargetLanguage(%q) = %q, want %q", testCase.input, got, testCase.want)
			}
		})
	}
}

// patchValue builds a merge-patch request that sets target_language to v.
func patchValue(v string) *models.PatchTenantSettingsRequest {
	return &models.PatchTenantSettingsRequest{
		TargetLanguage: models.Optional[string]{Present: true, Value: &v},
	}
}

func TestTenantSettingsService_PatchSettings_NormalizesProvidedField(t *testing.T) {
	repo := &mockTenantSettingsRepo{}
	svc := NewTenantSettingsService(repo)

	settings, err := svc.PatchSettings(context.Background(), " org-123 ", patchValue("en-us"))
	if err != nil {
		t.Fatalf("PatchSettings() error = %v", err)
	}

	if repo.patchTenantID != "org-123" {
		t.Fatalf("repo.Patch tenant = %q, want org-123", repo.patchTenantID)
	}

	if repo.patchSet.TargetLanguage != "en-US" {
		t.Fatalf("repo.Patch set target_language = %q, want en-US", repo.patchSet.TargetLanguage)
	}

	if len(repo.patchRemoveKeys) != 0 {
		t.Fatalf("repo.Patch removeKeys = %v, want none for a value set", repo.patchRemoveKeys)
	}

	if settings.Settings.TargetLanguage != "en-US" {
		t.Fatalf("returned TargetLanguage = %q, want en-US", settings.Settings.TargetLanguage)
	}
}

func TestTenantSettingsService_PatchSettings_OmittedFieldIsNoop(t *testing.T) {
	repo := &mockTenantSettingsRepo{}
	svc := NewTenantSettingsService(repo)

	// Absent member (Present false): nothing set, nothing removed.
	if _, err := svc.PatchSettings(
		context.Background(), "org-123", &models.PatchTenantSettingsRequest{},
	); err != nil {
		t.Fatalf("PatchSettings() error = %v", err)
	}

	if !repo.patchCalled {
		t.Fatal("repo.Patch not called")
	}

	if repo.patchSet.TargetLanguage != "" || len(repo.patchRemoveKeys) != 0 {
		t.Fatalf("omitted field: set=%q remove=%v, want empty set and no removals",
			repo.patchSet.TargetLanguage, repo.patchRemoveKeys)
	}
}

func TestTenantSettingsService_PatchSettings_NullRemovesField(t *testing.T) {
	repo := &mockTenantSettingsRepo{}
	svc := NewTenantSettingsService(repo)

	// Present with a nil Value is an explicit JSON null: remove the setting.
	req := &models.PatchTenantSettingsRequest{
		TargetLanguage: models.Optional[string]{Present: true, Value: nil},
	}
	if _, err := svc.PatchSettings(context.Background(), "org-123", req); err != nil {
		t.Fatalf("PatchSettings() error = %v", err)
	}

	if repo.patchSet.TargetLanguage != "" {
		t.Fatalf("repo.Patch set target_language = %q, want empty (removal, not a set)", repo.patchSet.TargetLanguage)
	}

	if len(repo.patchRemoveKeys) != 1 || repo.patchRemoveKeys[0] != "target_language" {
		t.Fatalf("repo.Patch removeKeys = %v, want [target_language]", repo.patchRemoveKeys)
	}
}

func TestTenantSettingsService_PatchSettings_EmptyStringRejected(t *testing.T) {
	repo := &mockTenantSettingsRepo{}
	svc := NewTenantSettingsService(repo)

	// Under RFC 7396 an empty string is a value, not a removal — and "" is not a
	// valid locale, so it must be rejected (callers clear via null).
	_, err := svc.PatchSettings(context.Background(), "org-123", patchValue(""))
	if !errors.Is(err, huberrors.ErrValidation) {
		t.Fatalf("PatchSettings() error = %v, want validation error for empty string", err)
	}

	if repo.patchCalled {
		t.Fatal("repo.Patch called despite empty-string value")
	}
}

func TestTenantSettingsService_PatchSettings_InvalidLanguageRejected(t *testing.T) {
	repo := &mockTenantSettingsRepo{}
	svc := NewTenantSettingsService(repo)

	_, err := svc.PatchSettings(context.Background(), "org-123", patchValue("not a locale"))
	if !errors.Is(err, huberrors.ErrValidation) {
		t.Fatalf("PatchSettings() error = %v, want validation error", err)
	}

	if repo.patchCalled {
		t.Fatal("repo.Patch called despite invalid locale")
	}
}

func TestTenantSettingsService_PatchSettings_BlankTenantRejected(t *testing.T) {
	repo := &mockTenantSettingsRepo{}
	svc := NewTenantSettingsService(repo)

	_, err := svc.PatchSettings(context.Background(), "  ", patchValue("en-US"))
	if !errors.Is(err, huberrors.ErrValidation) {
		t.Fatalf("PatchSettings() error = %v, want validation error", err)
	}

	if repo.patchCalled {
		t.Fatal("repo.Patch called despite invalid tenant")
	}
}

func TestTenantSettingsService_PatchSettings_RepoErrorPropagates(t *testing.T) {
	repo := &mockTenantSettingsRepo{patchErr: huberrors.NewTenantWriteConflictError("purge in progress")}
	svc := NewTenantSettingsService(repo)

	_, err := svc.PatchSettings(context.Background(), "org-123", patchValue("en-US"))
	if !errors.Is(err, huberrors.ErrTenantWriteConflict) {
		t.Fatalf("PatchSettings() error = %v, want tenant write conflict", err)
	}
}
