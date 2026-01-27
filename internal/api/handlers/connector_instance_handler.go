package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/formbricks/hub/internal/api/response"
	"github.com/formbricks/hub/internal/api/validation"
	apperrors "github.com/formbricks/hub/internal/errors"
	"github.com/formbricks/hub/internal/models"
	"github.com/google/uuid"
)

// ConnectorInstanceService defines the interface for connector instance business logic
type ConnectorInstanceService interface {
	CreateConnectorInstance(ctx context.Context, req *models.CreateConnectorInstanceRequest) (*models.ConnectorInstance, error)
	GetConnectorInstance(ctx context.Context, id uuid.UUID) (*models.ConnectorInstance, error)
	ListConnectorInstances(ctx context.Context, filters *models.ListConnectorInstancesFilters) (*models.ListConnectorInstancesResponse, error)
	UpdateConnectorInstance(ctx context.Context, id uuid.UUID, req *models.UpdateConnectorInstanceRequest) (*models.ConnectorInstance, error)
	DeleteConnectorInstance(ctx context.Context, id uuid.UUID) error
	StartConnectorInstance(ctx context.Context, id uuid.UUID) (*models.ConnectorInstance, error)
	StopConnectorInstance(ctx context.Context, id uuid.UUID) (*models.ConnectorInstance, error)
}

// ConnectorInstanceHandler handles HTTP requests for connector instances
type ConnectorInstanceHandler struct {
	service ConnectorInstanceService
}

// NewConnectorInstanceHandler creates a new connector instance handler
func NewConnectorInstanceHandler(service ConnectorInstanceService) *ConnectorInstanceHandler {
	return &ConnectorInstanceHandler{service: service}
}

// Create handles POST /v1/connector-instances
func (h *ConnectorInstanceHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req models.CreateConnectorInstanceRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		slog.Warn("Invalid request body",
			"method", r.Method,
			"path", r.URL.Path,
			"error", err,
		)
		response.RespondBadRequest(w, "Invalid request body")
		return
	}

	// Validate request
	if err := validation.ValidateStruct(&req); err != nil {
		validation.RespondValidationError(w, err)
		return
	}

	slog.Info("Creating connector instance",
		"name", req.Name,
		"instance_id", req.InstanceID,
		"type", req.Type,
	)

	instance, err := h.service.CreateConnectorInstance(r.Context(), &req)
	if err != nil {
		if errors.Is(err, apperrors.ErrValidation) {
			slog.Warn("Validation error creating connector instance",
				"method", r.Method,
				"path", r.URL.Path,
				"error", err,
			)
			response.RespondUnprocessableEntity(w, err.Error())
			return
		}
		slog.Error("Failed to create connector instance",
			"method", r.Method,
			"path", r.URL.Path,
			"error", err,
		)
		response.RespondInternalServerError(w, "An unexpected error occurred")
		return
	}

	response.RespondJSON(w, http.StatusCreated, instance)
}

// Get handles GET /v1/connector-instances/{id}
func (h *ConnectorInstanceHandler) Get(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	if idStr == "" {
		slog.Warn("Missing connector instance ID",
			"method", r.Method,
			"path", r.URL.Path,
		)
		response.RespondBadRequest(w, "Connector Instance ID is required")
		return
	}

	id, err := uuid.Parse(idStr)
	if err != nil {
		slog.Warn("Invalid UUID format",
			"method", r.Method,
			"path", r.URL.Path,
			"id", idStr,
			"error", err,
		)
		response.RespondBadRequest(w, "Invalid UUID format")
		return
	}

	instance, err := h.service.GetConnectorInstance(r.Context(), id)
	if err != nil {
		if errors.Is(err, apperrors.ErrNotFound) {
			slog.Warn("Connector instance not found",
				"method", r.Method,
				"path", r.URL.Path,
				"id", id,
				"error", err,
			)
			response.RespondNotFound(w, "Connector instance not found")
			return
		}
		slog.Error("Failed to get connector instance",
			"method", r.Method,
			"path", r.URL.Path,
			"id", id,
			"error", err,
		)
		response.RespondInternalServerError(w, "An unexpected error occurred")
		return
	}

	response.RespondJSON(w, http.StatusOK, instance)
}

// List handles GET /v1/connector-instances
func (h *ConnectorInstanceHandler) List(w http.ResponseWriter, r *http.Request) {
	filters := &models.ListConnectorInstancesFilters{}

	// Decode and validate query parameters
	if err := validation.ValidateAndDecodeQueryParams(r, filters); err != nil {
		validation.RespondValidationError(w, err)
		return
	}

	result, err := h.service.ListConnectorInstances(r.Context(), filters)
	if err != nil {
		slog.Error("Failed to list connector instances",
			"method", r.Method,
			"path", r.URL.Path,
			"error", err,
		)
		response.RespondInternalServerError(w, "An unexpected error occurred")
		return
	}

	response.RespondJSON(w, http.StatusOK, result)
}

// Update handles PATCH /v1/connector-instances/{id}
func (h *ConnectorInstanceHandler) Update(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	if idStr == "" {
		slog.Warn("Missing connector instance ID for update",
			"method", r.Method,
			"path", r.URL.Path,
		)
		response.RespondBadRequest(w, "Connector Instance ID is required")
		return
	}

	id, err := uuid.Parse(idStr)
	if err != nil {
		slog.Warn("Invalid UUID format for update",
			"method", r.Method,
			"path", r.URL.Path,
			"id", idStr,
			"error", err,
		)
		response.RespondBadRequest(w, "Invalid UUID format")
		return
	}

	var req models.UpdateConnectorInstanceRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		slog.Warn("Invalid request body for update",
			"method", r.Method,
			"path", r.URL.Path,
			"id", id,
			"error", err,
		)
		response.RespondBadRequest(w, "Invalid request body")
		return
	}

	// Validate request
	if err := validation.ValidateStruct(&req); err != nil {
		validation.RespondValidationError(w, err)
		return
	}

	instance, err := h.service.UpdateConnectorInstance(r.Context(), id, &req)
	if err != nil {
		if errors.Is(err, apperrors.ErrNotFound) {
			slog.Warn("Connector instance not found for update",
				"method", r.Method,
				"path", r.URL.Path,
				"id", id,
				"error", err,
			)
			response.RespondNotFound(w, "Connector instance not found")
			return
		}
		if errors.Is(err, apperrors.ErrValidation) {
			slog.Warn("Validation error updating connector instance",
				"method", r.Method,
				"path", r.URL.Path,
				"id", id,
				"error", err,
			)
			response.RespondUnprocessableEntity(w, err.Error())
			return
		}
		slog.Error("Failed to update connector instance",
			"method", r.Method,
			"path", r.URL.Path,
			"id", id,
			"error", err,
		)
		response.RespondInternalServerError(w, "An unexpected error occurred")
		return
	}

	response.RespondJSON(w, http.StatusOK, instance)
}

// Delete handles DELETE /v1/connector-instances/{id}
func (h *ConnectorInstanceHandler) Delete(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	if idStr == "" {
		slog.Warn("Missing connector instance ID for delete",
			"method", r.Method,
			"path", r.URL.Path,
		)
		response.RespondBadRequest(w, "Connector Instance ID is required")
		return
	}

	id, err := uuid.Parse(idStr)
	if err != nil {
		slog.Warn("Invalid UUID format for delete",
			"method", r.Method,
			"path", r.URL.Path,
			"id", idStr,
			"error", err,
		)
		response.RespondBadRequest(w, "Invalid UUID format")
		return
	}

	if err := h.service.DeleteConnectorInstance(r.Context(), id); err != nil {
		if errors.Is(err, apperrors.ErrNotFound) {
			slog.Warn("Connector instance not found for delete",
				"method", r.Method,
				"path", r.URL.Path,
				"id", id,
				"error", err,
			)
			response.RespondNotFound(w, "Connector instance not found")
			return
		}
		slog.Error("Failed to delete connector instance",
			"method", r.Method,
			"path", r.URL.Path,
			"id", id,
			"error", err,
		)
		response.RespondInternalServerError(w, "An unexpected error occurred")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// Start handles POST /v1/connector-instances/{id}/start
func (h *ConnectorInstanceHandler) Start(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	if idStr == "" {
		slog.Warn("Missing connector instance ID for start",
			"method", r.Method,
			"path", r.URL.Path,
		)
		response.RespondBadRequest(w, "Connector Instance ID is required")
		return
	}

	id, err := uuid.Parse(idStr)
	if err != nil {
		slog.Warn("Invalid UUID format for start",
			"method", r.Method,
			"path", r.URL.Path,
			"id", idStr,
			"error", err,
		)
		response.RespondBadRequest(w, "Invalid UUID format")
		return
	}

	slog.Info("Starting connector instance",
		"id", id,
	)

	instance, err := h.service.StartConnectorInstance(r.Context(), id)
	if err != nil {
		if errors.Is(err, apperrors.ErrNotFound) {
			slog.Warn("Connector instance not found for start",
				"method", r.Method,
				"path", r.URL.Path,
				"id", id,
				"error", err,
			)
			response.RespondNotFound(w, "Connector instance not found")
			return
		}
		if errors.Is(err, apperrors.ErrValidation) {
			slog.Warn("Validation error starting connector instance",
				"method", r.Method,
				"path", r.URL.Path,
				"id", id,
				"error", err,
			)
			response.RespondUnprocessableEntity(w, err.Error())
			return
		}
		slog.Error("Failed to start connector instance",
			"method", r.Method,
			"path", r.URL.Path,
			"id", id,
			"error", err,
		)
		response.RespondInternalServerError(w, "An unexpected error occurred")
		return
	}

	response.RespondJSON(w, http.StatusOK, instance)
}

// Stop handles POST /v1/connector-instances/{id}/stop
func (h *ConnectorInstanceHandler) Stop(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	if idStr == "" {
		slog.Warn("Missing connector instance ID for stop",
			"method", r.Method,
			"path", r.URL.Path,
		)
		response.RespondBadRequest(w, "Connector Instance ID is required")
		return
	}

	id, err := uuid.Parse(idStr)
	if err != nil {
		slog.Warn("Invalid UUID format for stop",
			"method", r.Method,
			"path", r.URL.Path,
			"id", idStr,
			"error", err,
		)
		response.RespondBadRequest(w, "Invalid UUID format")
		return
	}

	slog.Info("Stopping connector instance",
		"id", id,
	)

	instance, err := h.service.StopConnectorInstance(r.Context(), id)
	if err != nil {
		if errors.Is(err, apperrors.ErrNotFound) {
			slog.Warn("Connector instance not found for stop",
				"method", r.Method,
				"path", r.URL.Path,
				"id", id,
				"error", err,
			)
			response.RespondNotFound(w, "Connector instance not found")
			return
		}
		slog.Error("Failed to stop connector instance",
			"method", r.Method,
			"path", r.URL.Path,
			"id", id,
			"error", err,
		)
		response.RespondInternalServerError(w, "An unexpected error occurred")
		return
	}

	response.RespondJSON(w, http.StatusOK, instance)
}
