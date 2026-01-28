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
        6. Optionally generate Level 2 topics for dense clusters

        Args:
            tenant_id: Tenant ID to generate taxonomy for
            config: Optional clustering configuration overrides

        Returns:
            ClusterResult with generated topics
        """
        config = config or ClusterConfig()
        job_id = uuid4()
        started_at = datetime.utcnow()

        logger.info("Starting taxonomy generation", tenant_id=tenant_id, job_id=str(job_id))

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

            # 3. UMAP dimensionality reduction
            reducer = UMAPReducer(
                n_components=config.umap_n_components,
                n_neighbors=config.umap_n_neighbors,
                min_dist=config.umap_min_dist,
            )
            reduced_embeddings = reducer.fit_transform(embeddings)

            # 4. HDBSCAN clustering
            clusterer = HDBSCANClusterer(
                min_cluster_size=config.hdbscan_min_cluster_size,
                min_samples=config.hdbscan_min_samples,
            )
            clustering_result = clusterer.fit_predict(reduced_embeddings, record_ids, texts)

            # 5. Label each cluster and save as Level 1 topic
            topics: list[TopicResult] = []

            for cluster in clustering_result.clusters:
                # Get representative samples
                closest = clusterer.get_closest_to_centroid(
                    cluster, reduced_embeddings, n=settings.centroid_sample_size
                )
                representative_texts = [text for _, text, _ in closest]

                # Generate label with GPT-4o
                label = await self.labeler.label_cluster(
                    representative_texts=representative_texts,
                    cluster_size=cluster.size,
                )

                # Generate embedding for the topic title
                topic_embedding = await self.labeler.generate_embedding(label.title)

                # Save to database
                topic_id = await save_topic(
                    tenant_id=tenant_id,
                    title=label.title,
                    description=label.description,
                    level=1,
                    parent_id=None,
                    embedding=np.array(topic_embedding, dtype=np.float32),
                )

                # Update feedback records with topic classification
                for member_id in cluster.member_ids:
                    # Find the distance for this member (approximate confidence)
                    confidence = 1.0 - min(cluster.avg_distance_to_centroid, 1.0)
                    await update_feedback_topic(member_id, topic_id, confidence)

                topics.append(
                    TopicResult(
                        id=topic_id,
                        title=label.title,
                        description=label.description,
                        level=1,
                        parent_id=None,
                        cluster_size=cluster.size,
                        avg_distance_to_centroid=cluster.avg_distance_to_centroid,
                    )
                )

                logger.info(
                    "Created Level 1 topic",
                    topic_id=str(topic_id),
                    title=label.title,
                    cluster_size=cluster.size,
                )

                # 6. Optionally generate Level 2 topics for dense clusters
                if config.generate_level2 and cluster.size >= config.level2_min_cluster_size:
                    level2_topics = await self._generate_level2_topics(
                        tenant_id=tenant_id,
                        parent_topic_id=topic_id,
                        parent_title=label.title,
                        cluster=cluster,
                        embeddings=embeddings,  # Original high-dim embeddings
                        texts=texts,
                        config=config,
                    )
                    topics.extend(level2_topics)

            result = ClusterResult(
                tenant_id=tenant_id,
                job_id=job_id,
                status=ClusteringJobStatus.COMPLETED,
                total_records=len(record_ids),
                clustered_records=len(record_ids) - len(clustering_result.noise_ids),
                noise_records=len(clustering_result.noise_ids),
                num_clusters=len(clustering_result.clusters),
                topics=topics,
                started_at=started_at,
                completed_at=datetime.utcnow(),
            )

            logger.info(
                "Taxonomy generation complete",
                tenant_id=tenant_id,
                job_id=str(job_id),
                num_topics=len(topics),
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

    async def _generate_level2_topics(
        self,
        tenant_id: str,
        parent_topic_id: UUID,
        parent_title: str,
        cluster: "Cluster",  # type: ignore[name-defined]
        embeddings: np.ndarray,
        texts: list[str],
        config: ClusterConfig,
    ) -> list[TopicResult]:
        """
        Generate Level 2 sub-topics for a dense cluster.

        Re-runs UMAP + HDBSCAN on just the cluster members.
        """
        logger.info(
            "Generating Level 2 topics",
            parent_title=parent_title,
            cluster_size=cluster.size,
        )

        # Extract just this cluster's embeddings
        cluster_embeddings = embeddings[cluster.member_indices]
        cluster_texts = cluster.member_texts
        cluster_ids = cluster.member_ids

        # Need at least min_cluster_size * 2 for meaningful subdivision
        if len(cluster_ids) < (config.hdbscan_min_cluster_size or 50) * 2:
            return []

        # Re-run UMAP on this subset
        reducer = UMAPReducer(
            n_components=min(config.umap_n_components or 10, 5),  # Fewer dims for Level 2
            n_neighbors=min(config.umap_n_neighbors or 15, len(cluster_ids) - 1),
        )
        reduced = reducer.fit_transform(cluster_embeddings)

        # Re-run HDBSCAN with smaller min_cluster_size
        sub_clusterer = HDBSCANClusterer(
            min_cluster_size=max((config.hdbscan_min_cluster_size or 50) // 2, 10),
            min_samples=max((config.hdbscan_min_samples or 10) // 2, 5),
        )
        sub_result = sub_clusterer.fit_predict(reduced, cluster_ids, cluster_texts)

        level2_topics: list[TopicResult] = []

        for sub_cluster in sub_result.clusters:
            # Get representative samples
            closest = sub_clusterer.get_closest_to_centroid(
                sub_cluster, reduced, n=settings.centroid_sample_size
            )
            representative_texts = [text for _, text, _ in closest]

            # Generate label
            label = await self.labeler.label_cluster(
                representative_texts=representative_texts,
                cluster_size=sub_cluster.size,
                parent_title=parent_title,
            )

            # Generate embedding
            topic_embedding = await self.labeler.generate_embedding(label.title)

            # Save to database
            topic_id = await save_topic(
                tenant_id=tenant_id,
                title=label.title,
                description=label.description,
                level=2,
                parent_id=parent_topic_id,
                embedding=np.array(topic_embedding, dtype=np.float32),
            )

            # Update feedback records
            for member_id in sub_cluster.member_ids:
                confidence = 1.0 - min(sub_cluster.avg_distance_to_centroid, 1.0)
                await update_feedback_topic(member_id, topic_id, confidence)

            level2_topics.append(
                TopicResult(
                    id=topic_id,
                    title=label.title,
                    description=label.description,
                    level=2,
                    parent_id=parent_topic_id,
                    cluster_size=sub_cluster.size,
                    avg_distance_to_centroid=sub_cluster.avg_distance_to_centroid,
                )
            )

            logger.info(
                "Created Level 2 topic",
                topic_id=str(topic_id),
                title=label.title,
                parent_title=parent_title,
                cluster_size=sub_cluster.size,
            )

        return level2_topics
