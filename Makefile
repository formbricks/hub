.PHONY: help tests openapi build run migrate clean docker-up docker-down

# Default target - show help
help:
	@echo "Available targets:"
	@echo "  make tests       - Run all tests"
	@echo "  make openapi     - Generate OpenAPI/Swagger documentation"
	@echo "  make build       - Build the API server"
	@echo "  make run         - Run the API server"
	@echo "  make migrate     - Run database migrations"
	@echo "  make docker-up   - Start Docker containers"
	@echo "  make docker-down - Stop Docker containers"
	@echo "  make clean       - Clean build artifacts"

# Run all tests
tests:
	@echo "Running all tests..."
	go test ./tests/... -v

# Run tests with coverage
tests-coverage:
	@echo "Running tests with coverage..."
	go test ./tests/... -v -cover -coverprofile=coverage.out
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

# Generate OpenAPI/Swagger documentation
openapi:
	@echo "Generating OpenAPI documentation..."
	$(HOME)/go/bin/swag init -g cmd/api/main.go -o docs
	@echo "Documentation generated in docs/ folder"
	@echo "View at: http://localhost:8080/swagger/index.html"

# Build the API server
build:
	@echo "Building API server..."
	go build -o bin/api cmd/api/main.go
	@echo "Binary created: bin/api"

# Build the migration tool
build-migrate:
	@echo "Building migration tool..."
	go build -o bin/migrate cmd/migrate/main.go
	@echo "Binary created: bin/migrate"

# Run the API server
run:
	@echo "Starting API server..."
	go run cmd/api/main.go

# Run database migrations
migrate:
	@echo "Running database migrations..."
	go run cmd/migrate/main.go up

# Create an API key
create-key:
	@echo "Creating API key..."
	go run cmd/createkey/main.go

# Start Docker containers
docker-up:
	@echo "Starting Docker containers..."
	docker-compose up -d
	@echo "Waiting for services to be ready..."
	@sleep 3
	@docker-compose ps

# Stop Docker containers
docker-down:
	@echo "Stopping Docker containers..."
	docker-compose down

# Stop and remove volumes
docker-clean:
	@echo "Stopping Docker containers and removing volumes..."
	docker-compose down -v

# Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	rm -rf bin/
	rm -f coverage.out coverage.html
	@echo "Clean complete"

# Install dependencies
deps:
	@echo "Installing dependencies..."
	go mod download
	go mod tidy
	@echo "Dependencies installed"

# Install development tools
install-tools:
	@echo "Installing development tools..."
	go install github.com/swaggo/swag/cmd/swag@latest
	@echo "Tools installed"

# Format code
fmt:
	@echo "Formatting code..."
	go fmt ./...
	@echo "Code formatted"

# Lint code
lint:
	@echo "Linting code..."
	golangci-lint run ./...

# Run everything needed for development
dev-setup: docker-up deps install-tools migrate create-key
	@echo "Development environment ready!"
	@echo "Run 'make run' to start the API server"

# Full test suite (unit + integration)
test-all: tests
	@echo "All tests passed!"
