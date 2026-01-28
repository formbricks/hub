package jobs

import (
	"context"
	"log/slog"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// ErrorHandler handles job errors and panics for logging and alerting.
type ErrorHandler struct{}

// HandleError is called when a job returns an error.
func (h *ErrorHandler) HandleError(ctx context.Context, job *rivertype.JobRow, err error) *river.ErrorHandlerResult {
	slog.Error("job failed",
		"job_kind", job.Kind,
		"job_id", job.ID,
		"attempt", job.Attempt,
		"max_attempts", job.MaxAttempts,
		"error", err,
	)

	// Return nil to use default retry behavior
	return nil
}

// HandlePanic is called when a job panics.
func (h *ErrorHandler) HandlePanic(ctx context.Context, job *rivertype.JobRow, panicVal any, trace string) *river.ErrorHandlerResult {
	slog.Error("job panicked",
		"job_kind", job.Kind,
		"job_id", job.ID,
		"attempt", job.Attempt,
		"panic_value", panicVal,
		"stack_trace", trace,
	)

	// Return nil to use default behavior (mark as errored, will retry)
	return nil
}
