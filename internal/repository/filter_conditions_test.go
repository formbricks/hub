package repository

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/formbricks/hub/internal/models"
)

const testTenant = "org-123"

func tenantFilters() *models.ListFeedbackRecordsFilters {
	tenant := testTenant

	return &models.ListFeedbackRecordsFilters{TenantID: &tenant}
}

// TestBuildFilterConditions_FailsClosedWithoutTenant is the regression guard for the whole
// grouped-helper refactor. The tenant predicate used to sit at the top of one obvious function;
// it now lives inside appendIdentityConditions, and a future edit that drops it would produce a
// query spanning every tenant that looks entirely normal.
//
// It deliberately does not go through the HTTP layer: the HTTP layer's `required` tag is the thing
// this is proving the repository does not depend on.
func TestBuildFilterConditions_FailsClosedWithoutTenant(t *testing.T) {
	blank := "   "
	empty := ""

	tests := []struct {
		name    string
		filters *models.ListFeedbackRecordsFilters
	}{
		{name: "nil filters", filters: nil},
		{name: "nil tenant", filters: &models.ListFeedbackRecordsFilters{}},
		{name: "empty tenant", filters: &models.ListFeedbackRecordsFilters{TenantID: &empty}},
		{name: "whitespace tenant", filters: &models.ListFeedbackRecordsFilters{TenantID: &blank}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			where, args, err := buildFilterConditions(tt.filters)
			if err == nil {
				t.Fatalf("buildFilterConditions() error = nil, want a missing-tenant error (where = %q)", where)
			}

			if where != "" || args != nil {
				t.Fatalf("buildFilterConditions() = (%q, %v), want no clause and no args on error", where, args)
			}
		})
	}
}

// TestBuildFilterConditions_PlaceholdersAreDense asserts the numbering invariant directly: the
// placeholders appearing in the clause are exactly $1..$len(args), each once. This holds for any
// subset of filters, which is what makes it a better guard than a fixed expected list.
func TestBuildFilterConditions_PlaceholdersAreDense(t *testing.T) {
	filters := tenantFilters()
	filters.SourceType = []string{"survey"}
	filters.Emotions = []models.EmotionValue{models.EmotionJoy}
	filters.HasSentiment = new(bool) // false: contributes a condition but no arg

	where, args, err := buildFilterConditions(filters)
	if err != nil {
		t.Fatalf("buildFilterConditions() error = %v, want nil", err)
	}

	found := map[int]bool{}

	for _, match := range regexp.MustCompile(`\$(\d+)`).FindAllStringSubmatch(where, -1) {
		position, convErr := strconv.Atoi(match[1])
		if convErr != nil {
			t.Fatalf("unparseable placeholder %q", match[0])
		}

		found[position] = true
	}

	if len(found) != len(args) {
		t.Fatalf("clause uses %d distinct placeholders but binds %d args\nwhere: %s", len(found), len(args), where)
	}

	for position := 1; position <= len(args); position++ {
		if !found[position] {
			t.Fatalf("placeholder $%d is never used\nwhere: %s", position, where)
		}
	}
}

// TestBuildFilterConditions_EmotionsUsesOverlap pins ANY-vs-ALL semantics in one assertion.
//
// `&&` matches a record carrying at least one of the requested emotions, which is how two selected
// filter chips read. `@>` would mean "carries all of them" — a silently different, much narrower
// result that no test other than this one would catch.
func TestBuildFilterConditions_EmotionsUsesOverlap(t *testing.T) {
	filters := tenantFilters()
	filters.Emotions = []models.EmotionValue{models.EmotionAnger, models.EmotionFear}

	where, args, err := buildFilterConditions(filters)
	if err != nil {
		t.Fatalf("buildFilterConditions() error = %v, want nil", err)
	}

	if !strings.Contains(where, "emotions && $2") {
		t.Fatalf("where = %q, want an `emotions && $2` overlap test", where)
	}

	if strings.Contains(where, "@>") {
		t.Fatalf("where = %q, must not use containment: that would mean ALL emotions, not ANY", where)
	}

	// pgx has a codec for []string but not for a slice of a named string type.
	if _, ok := args[1].([]string); !ok {
		t.Fatalf("args[1] is %T, want []string", args[1])
	}
}

// TestBuildFilterConditions_FieldTypeCastsToEnum guards the one filter whose column is a
// PostgreSQL ENUM. Binding a Go slice without the ::text[] hop relies on pgx's reflection fallback
// resolving to _text, which works by coincidence rather than contract.
func TestBuildFilterConditions_FieldTypeCastsToEnum(t *testing.T) {
	filters := tenantFilters()
	filters.FieldType = []models.FieldType{models.FieldTypeText, models.FieldTypeRating}

	where, args, err := buildFilterConditions(filters)
	if err != nil {
		t.Fatalf("buildFilterConditions() error = %v, want nil", err)
	}

	if !strings.Contains(where, "field_type = ANY($2::text[]::field_type_enum[])") {
		t.Fatalf("where = %q, want the field_type_enum array cast", where)
	}

	bound, ok := args[1].([]string)
	if !ok {
		t.Fatalf("args[1] is %T, want []string", args[1])
	}

	if len(bound) != 2 || bound[0] != "text" || bound[1] != "rating" {
		t.Fatalf("args[1] = %v, want [text rating]", bound)
	}
}

// TestBuildFilterConditions_PresenceFiltersBindNothing verifies the tri-state presence filters:
// nil leaves the column unconstrained, and true/false render IS NOT NULL / IS NULL without
// consuming a placeholder.
func TestBuildFilterConditions_PresenceFiltersBindNothing(t *testing.T) {
	present, absent := true, false

	tests := []struct {
		name  string
		set   func(*models.ListFeedbackRecordsFilters)
		want  string
		avoid string
	}{
		{
			name:  "has_sentiment true",
			set:   func(f *models.ListFeedbackRecordsFilters) { f.HasSentiment = &present },
			want:  "sentiment IS NOT NULL",
			avoid: "sentiment IS NULL",
		},
		{
			name: "has_emotions false",
			set:  func(f *models.ListFeedbackRecordsFilters) { f.HasEmotions = &absent },
			want: "emotions IS NULL",
		},
		{
			name: "has_translation true",
			set:  func(f *models.ListFeedbackRecordsFilters) { f.HasTranslation = &present },
			want: "translation_lang_key IS NOT NULL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filters := tenantFilters()
			tt.set(filters)

			where, args, err := buildFilterConditions(filters)
			if err != nil {
				t.Fatalf("buildFilterConditions() error = %v, want nil", err)
			}

			if !strings.Contains(where, tt.want) {
				t.Fatalf("where = %q, want it to contain %q", where, tt.want)
			}

			if tt.avoid != "" && strings.Contains(where, tt.avoid) {
				t.Fatalf("where = %q, must not contain %q", where, tt.avoid)
			}

			// Only tenant_id binds an argument.
			if len(args) != 1 {
				t.Fatalf("args = %v, want only the tenant argument", args)
			}
		})
	}

	t.Run("nil leaves the column unconstrained", func(t *testing.T) {
		where, _, err := buildFilterConditions(tenantFilters())
		if err != nil {
			t.Fatalf("buildFilterConditions() error = %v, want nil", err)
		}

		if strings.Contains(where, "IS NULL") || strings.Contains(where, "IS NOT NULL") {
			t.Fatalf("where = %q, want no presence condition when the filters are nil", where)
		}
	})
}

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
