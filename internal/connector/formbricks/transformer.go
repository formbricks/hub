package formbricks

import (
	"encoding/json"
	"fmt"

	"github.com/formbricks/hub/internal/models"
	fb "github.com/formbricks/hub/pkg/formbricks"
)

// TransformResponseToFeedbackRecords converts a Formbricks response to Hub feedback records
// Each question/answer pair in the response becomes a separate feedback record
// Used by the polling connector
func TransformResponseToFeedbackRecords(response fb.Response) []*models.CreateFeedbackRecordRequest {
	var records []*models.CreateFeedbackRecordRequest

	// Build metadata from response
	metadata := buildMetadata(response)
	metadataJSON, _ := json.Marshal(metadata)

	// Transform each question/answer pair in response.Data to a feedback record
	for fieldID, value := range response.Data {
		record := &models.CreateFeedbackRecordRequest{
			CollectedAt:    &response.CreatedAt,
			SourceType:     "formbricks",
			SourceID:       &response.SurveyID,
			SourceName:     stringPtr("Formbricks Survey"),
			FieldID:        fieldID,
			FieldLabel:     stringPtr(fieldID), // Could be enhanced with question labels if available
			FieldType:      inferFieldType(value),
			Metadata:       metadataJSON,
			Language:       response.Language,
			UserIdentifier: response.ContactID,
			ResponseID:     &response.ID,
		}

		// Set value based on type
		setValue(record, value)

		records = append(records, record)
	}

	return records
}

// TransformWebhookToFeedbackRecords converts a Formbricks webhook event to Hub feedback records
// Each question/answer pair in the response becomes a separate feedback record
// Used by the webhook connector
func TransformWebhookToFeedbackRecords(event fb.WebhookEvent) []*models.CreateFeedbackRecordRequest {
	var records []*models.CreateFeedbackRecordRequest
	response := event.Data

	// Build metadata from webhook event (includes additional webhook-specific info)
	metadata := buildWebhookMetadata(event)
	metadataJSON, _ := json.Marshal(metadata)

	// Determine source name from survey title if available
	sourceName := "Formbricks Survey"
	if response.Survey != nil && response.Survey.Title != "" {
		sourceName = response.Survey.Title
	}

	// Transform each question/answer pair in response.Data to a feedback record
	for fieldID, value := range response.Data {
		record := &models.CreateFeedbackRecordRequest{
			CollectedAt:    &response.CreatedAt,
			SourceType:     "formbricks",
			SourceID:       &response.SurveyID,
			SourceName:     stringPtr(sourceName),
			FieldID:        fieldID,
			FieldLabel:     stringPtr(fieldID), // Could be enhanced with question labels if available
			FieldType:      inferFieldType(value),
			Metadata:       metadataJSON,
			Language:       response.Language,
			UserIdentifier: response.ContactID,
			ResponseID:     &response.ID,
		}

		// Set value based on type
		setValue(record, value)

		records = append(records, record)
	}

	return records
}

// buildWebhookMetadata constructs metadata from Formbricks webhook event
// Includes additional webhook-specific information
func buildWebhookMetadata(event fb.WebhookEvent) map[string]interface{} {
	response := event.Data

	metadata := map[string]interface{}{
		"formbricks_response_id": response.ID,
		"formbricks_display_id":  response.DisplayID,
		"formbricks_webhook_id":  event.WebhookID,
		"formbricks_event":       event.Event,
		"finished":               response.Finished,
		"meta": map[string]interface{}{
			"url":     response.Meta.URL,
			"country": response.Meta.Country,
			"user_agent": map[string]interface{}{
				"os":      response.Meta.UserAgent.OS,
				"device":  response.Meta.UserAgent.Device,
				"browser": response.Meta.UserAgent.Browser,
			},
		},
	}

	// Add survey info if available
	if response.Survey != nil {
		metadata["survey"] = map[string]interface{}{
			"title":  response.Survey.Title,
			"type":   response.Survey.Type,
			"status": response.Survey.Status,
		}
	}

	// Add tags if available
	if len(response.Tags) > 0 {
		metadata["tags"] = response.Tags
	}

	if response.Variables != nil {
		metadata["variables"] = response.Variables
	}
	if response.ContactAttributes != nil {
		metadata["contact_attributes"] = *response.ContactAttributes
	}
	if response.TTC != nil {
		metadata["ttc"] = response.TTC
	}

	return metadata
}

// buildMetadata constructs metadata from Formbricks response
func buildMetadata(response fb.Response) map[string]interface{} {
	metadata := map[string]interface{}{
		"formbricks_response_id": response.ID,
		"formbricks_display_id":  response.DisplayID,
		"finished":               response.Finished,
		"meta": map[string]interface{}{
			"url":     response.Meta.URL,
			"country": response.Meta.Country,
			"user_agent": map[string]interface{}{
				"os":      response.Meta.UserAgent.OS,
				"device":  response.Meta.UserAgent.Device,
				"browser": response.Meta.UserAgent.Browser,
			},
		},
	}

	if response.Variables != nil {
		metadata["variables"] = response.Variables
	}
	if response.ContactAttributes != nil {
		metadata["contact_attributes"] = *response.ContactAttributes
	}
	if response.TTC != nil {
		metadata["ttc"] = response.TTC
	}

	return metadata
}

// setValue sets the appropriate value field on the record based on the value type
func setValue(record *models.CreateFeedbackRecordRequest, value interface{}) {
	switch v := value.(type) {
	case string:
		record.ValueText = &v
	case float64:
		record.ValueNumber = &v
	case bool:
		record.ValueBoolean = &v
	case map[string]interface{}:
		// For complex objects, store as JSON in text
		if jsonBytes, err := json.Marshal(v); err == nil {
			jsonStr := string(jsonBytes)
			record.ValueText = &jsonStr
		}
	default:
		// Convert to string for unknown types
		valueStr := fmt.Sprintf("%v", v)
		record.ValueText = &valueStr
	}
}

// inferFieldType determines the field type based on the value
func inferFieldType(value interface{}) string {
	switch value.(type) {
	case string:
		return "text"
	case float64:
		return "number"
	case bool:
		return "boolean"
	case map[string]interface{}:
		return "text" // Complex objects stored as JSON text
	default:
		return "text"
	}
}

// stringPtr returns a pointer to the string value
func stringPtr(s string) *string {
	return &s
}
