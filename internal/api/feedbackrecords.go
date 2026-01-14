package api

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/formbricks/hub/internal/ent"
	"github.com/formbricks/hub/internal/ent/feedbackrecord"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/queue"
	"github.com/formbricks/hub/internal/webhook"
)

// enqueueAIJobs enqueues enrichment and embedding jobs for text responses.
func enqueueAIJobs(ctx context.Context, logger *slog.Logger, queue queue.Queue, fr *ent.FeedbackRecord, fieldLabel, valueText string) {
	// Build text with question context if available (used for both enrichment and embeddings)
	enrichmentText := valueText
	if fieldLabel != "" {
		enrichmentText = fmt.Sprintf("Question: %s\nResponse: %s", fieldLabel, valueText)
	}

	// Enqueue enrichment job (sentiment/topics/emotion) with question context
	if err := queue.Enqueue(ctx, fr.ID.String(), enrichmentText); err != nil {
		logger.Warn("failed to enqueue enrichment job", "feedback_record_id", fr.ID, "error", err)
	} else {
		logger.Debug("enrichment job enqueued", "feedback_record_id", fr.ID)
	}

	// Enqueue embedding job (vector generation for semantic search)
	if err := queue.EnqueueEmbedding(ctx, fr.ID.String(), enrichmentText); err != nil {
		logger.Warn("failed to enqueue embedding job", "feedback_record_id", fr.ID, "error", err)
	} else {
		logger.Debug("embedding job enqueued", "feedback_record_id", fr.ID)
	}
}

// RegisterFeedbackRecordRoutes registers all feedback record-related routes
func RegisterFeedbackRecordRoutes(api huma.API, client *ent.Client, dispatcher *webhook.Dispatcher, logger *slog.Logger, enrichmentQueue queue.Queue) {
	// POST /v1/feedback-records - Create feedback record
	huma.Register(api, huma.Operation{
		OperationID: "create-feedback-record",
		Method:      "POST",
		Path:        "/v1/feedback-records",
		Summary:     "Create a new feedback record",
		Description: "Creates a new feedback record data point",
		Tags:        []string{"Feedback Records"},
	}, func(ctx context.Context, input *CreateFeedbackRecordInput) (*FeedbackRecordOutput, error) {
		// Set default collected_at if not provided
		collectedAt := time.Now()
		if input.Body.CollectedAt != nil {
			collectedAt = *input.Body.CollectedAt
		}

		// Create the feedback record
		builder := client.FeedbackRecord.Create().
			SetSourceType(input.Body.SourceType).
			SetFieldID(input.Body.FieldID).
			SetFieldType(input.Body.FieldType).
			SetCollectedAt(collectedAt)

		// Set multi-tenancy and response grouping fields
		if input.Body.TenantID != nil {
			builder.SetTenantID(*input.Body.TenantID)
		}
		if input.Body.ResponseID != nil {
			builder.SetResponseID(*input.Body.ResponseID)
		}

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

		fr, err := builder.Save(ctx)
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
			enqueueAIJobs(ctx, logger, enrichmentQueue, fr, fieldLabel, *input.Body.ValueText)
		}

		logger.Info("feedback record created", "id", fr.ID, "queued_for_ai_processing", shouldProcess && enrichmentQueue != nil)

		// Dispatch webhook asynchronously
		dispatcher.DispatchAsync(webhook.EventFeedbackRecordCreated, entityToOutput(fr))

		return &FeedbackRecordOutput{Body: entityToOutput(fr)}, nil
	})

	// GET /v1/feedback-records/{id} - Get single feedback record
	huma.Register(api, huma.Operation{
		OperationID: "get-feedback-record",
		Method:      "GET",
		Path:        "/v1/feedback-records/{id}",
		Summary:     "Get a feedback record by ID",
		Description: "Retrieves a single feedback record data point by its UUID",
		Tags:        []string{"Feedback Records"},
	}, func(ctx context.Context, input *GetFeedbackRecordInput) (*FeedbackRecordOutput, error) {
		id, err := parseUUID(input.ID)
		if err != nil {
			return nil, err
		}

		fr, err := client.FeedbackRecord.Get(ctx, id)
		if err != nil {
			// Use sanitized error handling
			return nil, handleDatabaseError(logger, err, "get", id.String())
		}

		return &FeedbackRecordOutput{Body: entityToOutput(fr)}, nil
	})

	// GET /v1/feedback-records - List feedback records with filters
	huma.Register(api, huma.Operation{
		OperationID: "list-feedback-records",
		Method:      "GET",
		Path:        "/v1/feedback-records",
		Summary:     "List feedback records with filters",
		Description: "Lists feedback records with optional filters and pagination",
		Tags:        []string{"Feedback Records"},
	}, func(ctx context.Context, input *ListFeedbackRecordsInput) (*ListFeedbackRecordsOutput, error) {
		// Set defaults (already set by Huma's default tags)
		limit := input.Limit
		offset := input.Offset

		// Build query
		query := client.FeedbackRecord.Query()

		// Apply multi-tenancy and response grouping filters
		if input.TenantID != "" {
			query = query.Where(feedbackrecord.TenantIDEQ(input.TenantID))
		}
		if input.ResponseID != "" {
			query = query.Where(feedbackrecord.ResponseIDEQ(input.ResponseID))
		}

		// Apply filters (check for non-empty strings)
		if input.SourceType != "" {
			query = query.Where(feedbackrecord.SourceTypeEQ(input.SourceType))
		}
		if input.SourceID != "" {
			query = query.Where(feedbackrecord.SourceIDEQ(input.SourceID))
		}
		if input.FieldType != "" {
			query = query.Where(feedbackrecord.FieldTypeEQ(input.FieldType))
		}
		if input.UserIdentifier != "" {
			query = query.Where(feedbackrecord.UserIdentifierEQ(input.UserIdentifier))
		}
		if input.Since != "" {
			// Parse ISO 8601 time string
			sinceTime, err := time.Parse(time.RFC3339, input.Since)
			if err != nil {
				return nil, huma.Error400BadRequest("Invalid 'since' timestamp format. Expected ISO 8601 (RFC3339) format, e.g., 2024-01-01T00:00:00Z")
			}
			query = query.Where(feedbackrecord.CollectedAtGTE(sinceTime))
		}
		if input.Until != "" {
			// Parse ISO 8601 time string
			untilTime, err := time.Parse(time.RFC3339, input.Until)
			if err != nil {
				return nil, huma.Error400BadRequest("Invalid 'until' timestamp format. Expected ISO 8601 (RFC3339) format, e.g., 2024-12-31T23:59:59Z")
			}
			query = query.Where(feedbackrecord.CollectedAtLTE(untilTime))
		}

		// Get total count
		total, err := query.Count(ctx)
		if err != nil {
			// Use sanitized error handling
			return nil, handleDatabaseError(logger, err, "count", "feedback records")
		}

		// Apply pagination and ordering
		records, err := query.
			Limit(limit).
			Offset(offset).
			Order(ent.Desc(feedbackrecord.FieldCollectedAt)).
			All(ctx)
		if err != nil {
			// Use sanitized error handling
			return nil, handleDatabaseError(logger, err, "list", "feedback records")
		}

		// Convert to output
		data := make([]FeedbackRecordData, len(records))
		for i, r := range records {
			data[i] = entityToOutput(r)
		}

		return &ListFeedbackRecordsOutput{
			Body: struct {
				Data   []FeedbackRecordData `json:"data" doc:"List of feedback records"`
				Total  int                  `json:"total" doc:"Total count of feedback records matching filters"`
				Limit  int                  `json:"limit" doc:"Limit used in query"`
				Offset int                  `json:"offset" doc:"Offset used in query"`
			}{
				Data:   data,
				Total:  total,
				Limit:  limit,
				Offset: offset,
			},
		}, nil
	})

	// PATCH /v1/feedback-records/{id} - Update feedback record
	huma.Register(api, huma.Operation{
		OperationID: "update-feedback-record",
		Method:      "PATCH",
		Path:        "/v1/feedback-records/{id}",
		Summary:     "Update a feedback record",
		Description: "Updates specific fields of a feedback record data point",
		Tags:        []string{"Feedback Records"},
	}, func(ctx context.Context, input *UpdateFeedbackRecordInput) (*FeedbackRecordOutput, error) {
		id, err := parseUUID(input.ID)
		if err != nil {
			return nil, err
		}

		// Track if value_text is being updated for reprocessing
		valueTextChanged := input.Body.ValueText != nil

		// Build update query
		update := client.FeedbackRecord.UpdateOneID(id)

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

		fr, err := update.Save(ctx)
		if err != nil {
			// Use sanitized error handling
			return nil, handleDatabaseError(logger, err, "update", id.String())
		}

		// If value_text changed, re-enqueue AI processing jobs to update enrichment/embeddings
		if valueTextChanged && enrichmentQueue != nil && *input.Body.ValueText != "" {
			fieldType := models.FieldType(fr.FieldType)
			if fieldType.ShouldEnrich() {
				fieldLabel := fr.FieldLabel
				enqueueAIJobs(ctx, logger, enrichmentQueue, fr, fieldLabel, *input.Body.ValueText)
				logger.Info("feedback record updated with AI reprocessing", "id", fr.ID)
			}
		} else {
			logger.Info("feedback record updated", "id", fr.ID)
		}

		// Dispatch webhook asynchronously
		dispatcher.DispatchAsync(webhook.EventFeedbackRecordUpdated, entityToOutput(fr))

		return &FeedbackRecordOutput{Body: entityToOutput(fr)}, nil
	})

	// DELETE /v1/feedback-records/{id} - Delete feedback record
	huma.Register(api, huma.Operation{
		OperationID: "delete-feedback-record",
		Method:      "DELETE",
		Path:        "/v1/feedback-records/{id}",
		Summary:     "Delete a feedback record",
		Description: "Permanently deletes a feedback record data point",
		Tags:        []string{"Feedback Records"},
	}, func(ctx context.Context, input *DeleteFeedbackRecordInput) (*struct{}, error) {
		id, err := parseUUID(input.ID)
		if err != nil {
			return nil, err
		}

		// Get the feedback record before deleting (for webhook)
		fr, err := client.FeedbackRecord.Get(ctx, id)
		if err != nil {
			// Use sanitized error handling
			return nil, handleDatabaseError(logger, err, "get for deletion", id.String())
		}

		// Delete the feedback record
		err = client.FeedbackRecord.DeleteOneID(id).Exec(ctx)
		if err != nil {
			// Use sanitized error handling
			return nil, handleDatabaseError(logger, err, "delete", id.String())
		}

		logger.Info("feedback record deleted", "id", id)

		// Dispatch webhook asynchronously
		dispatcher.DispatchAsync(webhook.EventFeedbackRecordDeleted, entityToOutput(fr))

		return &struct{}{}, nil
	})

	// DELETE /v1/feedback-records - Bulk delete feedback records (GDPR compliance)
	huma.Register(api, huma.Operation{
		OperationID:   "bulk-delete-feedback-records",
		Method:        "DELETE",
		Path:          "/v1/feedback-records",
		Summary:       "Bulk delete feedback records by user identifier",
		Description:   "Permanently deletes all feedback record data points matching the specified user_identifier. This endpoint supports GDPR Article 17 (Right to Erasure) requests.",
		Tags:          []string{"Feedback Records"},
		DefaultStatus: 200,
	}, func(ctx context.Context, input *BulkDeleteFeedbackRecordsInput) (*BulkDeleteFeedbackRecordsOutput, error) {
		if input.UserIdentifier == "" {
			return nil, huma.Error400BadRequest("user_identifier query parameter is required for bulk deletion")
		}

		// Build query to find matching records
		query := client.FeedbackRecord.Query().
			Where(feedbackrecord.UserIdentifierEQ(input.UserIdentifier))

		// Apply optional tenant filter for multi-tenant deployments
		if input.TenantID != "" {
			query = query.Where(feedbackrecord.TenantIDEQ(input.TenantID))
		}

		// Get feedback records before deleting (for webhooks)
		records, err := query.All(ctx)
		if err != nil {
			return nil, handleDatabaseError(logger, err, "query for bulk deletion", input.UserIdentifier)
		}

		if len(records) == 0 {
			return &BulkDeleteFeedbackRecordsOutput{
				Body: struct {
					DeletedCount int    `json:"deleted_count" doc:"Number of records deleted"`
					Message      string `json:"message" doc:"Human-readable status message"`
				}{
					DeletedCount: 0,
					Message:      "No records found matching the specified user_identifier",
				},
			}, nil
		}

		// Build delete query
		deleteQuery := client.FeedbackRecord.Delete().
			Where(feedbackrecord.UserIdentifierEQ(input.UserIdentifier))

		if input.TenantID != "" {
			deleteQuery = deleteQuery.Where(feedbackrecord.TenantIDEQ(input.TenantID))
		}

		// Execute bulk deletion
		deletedCount, err := deleteQuery.Exec(ctx)
		if err != nil {
			return nil, handleDatabaseError(logger, err, "bulk delete", input.UserIdentifier)
		}

		logger.Info("bulk deletion completed",
			"user_identifier", input.UserIdentifier,
			"tenant_id", input.TenantID,
			"deleted_count", deletedCount,
		)

		// Dispatch webhooks for each deleted feedback record
		for _, r := range records {
			dispatcher.DispatchAsync(webhook.EventFeedbackRecordDeleted, entityToOutput(r))
		}

		return &BulkDeleteFeedbackRecordsOutput{
			Body: struct {
				DeletedCount int    `json:"deleted_count" doc:"Number of records deleted"`
				Message      string `json:"message" doc:"Human-readable status message"`
			}{
				DeletedCount: deletedCount,
				Message:      fmt.Sprintf("Successfully deleted %d feedback record(s) for user_identifier: %s", deletedCount, input.UserIdentifier),
			},
		}, nil
	})
}

// entityToOutput converts an Ent entity to the output format via the domain model.
// This allows for business logic transformation in the future.
func entityToOutput(fr *ent.FeedbackRecord) FeedbackRecordData {
	// Convert: Ent entity → Domain model → API response
	domainModel := models.FromEnt(fr)

	var apiData FeedbackRecordData
	apiData.FromModel(domainModel)

	return apiData
}
