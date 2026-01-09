package schema

import (
	"fmt"
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
	"github.com/pgvector/pgvector-go"
)

// Valid field types for experience data
var validFieldTypes = map[string]bool{
	"text":        true,
	"categorical": true,
	"nps":         true,
	"csat":        true,
	"ces":         true, // Customer Effort Score (typically 1-7 scale)
	"rating":      true,
	"number":      true,
	"boolean":     true,
	"date":        true,
}

// ExperienceData holds the schema definition for the ExperienceData entity.
// This schema is optimized for analytics and BI tools (Superset, Power BI).
// Each row represents a single question/response pair for easy SQL aggregations.
type ExperienceData struct {
	ent.Schema
}

// Fields of the ExperienceData.
func (ExperienceData) Fields() []ent.Field {
	return []ent.Field{
		// Core identification
		field.UUID("id", uuid.UUID{}).
			Default(func() uuid.UUID {
				id, _ := uuid.NewV7()
				return id
			}).
			Immutable().
			Comment("UUIDv7 primary key (time-ordered)"),

		field.Time("collected_at").
			Default(time.Now).
			Comment("When the feedback was collected"),

		field.Time("created_at").
			Default(time.Now).
			Immutable().
			Comment("When this record was created in the database"),

		field.Time("updated_at").
			Default(time.Now).
			UpdateDefault(time.Now).
			Comment("When this record was last updated"),

		// Multi-tenancy and response grouping
		field.String("tenant_id").
			Optional().
			MaxLen(255).
			Comment("Tenant/organization identifier for multi-tenancy (optional for self-hosted single-tenant deployments)"),

		field.String("response_id").
			Optional().
			MaxLen(255).
			Comment("Groups multiple answers from a single submission/session (e.g., all answers from one survey response)"),

		// Source tracking
		field.String("source_type").
			NotEmpty().
			Comment("Type of feedback source (e.g., survey, review, feedback_form, support, social)"),

		field.String("source_id").
			Optional().
			Comment("Reference to survey/form/ticket ID"),

		field.String("source_name").
			Optional().
			Comment("Human-readable name (e.g., 'Q1 NPS Survey')"),

		// Question/Field identification
		field.String("field_id").
			NotEmpty().
			Comment("Identifier for the question/field being answered"),

		field.String("field_label").
			Optional().
			Comment("The actual question text (e.g., 'How satisfied are you?')"),

		field.String("field_type").
			NotEmpty().
			Validate(func(s string) error {
				if !validFieldTypes[s] {
					return fmt.Errorf("invalid field_type: %s (must be one of: text, categorical, nps, csat, ces, rating, number, boolean, date)", s)
				}
				return nil
			}).
			Comment("Type of field: text (enrichable), categorical, nps, csat, ces, rating, number, boolean, date"),

		// Response values (typed for analytics)
		field.Text("value_text").
			Optional().
			Nillable().
			Comment("For open-ended text responses"),

		field.Float("value_number").
			Optional().
			Nillable().
			Comment("For ratings, NPS scores, numeric responses"),

		field.Bool("value_boolean").
			Optional().
			Nillable().
			Comment("For yes/no questions"),

		field.Time("value_date").
			Optional().
			Nillable().
			Comment("For date responses"),

		field.JSON("value_json", map[string]interface{}{}).
			Optional().
			Comment("For complex responses like multiple choice arrays"),

		// Context & enrichment
		field.JSON("metadata", map[string]interface{}{}).
			Optional().
			Comment("User agent, device, location, referrer, tags, custom fields, etc."),

		field.String("language").
			Optional().
			MaxLen(10).
			Comment("ISO language code (e.g., 'en', 'de')"),

		// AI Enrichment fields
		field.String("sentiment").
			Optional().
			Nillable().
			Comment("AI-detected sentiment (positive, negative, neutral)"),

		field.Float("sentiment_score").
			Optional().
			Nillable().
			Comment("Sentiment score from -1 (negative) to +1 (positive)"),

		field.String("emotion").
			Optional().
			Nillable().
			Comment("AI-detected emotion (joy, frustration, anger, etc.)"),

		field.JSON("topics", []string{}).
			Optional().
			Comment("AI-extracted topics/themes from text"),

		field.String("user_identifier").
			Optional().
			Comment("Anonymous ID or email hash for grouping responses"),

		// Embedding fields for semantic search
		field.Other("embedding", pgvector.Vector{}).
			Optional().
			Nillable().
			SchemaType(map[string]string{
				dialect.Postgres: "vector(1536)",
			}).
			Comment("OpenAI embedding vector for semantic search (1536 dimensions for text-embedding-3-small)"),

		field.String("embedding_model").
			Optional().
			Nillable().
			Comment("Name of the embedding model used (e.g., text-embedding-3-small)"),
	}
}

// Edges of the ExperienceData.
func (ExperienceData) Edges() []ent.Edge {
	return nil
}

// Indexes of the ExperienceData.
func (ExperienceData) Indexes() []ent.Index {
	return []ent.Index{
		// Multi-tenancy indexes
		index.Fields("tenant_id", "collected_at"),
		index.Fields("tenant_id", "source_type", "source_id", "collected_at"),

		// Response grouping index
		index.Fields("response_id"),
		index.Fields("tenant_id", "response_id"),

		// Composite index for querying by source
		index.Fields("source_type", "source_id", "collected_at").
			Annotations(entsql.IndexTypes(map[string]string{
				"metadata": "GIN",
			})),

		// Composite index for querying by field type and time
		index.Fields("field_type", "collected_at"),

		// Index for field_id aggregations
		index.Fields("field_id"),

		// Index for numeric aggregations (AVG, SUM, etc.)
		index.Fields("value_number"),

		// Index for user grouping
		index.Fields("user_identifier"),

		// Index for time-based queries
		index.Fields("collected_at"),

		// Indexes for AI enrichment fields
		index.Fields("sentiment"),
		index.Fields("emotion"),

		// HNSW index for fast vector similarity search (cosine distance)
		index.Fields("embedding").
			Annotations(
				entsql.IndexType("hnsw"),
				entsql.OpClass("vector_cosine_ops"),
			),
	}
}
