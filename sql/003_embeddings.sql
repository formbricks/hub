-- Embeddings schema for AI enrichment
-- Adds vector columns to existing tables for semantic search and classification

-- Add embedding column to knowledge_records
ALTER TABLE knowledge_records ADD COLUMN IF NOT EXISTS embedding vector(1536);

-- Create vector similarity index for knowledge_records
-- Using HNSW (Hierarchical Navigable Small World) for approximate nearest neighbor search
-- HNSW works well on empty tables and has better query performance than ivfflat
CREATE INDEX IF NOT EXISTS idx_knowledge_records_embedding 
    ON knowledge_records USING hnsw (embedding vector_cosine_ops);

-- Add embedding column to topics
ALTER TABLE topics ADD COLUMN IF NOT EXISTS embedding vector(1536);

-- Create vector similarity index for topics
CREATE INDEX IF NOT EXISTS idx_topics_embedding 
    ON topics USING hnsw (embedding vector_cosine_ops);

-- Add embedding and topic link to feedback_records
ALTER TABLE feedback_records ADD COLUMN IF NOT EXISTS embedding vector(1536);
ALTER TABLE feedback_records ADD COLUMN IF NOT EXISTS topic_id UUID REFERENCES topics(id) ON DELETE SET NULL;
ALTER TABLE feedback_records ADD COLUMN IF NOT EXISTS classification_confidence DOUBLE PRECISION;
ALTER TABLE feedback_records ADD COLUMN IF NOT EXISTS sentiment VARCHAR(50);
ALTER TABLE feedback_records ADD COLUMN IF NOT EXISTS sentiment_score DOUBLE PRECISION;
ALTER TABLE feedback_records ADD COLUMN IF NOT EXISTS emotion VARCHAR(50);

-- Create vector similarity index for feedback_records
CREATE INDEX IF NOT EXISTS idx_feedback_records_embedding 
    ON feedback_records USING hnsw (embedding vector_cosine_ops);

-- Create index for topic lookups on feedback_records
CREATE INDEX IF NOT EXISTS idx_feedback_records_topic_id ON feedback_records(topic_id);

-- Create indexes for sentiment filtering
CREATE INDEX IF NOT EXISTS idx_feedback_records_sentiment ON feedback_records(sentiment);
CREATE INDEX IF NOT EXISTS idx_feedback_records_emotion ON feedback_records(emotion);
