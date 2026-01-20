package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/xernobyl/formbricks_worktrial/internal/models"
	"github.com/xernobyl/formbricks_worktrial/internal/service"
)

// ExperienceHandler handles HTTP requests for experience data
type ExperienceHandler struct {
	service *service.ExperienceService
}

// NewExperienceHandler creates a new experience handler
func NewExperienceHandler(service *service.ExperienceService) *ExperienceHandler {
	return &ExperienceHandler{service: service}
}

// Create handles POST /v1/experiences
// @Summary Create experience data
// @Description Create a new experience data record
// @Tags experiences
// @Accept json
// @Produce json
// @Param request body models.CreateExperienceRequest true "Experience data to create"
// @Success 201 {object} models.ExperienceData
// @Failure 400 {object} ErrorResponse
// @Failure 401 {object} ErrorResponse "Unauthorized - Invalid or missing API key"
// @Security BearerAuth
// @Router /v1/experiences [post]
func (h *ExperienceHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req models.CreateExperienceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_request", "Invalid request body")
		return
	}

	exp, err := h.service.CreateExperience(r.Context(), &req)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "creation_failed", err.Error())
		return
	}

	RespondSuccess(w, http.StatusCreated, exp)
}

// Get handles GET /v1/experiences/{id}
// @Summary Get experience data by ID
// @Description Retrieve a single experience data record by its UUID
// @Tags experiences
// @Produce json
// @Param id path string true "Experience ID (UUID)"
// @Success 200 {object} models.ExperienceData
// @Failure 400 {object} ErrorResponse "Invalid UUID format"
// @Failure 401 {object} ErrorResponse "Unauthorized - Invalid or missing API key"
// @Failure 404 {object} ErrorResponse "Experience not found"
// @Security BearerAuth
// @Router /v1/experiences/{id} [get]
func (h *ExperienceHandler) Get(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	if idStr == "" {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Experience ID is required")
		return
	}

	id, err := uuid.Parse(idStr)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid UUID format")
		return
	}

	exp, err := h.service.GetExperience(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}

	RespondSuccess(w, http.StatusOK, exp)
}

// List handles GET /v1/experiences
// @Summary List experience data
// @Description Retrieve a list of experience data records with optional filters
// @Tags experiences
// @Produce json
// @Param source_type query string false "Filter by source type"
// @Param source_id query string false "Filter by source ID"
// @Param field_id query string false "Filter by field ID"
// @Param user_identifier query string false "Filter by user identifier"
// @Param limit query int false "Maximum number of records to return"
// @Param offset query int false "Number of records to skip"
// @Success 200 {array} models.ExperienceData
// @Failure 401 {object} ErrorResponse "Unauthorized - Invalid or missing API key"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Security BearerAuth
// @Router /v1/experiences [get]
func (h *ExperienceHandler) List(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	filters := &models.ListExperiencesFilters{}

	if sourceType := query.Get("source_type"); sourceType != "" {
		filters.SourceType = &sourceType
	}

	if sourceID := query.Get("source_id"); sourceID != "" {
		filters.SourceID = &sourceID
	}

	if fieldID := query.Get("field_id"); fieldID != "" {
		filters.FieldID = &fieldID
	}

	if userIdentifier := query.Get("user_identifier"); userIdentifier != "" {
		filters.UserIdentifier = &userIdentifier
	}

	if limitStr := query.Get("limit"); limitStr != "" {
		limit, err := strconv.Atoi(limitStr)
		if err == nil && limit > 0 {
			filters.Limit = limit
		}
	}

	if offsetStr := query.Get("offset"); offsetStr != "" {
		offset, err := strconv.Atoi(offsetStr)
		if err == nil && offset >= 0 {
			filters.Offset = offset
		}
	}

	experiences, err := h.service.ListExperiences(r.Context(), filters)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}

	RespondSuccess(w, http.StatusOK, experiences)
}

// Update handles PATCH /v1/experiences/{id}
// @Summary Update experience data
// @Description Update an existing experience data record
// @Tags experiences
// @Accept json
// @Produce json
// @Param id path string true "Experience ID (UUID)"
// @Param request body models.UpdateExperienceRequest true "Fields to update"
// @Success 200 {object} models.ExperienceData
// @Failure 400 {object} ErrorResponse "Invalid request or UUID format"
// @Failure 401 {object} ErrorResponse "Unauthorized - Invalid or missing API key"
// @Failure 404 {object} ErrorResponse "Experience not found"
// @Security BearerAuth
// @Router /v1/experiences/{id} [patch]
func (h *ExperienceHandler) Update(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	if idStr == "" {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Experience ID is required")
		return
	}

	id, err := uuid.Parse(idStr)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid UUID format")
		return
	}

	var req models.UpdateExperienceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_request", "Invalid request body")
		return
	}

	exp, err := h.service.UpdateExperience(r.Context(), id, &req)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "update_failed", err.Error())
		return
	}

	RespondSuccess(w, http.StatusOK, exp)
}

// Delete handles DELETE /v1/experiences/{id}
// @Summary Delete experience data
// @Description Delete an experience data record by ID
// @Tags experiences
// @Param id path string true "Experience ID (UUID)"
// @Success 204 "No Content - Successfully deleted"
// @Failure 400 {object} ErrorResponse "Invalid UUID format"
// @Failure 401 {object} ErrorResponse "Unauthorized - Invalid or missing API key"
// @Failure 404 {object} ErrorResponse "Experience not found"
// @Security BearerAuth
// @Router /v1/experiences/{id} [delete]
func (h *ExperienceHandler) Delete(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	if idStr == "" {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Experience ID is required")
		return
	}

	id, err := uuid.Parse(idStr)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid UUID format")
		return
	}

	if err := h.service.DeleteExperience(r.Context(), id); err != nil {
		RespondError(w, http.StatusNotFound, "delete_failed", err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// Search handles GET /v1/experiences/search
// @Summary Search experience data
// @Description Search experience data with advanced filters, full-text search, and pagination
// @Tags experiences
// @Produce json
// @Param query query string false "Full-text search query"
// @Param source_type query string false "Filter by source type"
// @Param source_id query string false "Filter by source ID"
// @Param field_id query string false "Filter by field ID"
// @Param field_type query string false "Filter by field type"
// @Param user_identifier query string false "Filter by user identifier"
// @Param start_date query string false "Filter by collected_at >= start_date (RFC3339 format)"
// @Param end_date query string false "Filter by collected_at <= end_date (RFC3339 format)"
// @Param pageSize query int false "Number of results per page (default 20, max 40)"
// @Param page query int false "Page number (starts at 0, default 0)"
// @Success 200 {object} models.SearchExperiencesResponse
// @Failure 400 {object} ErrorResponse "Invalid request parameters"
// @Failure 401 {object} ErrorResponse "Unauthorized - Invalid or missing API key"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Security BearerAuth
// @Router /v1/experiences/search [get]
func (h *ExperienceHandler) Search(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	req := &models.SearchExperiencesRequest{}

	// Parse full-text search query
	if q := query.Get("query"); q != "" {
		req.Query = &q
	}

	// Parse filters
	if sourceType := query.Get("source_type"); sourceType != "" {
		req.SourceType = &sourceType
	}

	if sourceID := query.Get("source_id"); sourceID != "" {
		req.SourceID = &sourceID
	}

	if fieldID := query.Get("field_id"); fieldID != "" {
		req.FieldID = &fieldID
	}

	if fieldType := query.Get("field_type"); fieldType != "" {
		req.FieldType = &fieldType
	}

	if userIdentifier := query.Get("user_identifier"); userIdentifier != "" {
		req.UserIdentifier = &userIdentifier
	}

	// Parse date range
	if startDateStr := query.Get("start_date"); startDateStr != "" {
		startDate, err := time.Parse(time.RFC3339, startDateStr)
		if err != nil {
			RespondError(w, http.StatusBadRequest, "invalid_date", "Invalid start_date format, use RFC3339")
			return
		}
		req.StartDate = &startDate
	}

	if endDateStr := query.Get("end_date"); endDateStr != "" {
		endDate, err := time.Parse(time.RFC3339, endDateStr)
		if err != nil {
			RespondError(w, http.StatusBadRequest, "invalid_date", "Invalid end_date format, use RFC3339")
			return
		}
		req.EndDate = &endDate
	}

	// Parse pagination parameters
	// pageSize defaults to 20, max 40 (enforced in service layer)
	if pageSizeStr := query.Get("pageSize"); pageSizeStr != "" {
		pageSize, err := strconv.Atoi(pageSizeStr)
		if err != nil || pageSize < 0 {
			RespondError(w, http.StatusBadRequest, "invalid_parameter", "Invalid pageSize parameter")
			return
		}
		req.PageSize = pageSize
	}

	// page defaults to 0 (enforced in service layer)
	if pageStr := query.Get("page"); pageStr != "" {
		page, err := strconv.Atoi(pageStr)
		if err != nil || page < 0 {
			RespondError(w, http.StatusBadRequest, "invalid_parameter", "Invalid page parameter")
			return
		}
		req.Page = page
	}

	// Call service to search
	result, err := h.service.SearchExperiences(r.Context(), req)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "search_failed", err.Error())
		return
	}

	RespondSuccess(w, http.StatusOK, result)
}
