"""HDBSCAN clustering for automatic cluster discovery."""

from dataclasses import dataclass
from uuid import UUID

import hdbscan
import numpy as np
import structlog

from src.config import settings

logger = structlog.get_logger()


@dataclass
class Cluster:
    """Represents a discovered cluster."""

    label: int
    member_indices: np.ndarray
    member_ids: list[UUID]
    member_texts: list[str]
    centroid: np.ndarray
    size: int
    avg_distance_to_centroid: float


@dataclass
class ClusteringResult:
    """Result of HDBSCAN clustering."""

    clusters: list[Cluster]
    noise_indices: np.ndarray
    noise_ids: list[UUID]
    labels: np.ndarray
    probabilities: np.ndarray


class HDBSCANClusterer:
    """Discovers natural clusters using HDBSCAN algorithm."""

    def __init__(
        self,
        min_cluster_size: int | None = None,
        min_samples: int | None = None,
        cluster_selection_epsilon: float | None = None,
    ):
        """
        Initialize HDBSCAN clusterer.

        Args:
            min_cluster_size: Minimum number of points to form a cluster (default: 50)
            min_samples: Number of samples in neighborhood for core points (default: 10)
            cluster_selection_epsilon: Distance threshold for cluster selection (default: 0.0)
        """
        self.min_cluster_size = min_cluster_size or settings.hdbscan_min_cluster_size
        self.min_samples = min_samples or settings.hdbscan_min_samples
        self.cluster_selection_epsilon = (
            cluster_selection_epsilon or settings.hdbscan_cluster_selection_epsilon
        )

    def fit_predict(
        self,
        embeddings: np.ndarray,
        record_ids: list[UUID],
        texts: list[str],
    ) -> ClusteringResult:
        """
        Cluster embeddings and return discovered clusters.

        Args:
            embeddings: Reduced embeddings (after UMAP)
            record_ids: List of record UUIDs corresponding to embeddings
            texts: List of text content for each record

        Returns:
            ClusteringResult with clusters and noise points
        """
        if embeddings.shape[0] == 0:
            return ClusteringResult(
                clusters=[],
                noise_indices=np.array([]),
                noise_ids=[],
                labels=np.array([]),
                probabilities=np.array([]),
            )

        logger.info(
            "Starting HDBSCAN clustering",
            n_samples=embeddings.shape[0],
            min_cluster_size=self.min_cluster_size,
        )

        clusterer = hdbscan.HDBSCAN(
            min_cluster_size=self.min_cluster_size,
            min_samples=self.min_samples,
            cluster_selection_epsilon=self.cluster_selection_epsilon,
            metric="euclidean",  # UMAP output works well with euclidean
            cluster_selection_method="eom",  # Excess of Mass - better for varying densities
            prediction_data=True,
        )

        labels = clusterer.fit_predict(embeddings)
        probabilities = clusterer.probabilities_

        # Separate noise from clusters
        noise_mask = labels == -1
        noise_indices = np.where(noise_mask)[0]
        noise_ids = [record_ids[i] for i in noise_indices]

        # Build cluster objects
        unique_labels = set(labels) - {-1}
        clusters: list[Cluster] = []

        for label in sorted(unique_labels):
            mask = labels == label
            indices = np.where(mask)[0]
            cluster_embeddings = embeddings[mask]

            # Compute centroid
            centroid = cluster_embeddings.mean(axis=0)

            # Compute distances to centroid
            distances = np.linalg.norm(cluster_embeddings - centroid, axis=1)
            avg_distance = float(distances.mean())

            cluster = Cluster(
                label=int(label),
                member_indices=indices,
                member_ids=[record_ids[i] for i in indices],
                member_texts=[texts[i] for i in indices],
                centroid=centroid,
                size=len(indices),
                avg_distance_to_centroid=avg_distance,
            )
            clusters.append(cluster)

        logger.info(
            "HDBSCAN clustering complete",
            num_clusters=len(clusters),
            noise_count=len(noise_indices),
            cluster_sizes=[c.size for c in clusters],
        )

        return ClusteringResult(
            clusters=clusters,
            noise_indices=noise_indices,
            noise_ids=noise_ids,
            labels=labels,
            probabilities=probabilities,
        )

    def get_closest_to_centroid(
        self,
        cluster: Cluster,
        embeddings: np.ndarray,
        n: int = 10,
    ) -> list[tuple[int, str, float]]:
        """
        Get the N points closest to the cluster centroid.

        Args:
            cluster: The cluster to analyze
            embeddings: Full embeddings array
            n: Number of points to return

        Returns:
            List of (index, text, distance) tuples sorted by distance
        """
        cluster_embeddings = embeddings[cluster.member_indices]
        distances = np.linalg.norm(cluster_embeddings - cluster.centroid, axis=1)

        # Sort by distance and take top N
        sorted_indices = np.argsort(distances)[:n]

        result = []
        for local_idx in sorted_indices:
            global_idx = cluster.member_indices[local_idx]
            result.append(
                (int(global_idx), cluster.member_texts[local_idx], float(distances[local_idx]))
            )

        return result
