package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/repository"
)

var (
	// ErrTaxonomyEmbeddingsNotConfigured is returned when Hub embeddings are not configured.
	ErrTaxonomyEmbeddingsNotConfigured = errors.New("taxonomy requires EMBEDDING_MODEL to be configured")
	// ErrTaxonomyServiceNotConfigured is returned when the taxonomy compute service is unavailable.
	ErrTaxonomyServiceNotConfigured = errors.New("taxonomy service is not configured")
	// ErrTaxonomyServiceStartFailed is returned when the taxonomy compute service rejects a run.
	ErrTaxonomyServiceStartFailed = errors.New("taxonomy service failed to start run")
)

const defaultMinimumTaxonomyEmbeddingCount = 20

// TaxonomyRepository persists taxonomy run state and generated artifacts.
type TaxonomyRepository interface { //nolint:interfacebloat // taxonomy service coordinates one cohesive repository boundary.
	ListFieldOptions(ctx context.Context, tenantID, embeddingModel string) ([]models.TaxonomyFieldOption, error)
	CountScopeInput(ctx context.Context, scope models.TaxonomyScope, embeddingModel string) (int, int, *string, error)
	CreateRunIfAvailable(ctx context.Context, params repository.CreateTaxonomyRunParams) (*models.TaxonomyRun, bool, error)
	MarkRunRunning(ctx context.Context, runID uuid.UUID) (*models.TaxonomyRun, error)
	MarkRunFailed(
		ctx context.Context,
		runID uuid.UUID,
		message string,
		errorCode models.TaxonomyRunFailureCode,
	) (*models.TaxonomyRun, error)
	GetRun(ctx context.Context, runID uuid.UUID) (*models.TaxonomyRun, error)
	GetActiveRun(ctx context.Context, scope models.TaxonomyScope) (*models.TaxonomyRun, error)
	ListRuns(ctx context.Context, filters models.ListTaxonomyRunsFilters) ([]models.TaxonomyRun, error)
	GetRunInput(ctx context.Context, runID uuid.UUID, embeddingModel string) (*models.TaxonomyRunInputResponse, error)
	StoreResultAndActivate(
		ctx context.Context,
		runID uuid.UUID,
		req models.TaxonomyRunResultRequest,
	) (*models.TaxonomyRun, error)
	GetTree(ctx context.Context, runID uuid.UUID) (*models.TaxonomyTreeResponse, error)
	RenameNode(ctx context.Context, nodeID uuid.UUID, tenantID, actorID, label string) (*models.TaxonomyNode, error)
	RemoveNode(ctx context.Context, nodeID uuid.UUID, tenantID, actorID string) (*models.TaxonomyNode, error)
	ListNodeRecords(ctx context.Context, nodeID uuid.UUID, tenantID string, limit int) ([]models.FeedbackRecord, int, error)
}

// TaxonomyRunStarter starts asynchronous taxonomy compute work.
type TaxonomyRunStarter interface {
	StartRun(ctx context.Context, runID string) error
}

// TaxonomyService coordinates taxonomy run lifecycle and edits.
type TaxonomyService struct {
	repo                  TaxonomyRepository
	starter               TaxonomyRunStarter
	embeddingModel        string
	minimumEmbeddingCount int
}

// NewTaxonomyServiceParams configures a TaxonomyService.
type NewTaxonomyServiceParams struct {
	Repo                  TaxonomyRepository
	Starter               TaxonomyRunStarter
	EmbeddingModel        string
	MinimumEmbeddingCount int
}

// NewTaxonomyService creates a taxonomy application service.
func NewTaxonomyService(params NewTaxonomyServiceParams) *TaxonomyService {
	minimumEmbeddingCount := params.MinimumEmbeddingCount
	if minimumEmbeddingCount <= 0 {
		minimumEmbeddingCount = defaultMinimumTaxonomyEmbeddingCount
	}

	return &TaxonomyService{
		repo:                  params.Repo,
		starter:               params.Starter,
		embeddingModel:        strings.TrimSpace(params.EmbeddingModel),
		minimumEmbeddingCount: minimumEmbeddingCount,
	}
}

// ListFieldOptions returns feedback fields that can run taxonomy generation.
func (s *TaxonomyService) ListFieldOptions(
	ctx context.Context,
	tenantID string,
) (*models.TaxonomyFieldsResponse, error) {
	if s.embeddingModel == "" {
		return nil, ErrTaxonomyEmbeddingsNotConfigured
	}

	normalizedTenantID, err := normalizeRequiredTenantIDValue(tenantID)
	if err != nil {
		return nil, err
	}

	options, err := s.repo.ListFieldOptions(ctx, normalizedTenantID, s.embeddingModel)
	if err != nil {
		return nil, fmt.Errorf("list taxonomy field options: %w", err)
	}

	return &models.TaxonomyFieldsResponse{Data: options}, nil
}

// StartManualRun creates and starts a manual taxonomy generation run.
func (s *TaxonomyService) StartManualRun(
	ctx context.Context,
	req models.CreateTaxonomyRunRequest,
) (*models.CreateTaxonomyRunResponse, error) {
	if s.embeddingModel == "" {
		return nil, ErrTaxonomyEmbeddingsNotConfigured
	}

	if s.starter == nil {
		return nil, ErrTaxonomyServiceNotConfigured
	}

	scope, err := normalizeTaxonomyScope(req.TaxonomyScope)
	if err != nil {
		return nil, err
	}

	recordCount, embeddingCount, discoveredFieldLabel, err := s.repo.CountScopeInput(ctx, scope, s.embeddingModel)
	if err != nil {
		return nil, fmt.Errorf("count taxonomy input: %w", err)
	}

	if recordCount == 0 {
		return nil, huberrors.NewValidationError("field_id", "no text feedback records found for this field scope")
	}

	if embeddingCount < s.minimumEmbeddingCount {
		return nil, huberrors.NewValidationError(
			"field_id",
			fmt.Sprintf("at least %d embedded text feedback records are required; found %d", s.minimumEmbeddingCount, embeddingCount),
		)
	}

	fieldLabel := req.FieldLabel
	if fieldLabel == nil {
		fieldLabel = discoveredFieldLabel
	}

	params := taxonomyRunParams(req.ActorID, s.embeddingModel)

	run, created, err := s.repo.CreateRunIfAvailable(ctx, repository.CreateTaxonomyRunParams{
		TaxonomyScope:  scope,
		FieldLabel:     fieldLabel,
		Params:         params,
		RecordCount:    recordCount,
		EmbeddingCount: embeddingCount,
	})
	if err != nil {
		return nil, fmt.Errorf("create taxonomy run: %w", err)
	}

	if !created {
		return &models.CreateTaxonomyRunResponse{Run: *run, InProgress: true}, nil
	}

	runningRun, err := s.repo.MarkRunRunning(ctx, run.ID)
	if err != nil {
		return nil, fmt.Errorf("mark taxonomy run running: %w", err)
	}

	if err := s.starter.StartRun(ctx, run.ID.String()); err != nil {
		_, markErr := s.repo.MarkRunFailed(
			ctx,
			run.ID,
			"taxonomy service did not accept the run",
			models.TaxonomyRunFailureCodeServiceUnavailable,
		)
		if markErr != nil {
			return nil, fmt.Errorf("mark taxonomy run failed after start error: %w", markErr)
		}

		return nil, fmt.Errorf("%w: %w", ErrTaxonomyServiceStartFailed, err)
	}

	return &models.CreateTaxonomyRunResponse{Run: *runningRun}, nil
}

// ListRuns returns taxonomy run history for a scoped tenant.
func (s *TaxonomyService) ListRuns(
	ctx context.Context,
	filters models.ListTaxonomyRunsFilters,
) (*models.ListTaxonomyRunsResponse, error) {
	normalizedTenantID, err := normalizeRequiredTenantIDValue(filters.TenantID)
	if err != nil {
		return nil, err
	}

	filters.TenantID = normalizedTenantID

	runs, err := s.repo.ListRuns(ctx, filters)
	if err != nil {
		return nil, fmt.Errorf("list taxonomy runs: %w", err)
	}

	return &models.ListTaxonomyRunsResponse{Data: runs}, nil
}

// GetRun returns a taxonomy run by ID.
func (s *TaxonomyService) GetRun(ctx context.Context, runID uuid.UUID) (*models.TaxonomyRun, error) {
	run, err := s.repo.GetRun(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("get taxonomy run: %w", err)
	}

	return run, nil
}

// GetActiveTree returns the active taxonomy tree for a field scope.
func (s *TaxonomyService) GetActiveTree(
	ctx context.Context,
	scope models.TaxonomyScope,
) (*models.TaxonomyTreeResponse, error) {
	normalizedScope, err := normalizeTaxonomyScope(scope)
	if err != nil {
		return nil, err
	}

	run, err := s.repo.GetActiveRun(ctx, normalizedScope)
	if err != nil {
		return nil, fmt.Errorf("get active taxonomy run: %w", err)
	}

	tree, err := s.repo.GetTree(ctx, run.ID)
	if err != nil {
		return nil, fmt.Errorf("get active taxonomy tree: %w", err)
	}

	return tree, nil
}

// GetTree returns a taxonomy tree by run ID.
func (s *TaxonomyService) GetTree(ctx context.Context, runID uuid.UUID) (*models.TaxonomyTreeResponse, error) {
	tree, err := s.repo.GetTree(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("get taxonomy tree: %w", err)
	}

	return tree, nil
}

// GetRunInput returns feedback text and embeddings for the taxonomy service.
func (s *TaxonomyService) GetRunInput(
	ctx context.Context,
	runID uuid.UUID,
) (*models.TaxonomyRunInputResponse, error) {
	if s.embeddingModel == "" {
		return nil, ErrTaxonomyEmbeddingsNotConfigured
	}

	input, err := s.repo.GetRunInput(ctx, runID, s.embeddingModel)
	if err != nil {
		return nil, fmt.Errorf("get taxonomy run input: %w", err)
	}

	return input, nil
}

// CompleteRun stores taxonomy output and activates the successful run.
func (s *TaxonomyService) CompleteRun(
	ctx context.Context,
	runID uuid.UUID,
	req models.TaxonomyRunResultRequest,
) (*models.TaxonomyRun, error) {
	run, err := s.repo.StoreResultAndActivate(ctx, runID, req)
	if err != nil {
		return nil, fmt.Errorf("complete taxonomy run: %w", err)
	}

	return run, nil
}

// FailRun records a taxonomy run failure.
func (s *TaxonomyService) FailRun(
	ctx context.Context,
	runID uuid.UUID,
	message string,
	errorCode models.TaxonomyRunFailureCode,
) (*models.TaxonomyRun, error) {
	sanitized, normalizedCode := normalizeRunFailure(message, errorCode)

	run, err := s.repo.MarkRunFailed(ctx, runID, sanitized, normalizedCode)
	if err != nil {
		return nil, fmt.Errorf("fail taxonomy run: %w", err)
	}

	return run, nil
}

func normalizeRunFailure(
	message string,
	errorCode models.TaxonomyRunFailureCode,
) (string, models.TaxonomyRunFailureCode) {
	sanitized := strings.TrimSpace(message)
	if sanitized == "" {
		sanitized = "taxonomy run failed"
	}

	if !knownTaxonomyFailureCode(errorCode) {
		errorCode = models.TaxonomyRunFailureCodeGenerationFailed
	}

	return sanitized, errorCode
}

func knownTaxonomyFailureCode(errorCode models.TaxonomyRunFailureCode) bool {
	switch errorCode {
	case models.TaxonomyRunFailureCodeInsufficientData,
		models.TaxonomyRunFailureCodeServiceUnavailable,
		models.TaxonomyRunFailureCodeGenerationFailed,
		models.TaxonomyRunFailureCodeInvalidOutput,
		models.TaxonomyRunFailureCodeInternalError:
		return true
	default:
		return false
	}
}

// RenameNode renames a taxonomy node.
func (s *TaxonomyService) RenameNode(
	ctx context.Context,
	nodeID uuid.UUID,
	req models.RenameTaxonomyNodeRequest,
) (*models.TaxonomyNode, error) {
	tenantID, err := normalizeRequiredTenantIDValue(req.TenantID)
	if err != nil {
		return nil, err
	}

	actorID, err := normalizeRequiredIdentifier("actor_id", req.ActorID)
	if err != nil {
		return nil, err
	}

	label := strings.TrimSpace(req.Label)
	if label == "" {
		return nil, huberrors.NewValidationError("label", "label is required and cannot be empty")
	}

	node, err := s.repo.RenameNode(ctx, nodeID, tenantID, actorID, label)
	if err != nil {
		return nil, fmt.Errorf("rename taxonomy node: %w", err)
	}

	return node, nil
}

// RemoveNode soft-removes a taxonomy node.
func (s *TaxonomyService) RemoveNode(
	ctx context.Context,
	nodeID uuid.UUID,
	filters models.RemoveTaxonomyNodeFilters,
) (*models.TaxonomyNode, error) {
	tenantID, err := normalizeRequiredTenantIDValue(filters.TenantID)
	if err != nil {
		return nil, err
	}

	actorID, err := normalizeRequiredIdentifier("actor_id", filters.ActorID)
	if err != nil {
		return nil, err
	}

	node, err := s.repo.RemoveNode(ctx, nodeID, tenantID, actorID)
	if err != nil {
		return nil, fmt.Errorf("remove taxonomy node: %w", err)
	}

	return node, nil
}

// ListNodeRecords returns feedback records assigned to a taxonomy node.
func (s *TaxonomyService) ListNodeRecords(
	ctx context.Context,
	nodeID uuid.UUID,
	filters models.TaxonomyNodeRecordsFilters,
) (*models.TaxonomyNodeRecordsResponse, error) {
	tenantID, err := normalizeRequiredTenantIDValue(filters.TenantID)
	if err != nil {
		return nil, err
	}

	records, limit, err := s.repo.ListNodeRecords(ctx, nodeID, tenantID, filters.Limit)
	if err != nil {
		return nil, fmt.Errorf("list taxonomy node records: %w", err)
	}

	return &models.TaxonomyNodeRecordsResponse{Data: records, Limit: limit}, nil
}

func normalizeTaxonomyScope(scope models.TaxonomyScope) (models.TaxonomyScope, error) {
	tenantID, err := normalizeRequiredTenantIDValue(scope.TenantID)
	if err != nil {
		return models.TaxonomyScope{}, err
	}

	sourceType, err := normalizeRequiredIdentifier("source_type", scope.SourceType)
	if err != nil {
		return models.TaxonomyScope{}, err
	}

	sourceID, err := normalizeRequiredIdentifier("source_id", scope.SourceID)
	if err != nil {
		return models.TaxonomyScope{}, err
	}

	fieldID, err := normalizeRequiredIdentifier("field_id", scope.FieldID)
	if err != nil {
		return models.TaxonomyScope{}, err
	}

	return models.TaxonomyScope{
		TenantID:   tenantID,
		SourceType: sourceType,
		SourceID:   sourceID,
		FieldID:    fieldID,
	}, nil
}

func taxonomyRunParams(actorID *string, embeddingModel string) json.RawMessage {
	params := map[string]string{
		"trigger":         "manual",
		"embedding_model": embeddingModel,
	}

	if actorID != nil && strings.TrimSpace(*actorID) != "" {
		params["requested_by"] = strings.TrimSpace(*actorID)
	}

	raw, err := json.Marshal(params)
	if err != nil {
		return json.RawMessage(`{"trigger":"manual"}`)
	}

	return raw
}
