package jobs

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// BackfillStats holds statistics from a backfill operation.
type BackfillStats struct {
	FeedbackRecordsEnqueued  int
	TopicsEnqueued           int
	KnowledgeRecordsEnqueued int
	Errors                   int
}

// Backfill enqueues embedding jobs for all records that are missing embeddings.
func Backfill(ctx context.Context, db *pgxpool.Pool, inserter JobInserter) (*BackfillStats, error) {
	stats := &BackfillStats{}

	// Backfill feedback records
	feedbackCount, err := backfillFeedbackRecords(ctx, db, inserter)
	if err != nil {
		slog.Error("failed to backfill feedback records", "error", err)
		stats.Errors++
	}
	stats.FeedbackRecordsEnqueued = feedbackCount

	// Backfill topics
	topicsCount, err := backfillTopics(ctx, db, inserter)
	if err != nil {
		slog.Error("failed to backfill topics", "error", err)
		stats.Errors++
	}
	stats.TopicsEnqueued = topicsCount

	// Backfill knowledge records
	knowledgeCount, err := backfillKnowledgeRecords(ctx, db, inserter)
	if err != nil {
		slog.Error("failed to backfill knowledge records", "error", err)
		stats.Errors++
	}
	stats.KnowledgeRecordsEnqueued = knowledgeCount

	return stats, nil
}

// backfillFeedbackRecords enqueues embedding jobs for feedback records without embeddings.
func backfillFeedbackRecords(ctx context.Context, db *pgxpool.Pool, inserter JobInserter) (int, error) {
	query := `
		SELECT id, value_text 
		FROM feedback_records 
		WHERE field_type = 'text' 
		  AND embedding IS NULL 
		  AND value_text IS NOT NULL 
		  AND value_text != ''
	`

	rows, err := db.Query(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("failed to query feedback records: %w", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var id uuid.UUID
		var text string
		if err := rows.Scan(&id, &text); err != nil {
			slog.Error("failed to scan feedback record", "error", err)
			continue
		}

		if err := inserter.InsertEmbeddingJob(ctx, EmbeddingJobArgs{
			RecordID:   id,
			RecordType: RecordTypeFeedback,
			Text:       text,
		}); err != nil {
			slog.Error("failed to enqueue feedback record embedding job", "id", id, "error", err)
			continue
		}
		count++
	}

	if err := rows.Err(); err != nil {
		return count, fmt.Errorf("error iterating feedback records: %w", err)
	}

	return count, nil
}

// backfillTopics enqueues embedding jobs for topics without embeddings.
// Note: This uses the topic title directly. For hierarchy paths, you may need to
// fetch parent topics and build the path.
func backfillTopics(ctx context.Context, db *pgxpool.Pool, inserter JobInserter) (int, error) {
	// Query topics with their hierarchy paths using a recursive CTE
	query := `
		WITH RECURSIVE topic_paths AS (
			-- Base case: Level 1 topics (no parent)
			SELECT id, title, parent_id, title::text as hierarchy_path
			FROM topics
			WHERE parent_id IS NULL AND embedding IS NULL
			
			UNION ALL
			
			-- Recursive case: children with their parent's path
			SELECT t.id, t.title, t.parent_id, (tp.hierarchy_path || ' > ' || t.title)::text
			FROM topics t
			INNER JOIN topic_paths tp ON t.parent_id = tp.id
			WHERE t.embedding IS NULL
		)
		SELECT id, hierarchy_path FROM topic_paths
	`

	rows, err := db.Query(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("failed to query topics: %w", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var id uuid.UUID
		var hierarchyPath string
		if err := rows.Scan(&id, &hierarchyPath); err != nil {
			slog.Error("failed to scan topic", "error", err)
			continue
		}

		if err := inserter.InsertEmbeddingJob(ctx, EmbeddingJobArgs{
			RecordID:   id,
			RecordType: RecordTypeTopic,
			Text:       hierarchyPath,
		}); err != nil {
			slog.Error("failed to enqueue topic embedding job", "id", id, "error", err)
			continue
		}
		count++
	}

	if err := rows.Err(); err != nil {
		return count, fmt.Errorf("error iterating topics: %w", err)
	}

	return count, nil
}

// backfillKnowledgeRecords enqueues embedding jobs for knowledge records without embeddings.
func backfillKnowledgeRecords(ctx context.Context, db *pgxpool.Pool, inserter JobInserter) (int, error) {
	query := `
		SELECT id, content 
		FROM knowledge_records 
		WHERE embedding IS NULL 
		  AND content IS NOT NULL 
		  AND content != ''
	`

	rows, err := db.Query(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("failed to query knowledge records: %w", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var id uuid.UUID
		var content string
		if err := rows.Scan(&id, &content); err != nil {
			slog.Error("failed to scan knowledge record", "error", err)
			continue
		}

		if err := inserter.InsertEmbeddingJob(ctx, EmbeddingJobArgs{
			RecordID:   id,
			RecordType: RecordTypeKnowledge,
			Text:       content,
		}); err != nil {
			slog.Error("failed to enqueue knowledge record embedding job", "id", id, "error", err)
			continue
		}
		count++
	}

	if err := rows.Err(); err != nil {
		return count, fmt.Errorf("error iterating knowledge records: %w", err)
	}

	return count, nil
}
