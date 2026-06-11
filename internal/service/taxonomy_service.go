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
	ErrTaxonomyEmbeddingsNotConfigured = errors.New("taxonomy requires EMBEDDING_MODEL to be configured")
	ErrTaxonomyServiceNotConfigured    = errors.New("taxonomy service is not configured")
	ErrTaxonomyServiceStartFailed      = errors.New("taxonomy service failed to start run")
)

const defaultMinimumTaxonomyEmbeddingCount = 20

type TaxonomyRepository interface {
	ListFieldOptions(ctx context.Context, tenantID, embeddingModel string) ([]models.TaxonomyFieldOption, error)
	CountScopeInput(ctx context.Context, scope models.TaxonomyScope, embeddingModel string) (int, int, *string, error)
	CreateRunIfAvailable(ctx context.Context, params repository.CreateTaxonomyRunParams) (*models.TaxonomyRun, bool, error)
	MarkRunRunning(ctx context.Context, runID uuid.UUID) (*models.TaxonomyRun, error)
	MarkRunFailed(ctx context.Context, runID uuid.UUID, message string) (*models.TaxonomyRun, error)
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

type TaxonomyRunStarter interface {
	StartRun(ctx context.Context, runID string) error
}

type TaxonomyService struct {
	repo                  TaxonomyRepository
	starter               TaxonomyRunStarter
	embeddingModel        string
	minimumEmbeddingCount int
}

type NewTaxonomyServiceParams struct {
	Repo                  TaxonomyRepository
	Starter               TaxonomyRunStarter
	EmbeddingModel        string
	MinimumEmbeddingCount int
}

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
		_, markErr := s.repo.MarkRunFailed(ctx, run.ID, "taxonomy service did not accept the run")
		if markErr != nil {
			return nil, fmt.Errorf("mark taxonomy run failed after start error: %w", markErr)
		}

		return nil, fmt.Errorf("%w: %w", ErrTaxonomyServiceStartFailed, err)
	}

	return &models.CreateTaxonomyRunResponse{Run: *runningRun}, nil
}

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

func (s *TaxonomyService) GetRun(ctx context.Context, runID uuid.UUID) (*models.TaxonomyRun, error) {
	return s.repo.GetRun(ctx, runID)
}

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
		return nil, err
	}

	return s.repo.GetTree(ctx, run.ID)
}

func (s *TaxonomyService) GetTree(ctx context.Context, runID uuid.UUID) (*models.TaxonomyTreeResponse, error) {
	return s.repo.GetTree(ctx, runID)
}

func (s *TaxonomyService) GetRunInput(
	ctx context.Context,
	runID uuid.UUID,
) (*models.TaxonomyRunInputResponse, error) {
	if s.embeddingModel == "" {
		return nil, ErrTaxonomyEmbeddingsNotConfigured
	}

	return s.repo.GetRunInput(ctx, runID, s.embeddingModel)
}

func (s *TaxonomyService) CompleteRun(
	ctx context.Context,
	runID uuid.UUID,
	req models.TaxonomyRunResultRequest,
) (*models.TaxonomyRun, error) {
	return s.repo.StoreResultAndActivate(ctx, runID, req)
}

func (s *TaxonomyService) FailRun(
	ctx context.Context,
	runID uuid.UUID,
	message string,
) (*models.TaxonomyRun, error) {
	sanitized := strings.TrimSpace(message)
	if sanitized == "" {
		sanitized = "taxonomy run failed"
	}

	return s.repo.MarkRunFailed(ctx, runID, sanitized)
}

func (s *TaxonomyService) RenameNode(
	ctx context.Context,
	nodeID uuid.UUID,
	req models.RenameTaxonomyNodeRequest,
) (*models.TaxonomyNode, error) {
	tenantID, err := normalizeRequiredTenantIDValue(req.TenantID)
	if err != nil {
		return nil, err
	}

	actorID, err := normalizeRequiredIdentifier("actor_id", req.ActorID, maxIdentifierLength)
	if err != nil {
		return nil, err
	}

	label := strings.TrimSpace(req.Label)
	if label == "" {
		return nil, huberrors.NewValidationError("label", "label is required and cannot be empty")
	}

	return s.repo.RenameNode(ctx, nodeID, tenantID, actorID, label)
}

func (s *TaxonomyService) RemoveNode(
	ctx context.Context,
	nodeID uuid.UUID,
	filters models.RemoveTaxonomyNodeFilters,
) (*models.TaxonomyNode, error) {
	tenantID, err := normalizeRequiredTenantIDValue(filters.TenantID)
	if err != nil {
		return nil, err
	}

	actorID, err := normalizeRequiredIdentifier("actor_id", filters.ActorID, maxIdentifierLength)
	if err != nil {
		return nil, err
	}

	return s.repo.RemoveNode(ctx, nodeID, tenantID, actorID)
}

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

	sourceType, err := normalizeRequiredIdentifier("source_type", scope.SourceType, maxIdentifierLength)
	if err != nil {
		return models.TaxonomyScope{}, err
	}

	sourceID, err := normalizeRequiredIdentifier("source_id", scope.SourceID, maxIdentifierLength)
	if err != nil {
		return models.TaxonomyScope{}, err
	}

	fieldID, err := normalizeRequiredIdentifier("field_id", scope.FieldID, maxIdentifierLength)
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
