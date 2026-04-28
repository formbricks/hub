package repository

import (
	"testing"
)

// BulkDelete is tested by integration tests in tests/integration_test.go:
//   - TestFeedbackRecordsRepository_BulkDelete exercises the repository directly and asserts
//     deleted records are returned grouped by tenant.
//   - TestBulkDeleteFeedbackRecords exercises the full stack (handler, service, repo) including
//     GDPR user_id erasure across tenants and response shape.
func TestFeedbackRecordsRepository_Package(_ *testing.T) {
	// No DB in unit tests; BulkDelete coverage is in tests/.
}
