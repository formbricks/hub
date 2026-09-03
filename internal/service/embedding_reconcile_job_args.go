package service

import "github.com/riverqueue/river"

const (
	// EmbeddingReconcileQueueName carries the level-triggered sweep itself. The queue is serialized;
	// River's elected leader schedules one job per interval across all worker replicas.
	EmbeddingReconcileQueueName = "embedding_reconcile"
	// EmbeddingsReconcileQueueName carries repaired taxonomy embeddings. It is deliberately
	// separate from live embeddings so old backlog cannot delay a record that just arrived.
	EmbeddingsReconcileQueueName = "embeddings_reconcile"
)

// EmbeddingReconcileArgs is an argument-free, level-triggered taxonomy embedding sweep.
// Database state and deployment configuration are read when the job runs.
type EmbeddingReconcileArgs struct{}

// Kind returns the River job kind.
func (EmbeddingReconcileArgs) Kind() string { return "embedding_reconcile" }

var _ river.JobArgs = EmbeddingReconcileArgs{}
