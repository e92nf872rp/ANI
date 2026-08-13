"""Apply kb_chunks table + pg_trgm extension."""
import asyncio
import asyncpg

DSN = "postgres://ani:ani_dev_password@10.10.1.66:30945/ani?sslmode=disable"

SQL = """
-- pg_trgm extension (required for GIN trigram index)
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- kb_chunks table
CREATE TABLE IF NOT EXISTS kb_chunks (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       UUID NOT NULL,
  kb_id           UUID NOT NULL,
  doc_id          UUID NOT NULL,
  parent_chunk_id UUID,
  chunk_type       TEXT NOT NULL CHECK (chunk_type IN ('child','parent','doc_summary')),
  content         TEXT NOT NULL,
  parent_content  TEXT,
  page_number     INT,
  content_type    TEXT,
  file_name       TEXT NOT NULL,
  token_count     INT,
  custom_metadata JSONB DEFAULT '{}'::jsonb,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_kb_chunks_kb_doc   ON kb_chunks(kb_id, doc_id);
CREATE INDEX IF NOT EXISTS idx_kb_chunks_parent   ON kb_chunks(parent_chunk_id);
CREATE INDEX IF NOT EXISTS idx_kb_chunks_type     ON kb_chunks(chunk_type);
CREATE INDEX IF NOT EXISTS idx_kb_chunks_content_trgm ON kb_chunks USING GIN (content gin_trgm_ops);

-- RLS
ALTER TABLE kb_chunks ENABLE ROW LEVEL SECURITY;
ALTER TABLE kb_chunks FORCE ROW LEVEL SECURITY;
DO $$ BEGIN
    CREATE POLICY tenant_isolation ON kb_chunks
        AS RESTRICTIVE
        USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
"""

async def main():
    conn = await asyncpg.connect(dsn=DSN)
    try:
        await conn.execute(SQL)
        print("kb_chunks table + pg_trgm extension applied successfully!")
    finally:
        await conn.close()

if __name__ == "__main__":
    asyncio.run(main())
