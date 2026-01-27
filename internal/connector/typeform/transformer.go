package typeform

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/formbricks/hub/internal/models"
	tf "github.com/formbricks/hub/pkg/typeform"
)

// TransformResponseToFeedbackRecords converts a Typeform response to Hub feedback records
// Each answer in the response becomes a separate feedback record
// fieldLabels maps field IDs/refs to question titles for human-readable labels
func TransformResponseToFeedbackRecords(response tf.Response, formID string, formTitle string, fieldLabels map[string]string) []*models.CreateFeedbackRecordRequest {
	var records []*models.CreateFeedbackRecordRequest

	// Build metadata from response
	metadata := buildMetadata(response, formID)
	metadataJSON, _ := json.Marshal(metadata)

	responseID := response.GetID()

	// Determine source name - use form title if available
	sourceName := "Typeform"
	if formTitle != "" {
		sourceName = formTitle
	}

	// Transform each answer to a feedback record
	for _, answer := range response.Answers {
		// Use field ref if available, otherwise use field ID
		fieldID := answer.Field.ID
		if answer.Field.Ref != "" {
			fieldID = answer.Field.Ref
		}

		// Look up field label from cached form data
		fieldLabel := getFieldLabel(fieldLabels, answer.Field.ID, answer.Field.Ref)

		record := &models.CreateFeedbackRecordRequest{
			CollectedAt: &response.SubmittedAt,
			SourceType:  "typeform",
			SourceID:    &formID,
			SourceName:  stringPtr(sourceName),
			FieldID:     fieldID,
			FieldLabel:  stringPtr(fieldLabel),
			FieldType:   mapFieldType(answer.Field.Type, answer.Type),
			Metadata:    metadataJSON,
			ResponseID:  &responseID,
		}

		// Set value based on answer type
		setValue(record, answer)

		records = append(records, record)
	}

	return records
}

// getFieldLabel looks up the field label from the cached field labels map
// Falls back to field ID if label not found
func getFieldLabel(fieldLabels map[string]string, fieldID string, fieldRef string) string {
	if fieldLabels == nil {
		return fieldID
	}

	// Try to find by ID first
	if label, ok := fieldLabels[fieldID]; ok && label != "" {
		return label
	}

	// Try to find by ref
	if fieldRef != "" {
		if label, ok := fieldLabels[fieldRef]; ok && label != "" {
			return label
		}
	}

	// Fallback to field ID
	return fieldID
}

// buildMetadata constructs metadata from Typeform response
func buildMetadata(response tf.Response, formID string) map[string]interface{} {
	metadata := map[string]interface{}{
		"typeform_response_id": response.GetID(),
		"typeform_landing_id":  response.LandingID,
		"typeform_form_id":     formID,
		"landed_at":            response.LandedAt,
		"submitted_at":         response.SubmittedAt,
		"metadata": map[string]interface{}{
			"user_agent": response.Metadata.UserAgent,
			"platform":   response.Metadata.Platform,
			"referer":    response.Metadata.Referer,
			"network_id": response.Metadata.NetworkID,
			"browser":    response.Metadata.Browser,
		},
	}

	// Add calculated score if present
	if response.Calculated.Score != 0 {
		metadata["calculated_score"] = response.Calculated.Score
	}

	// Add hidden fields if present
	if len(response.Hidden) > 0 {
		metadata["hidden_fields"] = response.Hidden
	}

	// Add variables if present
	if len(response.Variables) > 0 {
		vars := make(map[string]interface{})
		for _, v := range response.Variables {
			if v.Type == "number" && v.Number != nil {
				vars[v.Key] = *v.Number
			} else if v.Type == "text" {
				vars[v.Key] = v.Text
			}
		}
		if len(vars) > 0 {
			metadata["variables"] = vars
		}
	}

	return metadata
}

// setValue sets the appropriate value field on the record based on the answer type
func setValue(record *models.CreateFeedbackRecordRequest, answer tf.Answer) {
	switch answer.Type {
	case tf.AnswerTypeText:
		record.ValueText = &answer.Text

	case tf.AnswerTypeNumber:
		if answer.Number != nil {
			record.ValueNumber = answer.Number
		}

	case tf.AnswerTypeBoolean:
		if answer.Boolean != nil {
			record.ValueBoolean = answer.Boolean
		}

	case tf.AnswerTypeEmail:
		record.ValueText = &answer.Email

	case tf.AnswerTypeURL:
		record.ValueText = &answer.URL

	case tf.AnswerTypeFileURL:
		record.ValueText = &answer.FileURL

	case tf.AnswerTypePhoneNumber:
		record.ValueText = &answer.PhoneNumber

	case tf.AnswerTypeDate:
		if answer.Date != nil {
			dateStr := answer.Date.Format("2006-01-02")
			record.ValueText = &dateStr
		}

	case tf.AnswerTypeChoice:
		if answer.Choice != nil {
			label := answer.Choice.Label
			if answer.Choice.Other != "" {
				label = answer.Choice.Other
			}
			record.ValueText = &label
		}

	case tf.AnswerTypeChoices:
		if answer.Choices != nil {
			// Join multiple choices with comma
			labels := strings.Join(answer.Choices.Labels, ", ")
			if answer.Choices.Other != "" {
				labels += ", " + answer.Choices.Other
			}
			record.ValueText = &labels
		}

	case tf.AnswerTypePayment:
		if answer.Payment != nil {
			paymentJSON, _ := json.Marshal(answer.Payment)
			paymentStr := string(paymentJSON)
			record.ValueText = &paymentStr
		}

	case tf.AnswerTypeMultiFormat:
		if answer.MultiFormat != nil {
			multiJSON, _ := json.Marshal(answer.MultiFormat)
			multiStr := string(multiJSON)
			record.ValueText = &multiStr
		}

	default:
		// For unknown types, try to convert to string
		valueStr := fmt.Sprintf("%v", answer)
		record.ValueText = &valueStr
	}
}

// mapFieldType maps Typeform field/answer types to Hub field types
func mapFieldType(fieldType, answerType string) string {
	// First check answer type
	switch answerType {
	case tf.AnswerTypeNumber:
		return "number"
	case tf.AnswerTypeBoolean:
		return "boolean"
	case tf.AnswerTypeDate:
		return "date"
	case tf.AnswerTypeEmail:
		return "email"
	case tf.AnswerTypeURL:
		return "url"
	case tf.AnswerTypePhoneNumber:
		return "phone"
	}

	// Then check field type
	switch fieldType {
	case tf.QuestionTypeShortText, tf.QuestionTypeLongText:
		return "text"
	case tf.QuestionTypeNumber:
		return "number"
	case tf.QuestionTypeRating, tf.QuestionTypeOpinionScale:
		return "rating"
	case tf.QuestionTypeYesNo, tf.QuestionTypeLegal:
		return "boolean"
	case tf.QuestionTypeEmail:
		return "email"
	case tf.QuestionTypeWebsite:
		return "url"
	case tf.QuestionTypeDate:
		return "date"
	case tf.QuestionTypeDropdown, tf.QuestionTypeMultipleChoice, tf.QuestionTypePictureChoice:
		return "choice"
	case tf.QuestionTypeFileUpload:
		return "file"
	case tf.QuestionTypePhoneNumber:
		return "phone"
	case tf.QuestionTypePayment:
		return "payment"
	default:
		return "text"
	}
}

// stringPtr returns a pointer to the string value
func stringPtr(s string) *string {
	return &s
}
