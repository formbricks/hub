package service

// ListPaginationMeta holds pagination metadata for list endpoints (feedback records, webhooks).
type ListPaginationMeta struct {
	Total      *int64
	Offset     *int
	Limit      int
	NextCursor string
}

// BuildListPaginationMeta builds pagination metadata for list responses.
// When total is nil (cursor-based path), Total and Offset are nil.
// encodeLast is called only when there may be more results (recordCount == limit && recordCount > 0).
func BuildListPaginationMeta(
	total *int64, offset, limit, recordCount int, encodeLast func() (string, error),
) (ListPaginationMeta, error) {
	meta := ListPaginationMeta{Limit: limit}

	if total != nil {
		t := *total
		meta.Total = &t
		o := offset
		meta.Offset = &o
	}

	if recordCount == limit && recordCount > 0 && encodeLast != nil {
		next, err := encodeLast()
		if err != nil {
			return meta, err
		}

		meta.NextCursor = next
	}

	return meta, nil
}
