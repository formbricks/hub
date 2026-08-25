package service

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/jsonutil"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/observability"
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

const (
	minimumTaxonomyEmbeddingCoverage = 0.90
	percentageScale                  = 100.0
)

const directoryTaxonomyFieldLabel = "All feedback"

// TaxonomyRepository persists taxonomy run state and generated artifacts.
type TaxonomyRepository interface { //nolint:interfacebloat // taxonomy service coordinates one cohesive repository boundary.
	ListFieldOptions(ctx context.Context, tenantID, embeddingModel string) ([]models.TaxonomyFieldOption, error)
	CountScopeInput(ctx context.Context, scope models.TaxonomyScope, embeddingModel string) (int, int, *string, error)
	CreateRunIfAvailable(ctx context.Context, params repository.CreateTaxonomyRunParams) (*models.TaxonomyRun, bool, error)
	MarkRunRunning(ctx context.Context, runID uuid.UUID, tenantID string) (*models.TaxonomyRun, error)
	MarkRunFailed(
		ctx context.Context,
		runID uuid.UUID,
		tenantID string,
		message string,
		errorCode models.TaxonomyRunFailureCode,
		metrics json.RawMessage,
	) (*models.TaxonomyRun, error)
	Heartbeat(ctx context.Context, runID uuid.UUID, tenantID string) error
	GetRunForInternalService(ctx context.Context, runID uuid.UUID) (*models.TaxonomyRun, error)
	GetRunForTenant(ctx context.Context, runID uuid.UUID, tenantID string) (*models.TaxonomyRun, error)
	GetActiveRun(ctx context.Context, scope models.TaxonomyScope) (*models.TaxonomyRun, error)
	ListRuns(ctx context.Context, filters models.ListTaxonomyRunsFilters) ([]models.TaxonomyRun, error)
	GetRunInput(
		ctx context.Context,
		runID uuid.UUID,
		tenantID string,
		embeddingModel string,
	) (*models.TaxonomyRunInputResponse, error)
	GetRunInputRecordIDs(
		ctx context.Context,
		runID uuid.UUID,
		tenantID string,
	) ([]uuid.UUID, error)
	StoreResultAndActivate(
		ctx context.Context,
		runID uuid.UUID,
		tenantID string,
		req models.TaxonomyRunResultRequest,
	) (*models.TaxonomyRun, error)
	GetTree(ctx context.Context, runID uuid.UUID, tenantID string) (*models.TaxonomyTreeResponse, error)
	RenameNode(ctx context.Context, nodeID uuid.UUID, tenantID, actorID, label string) (*models.TaxonomyNode, error)
	RemoveNode(ctx context.Context, nodeID uuid.UUID, tenantID, actorID string) (*models.TaxonomyNode, error)
	ListNodeRecords(ctx context.Context, nodeID uuid.UUID, tenantID string, limit int) ([]models.FeedbackRecord, int, error)
	CountNodeRecords(ctx context.Context, runID uuid.UUID, tenantID string) ([]models.TaxonomyNodeRecordCount, error)
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
	metrics               observability.TaxonomyMetrics
}

// NewTaxonomyServiceParams configures a TaxonomyService.
type NewTaxonomyServiceParams struct {
	Repo                  TaxonomyRepository
	Starter               TaxonomyRunStarter
	EmbeddingModel        string
	MinimumEmbeddingCount int
	Metrics               observability.TaxonomyMetrics
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
		metrics:               params.Metrics,
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
		return nil, huberrors.NewValidationError(scopeValidationField(scope), "no text feedback records found for this taxonomy scope")
	}

	if embeddingCount < s.minimumEmbeddingCount {
		return nil, huberrors.NewValidationError(
			scopeValidationField(scope),
			fmt.Sprintf("at least %d embedded text feedback records are required; found %d", s.minimumEmbeddingCount, embeddingCount),
		)
	}

	selectedCount := min(recordCount, repository.MaxTaxonomyRunInputRows)

	selectedEmbeddingCount := min(embeddingCount, selectedCount)
	if selectedCount > 0 && float64(selectedEmbeddingCount)/float64(selectedCount) < minimumTaxonomyEmbeddingCoverage {
		return nil, huberrors.NewValidationError(
			scopeValidationField(scope),
			fmt.Sprintf(
				"at least %.0f%% embedding coverage is required; found %d of %d selected records",
				minimumTaxonomyEmbeddingCoverage*percentageScale,
				selectedEmbeddingCount,
				selectedCount,
			),
		)
	}

	fieldLabel := req.FieldLabel
	if fieldLabel != nil {
		trimmed := strings.TrimSpace(*fieldLabel)
		fieldLabel = &trimmed
	}

	if (fieldLabel == nil || *fieldLabel == "") && scope.ScopeType == models.TaxonomyScopeTypeDirectory {
		label := directoryTaxonomyFieldLabel
		fieldLabel = &label
	}

	if fieldLabel == nil {
		fieldLabel = discoveredFieldLabel
	}

	params := taxonomyRunParams(req.ActorID, s.embeddingModel, recordCount, embeddingCount)

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

	runningRun, err := s.repo.MarkRunRunning(ctx, run.ID, scope.TenantID)
	if err != nil {
		return nil, fmt.Errorf("mark taxonomy run running: %w", err)
	}

	s.recordStarted(ctx, runningRun)

	if err := s.starter.StartRun(ctx, run.ID.String()); err != nil {
		failedRun, markErr := s.repo.MarkRunFailed(
			ctx,
			run.ID,
			scope.TenantID,
			"taxonomy service did not accept the run",
			models.TaxonomyRunFailureCodeServiceUnavailable,
			nil,
		)
		if markErr != nil {
			if s.metrics != nil {
				s.metrics.RecordDispatchError(ctx, "mark_failed_failed")
			}

			return nil, fmt.Errorf("mark taxonomy run failed after start error: %w", markErr)
		}

		if s.metrics != nil {
			s.metrics.RecordDispatchError(ctx, "request_failed")
		}

		s.recordTerminal(ctx, failedRun, models.TaxonomyRunFailureCodeServiceUnavailable)

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
func (s *TaxonomyService) GetRun(
	ctx context.Context,
	runID uuid.UUID,
	tenantID string,
) (*models.TaxonomyRun, error) {
	normalizedTenantID, err := normalizeRequiredTenantIDValue(tenantID)
	if err != nil {
		return nil, err
	}

	run, err := s.repo.GetRunForTenant(ctx, runID, normalizedTenantID)
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

	tree, err := s.repo.GetTree(ctx, run.ID, normalizedScope.TenantID)
	if err != nil {
		return nil, fmt.Errorf("get active taxonomy tree: %w", err)
	}

	return tree, nil
}

// GetTree returns a taxonomy tree by run ID.
func (s *TaxonomyService) GetTree(
	ctx context.Context,
	runID uuid.UUID,
	tenantID string,
) (*models.TaxonomyTreeResponse, error) {
	normalizedTenantID, err := normalizeRequiredTenantIDValue(tenantID)
	if err != nil {
		return nil, err
	}

	tree, err := s.repo.GetTree(ctx, runID, normalizedTenantID)
	if err != nil {
		return nil, fmt.Errorf("get taxonomy tree: %w", err)
	}

	return tree, nil
}

// GetNodeRecordCounts returns the feedback-record count for every visible node in a run, as
// subtree totals (a branch reports the sum of its subtopics, the root reports the run total).
func (s *TaxonomyService) GetNodeRecordCounts(
	ctx context.Context,
	runID uuid.UUID,
	tenantID string,
) (*models.TaxonomyRecordCountsResponse, error) {
	normalizedTenantID, err := normalizeRequiredTenantIDValue(tenantID)
	if err != nil {
		return nil, err
	}

	counts, err := s.repo.CountNodeRecords(ctx, runID, normalizedTenantID)
	if err != nil {
		return nil, fmt.Errorf("get taxonomy node record counts: %w", err)
	}

	return &models.TaxonomyRecordCountsResponse{Counts: counts}, nil
}

// GetRunInput returns feedback text and embeddings for the taxonomy service.
func (s *TaxonomyService) GetRunInput(
	ctx context.Context,
	runID uuid.UUID,
) (*models.TaxonomyRunInputResponse, error) {
	if s.embeddingModel == "" {
		return nil, ErrTaxonomyEmbeddingsNotConfigured
	}

	run, err := s.repo.GetRunForInternalService(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("get taxonomy run: %w", err)
	}

	input, err := s.repo.GetRunInput(ctx, runID, run.TenantID, s.embeddingModel)
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
	if err := validateTaxonomyResultRequest(req); err != nil {
		return nil, err
	}

	existingRun, err := s.repo.GetRunForInternalService(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("get taxonomy run: %w", err)
	}

	resultDigest, err := canonicalTaxonomyResultDigest(req)
	if err != nil {
		return nil, huberrors.NewValidationError("result", "taxonomy result cannot be canonicalized")
	}

	req.Metrics, err = mergeTaxonomyMetric(req.Metrics, "result_digest", resultDigest)
	if err != nil {
		return nil, huberrors.NewValidationError("metrics", "taxonomy metrics must be a JSON object")
	}

	if existingRun.Status == models.TaxonomyRunStatusSucceeded {
		// Runs completed before result digests were introduced cannot prove that a retry is identical.
		// Keep the fail-safe conflict for a missing legacy digest instead of accepting a potentially
		// different tree as idempotent.
		if jsonutil.ObjectString(existingRun.Metrics, "result_digest") == resultDigest {
			return existingRun, nil
		}

		return nil, huberrors.NewConflictError("taxonomy run already succeeded with a different result")
	}

	selectedRecordIDs, err := s.repo.GetRunInputRecordIDs(ctx, runID, existingRun.TenantID)
	if err != nil {
		return nil, fmt.Errorf("get selected taxonomy input records: %w", err)
	}

	if err := validateTaxonomyMembershipCoverage(req.Memberships, selectedRecordIDs); err != nil {
		return nil, err
	}

	run, err := s.repo.StoreResultAndActivate(ctx, runID, existingRun.TenantID, req)
	if err != nil {
		return nil, fmt.Errorf("complete taxonomy run: %w", err)
	}

	s.recordTerminal(ctx, run, "")

	return run, nil
}

func validateTaxonomyResultRequest(req models.TaxonomyRunResultRequest) error {
	clusterSizes, err := validateTaxonomyResultClusters(req.Clusters)
	if err != nil {
		return err
	}

	if err := validateTaxonomyResultMemberships(req.Memberships, clusterSizes); err != nil {
		return err
	}

	return validateTaxonomyResultNodes(req.Nodes, clusterSizes)
}

//nolint:wsl_v5 // Adjacent guards document independent result invariants.
func validateTaxonomyResultClusters(clusters []models.TaxonomyResultCluster) (map[int]int, error) {
	clusterSizes := make(map[int]int, len(clusters))
	for _, cluster := range clusters {
		if _, exists := clusterSizes[cluster.ClusterKey]; exists {
			return nil, huberrors.NewValidationError("result.clusters", "cluster_key values must be unique")
		}
		if cluster.Size <= 0 {
			return nil, huberrors.NewValidationError("result.clusters", "cluster sizes must be positive")
		}
		if cluster.IsOutlier != (cluster.ClusterKey == -1) {
			return nil, huberrors.NewValidationError("result.clusters", "outlier cluster invariant failed")
		}
		if cluster.ClusterKey == -1 {
			label := cluster.Label
			if cluster.LLMLabel != nil {
				label = cluster.LLMLabel
			}
			if label == nil || strings.TrimSpace(*label) != "Uncategorized Feedback" {
				return nil, huberrors.NewValidationError("result.clusters", "outlier cluster label must be canonical")
			}
		}

		clusterSizes[cluster.ClusterKey] = cluster.Size
	}
	if len(clusterSizes) == 0 {
		return nil, huberrors.NewValidationError("result.clusters", "at least one cluster is required")
	}

	return clusterSizes, nil
}

//nolint:wsl_v5 // Adjacent guards document independent result invariants.
func validateTaxonomyResultMemberships(
	memberships []models.TaxonomyResultMembership,
	clusterSizes map[int]int,
) error {
	membershipCounts := make(map[int]int, len(clusterSizes))
	seenRecords := make(map[uuid.UUID]struct{}, len(memberships))
	for _, membership := range memberships {
		if _, exists := clusterSizes[membership.ClusterKey]; !exists {
			return huberrors.NewValidationError("result.memberships", "membership references an unknown cluster")
		}
		if membership.FeedbackRecordID == uuid.Nil {
			return huberrors.NewValidationError("result.memberships", "feedback_record_id is required")
		}
		if _, exists := seenRecords[membership.FeedbackRecordID]; exists {
			return huberrors.NewValidationError("result.memberships", "feedback records must appear exactly once")
		}
		if membership.Confidence != nil && (math.IsNaN(*membership.Confidence) || math.IsInf(*membership.Confidence, 0)) {
			return huberrors.NewValidationError("result.memberships", "confidence must be finite")
		}
		if membership.Distance != nil && (math.IsNaN(*membership.Distance) || math.IsInf(*membership.Distance, 0)) {
			return huberrors.NewValidationError("result.memberships", "distance must be finite")
		}

		seenRecords[membership.FeedbackRecordID] = struct{}{}
		membershipCounts[membership.ClusterKey]++
	}
	for clusterKey, size := range clusterSizes {
		if membershipCounts[clusterKey] != size {
			return huberrors.NewValidationError("result.memberships", "cluster size must match membership count")
		}
	}

	return nil
}

func validateTaxonomyMembershipCoverage(
	memberships []models.TaxonomyResultMembership,
	selectedRecordIDs []uuid.UUID,
) error {
	if len(memberships) != len(selectedRecordIDs) {
		return huberrors.NewValidationError(
			"result.memberships",
			"memberships must exactly cover the selected run input",
		)
	}

	selectedRecords := make(map[uuid.UUID]struct{}, len(selectedRecordIDs))
	for _, recordID := range selectedRecordIDs {
		selectedRecords[recordID] = struct{}{}
	}

	for _, membership := range memberships {
		if _, exists := selectedRecords[membership.FeedbackRecordID]; !exists {
			return huberrors.NewValidationError(
				"result.memberships",
				"memberships must exactly cover the selected run input",
			)
		}
	}

	return nil
}

//nolint:cyclop,wsl_v5 // Adjacent guards keep the five-level tree contract explicit and auditable.
func validateTaxonomyResultNodes(nodes []models.TaxonomyResultNode, clusterSizes map[int]int) error {
	nodesByKey := make(map[string]models.TaxonomyResultNode, len(nodes))
	childrenByParent := make(map[string]int, len(nodes))
	siblingLabels := make(map[string]map[string]struct{}, len(nodes))
	rootCount := 0
	leafClusters := make(map[int]struct{}, len(clusterSizes))
	for _, node := range nodes {
		if _, exists := nodesByKey[node.NodeKey]; exists {
			return huberrors.NewValidationError("result.nodes", "node_key values must be unique")
		}
		if node.SortOrder < 0 {
			return huberrors.NewValidationError("result.nodes", "sort_order must be non-negative")
		}

		nodesByKey[node.NodeKey] = node
		if node.ParentKey != nil {
			childrenByParent[*node.ParentKey]++
			normalizedLabel := strings.ToLower(strings.Join(strings.Fields(node.Label), " "))
			if siblingLabels[*node.ParentKey] == nil {
				siblingLabels[*node.ParentKey] = make(map[string]struct{})
			}
			if _, exists := siblingLabels[*node.ParentKey][normalizedLabel]; exists {
				return huberrors.NewValidationError("result.nodes", "sibling labels must be unique")
			}
			siblingLabels[*node.ParentKey][normalizedLabel] = struct{}{}
		}

		switch node.NodeType {
		case models.TaxonomyNodeTypeRoot:
			rootCount++
			if node.Level != 0 || node.ParentKey != nil || node.ClusterKey != nil {
				return huberrors.NewValidationError("result.nodes", "root node must be level 0 without parent or cluster")
			}
		case models.TaxonomyNodeTypeBranch:
			if node.Level < 1 || node.Level > 3 || node.ParentKey == nil || node.ClusterKey != nil {
				return huberrors.NewValidationError("result.nodes", "branch nodes must occupy levels 1 through 3")
			}
		case models.TaxonomyNodeTypeLeaf:
			if node.Level != 4 || node.ParentKey == nil || node.ClusterKey == nil {
				return huberrors.NewValidationError("result.nodes", "leaf nodes must occupy level 4 and reference a cluster")
			}
			if _, exists := clusterSizes[*node.ClusterKey]; !exists {
				return huberrors.NewValidationError("result.nodes", "leaf references an unknown cluster")
			}
			if _, exists := leafClusters[*node.ClusterKey]; exists {
				return huberrors.NewValidationError("result.nodes", "clusters must appear in exactly one leaf")
			}
			if *node.ClusterKey == -1 && strings.TrimSpace(node.Label) != "Uncategorized Feedback" {
				return huberrors.NewValidationError("result.nodes", "outlier leaf label must be canonical")
			}
			leafClusters[*node.ClusterKey] = struct{}{}
		default:
			return huberrors.NewValidationError("result.nodes", "node_type is invalid")
		}
	}
	if rootCount != 1 {
		return huberrors.NewValidationError("result.nodes", "exactly one root node is required")
	}
	if len(leafClusters) != len(clusterSizes) {
		return huberrors.NewValidationError("result.nodes", "leaves must cover every cluster exactly once")
	}
	for nodeKey, node := range nodesByKey {
		if node.ParentKey != nil {
			parent, exists := nodesByKey[*node.ParentKey]
			if !exists || parent.Level != node.Level-1 || parent.NodeType == models.TaxonomyNodeTypeLeaf {
				return huberrors.NewValidationError("result.nodes", "parent references must form an exact five-level tree")
			}
		}
		if node.NodeType != models.TaxonomyNodeTypeLeaf && childrenByParent[nodeKey] == 0 {
			return huberrors.NewValidationError("result.nodes", "root and branch nodes must have children")
		}
	}

	return nil
}

// FailRun records a taxonomy run failure.
func (s *TaxonomyService) FailRun(
	ctx context.Context,
	runID uuid.UUID,
	req models.TaxonomyRunFailedRequest,
) (*models.TaxonomyRun, error) {
	sanitized, normalizedCode := normalizeRunFailure(req.Error, req.ErrorCode)

	existingRun, err := s.repo.GetRunForInternalService(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("get taxonomy run: %w", err)
	}

	metrics, err := marshalFailureDiagnostics(req.Diagnostics)
	if err != nil {
		return nil, fmt.Errorf("marshal taxonomy failure diagnostics: %w", err)
	}

	if existingRun.Status == models.TaxonomyRunStatusFailed {
		if taxonomyFailureMatches(existingRun, sanitized, normalizedCode, metrics) {
			return existingRun, nil
		}

		return nil, huberrors.NewConflictError("taxonomy run already failed with a different result")
	}

	run, err := s.repo.MarkRunFailed(ctx, runID, existingRun.TenantID, sanitized, normalizedCode, metrics)
	if err != nil {
		return nil, fmt.Errorf("fail taxonomy run: %w", err)
	}

	s.recordTerminal(ctx, run, normalizedCode)

	return run, nil
}

// Heartbeat records that a taxonomy run is still alive, keeping it out of the stuck-run reaper's
// reach. Resolving the run first yields its tenant and a not-found error for unknown ids.
func (s *TaxonomyService) Heartbeat(
	ctx context.Context,
	runID uuid.UUID,
) error {
	existingRun, err := s.repo.GetRunForInternalService(ctx, runID)
	if err != nil {
		return fmt.Errorf("get taxonomy run: %w", err)
	}

	if err := s.repo.Heartbeat(ctx, runID, existingRun.TenantID); err != nil {
		return fmt.Errorf("heartbeat taxonomy run: %w", err)
	}

	return nil
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

func (s *TaxonomyService) recordStarted(ctx context.Context, run *models.TaxonomyRun) {
	if s.metrics != nil {
		s.metrics.RecordRunStarted(ctx, string(run.ScopeType))
	}

	slog.InfoContext(ctx, "taxonomy run started",
		"event", "hub.taxonomy.run.started", "run_id", run.ID, "tenant_id", run.TenantID,
		"scope_type", run.ScopeType, "source_type", run.SourceType, "source_id", run.SourceID,
		"field_id", run.FieldID, "record_count", run.RecordCount, "embedding_count", run.EmbeddingCount)
}

func (s *TaxonomyService) recordTerminal(
	ctx context.Context, run *models.TaxonomyRun, failureCode models.TaxonomyRunFailureCode,
) {
	if run == nil {
		return
	}

	code := "none"
	if failureCode != "" {
		code = string(failureCode)
	}

	duration := taxonomyRunDuration(run)
	if s.metrics != nil {
		s.metrics.RecordRunOutcome(ctx, string(run.Status), code, string(run.ScopeType))
		s.metrics.RecordRunDuration(ctx, duration, string(run.Status), string(run.ScopeType))
	}

	slog.InfoContext(ctx, "taxonomy run reached terminal state",
		"event", "hub.taxonomy.run.terminal", "run_id", run.ID, "tenant_id", run.TenantID,
		"scope_type", run.ScopeType, "source_type", run.SourceType, "source_id", run.SourceID,
		"field_id", run.FieldID, "status", run.Status, "failure_code", code,
		"duration_seconds", duration.Seconds(), "record_count", run.RecordCount,
		"embedding_count", run.EmbeddingCount, "cluster_count", run.ClusterCount, "node_count", run.NodeCount)
}

func marshalFailureDiagnostics(diagnostics *models.TaxonomyRunFailureDiagnostics) (json.RawMessage, error) {
	if diagnostics == nil {
		return nil, nil
	}

	raw, err := json.Marshal(map[string]*models.TaxonomyRunFailureDiagnostics{"failure_diagnostics": diagnostics})
	if err != nil {
		return nil, fmt.Errorf("marshal diagnostics: %w", err)
	}

	return raw, nil
}

func taxonomyRunDuration(run *models.TaxonomyRun) time.Duration {
	start := run.CreatedAt
	if run.StartedAt != nil {
		start = *run.StartedAt
	}

	end := time.Now()
	if run.FinishedAt != nil {
		end = *run.FinishedAt
	}

	if end.Before(start) {
		return 0
	}

	return end.Sub(start)
}

func normalizeTaxonomyScope(scope models.TaxonomyScope) (models.TaxonomyScope, error) {
	tenantID, err := normalizeRequiredTenantIDValue(scope.TenantID)
	if err != nil {
		return models.TaxonomyScope{}, err
	}

	scopeType := scope.ScopeType
	if scopeType == "" {
		scopeType = models.TaxonomyScopeTypeField
	}

	if scopeType == models.TaxonomyScopeTypeDirectory {
		if strings.TrimSpace(scope.SourceType) != "" {
			return models.TaxonomyScope{}, huberrors.NewValidationError("source_type", "must be empty for directory taxonomy scope")
		}

		if strings.TrimSpace(scope.SourceID) != "" {
			return models.TaxonomyScope{}, huberrors.NewValidationError("source_id", "must be empty for directory taxonomy scope")
		}

		if strings.TrimSpace(scope.FieldID) != "" {
			return models.TaxonomyScope{}, huberrors.NewValidationError("field_id", "must be empty for directory taxonomy scope")
		}

		return models.TaxonomyScope{
			ScopeType: models.TaxonomyScopeTypeDirectory,
			TenantID:  tenantID,
		}, nil
	}

	sourceType, err := normalizeRequiredIdentifier("source_type", scope.SourceType)
	if err != nil {
		return models.TaxonomyScope{}, err
	}

	// source_id is optional: an empty value is the canonical "no source" scope.
	sourceID, err := normalizeOptionalIdentifier("source_id", scope.SourceID)
	if err != nil {
		return models.TaxonomyScope{}, err
	}

	fieldID, err := normalizeRequiredIdentifier("field_id", scope.FieldID)
	if err != nil {
		return models.TaxonomyScope{}, err
	}

	return models.TaxonomyScope{
		ScopeType:  models.TaxonomyScopeTypeField,
		TenantID:   tenantID,
		SourceType: sourceType,
		SourceID:   sourceID,
		FieldID:    fieldID,
	}, nil
}

func scopeValidationField(scope models.TaxonomyScope) string {
	if scope.ScopeType == models.TaxonomyScopeTypeDirectory {
		return "tenant_id"
	}

	return "field_id"
}

func taxonomyRunParams(actorID *string, embeddingModel string, eligibleCount, embeddingCount int) json.RawMessage {
	selectedCount := min(embeddingCount, repository.MaxTaxonomyRunInputRows)
	params := map[string]any{
		"trigger":         "manual",
		"embedding_model": embeddingModel,
		"input_selection": map[string]any{
			"eligible_count":           eligibleCount,
			"embedding_eligible_count": embeddingCount,
			"selected_count":           selectedCount,
			"selection_cap":            repository.MaxTaxonomyRunInputRows,
			"truncation_flag":          eligibleCount > selectedCount,
			"selection_strategy":       "most_recent",
		},
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

func canonicalTaxonomyResultDigest(req models.TaxonomyRunResultRequest) (string, error) {
	clusters := slices.Clone(req.Clusters)
	memberships := slices.Clone(req.Memberships)
	nodes := slices.Clone(req.Nodes)

	slices.SortFunc(clusters, func(left, right models.TaxonomyResultCluster) int {
		return cmp.Compare(left.ClusterKey, right.ClusterKey)
	})
	slices.SortFunc(memberships, func(left, right models.TaxonomyResultMembership) int {
		if byRecord := strings.Compare(left.FeedbackRecordID.String(), right.FeedbackRecordID.String()); byRecord != 0 {
			return byRecord
		}

		return cmp.Compare(left.ClusterKey, right.ClusterKey)
	})
	slices.SortFunc(nodes, func(left, right models.TaxonomyResultNode) int {
		return strings.Compare(left.NodeKey, right.NodeKey)
	})

	raw, err := json.Marshal(struct {
		Clusters    []models.TaxonomyResultCluster    `json:"clusters"`
		Memberships []models.TaxonomyResultMembership `json:"memberships"`
		Nodes       []models.TaxonomyResultNode       `json:"nodes"`
	}{Clusters: clusters, Memberships: memberships, Nodes: nodes})
	if err != nil {
		return "", fmt.Errorf("marshal taxonomy result: %w", err)
	}

	var canonical any
	if err := json.Unmarshal(raw, &canonical); err != nil {
		return "", fmt.Errorf("parse taxonomy result: %w", err)
	}

	raw, err = json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("canonicalize taxonomy result: %w", err)
	}

	digest := sha256.Sum256(raw)

	return fmt.Sprintf("sha256:%x", digest), nil
}

func mergeTaxonomyMetric(metrics json.RawMessage, key, value string) (json.RawMessage, error) {
	values := make(map[string]any)
	if len(metrics) > 0 && string(metrics) != "null" {
		if err := json.Unmarshal(metrics, &values); err != nil {
			return nil, fmt.Errorf("unmarshal taxonomy metrics: %w", err)
		}
	}

	values[key] = value

	raw, err := json.Marshal(values)
	if err != nil {
		return nil, fmt.Errorf("marshal taxonomy metrics: %w", err)
	}

	return raw, nil
}

func taxonomyFailureMatches(
	run *models.TaxonomyRun,
	message string,
	code models.TaxonomyRunFailureCode,
	metrics json.RawMessage,
) bool {
	if run.Error == nil || *run.Error != message || run.ErrorCode == nil || *run.ErrorCode != code {
		return false
	}

	return jsonutil.CanonicalEqual(run.Metrics, metrics)
}
