"""Tests for clustering algorithms."""

import numpy as np
import pytest

from src.clustering import HDBSCANClusterer, UMAPReducer


class TestUMAPReducer:
    """Tests for UMAP dimensionality reduction."""

    def test_reduce_dimensions(self) -> None:
        """Test that UMAP reduces dimensions correctly."""
        # Create random high-dimensional data
        np.random.seed(42)
        embeddings = np.random.randn(100, 1536).astype(np.float32)

        reducer = UMAPReducer(n_components=10, n_neighbors=10)
        reduced = reducer.fit_transform(embeddings)

        assert reduced.shape == (100, 10)

    def test_empty_input(self) -> None:
        """Test handling of empty input."""
        reducer = UMAPReducer(n_components=10)
        reduced = reducer.fit_transform(np.array([]))

        assert len(reduced) == 0


class TestHDBSCANClusterer:
    """Tests for HDBSCAN clustering."""

    def test_find_clusters(self) -> None:
        """Test that HDBSCAN finds natural clusters."""
        from uuid import uuid4

        np.random.seed(42)

        # Create 3 distinct clusters
        cluster1 = np.random.randn(50, 10) + np.array([5, 0, 0, 0, 0, 0, 0, 0, 0, 0])
        cluster2 = np.random.randn(50, 10) + np.array([0, 5, 0, 0, 0, 0, 0, 0, 0, 0])
        cluster3 = np.random.randn(50, 10) + np.array([0, 0, 5, 0, 0, 0, 0, 0, 0, 0])

        embeddings = np.vstack([cluster1, cluster2, cluster3]).astype(np.float32)
        record_ids = [uuid4() for _ in range(150)]
        texts = [f"text {i}" for i in range(150)]

        clusterer = HDBSCANClusterer(min_cluster_size=10, min_samples=5)
        result = clusterer.fit_predict(embeddings, record_ids, texts)

        # Should find approximately 3 clusters
        assert len(result.clusters) >= 2
        assert len(result.clusters) <= 5

    def test_noise_detection(self) -> None:
        """Test that HDBSCAN identifies noise points."""
        from uuid import uuid4

        np.random.seed(42)

        # Create one tight cluster and some scattered points
        cluster = np.random.randn(50, 10) * 0.1
        noise = np.random.randn(20, 10) * 10  # Scattered noise

        embeddings = np.vstack([cluster, noise]).astype(np.float32)
        record_ids = [uuid4() for _ in range(70)]
        texts = [f"text {i}" for i in range(70)]

        clusterer = HDBSCANClusterer(min_cluster_size=10, min_samples=5)
        result = clusterer.fit_predict(embeddings, record_ids, texts)

        # Should have some noise points
        assert len(result.noise_ids) > 0

    def test_get_closest_to_centroid(self) -> None:
        """Test centroid representative selection."""
        from uuid import uuid4

        np.random.seed(42)

        # Create a cluster
        embeddings = np.random.randn(100, 10).astype(np.float32)
        record_ids = [uuid4() for _ in range(100)]
        texts = [f"text {i}" for i in range(100)]

        clusterer = HDBSCANClusterer(min_cluster_size=20, min_samples=5)
        result = clusterer.fit_predict(embeddings, record_ids, texts)

        if result.clusters:
            closest = clusterer.get_closest_to_centroid(
                result.clusters[0], embeddings, n=5
            )
            assert len(closest) <= 5
            # Distances should be sorted ascending
            distances = [d for _, _, d in closest]
            assert distances == sorted(distances)
