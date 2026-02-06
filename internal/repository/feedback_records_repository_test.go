package repository

import (
	"testing"
)

// BulkDelete is tested by integration tests in tests/integration_test.go:
//   - TestFeedbackRecordsRepository_BulkDelete exercises the repository directly and asserts
//     the returned slice of deleted records.
//   - TestBulkDeleteFeedbackRecords exercises the full stack (handler, service, repo) including
//     tenant_id filter and response shape.
func TestFeedbackRecordsRepository_Package(_ *testing.T) {
	// No DB in unit tests; BulkDelete coverage is in tests/.
}
