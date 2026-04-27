# Integration Tests

This directory contains integration tests for the Formbricks Hub API.

## Prerequisites

Before running the tests, ensure:

1. **PostgreSQL is running** (e.g. `make docker-up`). `make docker-up` starts dependency containers, currently PostgreSQL and River UI; it does not start the Hub API or worker. The tests use the connection string from `.env` (`DATABASE_URL`) when set; if empty, the default `postgres://postgres:postgres@localhost:5432/test_db?sslmode=disable` is used. If you set `POSTGRES_PORT` in `.env`, keep `DATABASE_URL` in sync. If you see `password authentication failed for user "postgres"`, start the stack with `make docker-up` and run `make init-db`.
2. **Database schema** has been initialized (`make init-db`).
3. **River queue migrations** have been applied (`make river-migrate`) when testing worker-backed webhook delivery flows.
4. **API_KEY** is set automatically by the tests; you do not need to set it.

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
- ✅ Create feedback records (with/without auth)
- ✅ List feedback records (with tenant and other filters)
- ✅ Get feedback record by ID
- ✅ Update feedback record
- ✅ Delete feedback record
- ✅ Bulk delete feedback records by user
- ✅ Webhook CRUD and validation
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
