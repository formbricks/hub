# Stage 1: Build the Go binary
FROM golang:1.25-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git ca-certificates

WORKDIR /build

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary (TARGETARCH is provided by buildx for multi-platform builds)
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH:-amd64} go build \
    -ldflags="-w -s" \
    -o /build/bin/hub \
    ./cmd/hub

# Stage 2: Create minimal runtime image
FROM alpine:latest

# Install runtime dependencies
RUN apk --no-cache add ca-certificates tzdata wget

# Create non-root user
RUN addgroup -g 1000 hub && \
    adduser -D -u 1000 -G hub hub

WORKDIR /app

# Copy binary from builder
COPY --from=builder --chown=hub:hub /build/bin/hub /app/hub

# Switch to non-root user
USER hub

# Expose port
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health || exit 1

# Run the application
ENTRYPOINT ["/app/hub"]






