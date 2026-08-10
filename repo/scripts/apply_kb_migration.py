"""Apply KB-related migration tables to the database.

Creates: knowledge_bases, kb_documents, kb_sessions, kb_messages, async_tasks, outbox_events
with RLS policies. Only creates tables that don't already exist.
"""
import asyncio
import asyncpg

DSN = "postgres://ani:ani_dev_password@10.10.1.66:30945/ani?sslmode=disable"

MIGRATION_SQL = """
-- SECTION 6: KNOWLEDGE_BASES, KB_DOCUMENTS, KB_SESSIONS, KB_MESSAGES

CREATE TABLE IF NOT EXISTS knowledge_bases (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    description     TEXT,
    embedding_model TEXT NOT NULL DEFAULT 'bge-m3',
    chunk_size      INT NOT NULL DEFAULT 512,
    top_k           INT NOT NULL DEFAULT 5,
    score_threshold FLOAT NOT NULL DEFAULT 0.3,
    retrieval_mode  TEXT NOT NULL DEFAULT 'hybrid'
        CHECK (retrieval_mode IN ('vector', 'hybrid', 'keyword')),
    status          TEXT NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'rebuilding', 'deleted')),
    doc_count       INT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, name)
);
CREATE INDEX IF NOT EXISTS idx_kb_tenant_id ON knowledge_bases(tenant_id);

CREATE TABLE IF NOT EXISTS kb_documents (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    kb_id           UUID NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
    tenant_id       UUID NOT NULL,
    file_name       TEXT NOT NULL,
    file_type       TEXT NOT NULL,
    file_size_bytes BIGINT NOT NULL,
    storage_path    TEXT NOT NULL,
    checksum_sha256 TEXT NOT NULL,
    parse_status    TEXT NOT NULL DEFAULT 'pending'
        CHECK (parse_status IN ('pending', 'parsing', 'indexing', 'ready', 'failed')),
    chunk_count     INT NOT NULL DEFAULT 0,
    error_message   TEXT,
    parsed_at       TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_kb_docs_kb_id ON kb_documents(kb_id);
CREATE INDEX IF NOT EXISTS idx_kb_docs_parse_status ON kb_documents(kb_id, parse_status);

CREATE TABLE IF NOT EXISTS kb_sessions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    kb_id       UUID NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
    tenant_id   UUID NOT NULL,
    user_id     UUID,
    title       TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS kb_messages (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id      UUID NOT NULL REFERENCES kb_sessions(id) ON DELETE CASCADE,
    tenant_id       UUID NOT NULL,
    role            TEXT NOT NULL CHECK (role IN ('user', 'assistant')),
    content         TEXT NOT NULL,
    source_chunks   JSONB,
    input_tokens    INT,
    output_tokens   INT,
    duration_ms     INT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_kb_messages_session ON kb_messages(session_id, created_at);
CREATE INDEX IF NOT EXISTS idx_kb_messages_tenant ON kb_messages(tenant_id, created_at);

-- SECTION 7: ASYNC_TASKS, OUTBOX_EVENTS

CREATE TABLE IF NOT EXISTS async_tasks (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           UUID NOT NULL,
    idempotency_key     TEXT NOT NULL,
    task_type           TEXT NOT NULL,
    resource_type       TEXT,
    resource_id         UUID,
    status              TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending','running','completed','failed','cancelled','dead_letter')),
    attempt_count       INT NOT NULL DEFAULT 0,
    max_attempts        INT NOT NULL DEFAULT 3,
    lease_owner         TEXT,
    lease_until         TIMESTAMPTZ,
    last_heartbeat_at   TIMESTAMPTZ,
    progress_pct        INT NOT NULL DEFAULT 0 CHECK (progress_pct BETWEEN 0 AND 100),
    payload             JSONB NOT NULL DEFAULT '{}',
    result              JSONB,
    error_message       TEXT,
    compensating_action TEXT,
    dead_letter_at      TIMESTAMPTZ,
    webhook_url         TEXT,
    started_at          TIMESTAMPTZ,
    completed_at        TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, idempotency_key)
);
CREATE INDEX IF NOT EXISTS idx_async_tasks_tenant ON async_tasks(tenant_id, status);
CREATE INDEX IF NOT EXISTS idx_async_tasks_type ON async_tasks(task_type, status);
CREATE INDEX IF NOT EXISTS idx_async_tasks_lease_owner ON async_tasks(tenant_id, lease_owner) WHERE status = 'running';
CREATE INDEX IF NOT EXISTS idx_async_tasks_lease ON async_tasks(lease_until) WHERE status = 'running';
CREATE INDEX IF NOT EXISTS idx_async_tasks_dead_letter ON async_tasks(dead_letter_at) WHERE status = 'dead_letter';

CREATE TABLE IF NOT EXISTS outbox_events (
    id              BIGSERIAL PRIMARY KEY,
    aggregate_type  TEXT NOT NULL,
    aggregate_id    UUID NOT NULL,
    event_type      TEXT NOT NULL,
    tenant_id       UUID NOT NULL,
    payload         JSONB NOT NULL DEFAULT '{}',
    published       BOOLEAN NOT NULL DEFAULT FALSE,
    published_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_outbox_unpublished ON outbox_events(created_at) WHERE NOT published;

-- SECTION 11: ROW LEVEL SECURITY for KB tables

ALTER TABLE knowledge_bases ENABLE ROW LEVEL SECURITY;
ALTER TABLE knowledge_bases FORCE ROW LEVEL SECURITY;
DO $$ BEGIN
    CREATE POLICY tenant_isolation ON knowledge_bases
        AS RESTRICTIVE
        USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

ALTER TABLE kb_documents ENABLE ROW LEVEL SECURITY;
ALTER TABLE kb_documents FORCE ROW LEVEL SECURITY;
DO $$ BEGIN
    CREATE POLICY tenant_isolation ON kb_documents
        AS RESTRICTIVE
        USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

ALTER TABLE kb_sessions ENABLE ROW LEVEL SECURITY;
ALTER TABLE kb_sessions FORCE ROW LEVEL SECURITY;
DO $$ BEGIN
    CREATE POLICY tenant_isolation ON kb_sessions
        AS RESTRICTIVE
        USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

ALTER TABLE kb_messages ENABLE ROW LEVEL SECURITY;
ALTER TABLE kb_messages FORCE ROW LEVEL SECURITY;
DO $$ BEGIN
    CREATE POLICY tenant_isolation ON kb_messages
        AS RESTRICTIVE
        USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

ALTER TABLE async_tasks ENABLE ROW LEVEL SECURITY;
ALTER TABLE async_tasks FORCE ROW LEVEL SECURITY;
DO $$ BEGIN
    CREATE POLICY tenant_isolation ON async_tasks
        AS RESTRICTIVE
        USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

ALTER TABLE outbox_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE outbox_events FORCE ROW LEVEL SECURITY;
DO $$ BEGIN
    CREATE POLICY tenant_isolation ON outbox_events
        AS RESTRICTIVE
        USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
"""

async def main():
    conn = await asyncpg.connect(dsn=DSN)
    try:
        await conn.execute(MIGRATION_SQL)
        print("Migration applied successfully!")

        # Verify tables exist
        rows = await conn.fetch("""
            SELECT table_name FROM information_schema.tables
             WHERE table_schema = 'public'
               AND table_name IN ('knowledge_bases', 'kb_documents', 'kb_sessions',
                                  'kb_messages', 'async_tasks', 'outbox_events')
             ORDER BY table_name
        """)
        print("\n=== KB tables now present ===")
        for r in rows:
            print(f"  {r['table_name']}")

    finally:
        await conn.close()

if __name__ == "__main__":
    asyncio.run(main())
