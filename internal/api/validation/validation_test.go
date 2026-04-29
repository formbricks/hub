package validation

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateStructUsesAPITagNames(t *testing.T) {
	t.Run("json tag", func(t *testing.T) {
		var req struct {
			FieldID string `json:"field_id" validate:"required"`
		}

		err := ValidateStruct(req)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrValidationFailed)
		assert.Equal(t, "validation failed: field_id is required", err.Error())

		details := GetValidationErrorDetails(err)
		require.Len(t, details, 1)
		assert.Equal(t, "field_id", details[0].Location)
		assert.Equal(t, "field_id is required", details[0].Message)
		assert.Empty(t, details[0].Value)
	})

	t.Run("form tag", func(t *testing.T) {
		var filters struct {
			TenantID *string `form:"tenant_id" validate:"required"`
		}

		err := ValidateStruct(filters)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrValidationFailed)
		assert.Equal(t, "validation failed: tenant_id is required", err.Error())

		details := GetValidationErrorDetails(err)
		require.Len(t, details, 1)
		assert.Equal(t, "tenant_id", details[0].Location)
		assert.Equal(t, "tenant_id is required", details[0].Message)
		assert.Nil(t, details[0].Value)
	})
}

func TestValidateStructPreservesValidationDetails(t *testing.T) {
	valueText := "contains\x00null"
	req := struct {
		ValueText *string `json:"value_text,omitempty" validate:"omitempty,no_null_bytes"`
	}{
		ValueText: &valueText,
	}

	err := ValidateStruct(req)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrValidationFailed)
	assert.Equal(t, "validation failed: value_text must not contain NULL bytes", err.Error())

	details := GetValidationErrorDetails(err)
	require.Len(t, details, 1)
	assert.Equal(t, "value_text", details[0].Location)
	assert.Equal(t, "value_text must not contain NULL bytes", details[0].Message)
	assert.Equal(t, valueText, details[0].Value)
}
