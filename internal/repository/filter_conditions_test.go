package repository

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

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

// TestFilterConditions_AccumulatorContract exercises the accumulator directly, independently of
// the filter policy built on top of it. buildFilterConditions always adds the tenant predicate, so
// the empty case is unreachable through it — but `where()` still has to return a valid (empty)
// clause, because ListAfterCursor now unconditionally appends " AND " on the strength of the
// clause never being empty in practice.
func TestFilterConditions_AccumulatorContract(t *testing.T) {
	t.Run("no conditions yields no clause", func(t *testing.T) {
		conds := &filterConditions{}
		if got := conds.where(); got != "" {
			t.Fatalf("where() = %q, want empty", got)
		}

		if len(conds.args) != 0 {
			t.Fatalf("args = %v, want none", conds.args)
		}
	})

	t.Run("conditions are ANDed under one WHERE", func(t *testing.T) {
		conds := &filterConditions{}
		conds.addComparison(colTenantID, opEqual, "t1")
		conds.addRaw("sentiment IS NULL")

		const want = " WHERE tenant_id = $1 AND sentiment IS NULL"
		if got := conds.where(); got != want {
			t.Fatalf("where() = %q, want %q", got, want)
		}
	})

	t.Run("empty value slices add nothing", func(t *testing.T) {
		conds := &filterConditions{}
		conds.addAnyOf(colUserID, nil)
		conds.addAnyOfEnum(colFieldType, fieldTypeEnum, nil)

		if got := conds.where(); got != "" {
			t.Fatalf("where() = %q, want empty", got)
		}
	})
}

// TestListAfterCursor_AlwaysAndsOntoTheTenantClause is the guard for the unconditional " AND " in
// ListAfterCursor: any filter set that builds successfully must already carry a WHERE clause, so
// appending " AND <keyset>" is always valid SQL.
func TestListAfterCursor_AlwaysAndsOntoTheTenantClause(t *testing.T) {
	where, _, err := buildFilterConditions(tenantFilters())
	if err != nil {
		t.Fatalf("buildFilterConditions() error = %v, want nil", err)
	}

	if !strings.HasPrefix(where, " WHERE ") {
		t.Fatalf("where = %q, want it to start with \" WHERE \"; ListAfterCursor appends \" AND \" onto it", where)
	}
}

// TestQueryBuildersPropagateTheTenantGuard verifies the fail-closed guard reaches every query
// builder rather than only the one with a direct test. These are the paths a worker or an internal
// caller would take, where the HTTP layer's `required` tag is not in play.
func TestQueryBuildersPropagateTheTenantGuard(t *testing.T) {
	noTenant := &models.ListFeedbackRecordsFilters{}

	t.Run("Count", func(t *testing.T) {
		count, err := NewFeedbackRecordsRepository(nil).Count(t.Context(), noTenant)
		if err == nil {
			t.Fatal("Count() error = nil, want a missing-tenant error")
		}

		if count != 0 {
			t.Fatalf("Count() = %d, want 0 on error", count)
		}
	})

	t.Run("buildCountQuery", func(t *testing.T) {
		query, args, err := buildCountQuery(noTenant)
		if err == nil {
			t.Fatal("buildCountQuery() error = nil, want a missing-tenant error")
		}

		if query != "" || args != nil {
			t.Fatalf("buildCountQuery() = (%q, %v), want nothing on error", query, args)
		}
	})

	// List and ListAfterCursor need a repository value but must reject before touching the pool,
	// so a nil pool is safe here and proves no query was attempted.
	repo := NewFeedbackRecordsRepository(nil)

	t.Run("List", func(t *testing.T) {
		records, hasMore, err := repo.List(t.Context(), noTenant)
		if err == nil {
			t.Fatal("List() error = nil, want a missing-tenant error")
		}

		if records != nil || hasMore {
			t.Fatalf("List() = (%v, %v), want no records", records, hasMore)
		}
	})

	t.Run("ListAfterCursor", func(t *testing.T) {
		records, hasMore, err := repo.ListAfterCursor(t.Context(), noTenant, time.Time{}, uuid.Nil)
		if err == nil {
			t.Fatal("ListAfterCursor() error = nil, want a missing-tenant error")
		}

		if records != nil || hasMore {
			t.Fatalf("ListAfterCursor() = (%v, %v), want no records", records, hasMore)
		}
	})
}

// TestListRejectsAnUnknownSortBeforeQuerying pins that an invalid ordering fails at the resolver
// rather than reaching the database — the same fail-closed property, on the sort axis.
func TestListRejectsAnUnknownSortBeforeQuerying(t *testing.T) {
	filters := tenantFilters()
	filters.Sort = models.SortField("value_number")

	repo := NewFeedbackRecordsRepository(nil)
	if _, _, err := repo.List(t.Context(), filters); err == nil {
		t.Fatal("List() error = nil, want an unsupported-sort error")
	}

	// ListAfterCursor resolves the ordering too — the cursor page must not fall back to a default
	// the first page did not use.
	if _, _, err := repo.ListAfterCursor(t.Context(), filters, time.Time{}, uuid.Nil); err == nil {
		t.Fatal("ListAfterCursor() error = nil, want an unsupported-sort error")
	}
}
