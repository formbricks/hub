# Integration Tests

This directory contains integration tests for the Formbricks Hub API.

## Prerequisites

Before running the tests, ensure:

1. **PostgreSQL is running** with the default test credentials (e.g. `make docker-up`). The tests use `postgres://postgres:postgres@localhost:5432/test_db` by default. If you see `password authentication failed for user "postgres"`, start the stack with `make docker-up` and run `make init-db`.
2. **Database schema** has been initialized (`make init-db`).
3. **API_KEY** is set automatically by the tests; you do not need to set it.

## Running Tests

Run all integration tests:

```bash
go test ./tests/... -v
```

Run a specific test:

```bash
go test ./tests -run TestHealthEndpoint -v
```

Run tests with coverage:

```bash
go test ./tests/... -v -cover
```

## Test Structure

- `integration_test.go` - Main integration tests for all API endpoints
- `helpers.go` - Helper functions for test setup and cleanup

## Test Coverage

The integration tests cover:

- ✅ Health endpoint (public)
- ✅ Create experience (with/without auth)
- ✅ List experiences (with filters)
- ✅ Get experience by ID
- ✅ Update experience
- ✅ Delete experience
- ✅ Search experiences (placeholder)
- ✅ Authentication middleware
- ✅ Error handling

## Test API Key

The tests use a predefined API key: `test-api-key-12345`

The test setup automatically sets this key in the `API_KEY` environment variable for authentication.

## Notes

- Tests use the actual database configured in `.env`
- Tests create real data in the database
- Consider using a separate test database for isolation
- The search endpoint tests will pass but return empty results until search is implemented
