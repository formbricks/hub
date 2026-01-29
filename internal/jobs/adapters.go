package jobs

import (
	"context"

	"github.com/formbricks/hub/internal/models"
	"github.com/google/uuid"
)

// FeedbackRecordsUpdater wraps the feedback records repository to implement EmbeddingUpdater.
type FeedbackRecordsUpdater struct {
	repo FeedbackEnrichmentRepository
}

// FeedbackEnrichmentRepository defines the interface needed from the feedback records repository.
type FeedbackEnrichmentRepository interface {
	UpdateEnrichment(ctx context.Context, id uuid.UUID, req *models.UpdateFeedbackEnrichmentRequest) error
}

// NewFeedbackRecordsUpdater creates a new feedback records updater.
func NewFeedbackRecordsUpdater(repo FeedbackEnrichmentRepository) *FeedbackRecordsUpdater {
	return &FeedbackRecordsUpdater{repo: repo}
}

// UpdateEmbedding implements EmbeddingUpdater for feedback records.
func (u *FeedbackRecordsUpdater) UpdateEmbedding(ctx context.Context, id uuid.UUID, embedding []float32) error {
	return u.repo.UpdateEnrichment(ctx, id, &models.UpdateFeedbackEnrichmentRequest{
		Embedding: embedding,
	})
}

// TopicsUpdater wraps the topics repository to implement EmbeddingUpdater.
type TopicsUpdater struct {
	repo TopicsEmbeddingRepository
}

// TopicsEmbeddingRepository defines the interface needed from the topics repository.
type TopicsEmbeddingRepository interface {
	UpdateEmbedding(ctx context.Context, id uuid.UUID, embedding []float32) error
}

// NewTopicsUpdater creates a new topics updater.
func NewTopicsUpdater(repo TopicsEmbeddingRepository) *TopicsUpdater {
	return &TopicsUpdater{repo: repo}
}

// UpdateEmbedding implements EmbeddingUpdater for topics.
func (u *TopicsUpdater) UpdateEmbedding(ctx context.Context, id uuid.UUID, embedding []float32) error {
	return u.repo.UpdateEmbedding(ctx, id, embedding)
}

// KnowledgeRecordsUpdater wraps the knowledge records repository to implement EmbeddingUpdater.
type KnowledgeRecordsUpdater struct {
	repo KnowledgeEmbeddingRepository
}

// KnowledgeEmbeddingRepository defines the interface needed from the knowledge records repository.
type KnowledgeEmbeddingRepository interface {
	UpdateEmbedding(ctx context.Context, id uuid.UUID, embedding []float32) error
}

// NewKnowledgeRecordsUpdater creates a new knowledge records updater.
func NewKnowledgeRecordsUpdater(repo KnowledgeEmbeddingRepository) *KnowledgeRecordsUpdater {
	return &KnowledgeRecordsUpdater{repo: repo}
}

// UpdateEmbedding implements EmbeddingUpdater for knowledge records.
func (u *KnowledgeRecordsUpdater) UpdateEmbedding(ctx context.Context, id uuid.UUID, embedding []float32) error {
	return u.repo.UpdateEmbedding(ctx, id, embedding)
}
