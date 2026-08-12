-- ANI Platform · Core-managed KB tables migration (issue #027)
-- Version: 001 (20260811)
-- Description: Fold kb-service private migrations (001_pg_trgm_extension,
--   002_kb_chunks, 003_kb_retrieval_mode) into Core-managed migrations so the
--   7 KB business tables' schema is owned by Core (SPEC §3.2, §4.4).
--
-- Scope (7 tables, all under Core control after this migration):
--   knowledge_bases, kb_documents, kb_chunks, kb_messages, kb_sessions,
--   async_tasks, outbox_events.
--   Of these, 6 were already created by 20260501_001_init_schema.sql (and
--   later amended for async_tasks). This migration adds the missing pieces:
--     1. pg_trgm extension (required for keyword retrieval on kb_chunks.content)
--     2. kb_chunks table + indexes + RLS (formerly kb-service 002_kb_chunks.sql)
--     3. knowledge_bases.retrieval_mode column (formerly 003_kb_retrieval_mode.sql)
--
-- Idempotency: all statements use CREATE ... IF NOT EXISTS / ADD COLUMN IF NOT
--   EXISTS so the migration is safe to re-run (SPEC §3.4, issue #027 AC-5).
--
-- Run with: psql $DATABASE_URL -f 20260811_001_kb_tables_managed.sql
-- Role: requires superuser/owner for CREATE EXTENSION; the rest runs as
--   ani_migrator. In managed Postgres (e.g. CloudNativePG) pg_trgm is part of
--   contrib and available by default.
--
-- Note on /data/tables: CREATE EXTENSION is a superuser operation and is
-- blocked by the data-plane handler's destructive-statement filter. The
-- pg_trgm extension therefore lives here in the Core deploy migration
-- (executed by the migrator role), not via POST /data/tables. The kb_chunks
-- table DDL is also placed here as the canonical Core-owned definition; the
-- data-plane /data/tables endpoint remains available for subsequent managed
-- alterations of these 7 tables (SPEC §3.2).

-- ===========================================================================
-- 1. pg_trgm EXTENSION (formerly kb-service 001_pg_trgm_extension.sql)
-- ===========================================================================
-- US-005: Enable pg_trgm for keyword (trigram) retrieval on kb_chunks.content.
-- Extension is created in the database default schema; the GIN index on
-- kb_chunks.content is created in section 2 below after the table exists.
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- ===========================================================================
-- 2. kb_chunks TABLE (formerly kb-service 002_kb_chunks.sql)
-- ===========================================================================
-- US-005: parent/child chunk storage and keyword retrieval.
-- Schema aligns with plan.md §3.1 and SPEC §3.1 (spec-services-kb-service).
--
-- Columns:
--   parent_chunk_id : self-reference for parent/child/doc_summary hierarchy
--   chunk_type      : 'child' | 'parent' | 'doc_summary'
--   parent_content  : denormalized parent text for child chunks (nullable)
--   custom_metadata : JSONB, inherits/overrides doc-level metadata (FR-14)
--
-- Foreign keys (align with kb_documents / kb_sessions convention in
-- 20260501_001_init_schema.sql):
--   kb_id           -> knowledge_bases(id) ON DELETE CASCADE
--   doc_id          -> kb_documents(id) ON DELETE CASCADE
--   parent_chunk_id -> kb_chunks(id) ON DELETE CASCADE (self-reference)
-- tenant_id intentionally has no FK (same as kb_documents / kb_sessions;
-- tenant isolation is enforced via RLS, not FK).
--
-- The kb_service layer writes kb_chunks (rag-engine parse_worker via
-- kb-service); kb-service reads kb_chunks for pg_trgm keyword retrieval
-- (FR-7 mixed retrieval).
CREATE TABLE IF NOT EXISTS kb_chunks (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       UUID NOT NULL,
  kb_id           UUID NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
  doc_id          UUID NOT NULL REFERENCES kb_documents(id) ON DELETE CASCADE,
  parent_chunk_id UUID REFERENCES kb_chunks(id) ON DELETE CASCADE,
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

-- Idempotent foreign-key constraints for brownfield upgrades where the
-- table already exists (created by the former kb-service 002_kb_chunks.sql
-- without FKs). CREATE TABLE IF NOT EXISTS does not alter an existing table,
-- so we add the constraints via DO $$ ... EXCEPTION blocks. PostgreSQL does
-- not support ADD CONSTRAINT IF NOT EXISTS; the exception handler makes it
-- safe to re-run. rag-engine's write_chunks inserts parents before children
-- (lines 116-134 in chunks.py), so the self-FK on parent_chunk_id is
-- satisfied by the existing insert ordering.
DO $$ BEGIN
  ALTER TABLE kb_chunks
    ADD CONSTRAINT kb_chunks_kb_id_fkey
    FOREIGN KEY (kb_id) REFERENCES knowledge_bases(id) ON DELETE CASCADE;
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
  ALTER TABLE kb_chunks
    ADD CONSTRAINT kb_chunks_doc_id_fkey
    FOREIGN KEY (doc_id) REFERENCES kb_documents(id) ON DELETE CASCADE;
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
  ALTER TABLE kb_chunks
    ADD CONSTRAINT kb_chunks_parent_chunk_id_fkey
    FOREIGN KEY (parent_chunk_id) REFERENCES kb_chunks(id) ON DELETE CASCADE;
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

-- B-tree indexes for point lookups and filtering.
CREATE INDEX IF NOT EXISTS idx_kb_chunks_kb_doc   ON kb_chunks(kb_id, doc_id);
CREATE INDEX IF NOT EXISTS idx_kb_chunks_parent   ON kb_chunks(parent_chunk_id);
CREATE INDEX IF NOT EXISTS idx_kb_chunks_type     ON kb_chunks(chunk_type);

-- GIN trigram index for keyword (LIKE/ILIKE/%query%) retrieval on content.
-- Requires pg_trgm extension (see section 1 above).
CREATE INDEX IF NOT EXISTS idx_kb_chunks_content_trgm ON kb_chunks USING GIN (content gin_trgm_ops);

-- Grant app role access (convention: all business tables follow
-- 20260501_001_init_schema.sql).
GRANT SELECT, INSERT, UPDATE, DELETE ON kb_chunks TO ani_app;

-- Row Level Security: tenant isolation (SPEC §8.1, FR-15).
-- Matches the pattern used by kb_documents / kb_messages / async_tasks /
-- outbox_events in 20260501_001_init_schema.sql. The app role (ani_app_user)
-- is non-superuser, non-bypassrls, so RLS is enforced.
ALTER TABLE kb_chunks ENABLE ROW LEVEL SECURITY;
ALTER TABLE kb_chunks FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON kb_chunks;
CREATE POLICY tenant_isolation ON kb_chunks
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

-- ===========================================================================
-- 3. knowledge_bases.retrieval_mode (formerly kb-service 003_kb_retrieval_mode.sql)
-- ===========================================================================
-- Specifies the retrieval method for a KB:
--   vector   vector retrieval (Milvus cosine)
--   hybrid   mixed retrieval (vector + full-text pg_trgm + RRF, default)
--   keyword  full-text retrieval (pg_trgm)
-- Idempotent: skips if the column already exists.
ALTER TABLE knowledge_bases
    ADD COLUMN IF NOT EXISTS retrieval_mode TEXT NOT NULL DEFAULT 'hybrid'
        CHECK (retrieval_mode IN ('vector', 'hybrid', 'keyword'));
