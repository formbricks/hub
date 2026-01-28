"""PostgreSQL database connection using asyncpg."""

from contextlib import asynccontextmanager
from typing import Any, AsyncGenerator
from uuid import UUID

import asyncpg
import numpy as np
import structlog
from asyncpg import Pool
from pgvector.asyncpg import register_vector

from src.config import settings

logger = structlog.get_logger()

_pool: Pool | None = None


async def get_db_pool() -> Pool:
    """Get or create the database connection pool."""
    global _pool
    if _pool is None:
        _pool = await asyncpg.create_pool(
            settings.database_url,
            min_size=2,
            max_size=10,
            init=_init_connection,
        )
        logger.info("Database pool created")
    return _pool


async def _init_connection(conn: asyncpg.Connection) -> None:
    """Initialize connection with pgvector support."""
    await register_vector(conn)


async def close_db_pool() -> None:
    """Close the database connection pool."""
    global _pool
    if _pool is not None:
        await _pool.close()
        _pool = None
        logger.info("Database pool closed")


@asynccontextmanager
async def get_connection() -> AsyncGenerator[asyncpg.Connection, None]:
    """Get a database connection from the pool."""
    pool = await get_db_pool()
    async with pool.acquire() as conn:
        yield conn


async def load_embeddings_for_tenant(
    tenant_id: str, limit: int = 100000
) -> tuple[list[UUID], np.ndarray, list[str]]:
    """
    Load embeddings for a specific tenant.

    Returns:
        Tuple of (record_ids, embeddings_array, texts)
    """
    async with get_connection() as conn:
        rows = await conn.fetch(
            """
            SELECT id, embedding, value_text
            FROM feedback_records
            WHERE tenant_id = $1
              AND embedding IS NOT NULL
              AND value_text IS NOT NULL
            ORDER BY created_at DESC
            LIMIT $2
            """,
            tenant_id,
            limit,
        )

    if not rows:
        return [], np.array([]), []

    record_ids = [row["id"] for row in rows]
    texts = [row["value_text"] or "" for row in rows]

    # Convert pgvector arrays to numpy
    embeddings = np.array([np.array(row["embedding"]) for row in rows], dtype=np.float32)

    logger.info(
        "Loaded embeddings",
        tenant_id=tenant_id,
        count=len(record_ids),
        embedding_dim=embeddings.shape[1] if len(embeddings) > 0 else 0,
    )

    return record_ids, embeddings, texts


async def save_topic(
    tenant_id: str,
    title: str,
    description: str,
    level: int,
    parent_id: UUID | None,
    embedding: np.ndarray,
) -> UUID:
    """Save a generated topic to the database."""
    async with get_connection() as conn:
        row = await conn.fetchrow(
            """
            INSERT INTO topics (title, level, parent_id, tenant_id, embedding)
            VALUES ($1, $2, $3, $4, $5)
            RETURNING id
            """,
            title,
            level,
            parent_id,
            tenant_id,
            embedding.tolist(),
        )
        return row["id"]  # type: ignore[return-value]


async def update_feedback_topic(
    record_id: UUID, topic_id: UUID, confidence: float
) -> None:
    """Update a feedback record with its classified topic."""
    async with get_connection() as conn:
        await conn.execute(
            """
            UPDATE feedback_records
            SET topic_id = $1, classification_confidence = $2, updated_at = NOW()
            WHERE id = $3
            """,
            topic_id,
            confidence,
            record_id,
        )


async def clear_tenant_topics(tenant_id: str) -> int:
    """Delete all topics for a tenant (before regenerating taxonomy)."""
    async with get_connection() as conn:
        # First, clear topic_id from feedback_records
        await conn.execute(
            """
            UPDATE feedback_records
            SET topic_id = NULL, classification_confidence = NULL
            WHERE tenant_id = $1
            """,
            tenant_id,
        )
        # Then delete topics
        result = await conn.execute(
            """
            DELETE FROM topics
            WHERE tenant_id = $1
            """,
            tenant_id,
        )
        # Parse "DELETE X" result
        count = int(result.split()[-1]) if result else 0
        logger.info("Cleared tenant topics", tenant_id=tenant_id, deleted=count)
        return count
