# Taxonomy Generator

A Python microservice for generating per-tenant taxonomies using UMAP dimensionality reduction, HDBSCAN clustering, and GPT-4o labeling.

## Features

- **UMAP Dimensionality Reduction**: Reduces 1536-dimensional embeddings to 10 dimensions for better clustering
- **HDBSCAN Clustering**: Automatically discovers natural clusters without specifying K
- **GPT-4o Labeling**: Generates human-readable titles and descriptions for each cluster
- **Per-Tenant Isolation**: Each tenant gets their own taxonomy
- **Noise Detection**: Identifies feedback that doesn't fit any cluster

## Development

### Prerequisites

- Python 3.11+
- Poetry
- PostgreSQL with pgvector extension

### Setup

```bash
cd services/taxonomy-generator

# Install dependencies
poetry install

# Run development server
poetry run uvicorn src.main:app --reload --port 8001
```

### Running Tests

```bash
poetry run pytest
```

### Linting

```bash
poetry run ruff check src/
poetry run mypy src/
```

## API Endpoints

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/health` | Health check |
| POST | `/cluster/{tenant_id}` | Trigger taxonomy generation for a tenant |
| GET | `/cluster/{tenant_id}/status` | Get clustering job status |

## Configuration

Environment variables:

| Variable | Description | Default |
|----------|-------------|---------|
| `DATABASE_URL` | PostgreSQL connection string | Required |
| `OPENAI_API_KEY` | OpenAI API key for GPT-4o | Required |
| `UMAP_N_COMPONENTS` | Target dimensions after UMAP | `10` |
| `HDBSCAN_MIN_CLUSTER_SIZE` | Minimum cluster size | `50` |
| `LOG_LEVEL` | Logging level | `INFO` |

## Docker

```bash
# Build
docker build -t taxonomy-generator .

# Run
docker run -p 8001:8001 \
  -e DATABASE_URL="postgresql://..." \
  -e OPENAI_API_KEY="sk-..." \
  taxonomy-generator
```
