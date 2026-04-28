package repository

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/datatypes"
)

func TestListEnabledForEventTypeAndTenantQuery(t *testing.T) {
	tenantID := "tenant-a"

	tests := []struct {
		name               string
		tenantID           *string
		wantArgs           []any
		wantTenantClause   string
		rejectTenantClause string
	}{
		{
			name:               "scoped event matches same tenant webhooks only",
			tenantID:           &tenantID,
			wantArgs:           []any{datatypes.FeedbackRecordCreated.String(), tenantID},
			wantTenantClause:   "AND tenant_id = $2",
			rejectTenantClause: "tenant_id IS NULL",
		},
		{
			name:               "tenant-less event matches no webhooks",
			tenantID:           nil,
			wantArgs:           []any{datatypes.FeedbackRecordCreated.String()},
			wantTenantClause:   "AND FALSE",
			rejectTenantClause: "tenant_id = $2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query, args := listEnabledForEventTypeAndTenantQuery(datatypes.FeedbackRecordCreated.String(), tt.tenantID)

			require.Equal(t, tt.wantArgs, args)
			assert.Contains(t, query, "WHERE enabled = true")
			assert.Contains(t, query, "event_types IS NULL OR event_types = '{}' OR event_types @> ARRAY[$1]::VARCHAR(64)[]")
			assert.Contains(t, query, tt.wantTenantClause)
			assert.NotContains(t, query, tt.rejectTenantClause)
			assert.True(t, strings.HasSuffix(strings.TrimSpace(query), "ORDER BY id"))
		})
	}
}
