-- Fallback search indexes for environments where pg_bigm is not available.
-- The search queries use LOWER(column) LIKE '%term%'; pg_trgm can accelerate
-- those predicates in local/self-hosted Postgres images that do not ship pg_bigm.
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_bigm') THEN
    CREATE EXTENSION IF NOT EXISTS pg_trgm;

    CREATE INDEX IF NOT EXISTS idx_issue_title_trgm
      ON issue USING gin (LOWER(title) gin_trgm_ops);
    CREATE INDEX IF NOT EXISTS idx_issue_description_trgm
      ON issue USING gin (LOWER(COALESCE(description, '')) gin_trgm_ops);
    CREATE INDEX IF NOT EXISTS idx_comment_content_trgm
      ON comment USING gin (LOWER(content) gin_trgm_ops);
    CREATE INDEX IF NOT EXISTS idx_project_title_trgm
      ON project USING gin (LOWER(title) gin_trgm_ops);
    CREATE INDEX IF NOT EXISTS idx_project_description_trgm
      ON project USING gin (LOWER(COALESCE(description, '')) gin_trgm_ops);
  END IF;
EXCEPTION WHEN OTHERS THEN
  RAISE NOTICE 'pg_trgm fallback search indexes skipped: %', SQLERRM;
END
$$;
