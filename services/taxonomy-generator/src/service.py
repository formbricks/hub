"""Taxonomy generation service - orchestrates the full pipeline."""

from datetime import datetime
from uuid import UUID, uuid4

import numpy as np
import structlog

from src.clustering import HDBSCANClusterer, UMAPReducer
from src.config import settings
from src.db.postgres import (
    clear_tenant_topics,
    load_embeddings_for_tenant,
    save_topic,
    update_feedback_topic,
)
from src.labeling import OpenAILabeler
from src.models import ClusterConfig, ClusteringJobStatus, ClusterResult, TopicResult

logger = structlog.get_logger()


class TaxonomyService:
    """Orchestrates the taxonomy generation pipeline."""

    def __init__(self):
        """Initialize the service with default components."""
        self.labeler = OpenAILabeler()

    async def generate_taxonomy(
        self,
        tenant_id: str,
        config: ClusterConfig | None = None,
    ) -> ClusterResult:
        """
        Generate taxonomy for a tenant.

        Pipeline:
        1. Load embeddings for tenant
        2. Reduce dimensions with UMAP
        3. Cluster with HDBSCAN
        4. Label clusters with GPT-4o
        5. Save topics to database
        6. Recursively generate sub-topics up to max_levels

        Args:
            tenant_id: Tenant ID to generate taxonomy for
            config: Optional clustering configuration overrides

        Returns:
            ClusterResult with generated topics
        """
        config = config or ClusterConfig()
        job_id = uuid4()
        started_at = datetime.utcnow()

        logger.info(
            "Starting taxonomy generation",
            tenant_id=tenant_id,
            job_id=str(job_id),
            max_levels=config.max_levels,
        )

        try:
            # 1. Clear existing topics for this tenant
            await clear_tenant_topics(tenant_id)

            # 2. Load embeddings
            max_embeddings = config.max_embeddings or settings.max_embeddings_per_tenant
            record_ids, embeddings, texts = await load_embeddings_for_tenant(
                tenant_id, limit=max_embeddings
            )

            if len(record_ids) == 0:
                logger.warning("No embeddings found for tenant", tenant_id=tenant_id)
                return ClusterResult(
                    tenant_id=tenant_id,
                    job_id=job_id,
                    status=ClusteringJobStatus.COMPLETED,
                    total_records=0,
                    clustered_records=0,
                    noise_records=0,
                    num_clusters=0,
                    topics=[],
                    started_at=started_at,
                    completed_at=datetime.utcnow(),
                )

            # 3. Generate topics recursively starting at level 1
            topics = await self._cluster_recursive(
                tenant_id=tenant_id,
                record_ids=record_ids,
                embeddings=embeddings,
                texts=texts,
                parent_id=None,
                parent_title=None,
                ancestor_titles=[],
                current_level=1,
                config=config,
            )

            result = ClusterResult(
                tenant_id=tenant_id,
                job_id=job_id,
                status=ClusteringJobStatus.COMPLETED,
                total_records=len(record_ids),
                clustered_records=len(record_ids),  # Updated by recursive process
                noise_records=0,
                num_clusters=len([t for t in topics if t.level == 1]),
                topics=topics,
                started_at=started_at,
                completed_at=datetime.utcnow(),
            )

            logger.info(
                "Taxonomy generation complete",
                tenant_id=tenant_id,
                job_id=str(job_id),
                num_topics=len(topics),
                level_counts={
                    level: len([t for t in topics if t.level == level])
                    for level in range(1, config.max_levels + 1)
                },
                duration_seconds=(datetime.utcnow() - started_at).total_seconds(),
            )

            return result

        except Exception as e:
            logger.error(
                "Taxonomy generation failed",
                tenant_id=tenant_id,
                job_id=str(job_id),
                error=str(e),
            )
            return ClusterResult(
                tenant_id=tenant_id,
                job_id=job_id,
                status=ClusteringJobStatus.FAILED,
                total_records=0,
                clustered_records=0,
                noise_records=0,
                num_clusters=0,
                topics=[],
                started_at=started_at,
                completed_at=datetime.utcnow(),
                error_message=str(e),
            )

    async def _cluster_recursive(
        self,
        tenant_id: str,
        record_ids: list[str],
        embeddings: np.ndarray,
        texts: list[str],
        parent_id: UUID | None,
        parent_title: str | None,
        ancestor_titles: list[str],
        current_level: int,
        config: ClusterConfig,
    ) -> list[TopicResult]:
        """
        Recursively cluster feedback into hierarchical topics.

        Args:
            tenant_id: Tenant ID
            record_ids: List of feedback record IDs
            embeddings: Original high-dimensional embeddings
            texts: Feedback text content
            parent_id: Parent topic ID (None for Level 1)
            parent_title: Parent topic title (None for Level 1)
            ancestor_titles: List of ancestor titles from root to parent
            current_level: Current level being generated (1-based)
            config: Clustering configuration

        Returns:
            List of generated topics at this level and all child levels
        """
        # Stop if we've reached max depth or not enough data
        if current_level > config.max_levels:
            return []

        if len(record_ids) < 5:
            return []

        logger.info(
            f"Clustering at level {current_level}",
            parent_title=parent_title,
            num_records=len(record_ids),
        )

        # Get level-specific HDBSCAN settings
        min_cluster_size = config.get_hdbscan_min_cluster_size_for_level(current_level)
        min_samples = max(min_cluster_size // 5, 3)

        # Adjust UMAP parameters for smaller datasets at deeper levels
        n_neighbors = min(
            config.umap_n_neighbors or 15,
            max(len(record_ids) - 1, 2),
        )
        n_components = min(
            config.umap_n_components or 10,
            max(5, 12 - current_level * 2),  # Fewer dims at deeper levels
        )

        # UMAP dimensionality reduction
        reducer = UMAPReducer(
            n_components=n_components,
            n_neighbors=n_neighbors,
            min_dist=config.umap_min_dist or 0.1,
        )

        try:
            reduced_embeddings = reducer.fit_transform(embeddings)
        except Exception as e:
            logger.warning(
                f"UMAP failed at level {current_level}",
                error=str(e),
                num_records=len(record_ids),
            )
            return []

        # HDBSCAN clustering
        clusterer = HDBSCANClusterer(
            min_cluster_size=min_cluster_size,
            min_samples=min_samples,
        )

        try:
            clustering_result = clusterer.fit_predict(reduced_embeddings, record_ids, texts)
        except Exception as e:
            logger.warning(
                f"HDBSCAN failed at level {current_level}",
                error=str(e),
                num_records=len(record_ids),
            )
            return []

        if len(clustering_result.clusters) == 0:
            logger.info(f"No clusters found at level {current_level}")
            return []

        topics: list[TopicResult] = []

        for cluster in clustering_result.clusters:
            # Get representative samples for labeling
            closest = clusterer.get_closest_to_centroid(
                cluster, reduced_embeddings, n=settings.centroid_sample_size
            )
            representative_texts = [text for _, text, _ in closest]

            # Generate label with GPT-4o
            label = await self.labeler.label_cluster(
                representative_texts=representative_texts,
                cluster_size=cluster.size,
                parent_title=parent_title,
                level=current_level,
                ancestor_titles=ancestor_titles if ancestor_titles else None,
            )

            # Generate embedding for the topic title
            topic_embedding = await self.labeler.generate_embedding(label.title)

            # Save to database
            topic_id = await save_topic(
                tenant_id=tenant_id,
                title=label.title,
                description=label.description,
                level=current_level,
                parent_id=parent_id,
                embedding=np.array(topic_embedding, dtype=np.float32),
            )

            # Update feedback records with topic classification
            for member_id in cluster.member_ids:
                confidence = 1.0 - min(cluster.avg_distance_to_centroid, 1.0)
                await update_feedback_topic(member_id, topic_id, confidence)

            topic_result = TopicResult(
                id=topic_id,
                title=label.title,
                description=label.description,
                level=current_level,
                parent_id=parent_id,
                cluster_size=cluster.size,
                avg_distance_to_centroid=cluster.avg_distance_to_centroid,
            )
            topics.append(topic_result)

            logger.info(
                f"Created Level {current_level} topic",
                topic_id=str(topic_id),
                title=label.title,
                parent_title=parent_title,
                cluster_size=cluster.size,
            )

            # Recursively create child topics if cluster is large enough
            min_size_for_children = config.get_min_cluster_size_for_level(current_level)
            if (
                current_level < config.max_levels
                and cluster.size >= min_size_for_children
            ):
                # Extract this cluster's data for sub-clustering
                cluster_embeddings = embeddings[cluster.member_indices]
                cluster_texts = cluster.member_texts
                cluster_ids = cluster.member_ids

                # Build ancestor chain for context
                new_ancestors = ancestor_titles + [parent_title] if parent_title else []

                child_topics = await self._cluster_recursive(
                    tenant_id=tenant_id,
                    record_ids=cluster_ids,
                    embeddings=cluster_embeddings,
                    texts=cluster_texts,
                    parent_id=topic_id,
                    parent_title=label.title,
                    ancestor_titles=new_ancestors,
                    current_level=current_level + 1,
                    config=config,
                )
                topics.extend(child_topics)

        return topics
