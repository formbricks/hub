package api

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/formbricks/hub/apps/hub/internal/ent"
	"github.com/formbricks/hub/apps/hub/internal/ent/experiencedata"
	"github.com/formbricks/hub/apps/hub/internal/models"
	"github.com/formbricks/hub/apps/hub/internal/queue"
	"github.com/formbricks/hub/apps/hub/internal/webhook"
)

// enqueueAIJobs enqueues enrichment and embedding jobs for text responses.
func enqueueAIJobs(ctx context.Context, logger *slog.Logger, queue queue.Queue, exp *ent.ExperienceData, fieldLabel, valueText string) {
	// Build text with question context if available (used for both enrichment and embeddings)
	enrichmentText := valueText
	if fieldLabel != "" {
		enrichmentText = fmt.Sprintf("Question: %s\nResponse: %s", fieldLabel, valueText)
	}

	// Enqueue enrichment job (sentiment/topics/emotion) with question context
	if err := queue.Enqueue(ctx, exp.ID.String(), enrichmentText); err != nil {
		logger.Warn("failed to enqueue enrichment job", "experience_id", exp.ID, "error", err)
	} else {
		logger.Debug("enrichment job enqueued", "experience_id", exp.ID)
	}

	// Enqueue embedding job (vector generation for semantic search)
	if err := queue.EnqueueEmbedding(ctx, exp.ID.String(), enrichmentText); err != nil {
		logger.Warn("failed to enqueue embedding job", "experience_id", exp.ID, "error", err)
	} else {
		logger.Debug("embedding job enqueued", "experience_id", exp.ID)
	}
}

// RegisterExperienceRoutes registers all experience-related routes
func RegisterExperienceRoutes(api huma.API, client *ent.Client, dispatcher *webhook.Dispatcher, logger *slog.Logger, enrichmentQueue queue.Queue) {
	// POST /v1/experiences - Create experience
	huma.Register(api, huma.Operation{
		OperationID: "create-experience",
		Method:      "POST",
		Path:        "/v1/experiences",
		Summary:     "Create a new experience data record",
		Description: "Creates a new experience data record",
		Tags:        []string{"Experiences"},
	}, func(ctx context.Context, input *CreateExperienceInput) (*ExperienceOutput, error) {
		// Set default collected_at if not provided
		collectedAt := time.Now()
		if input.Body.CollectedAt != nil {
			collectedAt = *input.Body.CollectedAt
		}

		// Create the experience
		builder := client.ExperienceData.Create().
			SetSourceType(input.Body.SourceType).
			SetFieldID(input.Body.FieldID).
			SetFieldType(input.Body.FieldType).
			SetCollectedAt(collectedAt)

		// Set optional fields
		if input.Body.SourceID != nil {
			builder.SetSourceID(*input.Body.SourceID)
		}
		if input.Body.SourceName != nil {
			builder.SetSourceName(*input.Body.SourceName)
		}
		if input.Body.FieldLabel != nil {
			builder.SetFieldLabel(*input.Body.FieldLabel)
		}
		if input.Body.ValueText != nil {
			builder.SetValueText(*input.Body.ValueText)
		}
		if input.Body.ValueNumber != nil {
			builder.SetValueNumber(*input.Body.ValueNumber)
		}
		if input.Body.ValueBoolean != nil {
			builder.SetValueBoolean(*input.Body.ValueBoolean)
		}
		if input.Body.ValueDate != nil {
			builder.SetValueDate(*input.Body.ValueDate)
		}
		if input.Body.ValueJSON != nil {
			builder.SetValueJSON(input.Body.ValueJSON)
		}
		if input.Body.Metadata != nil {
			builder.SetMetadata(input.Body.Metadata)
		}
		if input.Body.Language != nil {
			builder.SetLanguage(*input.Body.Language)
		}
		if input.Body.UserIdentifier != nil {
			builder.SetUserIdentifier(*input.Body.UserIdentifier)
		}

		exp, err := builder.Save(ctx)
		if err != nil {
			return nil, handleDatabaseError(logger, err, "create", "new")
		}

		// Enqueue AI processing jobs if applicable
		fieldType := models.FieldType(input.Body.FieldType)
		shouldProcess := fieldType.ShouldEnrich() &&
			input.Body.ValueText != nil &&
			*input.Body.ValueText != ""

		if shouldProcess && enrichmentQueue != nil {
			fieldLabel := ""
			if input.Body.FieldLabel != nil {
				fieldLabel = *input.Body.FieldLabel
			}
			enqueueAIJobs(ctx, logger, enrichmentQueue, exp, fieldLabel, *input.Body.ValueText)
		}

		logger.Info("experience created", "id", exp.ID, "queued_for_ai_processing", shouldProcess && enrichmentQueue != nil)

		// Dispatch webhook asynchronously
		dispatcher.DispatchAsync(webhook.EventExperienceCreated, entityToOutput(exp))

		return &ExperienceOutput{Body: entityToOutput(exp)}, nil
	})

	// GET /v1/experiences/{id} - Get single experience
	huma.Register(api, huma.Operation{
		OperationID: "get-experience",
		Method:      "GET",
		Path:        "/v1/experiences/{id}",
		Summary:     "Get an experience by ID",
		Description: "Retrieves a single experience data record by its UUID",
		Tags:        []string{"Experiences"},
	}, func(ctx context.Context, input *GetExperienceInput) (*ExperienceOutput, error) {
		id, err := parseUUID(input.ID)
		if err != nil {
			return nil, err
		}

		exp, err := client.ExperienceData.Get(ctx, id)
		if err != nil {
			// Use sanitized error handling
			return nil, handleDatabaseError(logger, err, "get", id.String())
		}

		return &ExperienceOutput{Body: entityToOutput(exp)}, nil
	})

	// GET /v1/experiences - List experiences with filters
	huma.Register(api, huma.Operation{
		OperationID: "list-experiences",
		Method:      "GET",
		Path:        "/v1/experiences",
		Summary:     "List experiences with filters",
		Description: "Lists experiences with optional filters and pagination",
		Tags:        []string{"Experiences"},
	}, func(ctx context.Context, input *ListExperiencesInput) (*ListExperiencesOutput, error) {
		// Set defaults (already set by Huma's default tags)
		limit := input.Limit
		offset := input.Offset

		// Build query
		query := client.ExperienceData.Query()

		// Apply filters (check for non-empty strings)
		if input.SourceType != "" {
			query = query.Where(experiencedata.SourceTypeEQ(input.SourceType))
		}
		if input.SourceID != "" {
			query = query.Where(experiencedata.SourceIDEQ(input.SourceID))
		}
		if input.FieldType != "" {
			query = query.Where(experiencedata.FieldTypeEQ(input.FieldType))
		}
		if input.UserIdentifier != "" {
			query = query.Where(experiencedata.UserIdentifierEQ(input.UserIdentifier))
		}
		if input.Since != "" {
			// Parse ISO 8601 time string
			sinceTime, err := time.Parse(time.RFC3339, input.Since)
			if err == nil {
				query = query.Where(experiencedata.CollectedAtGTE(sinceTime))
			}
		}
		if input.Until != "" {
			// Parse ISO 8601 time string
			untilTime, err := time.Parse(time.RFC3339, input.Until)
			if err == nil {
				query = query.Where(experiencedata.CollectedAtLTE(untilTime))
			}
		}

		// Get total count
		total, err := query.Count(ctx)
		if err != nil {
			// Use sanitized error handling
			return nil, handleDatabaseError(logger, err, "count", "experiences")
		}

		// Apply pagination and ordering
		experiences, err := query.
			Limit(limit).
			Offset(offset).
			Order(ent.Desc(experiencedata.FieldCollectedAt)).
			All(ctx)
		if err != nil {
			// Use sanitized error handling
			return nil, handleDatabaseError(logger, err, "list", "experiences")
		}

		// Convert to output
		data := make([]ExperienceData, len(experiences))
		for i, exp := range experiences {
			data[i] = entityToOutput(exp)
		}

		return &ListExperiencesOutput{
			Body: struct {
				Data   []ExperienceData `json:"data" doc:"List of experiences"`
				Total  int              `json:"total" doc:"Total count of experiences matching filters"`
				Limit  int              `json:"limit" doc:"Limit used in query"`
				Offset int              `json:"offset" doc:"Offset used in query"`
			}{
				Data:   data,
				Total:  total,
				Limit:  limit,
				Offset: offset,
			},
		}, nil
	})

	// PATCH /v1/experiences/{id} - Update experience
	huma.Register(api, huma.Operation{
		OperationID: "update-experience",
		Method:      "PATCH",
		Path:        "/v1/experiences/{id}",
		Summary:     "Update an experience",
		Description: "Updates specific fields of an experience data record",
		Tags:        []string{"Experiences"},
	}, func(ctx context.Context, input *UpdateExperienceInput) (*ExperienceOutput, error) {
		id, err := parseUUID(input.ID)
		if err != nil {
			return nil, err
		}

		// Build update query
		update := client.ExperienceData.UpdateOneID(id)

		// Apply updates for provided fields
		if input.Body.ValueText != nil {
			update.SetValueText(*input.Body.ValueText)
		}
		if input.Body.ValueNumber != nil {
			update.SetValueNumber(*input.Body.ValueNumber)
		}
		if input.Body.ValueBoolean != nil {
			update.SetValueBoolean(*input.Body.ValueBoolean)
		}
		if input.Body.ValueDate != nil {
			update.SetValueDate(*input.Body.ValueDate)
		}
		if input.Body.ValueJSON != nil {
			update.SetValueJSON(input.Body.ValueJSON)
		}
		if input.Body.Metadata != nil {
			update.SetMetadata(input.Body.Metadata)
		}
		if input.Body.Language != nil {
			update.SetLanguage(*input.Body.Language)
		}
		if input.Body.UserIdentifier != nil {
			update.SetUserIdentifier(*input.Body.UserIdentifier)
		}

		exp, err := update.Save(ctx)
		if err != nil {
			// Use sanitized error handling
			return nil, handleDatabaseError(logger, err, "update", id.String())
		}

		logger.Info("experience updated", "id", exp.ID)

		// Dispatch webhook asynchronously
		dispatcher.DispatchAsync(webhook.EventExperienceUpdated, entityToOutput(exp))

		return &ExperienceOutput{Body: entityToOutput(exp)}, nil
	})

	// DELETE /v1/experiences/{id} - Delete experience
	huma.Register(api, huma.Operation{
		OperationID: "delete-experience",
		Method:      "DELETE",
		Path:        "/v1/experiences/{id}",
		Summary:     "Delete an experience",
		Description: "Permanently deletes an experience data record",
		Tags:        []string{"Experiences"},
	}, func(ctx context.Context, input *DeleteExperienceInput) (*struct{}, error) {
		id, err := parseUUID(input.ID)
		if err != nil {
			return nil, err
		}

		// Get the experience before deleting (for webhook)
		exp, err := client.ExperienceData.Get(ctx, id)
		if err != nil {
			// Use sanitized error handling
			return nil, handleDatabaseError(logger, err, "get for deletion", id.String())
		}

		// Delete the experience
		err = client.ExperienceData.DeleteOneID(id).Exec(ctx)
		if err != nil {
			// Use sanitized error handling
			return nil, handleDatabaseError(logger, err, "delete", id.String())
		}

		logger.Info("experience deleted", "id", id)

		// Dispatch webhook asynchronously
		dispatcher.DispatchAsync(webhook.EventExperienceDeleted, entityToOutput(exp))

		return &struct{}{}, nil
	})
}

// entityToOutput converts an Ent entity to the output format via the domain model.
// This allows for business logic transformation in the future.
func entityToOutput(exp *ent.ExperienceData) ExperienceData {
	// Convert: Ent entity → Domain model → API response
	domainModel := models.FromEnt(exp)

	var apiData ExperienceData
	apiData.FromModel(domainModel)

	return apiData
}
