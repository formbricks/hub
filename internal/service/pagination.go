package service

// ListPaginationMeta holds pagination metadata for list endpoints (feedback records, webhooks).
type ListPaginationMeta struct {
	Limit      int
	NextCursor string
}

// BuildListPaginationMeta builds pagination metadata for cursor-based list responses.
// hasMore indicates a sentinel row was fetched (limit+1 returned, trimmed to limit).
// encodeLast is called only when hasMore is true to produce next_cursor.
func BuildListPaginationMeta(
	limit int, hasMore bool, encodeLast func() (string, error),
) (ListPaginationMeta, error) {
	meta := ListPaginationMeta{Limit: limit}

	if hasMore && encodeLast != nil {
		next, err := encodeLast()
		if err != nil {
			return meta, err
		}

		meta.NextCursor = next
	}

	return meta, nil
}
