package handlers

import (
	"net/http"

	"github.com/formbricks/hub/internal/api/response"
)

// TaxonomyInternalHandler hosts internal taxonomy service endpoints.
type TaxonomyInternalHandler struct{}

// NewTaxonomyInternalHandler creates a taxonomy internal handler.
func NewTaxonomyInternalHandler() *TaxonomyInternalHandler {
	return &TaxonomyInternalHandler{}
}

// AuthCheck verifies that the caller passed the internal Hub API token.
func (h *TaxonomyInternalHandler) AuthCheck(w http.ResponseWriter, _ *http.Request) {
	response.RespondJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"service": "hub-taxonomy-internal",
	})
}
