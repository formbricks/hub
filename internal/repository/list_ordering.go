package repository

import (
	"fmt"

	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
)

// updatedAtAscendingOnlyMessage is the reason reported when sort=updated_at comes with any order but
// asc, including the default (desc). Shared with the HTTP validator so both layers say the same.
const updatedAtAscendingOnlyMessage = models.UpdatedAtAscendingOnlyReason

// listOrdering is the resolved ORDER BY for a feedback-record listing. Only resolveListOrdering
// produces one, so no identifier it carries can originate from a request value.
type listOrdering struct {
	column sqlColumn
	desc   bool
}

// resolveListOrdering maps the request's sort tokens onto SQL, applying the defaults when unset.
//
// The switch is exhaustive over models.SortField and its default arm fails closed, so the SQL
// builder never assumes HTTP validation ran — the repository is also reached from workers and
// tests that construct filters directly in Go, where the `oneof` tag is not in the path at all.
// Never `column = sqlColumn(field)`: the tokens happening to equal the column names is a
// coincidence, and relying on it would turn any future SortField member into an interpolated
// identifier.
func resolveListOrdering(filters *models.ListFeedbackRecordsFilters) (listOrdering, error) {
	field := models.DefaultSortField
	order := models.DefaultSortOrder

	if filters != nil {
		if filters.Sort != "" {
			field = filters.Sort
		}

		if filters.Order != "" {
			order = filters.Order
		}
	}

	var column sqlColumn

	switch field {
	case models.SortFieldCollectedAt:
		column = colCollectedAt
	case models.SortFieldCreatedAt:
		column = colCreatedAt
	case models.SortFieldUpdatedAt:
		// Ascending only, and checked here as well as in HTTP validation: the repository is also
		// reached from Go callers, and a descending updated_at traversal skips rows that change
		// mid-traversal (see models.SortField).
		if order != models.SortOrderAsc {
			return listOrdering{}, huberrors.NewValidationError("order", updatedAtAscendingOnlyMessage)
		}

		column = colUpdatedAt
	default:
		return listOrdering{}, huberrors.NewValidationError("sort",
			"must be one of: "+string(models.SortFieldCollectedAt)+", "+string(models.SortFieldCreatedAt)+
				", "+string(models.SortFieldUpdatedAt))
	}

	switch order {
	case models.SortOrderAsc:
		return listOrdering{column: column, desc: false}, nil
	case models.SortOrderDesc:
		return listOrdering{column: column, desc: true}, nil
	default:
		return listOrdering{}, huberrors.NewValidationError("order",
			"must be one of: "+string(models.SortOrderAsc)+", "+string(models.SortOrderDesc))
	}
}

// orderByClause renders the deterministic ORDER BY.
//
// The `id ASC` tiebreak is invariant across every (column, direction). It is what the previously
// fixed ordering used, so rows tied on the sort column keep their relative order and a cursor's ID
// comparison keeps one meaning. Ties are common here: collected_at is client-supplied, so every
// answer of one submission typically carries the same value.
//
// No NULLS FIRST/LAST: every sortable column is NOT NULL, and an explicit clause disagreeing with
// how the index was declared would make the index unusable for the ordering.
func (o listOrdering) orderByClause() string {
	if o.desc {
		return " ORDER BY " + string(o.column) + " DESC, id ASC"
	}

	return " ORDER BY " + string(o.column) + " ASC, id ASC"
}

// keysetPredicate renders "strictly after the cursor row" for this ordering, with $timeArg bound
// to the cursor's sort-column value and $idArg to its ID.
//
// ASC: (col, id) > ($t, $id), a row comparison. Both parts ascend, so it means exactly "further up
// the column, or tied on it with a larger id" — and unlike the equivalent OR it is an index range
// condition, so a page deep into the traversal starts at the cursor instead of reading and
// discarding every row before it. On the (tenant_id, updated_at, id) index that keeps every page of
// a sort=updated_at sync O(limit) however deep it is (ENG-3420): a page 150k rows in read 5 buffers
// instead of discarding 150k rows.
//
// DESC: the next row is further down the column, or tied on it with a larger id. The tiebreak
// still ascends (`id ASC` in every ORDER BY), so the two parts run in opposite directions and no
// row comparison can express it; the OR form is the only correct one.
func (o listOrdering) keysetPredicate(timeArg, idArg int) string {
	if !o.desc {
		return fmt.Sprintf("(%s, id) > ($%d, $%d)", o.column, timeArg, idArg)
	}

	return fmt.Sprintf("(%s < $%d OR (%s = $%d AND id > $%d))",
		o.column, timeArg, o.column, timeArg, idArg)
}
