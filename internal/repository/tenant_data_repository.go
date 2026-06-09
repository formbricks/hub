package repository

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/formbricks/hub/internal/models"
)

type tenantDataExecutor interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

type tenantDataTx interface {
	tenantDataExecutor
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

type tenantDataTxBeginner interface {
	BeginTx(ctx context.Context, txOptions pgx.TxOptions) (tenantDataTx, error)
}

type tenantDataPool struct {
	db *pgxpool.Pool
}

func (p tenantDataPool) BeginTx(ctx context.Context, txOptions pgx.TxOptions) (tenantDataTx, error) {
	dbTx, err := p.db.BeginTx(ctx, txOptions)
	if err != nil {
		return nil, fmt.Errorf("begin tenant data transaction: %w", err)
	}

	return dbTx, nil
}

// TenantDataRepository handles tenant-scoped data purge operations.
type TenantDataRepository struct {
	db tenantDataTxBeginner
}

// NewTenantDataRepository creates a new tenant data repository.
func NewTenantDataRepository(db *pgxpool.Pool) *TenantDataRepository {
	return &TenantDataRepository{db: tenantDataPool{db: db}}
}

// DeleteByTenant deletes all Hub-owned data for a tenant and returns per-resource counts.
func (r *TenantDataRepository) DeleteByTenant(ctx context.Context, tenantID string) (*models.TenantDataDeleteCounts, error) {
	dbTx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin tenant data delete transaction: %w", err)
	}

	defer func() {
		if err := dbTx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			slog.Error(
				"tenant data delete: rollback failed",
				"tenant_id_present", tenantID != "",
				"tenant_id_length", len(tenantID),
				"error", err,
			)
		}
	}()

	counts, err := deleteTenantDataInTx(ctx, dbTx, tenantID)
	if err != nil {
		return nil, err
	}

	if err := dbTx.Commit(ctx); err != nil {
		slog.Error(
			"tenant data delete: commit failed",
			"tenant_id_present", tenantID != "",
			"tenant_id_length", len(tenantID),
			"error", err,
		)

		return nil, fmt.Errorf("commit tenant data delete transaction: %w", err)
	}

	return counts, nil
}

func deleteTenantDataInTx(
	ctx context.Context, exec tenantDataExecutor, tenantID string,
) (*models.TenantDataDeleteCounts, error) {
	embeddingTag, err := exec.Exec(ctx, `
		DELETE FROM embeddings e
		USING feedback_records fr
		WHERE e.feedback_record_id = fr.id
			AND fr.tenant_id = $1`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("delete tenant embeddings: %w", err)
	}

	_, err = exec.Exec(ctx, `
		DELETE FROM taxonomy_runs
		WHERE tenant_id = $1`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("delete tenant taxonomy runs: %w", err)
	}

	feedbackRecordsTag, err := exec.Exec(ctx, `
		DELETE FROM feedback_records
		WHERE tenant_id = $1`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("delete tenant feedback records: %w", err)
	}

	webhooksTag, err := exec.Exec(ctx, `
		DELETE FROM webhooks
		WHERE tenant_id = $1`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("delete tenant webhooks: %w", err)
	}

	return &models.TenantDataDeleteCounts{
		DeletedFeedbackRecords: feedbackRecordsTag.RowsAffected(),
		DeletedEmbeddings:      embeddingTag.RowsAffected(),
		DeletedWebhooks:        webhooksTag.RowsAffected(),
	}, nil
}
