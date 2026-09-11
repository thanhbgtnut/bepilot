-- +goose Up
-- The vector dimension here must match config `embedding.dim` (default 1536).
-- If you change the embedding model/dimension, adjust this column type too.
CREATE EXTENSION IF NOT EXISTS "vector";

CREATE TABLE skills (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug          text NOT NULL UNIQUE,
    name          text NOT NULL,
    description   text NOT NULL DEFAULT '',
    path          text NOT NULL DEFAULT '',
    body          text NOT NULL DEFAULT '',
    checksum      text NOT NULL DEFAULT '',
    allowed_tools text[] NOT NULL DEFAULT '{}',
    enabled       boolean NOT NULL DEFAULT true,
    embedding     vector(1536),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

-- Cosine-distance index for skill retrieval. ivfflat needs data before it is
-- useful; for small skill catalogs a sequential scan is fine and this still
-- works. lists is deliberately small.
CREATE INDEX skills_embedding_idx ON skills
    USING ivfflat (embedding vector_cosine_ops) WITH (lists = 10);

-- +goose Down
DROP TABLE IF EXISTS skills;
