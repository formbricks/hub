package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/formbricks/hub/internal/models"
)

type taxonomyInternalHandlerTestService struct {
	completeCalled bool
}

func (s *taxonomyInternalHandlerTestService) GetRunInput(
	context.Context, uuid.UUID,
) (*models.TaxonomyRunInputResponse, error) {
	return nil, nil
}

func (s *taxonomyInternalHandlerTestService) CompleteRun(
	context.Context, uuid.UUID, models.TaxonomyRunResultRequest,
) (*models.TaxonomyRun, error) {
	s.completeCalled = true

	return nil, nil
}

func (s *taxonomyInternalHandlerTestService) FailRun(
	context.Context, uuid.UUID, models.TaxonomyRunFailedRequest,
) (*models.TaxonomyRun, error) {
	return nil, nil
}

func (s *taxonomyInternalHandlerTestService) Heartbeat(context.Context, uuid.UUID) error {
	return nil
}

func TestTaxonomyInternalHandlerCompleteRunRejectsOversizedBody(t *testing.T) {
	t.Parallel()

	service := &taxonomyInternalHandlerTestService{}
	handler := NewTaxonomyInternalHandler(service)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/internal/v1/taxonomy/runs/ignored/result",
		strings.NewReader(strings.Repeat(" ", maxTaxonomyResultBodyBytes+1)))
	req.SetPathValue("run_id", uuid.NewString())

	recorder := httptest.NewRecorder()

	handler.CompleteRun(recorder, req)

	assert.Equal(t, http.StatusRequestEntityTooLarge, recorder.Code)
	assert.False(t, service.completeCalled)
}
