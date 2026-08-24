package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/formbricks/hub/internal/jsonutil"
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
	markRunFailedMetrics json.RawMessage
	markRunFailedCalls   int
	heartbeatTenant      string
	storeResultTenant    string
	storeResultRequest   models.TaxonomyRunResultRequest
	storeResultRun       *models.TaxonomyRun
	storeResultCalls     int
	selectedRecordIDs    []uuid.UUID
	selectedRecordIDsErr error

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
	metrics json.RawMessage,
) (*models.TaxonomyRun, error) {
	m.markRunFailedCalls++
	m.markRunFailedTenant = tenantID
	m.markRunFailedMessage = message
	m.markRunFailedCode = errorCode
	m.markRunFailedMetrics = metrics

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

func (m *mockTaxonomyRepo) GetRunInputRecordIDs(
	_ context.Context,
	_ uuid.UUID,
	_ string,
	_ string,
) ([]uuid.UUID, error) {
	return m.selectedRecordIDs, m.selectedRecordIDsErr
}

func (m *mockTaxonomyRepo) StoreResultAndActivate(
	_ context.Context,
	runID uuid.UUID,
	tenantID string,
	req models.TaxonomyRunResultRequest,
) (*models.TaxonomyRun, error) {
	m.storeResultCalls++
	m.storeResultTenant = tenantID
	m.storeResultRequest = req

	if m.storeResultRun != nil {
		return m.storeResultRun, nil
	}

	return &models.TaxonomyRun{ID: runID, TenantID: tenantID, Status: models.TaxonomyRunStatusSucceeded}, nil
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

func TestTaxonomyService_StartManualRunRequiresNinetyPercentEmbeddingCoverage(t *testing.T) {
	repo := &mockTaxonomyRepo{countRecordCount: 100, countEmbeddingCount: 89}
	svc := NewTaxonomyService(NewTaxonomyServiceParams{
		Repo:           repo,
		Starter:        successfulTaxonomyStarter{},
		EmbeddingModel: "text-embedding-004",
	})

	result, err := svc.StartManualRun(context.Background(), models.CreateTaxonomyRunRequest{
		TaxonomyScope: models.TaxonomyScope{
			TenantID:   "tenant-1",
			SourceType: "survey",
			FieldID:    "question-1",
		},
	})
	if err == nil {
		t.Fatal("StartManualRun() error = nil, want embedding coverage validation error")
	}

	if result != nil {
		t.Fatalf("StartManualRun() result = %+v, want nil", result)
	}

	if repo.createRun != nil || len(repo.createRunParams.Params) != 0 {
		t.Fatal("CreateRunIfAvailable() was called for insufficient embedding coverage")
	}
}

func TestTaxonomyService_StartManualRunMeasuresCoverageAgainstTheSelectedCap(t *testing.T) {
	runID := uuid.MustParse("018e1234-5678-9abc-def0-111111111113")
	repo := &mockTaxonomyRepo{
		countRecordCount:    20_000,
		countEmbeddingCount: 10_000,
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
			TenantID:   "tenant-1",
			SourceType: "survey",
			FieldID:    "question-1",
		},
	})
	if err != nil {
		t.Fatalf("StartManualRun() error = %v", err)
	}
}

func TestTaxonomyService_StartManualRunStoresBoundedSelectionContract(t *testing.T) {
	runID := uuid.MustParse("018e1234-5678-9abc-def0-111111111112")
	repo := &mockTaxonomyRepo{
		countRecordCount:    12_000,
		countEmbeddingCount: 11_000,
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
			TenantID:   "tenant-1",
			SourceType: "survey",
			FieldID:    "question-1",
		},
	})
	if err != nil {
		t.Fatalf("StartManualRun() error = %v", err)
	}

	var params struct {
		InputSelection struct {
			EligibleCount          int    `json:"eligible_count"`
			EmbeddingEligibleCount int    `json:"embedding_eligible_count"`
			SelectedCount          int    `json:"selected_count"`
			SelectionCap           int    `json:"selection_cap"`
			TruncationFlag         bool   `json:"truncation_flag"`
			SelectionStrategy      string `json:"selection_strategy"`
		} `json:"input_selection"`
	}
	if err := json.Unmarshal(repo.createRunParams.Params, &params); err != nil {
		t.Fatalf("unmarshal run params: %v", err)
	}

	selection := params.InputSelection
	if selection.EligibleCount != 12_000 || selection.EmbeddingEligibleCount != 11_000 ||
		selection.SelectedCount != 10_000 || selection.SelectionCap != 10_000 ||
		!selection.TruncationFlag || selection.SelectionStrategy != "most_recent" {
		t.Fatalf("input selection = %+v", selection)
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

	result, err := svc.FailRun(context.Background(), runID, models.TaxonomyRunFailedRequest{
		Error: " generated invalid taxonomy ",
	})
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

func TestTaxonomyService_FailRunPersistsSafeDiagnosticsInMetrics(t *testing.T) {
	runID := uuid.MustParse("018e1234-5678-9abc-def0-222222222223")
	attempts := 2
	tokens := int64(42)
	repo := &mockTaxonomyRepo{}
	svc := NewTaxonomyService(NewTaxonomyServiceParams{Repo: repo})

	_, err := svc.FailRun(context.Background(), runID, models.TaxonomyRunFailedRequest{
		Error:     "provider request failed",
		ErrorCode: models.TaxonomyRunFailureCodeServiceUnavailable,
		Diagnostics: &models.TaxonomyRunFailureDiagnostics{
			Phase:         "taxonomy_generation",
			FailureReason: "provider_timeout",
			Provider:      "vertex",
			Model:         "gemini-2.5-flash",
			LLMAttempts:   &attempts,
			TotalTokens:   &tokens,
		},
	})
	if err != nil {
		t.Fatalf("FailRun() error = %v", err)
	}

	var persisted map[string]models.TaxonomyRunFailureDiagnostics
	if err := json.Unmarshal(repo.markRunFailedMetrics, &persisted); err != nil {
		t.Fatalf("unmarshal persisted diagnostics: %v", err)
	}

	diagnostics := persisted["failure_diagnostics"]
	if diagnostics.FailureReason != "provider_timeout" || diagnostics.Provider != "vertex" {
		t.Fatalf("persisted diagnostics = %+v", diagnostics)
	}

	if diagnostics.TotalTokens == nil || *diagnostics.TotalTokens != 42 {
		t.Fatalf("persisted total tokens = %v", diagnostics.TotalTokens)
	}
}

func TestTaxonomyService_FailRunIsIdempotentForIdenticalTerminalCallback(t *testing.T) {
	runID := uuid.MustParse("018e1234-5678-9abc-def0-222222222224")
	message := "provider request failed"
	code := models.TaxonomyRunFailureCodeServiceUnavailable
	diagnostics := &models.TaxonomyRunFailureDiagnostics{
		Phase:            "taxonomy_generation",
		FailureReason:    "provider_timeout",
		GenerationStep:   "hierarchy_plan",
		ValidationReason: "none",
		RecoveryAction:   "provider_retry",
	}

	metrics, err := marshalFailureDiagnostics(diagnostics)
	if err != nil {
		t.Fatalf("marshal diagnostics: %v", err)
	}

	repo := &mockTaxonomyRepo{internalRun: &models.TaxonomyRun{
		ID: runID, TenantID: "tenant-1", Status: models.TaxonomyRunStatusFailed,
		Error: &message, ErrorCode: &code, Metrics: metrics,
	}}
	svc := NewTaxonomyService(NewTaxonomyServiceParams{Repo: repo})

	run, err := svc.FailRun(context.Background(), runID, models.TaxonomyRunFailedRequest{
		Error: message, ErrorCode: code, Diagnostics: diagnostics,
	})
	if err != nil {
		t.Fatalf("FailRun() error = %v", err)
	}

	if run != repo.internalRun {
		t.Fatal("FailRun() did not return the existing terminal run")
	}

	if repo.markRunFailedCalls != 0 {
		t.Fatalf("MarkRunFailed() calls = %d, want 0", repo.markRunFailedCalls)
	}
}

func TestTaxonomyService_FailRunNormalizesEmptyTerminalMetrics(t *testing.T) {
	runID := uuid.MustParse("018e1234-5678-9abc-def0-222222222226")
	message := "provider request failed"
	code := models.TaxonomyRunFailureCodeServiceUnavailable
	repo := &mockTaxonomyRepo{internalRun: &models.TaxonomyRun{
		ID: runID, TenantID: "tenant-1", Status: models.TaxonomyRunStatusFailed,
		Error: &message, ErrorCode: &code, Metrics: json.RawMessage(`{}`),
	}}
	svc := NewTaxonomyService(NewTaxonomyServiceParams{Repo: repo})

	run, err := svc.FailRun(context.Background(), runID, models.TaxonomyRunFailedRequest{
		Error: message, ErrorCode: code,
	})
	if err != nil {
		t.Fatalf("FailRun() error = %v", err)
	}

	if run != repo.internalRun {
		t.Fatal("FailRun() did not return the existing terminal run")
	}

	if repo.markRunFailedCalls != 0 {
		t.Fatalf("MarkRunFailed() calls = %d, want 0", repo.markRunFailedCalls)
	}
}

func TestTaxonomyService_CompleteRunAddsCanonicalDigestAndIsOrderIndependent(t *testing.T) {
	runID := uuid.MustParse("018e1234-5678-9abc-def0-222222222225")
	firstID := uuid.MustParse("018e1234-5678-9abc-def0-100000000001")
	secondID := uuid.MustParse("018e1234-5678-9abc-def0-100000000002")
	firstLabel, secondLabel := "First", "Second"
	req := models.TaxonomyRunResultRequest{
		Metrics: json.RawMessage(`{"duration_seconds":12}`),
		Clusters: []models.TaxonomyResultCluster{
			{ClusterKey: 2, Label: &secondLabel, Size: 1},
			{ClusterKey: 1, Label: &firstLabel, Size: 1},
		},
		Memberships: []models.TaxonomyResultMembership{
			{ClusterKey: 2, FeedbackRecordID: secondID},
			{ClusterKey: 1, FeedbackRecordID: firstID},
		},
		Nodes: []models.TaxonomyResultNode{
			{NodeKey: "root", NodeType: models.TaxonomyNodeTypeRoot, Label: "Feedback", Level: 0},
			{NodeKey: "level-2", ParentKey: new("root"), NodeType: models.TaxonomyNodeTypeBranch, Label: "Experience", Level: 1},
			{NodeKey: "level-3", ParentKey: new("level-2"), NodeType: models.TaxonomyNodeTypeBranch, Label: "Product", Level: 2},
			{NodeKey: "level-4", ParentKey: new("level-3"), NodeType: models.TaxonomyNodeTypeBranch, Label: "Specific Topics", Level: 3},
			{NodeKey: "leaf-2", ParentKey: new("level-4"), ClusterKey: new(2), NodeType: models.TaxonomyNodeTypeLeaf, Label: secondLabel, Level: 4},
			{
				NodeKey: "leaf-1", ParentKey: new("level-4"), ClusterKey: new(1),
				NodeType: models.TaxonomyNodeTypeLeaf, Label: firstLabel, Level: 4, SortOrder: 1,
			},
		},
	}

	reordered := req
	reordered.Clusters = []models.TaxonomyResultCluster{req.Clusters[1], req.Clusters[0]}
	reordered.Memberships = []models.TaxonomyResultMembership{req.Memberships[1], req.Memberships[0]}
	reordered.Nodes = []models.TaxonomyResultNode{
		req.Nodes[5], req.Nodes[4], req.Nodes[3], req.Nodes[2], req.Nodes[1], req.Nodes[0],
	}

	firstDigest, err := canonicalTaxonomyResultDigest(req)
	if err != nil {
		t.Fatalf("canonicalTaxonomyResultDigest() error = %v", err)
	}

	secondDigest, err := canonicalTaxonomyResultDigest(reordered)
	if err != nil {
		t.Fatalf("canonicalTaxonomyResultDigest(reordered) error = %v", err)
	}

	if firstDigest != secondDigest {
		t.Fatalf("digest changed with ordering: %q != %q", firstDigest, secondDigest)
	}

	repo := &mockTaxonomyRepo{
		internalRun: &models.TaxonomyRun{
			ID: runID, TenantID: "tenant-1", Status: models.TaxonomyRunStatusRunning,
		},
		selectedRecordIDs: []uuid.UUID{firstID, secondID},
	}
	svc := NewTaxonomyService(NewTaxonomyServiceParams{Repo: repo})

	run, err := svc.CompleteRun(context.Background(), runID, req)
	if err != nil {
		t.Fatalf("CompleteRun() error = %v", err)
	}

	if run.Status != models.TaxonomyRunStatusSucceeded {
		t.Fatalf("CompleteRun() status = %q", run.Status)
	}

	if repo.storeResultCalls != 1 || repo.storeResultTenant != "tenant-1" {
		t.Fatalf("StoreResultAndActivate() calls=%d tenant=%q", repo.storeResultCalls, repo.storeResultTenant)
	}

	if got := jsonutil.ObjectString(repo.storeResultRequest.Metrics, "result_digest"); got != firstDigest {
		t.Fatalf("persisted result digest = %q, want %q", got, firstDigest)
	}

	repo.internalRun = &models.TaxonomyRun{
		ID: runID, TenantID: "tenant-1", Status: models.TaxonomyRunStatusSucceeded,
		Metrics: repo.storeResultRequest.Metrics,
	}
	if _, err := svc.CompleteRun(context.Background(), runID, reordered); err != nil {
		t.Fatalf("idempotent CompleteRun() error = %v", err)
	}

	if repo.storeResultCalls != 1 {
		t.Fatalf("StoreResultAndActivate() calls after retry = %d, want 1", repo.storeResultCalls)
	}
}

func TestTaxonomyService_CompleteRunRejectsIncompleteSelectedInputCoverage(t *testing.T) {
	runID := uuid.MustParse("018e1234-5678-9abc-def0-222222222227")
	selectedID := uuid.MustParse("018e1234-5678-9abc-def0-100000000003")
	missingID := uuid.MustParse("018e1234-5678-9abc-def0-100000000004")
	repo := &mockTaxonomyRepo{
		internalRun: &models.TaxonomyRun{
			ID: runID, TenantID: "tenant-1", Status: models.TaxonomyRunStatusRunning,
		},
		selectedRecordIDs: []uuid.UUID{selectedID, missingID},
	}
	svc := NewTaxonomyService(NewTaxonomyServiceParams{Repo: repo})

	_, err := svc.CompleteRun(context.Background(), runID, validTaxonomyServiceResult(selectedID))
	if err == nil {
		t.Fatal("CompleteRun() error = nil, want selected-input coverage validation error")
	}

	if repo.storeResultCalls != 0 {
		t.Fatalf("StoreResultAndActivate() calls = %d, want 0", repo.storeResultCalls)
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

func validTaxonomyServiceResult(feedbackRecordID uuid.UUID) models.TaxonomyRunResultRequest {
	label := "Login"

	return models.TaxonomyRunResultRequest{
		Clusters: []models.TaxonomyResultCluster{{ClusterKey: 1, Label: &label, Size: 1}},
		Memberships: []models.TaxonomyResultMembership{
			{ClusterKey: 1, FeedbackRecordID: feedbackRecordID},
		},
		Nodes: []models.TaxonomyResultNode{
			{NodeKey: "root", NodeType: models.TaxonomyNodeTypeRoot, Label: "Feedback", Level: 0},
			{
				NodeKey: "level-2", ParentKey: new("root"), NodeType: models.TaxonomyNodeTypeBranch,
				Label: "Experience", Level: 1,
			},
			{
				NodeKey: "level-3", ParentKey: new("level-2"), NodeType: models.TaxonomyNodeTypeBranch,
				Label: "Account Access", Level: 2,
			},
			{
				NodeKey: "level-4", ParentKey: new("level-3"), NodeType: models.TaxonomyNodeTypeBranch,
				Label: "Authentication", Level: 3,
			},
			{
				NodeKey: "leaf", ParentKey: new("level-4"), ClusterKey: new(1),
				NodeType: models.TaxonomyNodeTypeLeaf, Label: label, Level: 4,
			},
		},
	}
}
