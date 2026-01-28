"""Pydantic schemas for API requests and responses."""

from datetime import datetime
from enum import Enum
from uuid import UUID

from pydantic import BaseModel, Field


class HealthResponse(BaseModel):
    """Health check response."""

    status: str = "healthy"
    version: str = "0.1.0"


class ClusterConfig(BaseModel):
    """Configuration for clustering operation."""

    # UMAP settings (optional overrides)
    umap_n_components: int | None = Field(None, ge=2, le=50)
    umap_n_neighbors: int | None = Field(None, ge=2, le=200)
    umap_min_dist: float | None = Field(None, ge=0.0, le=1.0)

    # HDBSCAN settings (optional overrides)
    hdbscan_min_cluster_size: int | None = Field(None, ge=5, le=1000)
    hdbscan_min_samples: int | None = Field(None, ge=1, le=100)

    # Limit on embeddings to process
    max_embeddings: int | None = Field(None, ge=100, le=500000)

    # Maximum depth of taxonomy hierarchy (1-4)
    max_levels: int = Field(default=4, ge=1, le=10)

    # Minimum cluster sizes per level for subdivision
    # Key: level number, Value: minimum cluster size to create sub-topics
    level_min_cluster_sizes: dict[int, int] = Field(
        default_factory=lambda: {
            1: 100,  # Level 1: need 100+ items to create L2 children
            2: 50,   # Level 2: need 50+ items to create L3 children
            3: 25,   # Level 3: need 25+ items to create L4 children
            4: 15,   # Level 4: terminal level (no children)
        }
    )

    # HDBSCAN min_cluster_size per level (smaller clusters at deeper levels)
    level_hdbscan_min_cluster_sizes: dict[int, int] = Field(
        default_factory=lambda: {
            1: 50,   # Level 1: larger clusters
            2: 25,   # Level 2: medium clusters
            3: 15,   # Level 3: smaller clusters
            4: 10,   # Level 4: smallest clusters
        }
    )

    # DEPRECATED: kept for backwards compatibility
    generate_level2: bool = True
    level2_min_cluster_size: int = 50

    def get_min_cluster_size_for_level(self, level: int) -> int:
        """Get the minimum cluster size required to subdivide at this level."""
        return self.level_min_cluster_sizes.get(level, 25)

    def get_hdbscan_min_cluster_size_for_level(self, level: int) -> int:
        """Get the HDBSCAN min_cluster_size for clustering at this level."""
        return self.level_hdbscan_min_cluster_sizes.get(level, 10)


class ClusteringJobStatus(str, Enum):
    """Status of a clustering job."""

    PENDING = "pending"
    RUNNING = "running"
    COMPLETED = "completed"
    FAILED = "failed"


class TopicResult(BaseModel):
    """Result of a generated topic."""

    id: UUID
    title: str
    description: str
    level: int
    parent_id: UUID | None = None
    cluster_size: int
    avg_distance_to_centroid: float


class ClusterResult(BaseModel):
    """Result of clustering operation."""

    tenant_id: str
    job_id: UUID
    status: ClusteringJobStatus
    total_records: int
    clustered_records: int
    noise_records: int
    num_clusters: int
    topics: list[TopicResult]
    started_at: datetime
    completed_at: datetime | None = None
    error_message: str | None = None


class ClusteringJobResponse(BaseModel):
    """Response for clustering job status."""

    job_id: UUID
    tenant_id: str
    status: ClusteringJobStatus
    progress: float = Field(ge=0.0, le=1.0, description="Progress from 0.0 to 1.0")
    message: str | None = None
    result: ClusterResult | None = None
