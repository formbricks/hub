package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/formbricks/hub/internal/models"
)

// errWriteHookNeedsPgxTx reports a write transaction that is not a pgx.Tx. Pool-backed tenant
// write transactions always are; only a test double can trip it.
var errWriteHookNeedsPgxTx = errors.New("record write hook: transaction is not a pgx.Tx")

// RecordWriteHook runs inside the transaction that wrote a feedback record: after the write,
// before the commit. Returning an error rolls the write back.
//
// It exists so work derived from a write is enqueued atomically with it: both commit or neither
// does. Enqueueing after the commit instead, through the in-process event channel, loses the job
// whenever the process dies between the two or the channel is full, and the record is then never
// enriched (ENG-2792: a hub-api SIGKILL right after 400 acknowledged creates left between a
// quarter and a half of them with no job, across runs). River keeps its queue in this database, so
// river_job is the outbox and InsertTx is the whole pattern; no relay process is needed.
//
// written is the row as this write left it; previous is the row it replaced (nil on create).
// SetTranslation passes nil for both: its hook needs only the record id its caller already holds.
type RecordWriteHook func(ctx context.Context, tx pgx.Tx, written, previous *models.FeedbackRecord) error

// runRecordWriteHook calls hook, if any, with the concrete transaction River needs.
func runRecordWriteHook(
	ctx context.Context, dbTx tenantWriteTx, hook RecordWriteHook, written, previous *models.FeedbackRecord,
) error {
	if hook == nil {
		return nil
	}

	tx, ok := dbTx.(pgx.Tx)
	if !ok {
		return errWriteHookNeedsPgxTx
	}

	return hook(ctx, tx, written, previous)
}
