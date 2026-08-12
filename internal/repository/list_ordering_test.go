package repository

import (
	"testing"

	"github.com/formbricks/hub/internal/models"
)

// TestResolveListOrdering covers the defaults and every valid (sort, order) pair.
func TestResolveListOrdering(t *testing.T) {
	tests := []struct {
		name       string
		sort       models.SortField
		order      models.SortOrder
		wantColumn sqlColumn
		wantDesc   bool
	}{
		{name: "defaults to collected_at desc", wantColumn: colCollectedAt, wantDesc: true},
		{
			name: "collected_at asc", sort: models.SortFieldCollectedAt, order: models.SortOrderAsc,
			wantColumn: colCollectedAt, wantDesc: false,
		},
		{
			name: "created_at desc", sort: models.SortFieldCreatedAt, order: models.SortOrderDesc,
			wantColumn: colCreatedAt, wantDesc: true,
		},
		{
			name: "created_at asc", sort: models.SortFieldCreatedAt, order: models.SortOrderAsc,
			wantColumn: colCreatedAt, wantDesc: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filters := tenantFilters()
			filters.Sort = tt.sort
			filters.Order = tt.order

			ordering, err := resolveListOrdering(filters)
			if err != nil {
				t.Fatalf("resolveListOrdering() error = %v, want nil", err)
			}

			if ordering.column != tt.wantColumn || ordering.desc != tt.wantDesc {
				t.Fatalf("resolveListOrdering() = (%q, desc=%v), want (%q, desc=%v)",
					ordering.column, ordering.desc, tt.wantColumn, tt.wantDesc)
			}
		})
	}
}

// TestResolveListOrdering_RejectsUnknownTokens is the SQL-identifier injection guard.
//
// It constructs the filters directly in Go, bypassing HTTP validation exactly as a worker or an
// internal caller would, because that is the path where the `oneof` tag is not in play at all. The
// resolver must refuse rather than interpolate — `column = sqlColumn(field)` would pass every
// other test in this file and hand the caller a SQL injection.
func TestResolveListOrdering_RejectsUnknownTokens(t *testing.T) {
	tests := []struct {
		name  string
		sort  models.SortField
		order models.SortOrder
	}{
		{name: "unknown sort", sort: models.SortField("value_number"), order: models.SortOrderDesc},
		{name: "unknown order", sort: models.SortFieldCreatedAt, order: models.SortOrder("sideways")},
		{
			name:  "injection attempt",
			sort:  models.SortField(`collected_at"; DROP TABLE feedback_records--`),
			order: models.SortOrderDesc,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filters := tenantFilters()
			filters.Sort = tt.sort
			filters.Order = tt.order

			ordering, err := resolveListOrdering(filters)
			if err == nil {
				t.Fatalf("resolveListOrdering() error = nil, want a rejection (got column %q)", ordering.column)
			}

			if ordering.column != "" {
				t.Fatalf("resolveListOrdering() column = %q, want empty on error", ordering.column)
			}
		})
	}
}

// TestOrderByClause_DefaultIsByteIdentical pins the pre-sort-control SQL exactly. Behavior
// preservation is easy to assert loosely and easy to get subtly wrong; comparing the rendered
// string is the strongest form of the claim.
func TestOrderByClause_DefaultIsByteIdentical(t *testing.T) {
	ordering, err := resolveListOrdering(tenantFilters())
	if err != nil {
		t.Fatalf("resolveListOrdering() error = %v, want nil", err)
	}

	const want = " ORDER BY collected_at DESC, id ASC"
	if got := ordering.orderByClause(); got != want {
		t.Fatalf("orderByClause() = %q, want %q", got, want)
	}

	const wantPredicate = "(collected_at < $5 OR (collected_at = $5 AND id > $6))"
	if got := ordering.keysetPredicate(5, 6); got != wantPredicate {
		t.Fatalf("keysetPredicate() = %q, want %q", got, wantPredicate)
	}
}

// TestOrderingRendering covers the remaining (column, direction) combinations. The tiebreak is
// `id ASC` in the ORDER BY and `id >` in the predicate for every one of them — flipping the
// tiebreak with the primary direction would invalidate every cursor already in client hands.
func TestOrderingRendering(t *testing.T) {
	tests := []struct {
		name          string
		ordering      listOrdering
		wantOrderBy   string
		wantPredicate string
	}{
		{
			name:          "collected_at asc",
			ordering:      listOrdering{column: colCollectedAt, desc: false},
			wantOrderBy:   " ORDER BY collected_at ASC, id ASC",
			wantPredicate: "(collected_at > $1 OR (collected_at = $1 AND id > $2))",
		},
		{
			name:          "created_at desc",
			ordering:      listOrdering{column: colCreatedAt, desc: true},
			wantOrderBy:   " ORDER BY created_at DESC, id ASC",
			wantPredicate: "(created_at < $1 OR (created_at = $1 AND id > $2))",
		},
		{
			name:          "created_at asc",
			ordering:      listOrdering{column: colCreatedAt, desc: false},
			wantOrderBy:   " ORDER BY created_at ASC, id ASC",
			wantPredicate: "(created_at > $1 OR (created_at = $1 AND id > $2))",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.ordering.orderByClause(); got != tt.wantOrderBy {
				t.Fatalf("orderByClause() = %q, want %q", got, tt.wantOrderBy)
			}

			if got := tt.ordering.keysetPredicate(1, 2); got != tt.wantPredicate {
				t.Fatalf("keysetPredicate() = %q, want %q", got, tt.wantPredicate)
			}
		})
	}
}
