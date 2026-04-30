package repository

import (
	"testing"
)

// BulkDelete is tested by integration tests in tests/integration_test.go:
//   - TestFeedbackRecordsRepository_BulkDelete exercises the repository directly and asserts
//     the optional tenant filter and tenant-grouped return values.
//   - TestBulkDeleteFeedbackRecords exercises the full stack (handler, service, repo) including
//     tenant-scoped deletion, GDPR user_id erasure across tenants, and response shape.
func TestFeedbackRecordsRepository_Package(_ *testing.T) {
	// No DB in unit tests; BulkDelete coverage is in tests/.
}
