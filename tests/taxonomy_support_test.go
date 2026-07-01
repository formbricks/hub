package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/api/handlers"
	"github.com/formbricks/hub/internal/api/middleware"
	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/repository"
	"github.com/formbricks/hub/internal/service"
	"github.com/formbricks/hub/pkg/database"
)

// taxonomyEmbeddingModel is the embedding model taxonomy tests configure the service
// with. Embeddings must be seeded with this model or CountScopeInput will not count
// them (and StartManualRun will reject the run for insufficient embedded records).
const taxonomyEmbeddingModel = "model-name"

// testInternalToken authenticates the internal taxonomy service endpoints. It is
// deliberately different from testAPIKey so cross-auth isolation can be asserted.
const testInternalToken = "test-internal-token-67890"

// taxonomyMinEmbeddedRecords keeps the StartManualRun embedding threshold low so the
// happy path only needs a couple of seeded records instead of the production default (20).
const taxonomyMinEmbeddedRecords = 2

// taxonomyTestDB opens a Postgres pool for DB-backed taxonomy tests, mirroring the
// existing integration helpers (DATABASE_URL env, falling back to the compose default).
func taxonomyTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = defaultTestDatabaseURL
	}

	t.Setenv("DATABASE_URL", databaseURL)

	cfg, err := config.Load()
	require.NoError(t, err)

	db, err := database.NewPostgresPool(context.Background(), cfg.Database.URL,
		database.WithPoolConfig(cfg.Database.PoolConfig()),
	)
	require.NoError(t, err)

	t.Cleanup(db.Close)

	return db
}

// fakeTaxonomyStarter is an in-memory service.TaxonomyRunStarter. It records the run
// IDs it was asked to start and returns the configured error, so tests can drive both
// the accept and reject paths of StartManualRun without a real compute service.
type fakeTaxonomyStarter struct {
	mu       sync.Mutex
	startErr error
	runIDs   []string
}

func (f *fakeTaxonomyStarter) StartRun(_ context.Context, runID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.runIDs = append(f.runIDs, runID)

	return f.startErr
}

func (f *fakeTaxonomyStarter) startedRunIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]string(nil), f.runIDs...)
}

// taxonomyTestServer bundles the taxonomy HTTP test server with the dependencies tests
// need to seed state and assert behavior.
type taxonomyTestServer struct {
	server         *httptest.Server
	db             *pgxpool.Pool
	repo           *repository.TaxonomyRepository
	embeddingsRepo *repository.EmbeddingsRepository
	starter        *fakeTaxonomyStarter
	internalToken  string
	apiKey         string
}

// setupTaxonomyAPIServer builds an httptest server that mirrors cmd/api/app.go's taxonomy
// wiring: public taxonomy routes behind the Hub API key, and the internal taxonomy routes
// behind a separate internal token. It returns the server plus the repo/embeddings/starter
// so tests can seed data and inspect start calls.
func setupTaxonomyAPIServer(t *testing.T) *taxonomyTestServer {
	t.Helper()

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = defaultTestDatabaseURL
	}

	t.Setenv("API_KEY", testAPIKey)
	t.Setenv("DATABASE_URL", databaseURL)

	cfg, err := config.Load()
	require.NoError(t, err)

	db, err := database.NewPostgresPool(context.Background(), cfg.Database.URL,
		database.WithPoolConfig(cfg.Database.PoolConfig()),
	)
	require.NoError(t, err)

	repo := repository.NewTaxonomyRepository(db)
	embeddingsRepo := repository.NewEmbeddingsRepository(db)
	starter := &fakeTaxonomyStarter{}

	taxonomyService := service.NewTaxonomyService(service.NewTaxonomyServiceParams{
		Repo:                  repo,
		Starter:               starter,
		EmbeddingModel:        taxonomyEmbeddingModel,
		MinimumEmbeddingCount: taxonomyMinEmbeddedRecords,
	})
	taxonomyHandler := handlers.NewTaxonomyHandler(taxonomyService)
	taxonomyInternalHandler := handlers.NewTaxonomyInternalHandler(taxonomyService)

	// Public taxonomy routes (Hub API key auth), matching cmd/api/app.go ordering so the
	// {run_id} literal-vs-pattern precedence (active/tree before {run_id}) is preserved.
	protected := http.NewServeMux()
	protected.HandleFunc("GET /v1/taxonomy/fields", taxonomyHandler.ListFields)
	protected.HandleFunc("POST /v1/taxonomy/runs", taxonomyHandler.CreateRun)
	protected.HandleFunc("GET /v1/taxonomy/runs", taxonomyHandler.ListRuns)
	protected.HandleFunc("GET /v1/taxonomy/runs/active/tree", taxonomyHandler.GetActiveTree)
	protected.HandleFunc("GET /v1/taxonomy/runs/{run_id}", taxonomyHandler.GetRun)
	protected.HandleFunc("GET /v1/taxonomy/runs/{run_id}/tree", taxonomyHandler.GetTree)
	protected.HandleFunc("PATCH /v1/taxonomy/nodes/{node_id}", taxonomyHandler.RenameNode)
	protected.HandleFunc("DELETE /v1/taxonomy/nodes/{node_id}", taxonomyHandler.RemoveNode)
	protected.HandleFunc("GET /v1/taxonomy/nodes/{node_id}/records", taxonomyHandler.ListNodeRecords)
	protectedWithAuth := middleware.Auth(cfg.Server.HubAPIKey)(protected)

	// Internal taxonomy routes (separate internal-service token auth).
	internal := http.NewServeMux()
	internal.HandleFunc("GET /internal/v1/taxonomy/auth-check", taxonomyInternalHandler.AuthCheck)
	internal.HandleFunc("GET /internal/v1/taxonomy/runs/{run_id}/input", taxonomyInternalHandler.GetRunInput)
	internal.HandleFunc("PUT /internal/v1/taxonomy/runs/{run_id}/result", taxonomyInternalHandler.CompleteRun)
	internal.HandleFunc("POST /internal/v1/taxonomy/runs/{run_id}/failed", taxonomyInternalHandler.FailRun)
	internalWithAuth := middleware.Auth(testInternalToken)(internal)

	mux := http.NewServeMux()
	mux.Handle("/v1/", protectedWithAuth)
	mux.Handle("/internal/v1/taxonomy/", internalWithAuth)

	server := httptest.NewServer(mux)

	t.Cleanup(func() {
		server.Close()
		db.Close()
	})

	return &taxonomyTestServer{
		server:         server,
		db:             db,
		repo:           repo,
		embeddingsRepo: embeddingsRepo,
		starter:        starter,
		internalToken:  testInternalToken,
		apiKey:         testAPIKey,
	}
}

// taxonomyGraphIDs holds the identifiers produced by seedTaxonomyGraph.
type taxonomyGraphIDs struct {
	FeedbackRecordID uuid.UUID
	RunID            uuid.UUID
	ClusterID        uuid.UUID
	RootID           uuid.UUID
	BranchID         uuid.UUID
	LeafID           uuid.UUID
	NodeEventID      uuid.UUID
}

// seedTaxonomyGraph inserts a complete, activated taxonomy graph for a scope using raw
// SQL: one feedback record, a succeeded run, one cluster, one membership, a root/branch/leaf
// node chain, an active-run pointer, and one node event. It returns every identifier so
// tests can exercise reads, edits, and cross-tenant access against known rows.
func seedTaxonomyGraph(ctx context.Context, t *testing.T, db *pgxpool.Pool, scope models.TaxonomyScope) taxonomyGraphIDs {
	t.Helper()

	var ids taxonomyGraphIDs

	err := db.QueryRow(ctx, `
		INSERT INTO feedback_records (
			source_type, source_id, field_id, field_label, field_type,
			value_text, tenant_id, submission_id
		)
		VALUES ($1, NULLIF($2, ''), $3, 'Feedback', 'text'::field_type_enum, $4, $5, $6)
		RETURNING id`,
		scope.SourceType, scope.SourceID, scope.FieldID,
		"Login was confusing", scope.TenantID, "submission-"+uuid.NewString(),
	).Scan(&ids.FeedbackRecordID)
	require.NoError(t, err)

	err = db.QueryRow(ctx, `
		INSERT INTO taxonomy_runs (
			tenant_id, source_type, source_id, field_id, field_label, status,
			record_count, embedding_count, cluster_count, node_count
		)
		VALUES ($1, $2, $3, $4, 'Feedback', 'succeeded'::taxonomy_run_status_enum, 1, 1, 1, 3)
		RETURNING id`,
		scope.TenantID, scope.SourceType, scope.SourceID, scope.FieldID,
	).Scan(&ids.RunID)
	require.NoError(t, err)

	err = db.QueryRow(ctx, `
		INSERT INTO taxonomy_clusters (run_id, cluster_key, label, llm_label, keywords, size)
		VALUES ($1, 1, 'login', 'Login issues', '["login"]'::jsonb, 1)
		RETURNING id`,
		ids.RunID,
	).Scan(&ids.ClusterID)
	require.NoError(t, err)

	_, err = db.Exec(ctx, `
		INSERT INTO taxonomy_cluster_memberships (run_id, tenant_id, cluster_id, feedback_record_id, confidence)
		VALUES ($1, $2, $3, $4, 0.95)`,
		ids.RunID, scope.TenantID, ids.ClusterID, ids.FeedbackRecordID,
	)
	require.NoError(t, err)

	err = db.QueryRow(ctx, `
		INSERT INTO taxonomy_nodes (run_id, node_type, label, original_label, level, sort_order)
		VALUES ($1, 'root'::taxonomy_node_type_enum, 'Feedback', 'Feedback', 0, 0)
		RETURNING id`,
		ids.RunID,
	).Scan(&ids.RootID)
	require.NoError(t, err)

	err = db.QueryRow(ctx, `
		INSERT INTO taxonomy_nodes (run_id, parent_id, node_type, label, original_label, level, sort_order)
		VALUES ($1, $2, 'branch'::taxonomy_node_type_enum, 'Product Access', 'Product Access', 1, 0)
		RETURNING id`,
		ids.RunID, ids.RootID,
	).Scan(&ids.BranchID)
	require.NoError(t, err)

	err = db.QueryRow(ctx, `
		INSERT INTO taxonomy_nodes (run_id, parent_id, cluster_id, node_type, label, original_label, level, sort_order)
		VALUES ($1, $2, $3, 'leaf'::taxonomy_node_type_enum, 'Login Problems', 'Login Problems', 2, 0)
		RETURNING id`,
		ids.RunID, ids.BranchID, ids.ClusterID,
	).Scan(&ids.LeafID)
	require.NoError(t, err)

	_, err = db.Exec(ctx, `
		INSERT INTO taxonomy_active_runs (tenant_id, source_type, source_id, field_id, run_id, activated_by)
		VALUES ($1, $2, $3, $4, $5, 'seed-user')`,
		scope.TenantID, scope.SourceType, scope.SourceID, scope.FieldID, ids.RunID,
	)
	require.NoError(t, err)

	err = db.QueryRow(ctx, `
		INSERT INTO taxonomy_node_events (
			tenant_id, source_type, source_id, field_id, run_id, node_id,
			event_type, actor_id, old_value, new_value
		)
		VALUES (
			$1, $2, $3, $4, $5, $6,
			'rename'::taxonomy_node_event_type_enum, 'seed-user',
			'{"label":"Login Problems"}'::jsonb, '{"label":"Authentication Problems"}'::jsonb
		)
		RETURNING id`,
		scope.TenantID, scope.SourceType, scope.SourceID, scope.FieldID, ids.RunID, ids.LeafID,
	).Scan(&ids.NodeEventID)
	require.NoError(t, err)

	t.Cleanup(func() {
		// taxonomy_runs cascade removes clusters/memberships/nodes/active-runs/events.
		_, _ = db.Exec(ctx, `DELETE FROM taxonomy_runs WHERE tenant_id = $1`, scope.TenantID)
		_, _ = db.Exec(ctx, `DELETE FROM feedback_records WHERE tenant_id = $1`, scope.TenantID)
	})

	return ids
}

// seedEmbeddedFeedback inserts count text feedback records for a scope, each with an
// embedding under taxonomyEmbeddingModel, so StartManualRun's input counts are satisfied.
func seedEmbeddedFeedback(
	ctx context.Context, t *testing.T, harness *taxonomyTestServer, scope models.TaxonomyScope, count int,
) {
	t.Helper()

	for range count {
		var recordID uuid.UUID

		err := harness.db.QueryRow(ctx, `
			INSERT INTO feedback_records (
				source_type, source_id, field_id, field_label, field_type,
				value_text, tenant_id, submission_id
			)
			VALUES ($1, NULLIF($2, ''), $3, 'Feedback', 'text'::field_type_enum, $4, $5, $6)
			RETURNING id`,
			scope.SourceType, scope.SourceID, scope.FieldID,
			"Feedback text for embedding", scope.TenantID, "submission-"+uuid.NewString(),
		).Scan(&recordID)
		require.NoError(t, err)

		embedding := make([]float32, models.EmbeddingVectorDimensions)
		embedding[0] = 0.25

		require.NoError(t, harness.embeddingsRepo.Upsert(ctx, recordID, taxonomyEmbeddingModel, embedding))
	}

	t.Cleanup(func() {
		_, _ = harness.db.Exec(ctx, `DELETE FROM feedback_records WHERE tenant_id = $1`, scope.TenantID)
	})
}

// doTaxonomyRequest issues an HTTP request against the taxonomy test server. When token is
// non-empty it is sent as a Bearer credential; when body is non-nil it is JSON-encoded.
// The caller owns closing the returned response body.
func doTaxonomyRequest(
	ctx context.Context, t *testing.T, method, url, token string, body any,
) *http.Response {
	t.Helper()

	var reader *bytes.Reader

	if body != nil {
		encoded, err := json.Marshal(body)
		require.NoError(t, err)

		reader = bytes.NewReader(encoded)
	} else {
		reader = bytes.NewReader(nil)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	require.NoError(t, err)

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := (&http.Client{}).Do(req)
	require.NoError(t, err)

	return resp
}

// uniqueTaxonomyScope builds a scope with a unique tenant ID so parallel/repeated runs of
// a test never collide on tenant-scoped rows.
func uniqueTaxonomyScope(prefix string) models.TaxonomyScope {
	return models.TaxonomyScope{
		TenantID:   prefix + "-" + uuid.NewString(),
		SourceType: "formbricks",
		SourceID:   "survey-" + uuid.NewString(),
		FieldID:    "feedback",
	}
}
