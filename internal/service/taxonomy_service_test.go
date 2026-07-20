package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/repository"
)

type mockTaxonomyRepo struct {
	countRecordCount     int
	countEmbeddingCount  int
	createRunParams      repository.CreateTaxonomyRunParams
	createRun            *models.TaxonomyRun
	createRunCreated     bool
	internalRun          *models.TaxonomyRun
	markRunRunningTenant string
	markRunFailedMessage string
	markRunFailedCode    models.TaxonomyRunFailureCode
	markRunFailedTenant  string
	heartbeatTenant      string

	countNodeRecords       []models.TaxonomyNodeRecordCount
	countNodeRecordsErr    error
	countNodeRecordsRunID  uuid.UUID
	countNodeRecordsTenant string
}

func (m *mockTaxonomyRepo) ListFieldOptions(
	_ context.Context,
	_ string,
	_ string,
) ([]models.TaxonomyFieldOption, error) {
	return nil, nil
}

func (m *mockTaxonomyRepo) CountScopeInput(
	_ context.Context,
	_ models.TaxonomyScope,
	_ string,
) (int, int, *string, error) {
	return m.countRecordCount, m.countEmbeddingCount, nil, nil
}

func (m *mockTaxonomyRepo) CreateRunIfAvailable(
	_ context.Context,
	params repository.CreateTaxonomyRunParams,
) (*models.TaxonomyRun, bool, error) {
	m.createRunParams = params

	return m.createRun, m.createRunCreated, nil
}

func (m *mockTaxonomyRepo) MarkRunRunning(
	_ context.Context,
	runID uuid.UUID,
	tenantID string,
) (*models.TaxonomyRun, error) {
	m.markRunRunningTenant = tenantID

	return &models.TaxonomyRun{ID: runID, Status: models.TaxonomyRunStatusRunning}, nil
}

func (m *mockTaxonomyRepo) MarkRunFailed(
	_ context.Context,
	runID uuid.UUID,
	tenantID string,
	message string,
	errorCode models.TaxonomyRunFailureCode,
) (*models.TaxonomyRun, error) {
	m.markRunFailedTenant = tenantID
	m.markRunFailedMessage = message
	m.markRunFailedCode = errorCode

	return &models.TaxonomyRun{
		ID:        runID,
		Status:    models.TaxonomyRunStatusFailed,
		Error:     &message,
		ErrorCode: &errorCode,
	}, nil
}

func (m *mockTaxonomyRepo) Heartbeat(_ context.Context, _ uuid.UUID, tenantID string) error {
	m.heartbeatTenant = tenantID

	return nil
}

func (m *mockTaxonomyRepo) GetRunForInternalService(_ context.Context, runID uuid.UUID) (*models.TaxonomyRun, error) {
	if m.internalRun != nil {
		return m.internalRun, nil
	}

	return &models.TaxonomyRun{ID: runID, TenantID: "tenant-1"}, nil
}

func (m *mockTaxonomyRepo) GetRunForTenant(
	_ context.Context,
	runID uuid.UUID,
	tenantID string,
) (*models.TaxonomyRun, error) {
	return &models.TaxonomyRun{ID: runID, TenantID: tenantID}, nil
}

func (m *mockTaxonomyRepo) GetActiveRun(
	_ context.Context,
	_ models.TaxonomyScope,
) (*models.TaxonomyRun, error) {
	return nil, nil
}

func (m *mockTaxonomyRepo) ListRuns(
	_ context.Context,
	_ models.ListTaxonomyRunsFilters,
) ([]models.TaxonomyRun, error) {
	return nil, nil
}

func (m *mockTaxonomyRepo) GetRunInput(
	_ context.Context,
	_ uuid.UUID,
	_ string,
	_ string,
) (*models.TaxonomyRunInputResponse, error) {
	return nil, nil
}

func (m *mockTaxonomyRepo) StoreResultAndActivate(
	_ context.Context,
	_ uuid.UUID,
	_ string,
	_ models.TaxonomyRunResultRequest,
) (*models.TaxonomyRun, error) {
	return nil, nil
}

func (m *mockTaxonomyRepo) GetTree(
	_ context.Context,
	_ uuid.UUID,
	_ string,
) (*models.TaxonomyTreeResponse, error) {
	return nil, nil
}

func (m *mockTaxonomyRepo) RenameNode(
	_ context.Context,
	_ uuid.UUID,
	_ string,
	_ string,
	_ string,
) (*models.TaxonomyNode, error) {
	return nil, nil
}

func (m *mockTaxonomyRepo) RemoveNode(
	_ context.Context,
	_ uuid.UUID,
	_ string,
	_ string,
) (*models.TaxonomyNode, error) {
	return nil, nil
}

func (m *mockTaxonomyRepo) ListNodeRecords(
	_ context.Context,
	_ uuid.UUID,
	_ string,
	_ int,
) ([]models.FeedbackRecord, int, error) {
	return nil, 0, nil
}

func (m *mockTaxonomyRepo) CountNodeRecords(
	_ context.Context,
	runID uuid.UUID,
	tenantID string,
) ([]models.TaxonomyNodeRecordCount, error) {
	m.countNodeRecordsRunID = runID
	m.countNodeRecordsTenant = tenantID

	if m.countNodeRecordsErr != nil {
		return nil, m.countNodeRecordsErr
	}

	return m.countNodeRecords, nil
}

type failingTaxonomyStarter struct{}

func (f failingTaxonomyStarter) StartRun(_ context.Context, _ string) error {
	return errors.New("taxonomy service unavailable")
}

type successfulTaxonomyStarter struct{}

func (s successfulTaxonomyStarter) StartRun(_ context.Context, _ string) error {
	return nil
}

func TestTaxonomyService_StartManualRunUsesDirectoryFallbackLabel(t *testing.T) {
	runID := uuid.MustParse("018e1234-5678-9abc-def0-333333333333")
	blankLabel := "   "
	repo := &mockTaxonomyRepo{
		countRecordCount:    20,
		countEmbeddingCount: 20,
		createRun:           &models.TaxonomyRun{ID: runID, Status: models.TaxonomyRunStatusPending},
		createRunCreated:    true,
	}
	svc := NewTaxonomyService(NewTaxonomyServiceParams{
		Repo:           repo,
		Starter:        successfulTaxonomyStarter{},
		EmbeddingModel: "text-embedding-004",
	})

	_, err := svc.StartManualRun(context.Background(), models.CreateTaxonomyRunRequest{
		TaxonomyScope: models.TaxonomyScope{
			ScopeType: models.TaxonomyScopeTypeDirectory,
			TenantID:  "tenant-1",
		},
		FieldLabel: &blankLabel,
	})
	if err != nil {
		t.Fatalf("StartManualRun() error = %v", err)
	}

	if repo.createRunParams.FieldLabel == nil || *repo.createRunParams.FieldLabel != directoryTaxonomyFieldLabel {
		t.Fatalf("FieldLabel = %v, want %q", repo.createRunParams.FieldLabel, directoryTaxonomyFieldLabel)
	}
}

func TestTaxonomyService_StartManualRunMarksServiceUnavailableFailure(t *testing.T) {
	runID := uuid.MustParse("018e1234-5678-9abc-def0-111111111111")
	repo := &mockTaxonomyRepo{
		countRecordCount:    20,
		countEmbeddingCount: 20,
		createRun:           &models.TaxonomyRun{ID: runID, Status: models.TaxonomyRunStatusPending},
		createRunCreated:    true,
	}
	svc := NewTaxonomyService(NewTaxonomyServiceParams{
		Repo:           repo,
		Starter:        failingTaxonomyStarter{},
		EmbeddingModel: "text-embedding-004",
	})

	result, err := svc.StartManualRun(context.Background(), models.CreateTaxonomyRunRequest{
		TaxonomyScope: models.TaxonomyScope{
			TenantID:   "tenant-1",
			SourceType: "survey",
			SourceID:   "survey-1",
			FieldID:    "question-1",
		},
	})
	if !errors.Is(err, ErrTaxonomyServiceStartFailed) {
		t.Fatalf("StartManualRun() error = %v, want taxonomy service start failure", err)
	}

	if result != nil {
		t.Fatalf("StartManualRun() result = %+v, want nil", result)
	}

	if repo.markRunFailedMessage != "taxonomy service did not accept the run" {
		t.Fatalf("MarkRunFailed message = %q", repo.markRunFailedMessage)
	}

	if repo.markRunRunningTenant != "tenant-1" {
		t.Fatalf("MarkRunRunning tenant = %q, want tenant-1", repo.markRunRunningTenant)
	}

	if repo.markRunFailedTenant != "tenant-1" {
		t.Fatalf("MarkRunFailed tenant = %q, want tenant-1", repo.markRunFailedTenant)
	}

	if repo.markRunFailedCode != models.TaxonomyRunFailureCodeServiceUnavailable {
		t.Fatalf("MarkRunFailed code = %q, want service_unavailable", repo.markRunFailedCode)
	}
}

func TestTaxonomyService_HeartbeatResolvesTenant(t *testing.T) {
	runID := uuid.MustParse("018e1234-5678-9abc-def0-333333333333")
	repo := &mockTaxonomyRepo{internalRun: &models.TaxonomyRun{ID: runID, TenantID: "tenant-7"}}
	svc := NewTaxonomyService(NewTaxonomyServiceParams{Repo: repo})

	if err := svc.Heartbeat(context.Background(), runID); err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}

	if repo.heartbeatTenant != "tenant-7" {
		t.Fatalf("Heartbeat tenant = %q, want tenant-7", repo.heartbeatTenant)
	}
}

func TestTaxonomyService_FailRunDefaultsFailureCode(t *testing.T) {
	runID := uuid.MustParse("018e1234-5678-9abc-def0-222222222222")
	repo := &mockTaxonomyRepo{}
	svc := NewTaxonomyService(NewTaxonomyServiceParams{Repo: repo})

	result, err := svc.FailRun(context.Background(), runID, " generated invalid taxonomy ", "")
	if err != nil {
		t.Fatalf("FailRun() error = %v", err)
	}

	if result == nil || result.ErrorCode == nil {
		t.Fatalf("FailRun() result = %+v, want error code", result)
	}

	if repo.markRunFailedMessage != "generated invalid taxonomy" {
		t.Fatalf("MarkRunFailed message = %q", repo.markRunFailedMessage)
	}

	if repo.markRunFailedTenant != "tenant-1" {
		t.Fatalf("MarkRunFailed tenant = %q, want tenant-1", repo.markRunFailedTenant)
	}

	if *result.ErrorCode != models.TaxonomyRunFailureCodeGenerationFailed {
		t.Fatalf("result error code = %q, want generation_failed", *result.ErrorCode)
	}
}

func TestTaxonomyService_GetNodeRecordCounts(t *testing.T) {
	runID := uuid.MustParse("018e1234-5678-9abc-def0-444444444444")
	nodeID := uuid.MustParse("018e1234-5678-9abc-def0-555555555555")

	t.Run("returns counts and forwards normalized tenant", func(t *testing.T) {
		repo := &mockTaxonomyRepo{
			countNodeRecords: []models.TaxonomyNodeRecordCount{{NodeID: nodeID, RecordCount: 7}},
		}
		svc := NewTaxonomyService(NewTaxonomyServiceParams{Repo: repo})

		result, err := svc.GetNodeRecordCounts(context.Background(), runID, "  tenant-1  ")
		if err != nil {
			t.Fatalf("GetNodeRecordCounts() error = %v", err)
		}

		if len(result.Counts) != 1 || result.Counts[0].RecordCount != 7 {
			t.Fatalf("counts = %+v, want one entry with count 7", result.Counts)
		}

		if repo.countNodeRecordsRunID != runID {
			t.Fatalf("repo run ID = %s, want %s", repo.countNodeRecordsRunID, runID)
		}

		if repo.countNodeRecordsTenant != "tenant-1" {
			t.Fatalf("repo tenant = %q, want trimmed %q", repo.countNodeRecordsTenant, "tenant-1")
		}
	})

	t.Run("rejects empty tenant without hitting repo", func(t *testing.T) {
		repo := &mockTaxonomyRepo{}
		svc := NewTaxonomyService(NewTaxonomyServiceParams{Repo: repo})

		if _, err := svc.GetNodeRecordCounts(context.Background(), runID, "   "); err == nil {
			t.Fatal("GetNodeRecordCounts() = nil error, want validation error for empty tenant")
		}

		if repo.countNodeRecordsTenant != "" {
			t.Fatalf("repo was called with tenant %q; expected no repo call", repo.countNodeRecordsTenant)
		}
	})

	t.Run("propagates repo error", func(t *testing.T) {
		repo := &mockTaxonomyRepo{countNodeRecordsErr: errors.New("boom")}
		svc := NewTaxonomyService(NewTaxonomyServiceParams{Repo: repo})

		if _, err := svc.GetNodeRecordCounts(context.Background(), runID, "tenant-1"); err == nil {
			t.Fatal("GetNodeRecordCounts() = nil error, want propagated repo error")
		}
	})
}
