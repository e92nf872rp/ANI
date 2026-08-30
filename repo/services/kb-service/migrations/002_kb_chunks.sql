-- US-005: kb_chunks table for parent/child chunk storage and keyword retrieval.
-- Schema aligns with plan.md §3.1 and SPEC §3.1 (spec-services-kb-service).
-- Idempotent: all statements use CREATE ... IF NOT EXISTS (SPEC §3.4).
-- Rollback (P0 only, no production data): DROP TABLE kb_chunks; DROP EXTENSION pg_trgm;
--
-- Columns:
--   parent_chunk_id : self-reference for parent/child/doc_summary chunk hierarchy
--   chunk_type      : 'child' | 'parent' | 'doc_summary'
--   parent_content  : denormalized parent text for child chunks (nullable)
--   custom_metadata : JSONB, inherits/overrides doc-level metadata (FR-14)
--
-- The kb_service layer writes kb_chunks (rag-engine parse_worker via kb-service);
-- kb-service reads kb_chunks for pg_trgm keyword retrieval (FR-7 mixed retrieval).
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

-- B-tree indexes for point lookups and filtering.
CREATE INDEX IF NOT EXISTS idx_kb_chunks_kb_doc   ON kb_chunks(kb_id, doc_id);
CREATE INDEX IF NOT EXISTS idx_kb_chunks_parent   ON kb_chunks(parent_chunk_id);
CREATE INDEX IF NOT EXISTS idx_kb_chunks_type     ON kb_chunks(chunk_type);

-- GIN trigram index for keyword (LIKE/ILIKE/%query%) retrieval on content.
-- Requires pg_trgm extension (see 001_pg_trgm_extension.sql).
CREATE INDEX IF NOT EXISTS idx_kb_chunks_content_trgm ON kb_chunks USING GIN (content gin_trgm_ops);

-- Grant app role access (convention: all business tables follow 20260501000100_init_schema.sql).
GRANT SELECT, INSERT, UPDATE, DELETE ON kb_chunks TO ani_app;

-- Row Level Security: tenant isolation (SPEC §8.1, FR-15).
-- Matches the pattern used by kb_documents / kb_messages / async_tasks / outbox_events
-- in 20260501000100_init_schema.sql and later migrations (e.g. 20260520000500).
-- The app role (ani_app_user) is non-superuser, non-bypassrls, so RLS is enforced.
ALTER TABLE kb_chunks ENABLE ROW LEVEL SECURITY;
ALTER TABLE kb_chunks FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON kb_chunks;
CREATE POLICY tenant_isolation ON kb_chunks
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);
