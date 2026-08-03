-- US-005: Enable pg_trgm extension for keyword (trigram) retrieval on kb_chunks.content.
-- Idempotent: safe to re-run (SPEC §3.4). Extension is created in the database default
-- schema; the GIN index on kb_chunks.content is created in 002_kb_chunks.sql after the
-- table exists. Extension creation requires superuser/owner privileges; in managed
-- Postgres (e.g. CloudNativePG) pg_trgm is part of contrib and available by default.
CREATE EXTENSION IF NOT EXISTS pg_trgm;
