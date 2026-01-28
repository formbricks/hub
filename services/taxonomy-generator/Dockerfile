# syntax=docker/dockerfile:1

FROM python:3.11-slim AS builder

# Install build dependencies for numpy/hdbscan
RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential \
    gcc \
    g++ \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Copy dependency files
COPY pyproject.toml poetry.lock* ./

# Install dependencies using pip (more reliable for binary wheels)
# First install the packages with native extensions using binary wheels only
RUN pip install --no-cache-dir --upgrade pip setuptools wheel \
    && pip install --no-cache-dir --only-binary=:all: numpy numba llvmlite \
    && pip install --no-cache-dir \
        fastapi[standard] \
        uvicorn[standard] \
        pydantic \
        pydantic-settings \
        asyncpg \
        pgvector \
        umap-learn \
        hdbscan \
        openai \
        httpx \
        structlog

# Production stage
FROM python:3.11-slim AS runtime

# Install runtime dependencies for numpy/hdbscan
RUN apt-get update && apt-get install -y --no-install-recommends \
    libgomp1 \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Copy installed packages from builder
COPY --from=builder /usr/local/lib/python3.11/site-packages /usr/local/lib/python3.11/site-packages
COPY --from=builder /usr/local/bin /usr/local/bin

# Copy application code
COPY src/ ./src/

# Create non-root user
RUN useradd --create-home --shell /bin/bash appuser
USER appuser

# Expose port
EXPOSE 8001

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD python -c "import httpx; httpx.get('http://localhost:8001/health')" || exit 1

# Run the application
CMD ["uvicorn", "src.main:app", "--host", "0.0.0.0", "--port", "8001"]
