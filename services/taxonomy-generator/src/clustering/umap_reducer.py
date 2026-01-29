"""UMAP dimensionality reduction for embeddings."""

import warnings

import numpy as np
import structlog
import umap

from src.config import settings

# Suppress UMAP warning about n_jobs when random_state is set (expected behavior)
warnings.filterwarnings("ignore", message="n_jobs value .* overridden to 1 by setting random_state")

logger = structlog.get_logger()


class UMAPReducer:
    """Reduces high-dimensional embeddings to lower dimensions using UMAP."""

    def __init__(
        self,
        n_components: int | None = None,
        n_neighbors: int | None = None,
        min_dist: float | None = None,
        metric: str | None = None,
    ):
        """
        Initialize UMAP reducer.

        Args:
            n_components: Target number of dimensions (default: 10)
            n_neighbors: Number of neighbors for local structure (default: 15)
            min_dist: Minimum distance between points (default: 0.1)
            metric: Distance metric (default: cosine)
        """
        self.n_components = n_components or settings.umap_n_components
        self.n_neighbors = n_neighbors or settings.umap_n_neighbors
        self.min_dist = min_dist or settings.umap_min_dist
        self.metric = metric or settings.umap_metric

        self._reducer: umap.UMAP | None = None

    def fit_transform(self, embeddings: np.ndarray) -> np.ndarray:
        """
        Fit UMAP and transform embeddings to lower dimensions.

        Args:
            embeddings: Array of shape (n_samples, n_features)
                        Typically 1536-dimensional OpenAI embeddings

        Returns:
            Reduced embeddings of shape (n_samples, n_components)
        """
        if embeddings.shape[0] == 0:
            return np.array([])

        logger.info(
            "Starting UMAP reduction",
            input_dim=embeddings.shape[1],
            output_dim=self.n_components,
            n_samples=embeddings.shape[0],
        )

        self._reducer = umap.UMAP(
            n_components=self.n_components,
            n_neighbors=min(self.n_neighbors, embeddings.shape[0] - 1),
            min_dist=self.min_dist,
            metric=self.metric,
            random_state=42,  # For reproducibility
            low_memory=True,  # Better for large datasets
            verbose=False,
        )

        reduced = self._reducer.fit_transform(embeddings)

        logger.info(
            "UMAP reduction complete",
            output_shape=reduced.shape,
        )

        return reduced

    def transform(self, embeddings: np.ndarray) -> np.ndarray:
        """
        Transform new embeddings using a fitted UMAP model.

        Args:
            embeddings: New embeddings to transform

        Returns:
            Reduced embeddings
        """
        if self._reducer is None:
            raise ValueError("UMAP model not fitted. Call fit_transform first.")

        return self._reducer.transform(embeddings)
