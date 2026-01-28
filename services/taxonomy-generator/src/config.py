"""Application configuration using pydantic-settings."""

from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    """Application settings loaded from environment variables."""

    model_config = SettingsConfigDict(
        env_file=".env",
        env_file_encoding="utf-8",
        case_sensitive=False,
    )

    # Database
    database_url: str

    # OpenAI
    openai_api_key: str

    # UMAP settings
    umap_n_components: int = 10
    umap_n_neighbors: int = 15
    umap_min_dist: float = 0.1
    umap_metric: str = "cosine"

    # HDBSCAN settings
    hdbscan_min_cluster_size: int = 50
    hdbscan_min_samples: int = 10
    hdbscan_cluster_selection_epsilon: float = 0.0

    # Clustering limits
    max_embeddings_per_tenant: int = 100000
    centroid_sample_size: int = 10

    # API settings
    log_level: str = "INFO"
    api_host: str = "0.0.0.0"
    api_port: int = 8001


settings = Settings()  # type: ignore[call-arg]
