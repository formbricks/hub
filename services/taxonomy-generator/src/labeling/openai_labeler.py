"""GPT-4o based cluster labeling."""

import json
from dataclasses import dataclass

import structlog
from openai import AsyncOpenAI

from src.config import settings

logger = structlog.get_logger()


@dataclass
class TopicLabel:
    """Generated label for a cluster."""

    title: str
    description: str


class OpenAILabeler:
    """Generates human-readable labels for clusters using GPT-4o."""

    def __init__(self, api_key: str | None = None):
        """
        Initialize OpenAI labeler.

        Args:
            api_key: OpenAI API key (defaults to settings)
        """
        self.client = AsyncOpenAI(api_key=api_key or settings.openai_api_key)
        self.model = "gpt-4o"

    async def label_cluster(
        self,
        representative_texts: list[str],
        cluster_size: int,
        parent_title: str | None = None,
        level: int = 1,
        ancestor_titles: list[str] | None = None,
    ) -> TopicLabel:
        """
        Generate a title and description for a cluster.

        Args:
            representative_texts: 10 texts closest to centroid
            cluster_size: Total number of items in cluster
            parent_title: If generating sub-level, the parent topic title
            level: The level being generated (1, 2, 3, 4, etc.)
            ancestor_titles: List of all ancestor titles from root to parent

        Returns:
            TopicLabel with title and description
        """
        # Build level-specific context
        level_descriptions = {
            1: "broad categories",
            2: "sub-categories",
            3: "specific themes",
            4: "detailed sub-themes",
            5: "granular topics",
        }
        level_desc = level_descriptions.get(level, f"level {level} topics")

        if level == 1:
            context = f"""You are categorizing user feedback into {level_desc} (Level 1 topics).
These are the broadest groupings of feedback themes."""
        else:
            # Build hierarchy context
            if ancestor_titles:
                hierarchy = " > ".join(ancestor_titles + [parent_title or ""])
                context = f"""You are categorizing user feedback within the hierarchy: {hierarchy}
This is a Level {level} topic ({level_desc}), which should be more specific than its parent "{parent_title}"."""
            else:
                context = f"""You are categorizing user feedback within the category "{parent_title}".
This is a Level {level} topic ({level_desc})."""

        # Adjust title length guidance based on level
        if level == 1:
            title_guidance = "2-4 word title for this broad category"
        elif level == 2:
            title_guidance = "2-3 word title for this sub-category"
        else:
            title_guidance = "2-4 word specific title for this theme"

        prompt = f"""{context}

Analyze these {len(representative_texts)} representative feedback items from a cluster of {cluster_size} total items.

Feedback items:
{chr(10).join(f'- {text[:500]}' for text in representative_texts)}

Based on the common theme in these items, provide:
1. A concise {title_guidance}
2. A single sentence description (max 100 characters)

Important: The title should be distinct from the parent category and capture what makes this sub-group unique.

Respond ONLY with valid JSON in this exact format:
{{"title": "Example Title", "description": "Brief description of what this category contains."}}"""

        try:
            response = await self.client.chat.completions.create(
                model=self.model,
                messages=[{"role": "user", "content": prompt}],
                response_format={"type": "json_object"},
                temperature=0.3,  # Lower temperature for consistency
                max_tokens=150,
            )

            content = response.choices[0].message.content
            if not content:
                raise ValueError("Empty response from OpenAI")

            result = json.loads(content)

            label = TopicLabel(
                title=result.get("title", "Unnamed Category")[:255],
                description=result.get("description", "")[:500],
            )

            logger.info(
                "Generated cluster label",
                title=label.title,
                cluster_size=cluster_size,
                level=level,
            )

            return label

        except json.JSONDecodeError as e:
            logger.error("Failed to parse OpenAI response", error=str(e), content=content)
            return TopicLabel(
                title=f"Cluster ({cluster_size} items)",
                description="Auto-generated cluster",
            )
        except Exception as e:
            logger.error("OpenAI labeling failed", error=str(e))
            return TopicLabel(
                title=f"Cluster ({cluster_size} items)",
                description="Auto-generated cluster",
            )

    async def generate_embedding(self, text: str) -> list[float]:
        """
        Generate embedding for a topic title.

        Args:
            text: Text to embed (usually the topic title)

        Returns:
            Embedding vector
        """
        response = await self.client.embeddings.create(
            model="text-embedding-3-small",
            input=text,
        )
        return response.data[0].embedding
