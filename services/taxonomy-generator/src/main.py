"""FastAPI application entry point."""

from contextlib import asynccontextmanager
from typing import AsyncGenerator

import structlog
from fastapi import BackgroundTasks, FastAPI, HTTPException
from fastapi.middleware.cors import CORSMiddleware

from src.config import settings
from src.db import close_db_pool, get_db_pool
from src.models import ClusterConfig, ClusteringJobResponse, ClusteringJobStatus, HealthResponse
from src.service import TaxonomyService

# Configure structured logging
structlog.configure(
    processors=[
        structlog.stdlib.filter_by_level,
        structlog.stdlib.add_logger_name,
        structlog.stdlib.add_log_level,
        structlog.stdlib.PositionalArgumentsFormatter(),
        structlog.processors.TimeStamper(fmt="iso"),
        structlog.processors.StackInfoRenderer(),
        structlog.processors.format_exc_info,
        structlog.processors.UnicodeDecoder(),
        structlog.processors.JSONRenderer(),
    ],
    wrapper_class=structlog.stdlib.BoundLogger,
    context_class=dict,
    logger_factory=structlog.stdlib.LoggerFactory(),
    cache_logger_on_first_use=True,
)

logger = structlog.get_logger()

# In-memory job tracking (replace with Redis/DB for production)
_jobs: dict[str, ClusteringJobResponse] = {}


@asynccontextmanager
async def lifespan(app: FastAPI) -> AsyncGenerator[None, None]:
    """Application lifespan - startup and shutdown events."""
    # Startup
    logger.info("Starting taxonomy-generator service", port=settings.api_port)
    await get_db_pool()
    yield
    # Shutdown
    logger.info("Shutting down taxonomy-generator service")
    await close_db_pool()


app = FastAPI(
    title="Taxonomy Generator",
    description="Microservice for generating per-tenant taxonomies using UMAP, HDBSCAN, and GPT-4o",
    version="0.1.0",
    lifespan=lifespan,
)

# CORS middleware
app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],  # Configure appropriately for production
    allow_credentials=True,
    allow_methods=["*"],
    allow_headers=["*"],
)

# Service instance
taxonomy_service = TaxonomyService()


@app.get("/health", response_model=HealthResponse)
async def health_check() -> HealthResponse:
    """Health check endpoint."""
    return HealthResponse()


@app.post("/cluster/{tenant_id}", response_model=ClusteringJobResponse)
async def trigger_clustering(
    tenant_id: str,
    config: ClusterConfig | None = None,
    background_tasks: BackgroundTasks = BackgroundTasks(),
) -> ClusteringJobResponse:
    """
    Trigger taxonomy generation for a tenant.

    This starts a background job and returns immediately.
    Use GET /cluster/{tenant_id}/status to check progress.
    """
    from uuid import uuid4

    job_id = uuid4()
    job_key = f"{tenant_id}:{job_id}"

    # Create initial job response
    job_response = ClusteringJobResponse(
        job_id=job_id,
        tenant_id=tenant_id,
        status=ClusteringJobStatus.PENDING,
        progress=0.0,
        message="Job queued",
    )
    _jobs[job_key] = job_response

    # Start background task
    async def run_clustering() -> None:
        try:
            _jobs[job_key].status = ClusteringJobStatus.RUNNING
            _jobs[job_key].progress = 0.1
            _jobs[job_key].message = "Loading embeddings..."

            result = await taxonomy_service.generate_taxonomy(
                tenant_id=tenant_id,
                config=config,
            )

            _jobs[job_key].status = result.status
            _jobs[job_key].progress = 1.0
            _jobs[job_key].result = result
            _jobs[job_key].message = f"Generated {len(result.topics)} topics"

        except Exception as e:
            logger.error("Clustering job failed", error=str(e), tenant_id=tenant_id)
            _jobs[job_key].status = ClusteringJobStatus.FAILED
            _jobs[job_key].message = str(e)

    background_tasks.add_task(run_clustering)

    logger.info("Clustering job queued", tenant_id=tenant_id, job_id=str(job_id))
    return job_response


@app.get("/cluster/{tenant_id}/status", response_model=ClusteringJobResponse)
async def get_clustering_status(tenant_id: str, job_id: str | None = None) -> ClusteringJobResponse:
    """
    Get status of a clustering job.

    If job_id is not provided, returns the most recent job for the tenant.
    """
    if job_id:
        job_key = f"{tenant_id}:{job_id}"
        if job_key not in _jobs:
            raise HTTPException(status_code=404, detail="Job not found")
        return _jobs[job_key]

    # Find most recent job for tenant
    tenant_jobs = [
        (key, job) for key, job in _jobs.items() if key.startswith(f"{tenant_id}:")
    ]
    if not tenant_jobs:
        raise HTTPException(status_code=404, detail="No jobs found for tenant")

    # Return most recent (last in dict, assuming insertion order)
    return tenant_jobs[-1][1]


@app.post("/cluster/{tenant_id}/sync")
async def sync_clustering(
    tenant_id: str,
    config: ClusterConfig | None = None,
) -> ClusteringJobResponse:
    """
    Synchronously generate taxonomy (blocking).

    Use this for testing or when you need to wait for results.
    For production, prefer the async POST /cluster/{tenant_id} endpoint.
    """
    from uuid import uuid4

    job_id = uuid4()

    logger.info("Starting synchronous clustering", tenant_id=tenant_id, job_id=str(job_id))

    result = await taxonomy_service.generate_taxonomy(
        tenant_id=tenant_id,
        config=config,
    )

    return ClusteringJobResponse(
        job_id=job_id,
        tenant_id=tenant_id,
        status=result.status,
        progress=1.0,
        message=f"Generated {len(result.topics)} topics",
        result=result,
    )


if __name__ == "__main__":
    import uvicorn

    uvicorn.run(
        "src.main:app",
        host=settings.api_host,
        port=settings.api_port,
        reload=True,
    )
