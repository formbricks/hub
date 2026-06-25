package service

import (
	"context"
	"errors"
	"testing"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// recordingInserter records every job inserted (args + opts) and returns a fixed error.
type recordingInserter struct {
	args []river.JobArgs
	opts []*river.InsertOpts
	err  error
}

func (r *recordingInserter) Insert(
	_ context.Context, args river.JobArgs, opts *river.InsertOpts,
) (*rivertype.JobInsertResult, error) {
	r.args = append(r.args, args)
	r.opts = append(r.opts, opts)

	return &rivertype.JobInsertResult{}, r.err
}

func TestEnrichmentSettingsListener_DispatchesTargetLanguage(t *testing.T) {
	inserter := &recordingInserter{}
	listener := NewTranslationSettingsListener(inserter, TranslationBackfillsQueueName, 3)

	listener.OnSettingsChanged(context.Background(), "org-1", []string{settingKeyTargetLanguage})

	if len(inserter.args) != 1 {
		t.Fatalf("enqueued %d jobs, want 1", len(inserter.args))
	}

	job, ok := inserter.args[0].(TenantTranslationBackfillArgs)
	if !ok {
		t.Fatalf("enqueued args type = %T, want TenantTranslationBackfillArgs", inserter.args[0])
	}

	if job.TenantID != "org-1" {
		t.Fatalf("job tenant = %q, want org-1", job.TenantID)
	}

	if inserter.opts[0].Queue != TranslationBackfillsQueueName {
		t.Fatalf("queue = %q, want %q", inserter.opts[0].Queue, TranslationBackfillsQueueName)
	}

	if !inserter.opts[0].UniqueOpts.ByArgs {
		t.Fatal("want ByArgs uniqueness (one in-flight backfill per tenant)")
	}
}

func TestEnrichmentSettingsListener_IgnoresUnknownKey(t *testing.T) {
	inserter := &recordingInserter{}
	listener := NewTranslationSettingsListener(inserter, TranslationBackfillsQueueName, 3)

	listener.OnSettingsChanged(context.Background(), "org-1", []string{"some_other_setting"})

	if len(inserter.args) != 0 {
		t.Fatalf("enqueued %d jobs for an unrelated key, want 0", len(inserter.args))
	}
}

func TestEnrichmentSettingsListener_SwallowsEnqueueError(t *testing.T) {
	inserter := &recordingInserter{err: errors.New("insert failed")}
	listener := NewTranslationSettingsListener(inserter, TranslationBackfillsQueueName, 3)

	// Must neither panic nor propagate: the settings write already committed.
	listener.OnSettingsChanged(context.Background(), "org-1", []string{settingKeyTargetLanguage})

	if len(inserter.args) != 1 {
		t.Fatalf("attempted %d inserts, want 1", len(inserter.args))
	}
}
