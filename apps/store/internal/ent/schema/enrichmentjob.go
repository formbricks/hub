package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// EnrichmentJob holds the schema definition for the EnrichmentJob entity.
type EnrichmentJob struct {
	ent.Schema
}

// Fields of the EnrichmentJob.
func (EnrichmentJob) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).
			Default(uuid.New).
			Immutable(),
		field.UUID("experience_id", uuid.UUID{}).
			Immutable(),
		field.String("job_type").
			Default("enrichment").
			Comment("Job type: enrichment (sentiment/topics) or embedding (vector generation)"),
		field.String("status").
			Default("pending").
			Comment("Job status: pending, processing, completed, failed"),
		field.Text("text").
			Comment("Text content to be enriched or embedded"),
		field.Text("error").
			Optional().
			Nillable().
			Comment("Error message if job failed"),
		field.Int("attempts").
			Default(0).
			Comment("Number of processing attempts"),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
		field.Time("processed_at").
			Optional().
			Nillable(),
	}
}

// Edges of the EnrichmentJob.
func (EnrichmentJob) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("experience", ExperienceData.Type).
			Unique().
			Required().
			Immutable().
			Field("experience_id"),
	}
}

// Indexes of the EnrichmentJob.
func (EnrichmentJob) Indexes() []ent.Index {
	return []ent.Index{
		// Index for efficient queue polling: find pending jobs by type, ordered by creation time
		index.Fields("job_type", "status", "created_at"),
		// Index for looking up jobs by experience
		index.Fields("experience_id"),
	}
}
