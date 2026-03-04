package service

// ListPaginationMeta holds pagination metadata for list endpoints (feedback records, webhooks).
type ListPaginationMeta struct {
	Limit      int
	NextCursor string
}

// BuildListPaginationMeta builds pagination metadata for cursor-based list responses.
// encodeLast is called only when there may be more results (recordCount == limit && recordCount > 0).
func BuildListPaginationMeta(
	limit, recordCount int, encodeLast func() (string, error),
) (ListPaginationMeta, error) {
	meta := ListPaginationMeta{Limit: limit}

	if recordCount == limit && recordCount > 0 && encodeLast != nil {
		next, err := encodeLast()
		if err != nil {
			return meta, err
		}

		meta.NextCursor = next
	}

	return meta, nil
}
