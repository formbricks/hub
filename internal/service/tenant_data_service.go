package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/formbricks/hub/internal/models"
)

var errTenantDataNilCounts = errors.New("tenant data repository returned nil counts")

// TenantDataRepository defines tenant data purge access.
type TenantDataRepository interface {
	DeleteByTenant(ctx context.Context, tenantID string) (*models.TenantDataDeleteCounts, error)
}

// TenantDataService handles tenant data purge business logic.
type TenantDataService struct {
	repo TenantDataRepository
}

// NewTenantDataService creates a new tenant data service.
func NewTenantDataService(repo TenantDataRepository) *TenantDataService {
	return &TenantDataService{repo: repo}
}

// DeleteTenantData deletes all Hub-owned data for a tenant.
func (s *TenantDataService) DeleteTenantData(ctx context.Context, tenantID string) (*models.TenantDataDeleteResult, error) {
	normalizedTenantID, err := normalizeRequiredTenantIDValue(tenantID)
	if err != nil {
		return nil, err
	}

	counts, err := s.repo.DeleteByTenant(ctx, normalizedTenantID)
	if err != nil {
		return nil, fmt.Errorf("delete tenant data: %w", err)
	}

	if counts == nil {
		return nil, fmt.Errorf("delete tenant data: %w", errTenantDataNilCounts)
	}

	return &models.TenantDataDeleteResult{
		TenantID:               normalizedTenantID,
		TenantDataDeleteCounts: *counts,
	}, nil
}
