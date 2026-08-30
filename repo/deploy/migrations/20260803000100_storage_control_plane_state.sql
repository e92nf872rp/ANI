-- ANI Platform · Migration
-- Description: STORAGE-CONTROL-PLANE-STATE-A storage/vector control-plane schema
-- Depends on: 20260520000600_storage_resources.sql, 20260520000500_network_resources.sql
-- Notes:
--   - PostgreSQL is control-plane authority; MinIO/Milvus remain content authority.
--   - Do not persist object body, signed download URLs, vector payloads, or search results.
--   - Brownfield-safe: existing storage_buckets / vector_stores are altered in place
--     (bucket_id TEXT, store_id -> vector_store_id, idempotency_key backfill).


-- ── Extend existing volume / filesystem / object tables ──────────────────────

ALTER TABLE storage_volumes
    ADD COLUMN IF NOT EXISTS zone TEXT,
    ADD COLUMN IF NOT EXISTS volume_type TEXT
        CHECK (volume_type IS NULL OR volume_type IN ('ssd', 'hdd', 'high_performance_ssd')),
    ADD COLUMN IF NOT EXISTS iops INTEGER CHECK (iops IS NULL OR iops >= 0),
    ADD COLUMN IF NOT EXISTS encrypted BOOLEAN,
    ADD COLUMN IF NOT EXISTS mount_instance_id TEXT,
    ADD COLUMN IF NOT EXISTS mount_route TEXT,
    ADD COLUMN IF NOT EXISTS mount_name TEXT,
    ADD COLUMN IF NOT EXISTS os_init_status TEXT
        CHECK (os_init_status IS NULL OR os_init_status IN ('pending', 'done', 'skipped', 'n_a')),
    ADD COLUMN IF NOT EXISTS os_init_device TEXT,
    ADD COLUMN IF NOT EXISTS from_snapshot_id TEXT,
    ADD COLUMN IF NOT EXISTS from_snapshot_name TEXT,
    ADD COLUMN IF NOT EXISTS provider TEXT,
    ADD COLUMN IF NOT EXISTS provider_refs JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS last_observed_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS create_idempotency_key TEXT,
    ADD COLUMN IF NOT EXISTS create_request_fingerprint TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS idx_storage_volumes_tenant_create_idempotency
    ON storage_volumes (tenant_id, create_idempotency_key)
    WHERE create_idempotency_key IS NOT NULL;

ALTER TABLE storage_filesystems
    ADD COLUMN IF NOT EXISTS zone TEXT,
    ADD COLUMN IF NOT EXISTS performance_mode TEXT
        CHECK (performance_mode IS NULL OR performance_mode IN ('standard', 'throughput')),
    ADD COLUMN IF NOT EXISTS mount_command TEXT,
    ADD COLUMN IF NOT EXISTS provider TEXT,
    ADD COLUMN IF NOT EXISTS provider_refs JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS last_observed_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS create_idempotency_key TEXT,
    ADD COLUMN IF NOT EXISTS create_request_fingerprint TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS idx_storage_filesystems_tenant_create_idempotency
    ON storage_filesystems (tenant_id, create_idempotency_key)
    WHERE create_idempotency_key IS NOT NULL;

ALTER TABLE storage_objects
    ADD COLUMN IF NOT EXISTS bucket_id TEXT,
    ADD COLUMN IF NOT EXISTS storage_class TEXT
        CHECK (storage_class IS NULL OR storage_class IN ('standard', 'infrequent_access')),
    ADD COLUMN IF NOT EXISTS provider TEXT,
    ADD COLUMN IF NOT EXISTS provider_refs JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS last_observed_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS create_idempotency_key TEXT,
    ADD COLUMN IF NOT EXISTS create_request_fingerprint TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS idx_storage_objects_tenant_create_idempotency
    ON storage_objects (tenant_id, create_idempotency_key)
    WHERE create_idempotency_key IS NOT NULL;

-- ── Volume children ──────────────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS storage_volume_auto_snapshot_policies (
    tenant_id       UUID        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    volume_id       TEXT        NOT NULL,
    enabled         BOOLEAN     NOT NULL DEFAULT FALSE,
    retain_days     INTEGER     CHECK (retain_days IS NULL OR retain_days > 0),
    schedule        TEXT,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at      TIMESTAMPTZ,
    PRIMARY KEY (tenant_id, volume_id),
    FOREIGN KEY (tenant_id, volume_id)
        REFERENCES storage_volumes(tenant_id, volume_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS storage_volume_mount_events (
    tenant_id       UUID        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    event_id        TEXT        NOT NULL,
    volume_id       TEXT        NOT NULL,
    action          TEXT        NOT NULL,
    target          TEXT,
    result          TEXT,
    occurred_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, event_id),
    FOREIGN KEY (tenant_id, volume_id)
        REFERENCES storage_volumes(tenant_id, volume_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_storage_volume_mount_events_volume
    ON storage_volume_mount_events (tenant_id, volume_id, occurred_at DESC);

CREATE TABLE IF NOT EXISTS storage_volume_snapshots (
    tenant_id                   UUID        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    snapshot_id                 TEXT        NOT NULL,
    volume_id                   TEXT        NOT NULL,
    name                        TEXT        NOT NULL,
    description                 TEXT,
    status                      TEXT        NOT NULL
        CHECK (status IN ('pending', 'available', 'failed', 'deleting', 'deleted')),
    size_bytes                  BIGINT      CHECK (size_bytes IS NULL OR size_bytes >= 0),
    provider                    TEXT,
    provider_refs               JSONB       NOT NULL DEFAULT '[]'::jsonb,
    last_observed_at            TIMESTAMPTZ,
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at                  TIMESTAMPTZ,
    create_idempotency_key      TEXT,
    create_request_fingerprint  TEXT,
    PRIMARY KEY (tenant_id, snapshot_id),
    FOREIGN KEY (tenant_id, volume_id)
        REFERENCES storage_volumes(tenant_id, volume_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_storage_volume_snapshots_tenant_state
    ON storage_volume_snapshots (tenant_id, status, updated_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_storage_volume_snapshots_tenant_create_idempotency
    ON storage_volume_snapshots (tenant_id, create_idempotency_key)
    WHERE create_idempotency_key IS NOT NULL;

-- ── Filesystem children ──────────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS storage_filesystem_mount_targets (
    tenant_id                   UUID        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    mount_target_id             TEXT        NOT NULL,
    filesystem_id               TEXT        NOT NULL,
    subnet_id                   TEXT        NOT NULL,
    vpc_id                      TEXT,
    ip_address                  INET,
    status                      TEXT        NOT NULL
        CHECK (status IN ('pending', 'available', 'failed', 'deleting', 'deleted')),
    provider                    TEXT,
    provider_refs               JSONB       NOT NULL DEFAULT '[]'::jsonb,
    last_observed_at            TIMESTAMPTZ,
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at                  TIMESTAMPTZ,
    create_idempotency_key      TEXT,
    create_request_fingerprint  TEXT,
    PRIMARY KEY (tenant_id, mount_target_id),
    FOREIGN KEY (tenant_id, filesystem_id)
        REFERENCES storage_filesystems(tenant_id, filesystem_id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, subnet_id)
        REFERENCES network_subnets(tenant_id, subnet_id)
);
CREATE INDEX IF NOT EXISTS idx_storage_filesystem_mount_targets_fs
    ON storage_filesystem_mount_targets (tenant_id, filesystem_id, status, updated_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_storage_filesystem_mount_targets_create_idempotency
    ON storage_filesystem_mount_targets (tenant_id, create_idempotency_key)
    WHERE create_idempotency_key IS NOT NULL;

CREATE TABLE IF NOT EXISTS storage_filesystem_attachments (
    tenant_id                   UUID        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    attachment_id               TEXT        NOT NULL,
    filesystem_id               TEXT        NOT NULL,
    instance_id                 TEXT        NOT NULL,
    instance_name               TEXT,
    instance_route              TEXT,
    mount_path                  TEXT        NOT NULL,
    ip_address                  INET,
    protocol                    TEXT,
    auto_mount                  BOOLEAN     NOT NULL DEFAULT FALSE,
    attached_at                 TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    detached_at                 TIMESTAMPTZ,
    create_idempotency_key      TEXT,
    create_request_fingerprint  TEXT,
    PRIMARY KEY (tenant_id, attachment_id),
    FOREIGN KEY (tenant_id, filesystem_id)
        REFERENCES storage_filesystems(tenant_id, filesystem_id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, instance_id)
        REFERENCES workload_instances(tenant_id, instance_id)
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_storage_filesystem_attachments_active
    ON storage_filesystem_attachments (tenant_id, filesystem_id, instance_id, mount_path)
    WHERE detached_at IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_storage_filesystem_attachments_create_idempotency
    ON storage_filesystem_attachments (tenant_id, create_idempotency_key)
    WHERE create_idempotency_key IS NOT NULL;

-- ── Object buckets (brownfield ALTER; greenfield CREATE) ─────────────────────

CREATE TABLE IF NOT EXISTS storage_buckets (
    tenant_id                   UUID        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    bucket_id                   TEXT        NOT NULL,
    name                        TEXT        NOT NULL,
    region                      TEXT,
    access_mode                 TEXT        NOT NULL DEFAULT 'private'
        CHECK (access_mode IN ('private', 'public_read')),
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, bucket_id)
);

ALTER TABLE storage_buckets
    ADD COLUMN IF NOT EXISTS region TEXT,
    ADD COLUMN IF NOT EXISTS endpoint TEXT,
    ADD COLUMN IF NOT EXISTS access_mode TEXT,
    ADD COLUMN IF NOT EXISTS acl TEXT,
    ADD COLUMN IF NOT EXISTS acl_label TEXT,
    ADD COLUMN IF NOT EXISTS storage_class TEXT,
    ADD COLUMN IF NOT EXISTS versioning TEXT,
    ADD COLUMN IF NOT EXISTS object_count BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS size_bytes BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS lifecycle_note TEXT,
    ADD COLUMN IF NOT EXISTS state TEXT,
    ADD COLUMN IF NOT EXISTS reason TEXT,
    ADD COLUMN IF NOT EXISTS provider TEXT,
    ADD COLUMN IF NOT EXISTS provider_refs JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS last_observed_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS create_idempotency_key TEXT,
    ADD COLUMN IF NOT EXISTS create_request_fingerprint TEXT;

UPDATE storage_buckets
SET state = COALESCE(state, 'available'),
    access_mode = COALESCE(access_mode, 'private')
WHERE state IS NULL OR access_mode IS NULL;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'public'
          AND table_name = 'storage_buckets'
          AND column_name = 'idempotency_key'
    ) THEN
        UPDATE storage_buckets
        SET create_idempotency_key = COALESCE(
            create_idempotency_key,
            NULLIF(idempotency_key, '')
        )
        WHERE create_idempotency_key IS NULL;
    END IF;
END $$;

ALTER TABLE storage_buckets
    ALTER COLUMN state SET DEFAULT 'available',
    ALTER COLUMN state SET NOT NULL,
    ALTER COLUMN access_mode SET DEFAULT 'private',
    ALTER COLUMN access_mode SET NOT NULL;

DO $$
BEGIN
    ALTER TABLE storage_buckets
        ADD CONSTRAINT storage_buckets_state_check
        CHECK (state IN ('pending', 'available', 'failed', 'deleting', 'deleted'));
EXCEPTION
    WHEN duplicate_object THEN NULL;
END $$;

DO $$
BEGIN
    ALTER TABLE storage_buckets
        ADD CONSTRAINT storage_buckets_access_mode_check
        CHECK (access_mode IN ('private', 'public_read'));
EXCEPTION
    WHEN duplicate_object THEN NULL;
END $$;

DO $$
BEGIN
    ALTER TABLE storage_buckets
        ADD CONSTRAINT storage_buckets_acl_check
        CHECK (acl IS NULL OR acl IN ('private', 'tenant_read'));
EXCEPTION
    WHEN duplicate_object THEN NULL;
END $$;

DO $$
BEGIN
    ALTER TABLE storage_buckets
        ADD CONSTRAINT storage_buckets_storage_class_check
        CHECK (storage_class IS NULL OR storage_class IN ('standard', 'infrequent_access'));
EXCEPTION
    WHEN duplicate_object THEN NULL;
END $$;

DO $$
BEGIN
    ALTER TABLE storage_buckets
        ADD CONSTRAINT storage_buckets_versioning_check
        CHECK (versioning IS NULL OR versioning IN ('disabled', 'enabled'));
EXCEPTION
    WHEN duplicate_object THEN NULL;
END $$;

DO $$
BEGIN
    ALTER TABLE storage_buckets
        ADD CONSTRAINT storage_buckets_object_count_check
        CHECK (object_count >= 0);
EXCEPTION
    WHEN duplicate_object THEN NULL;
END $$;

DO $$
BEGIN
    ALTER TABLE storage_buckets
        ADD CONSTRAINT storage_buckets_size_bytes_check
        CHECK (size_bytes >= 0);
EXCEPTION
    WHEN duplicate_object THEN NULL;
END $$;

ALTER TABLE storage_buckets
    DROP CONSTRAINT IF EXISTS storage_buckets_tenant_id_name_key;

CREATE UNIQUE INDEX IF NOT EXISTS idx_storage_buckets_tenant_name_active
    ON storage_buckets (tenant_id, name)
    WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_storage_buckets_tenant_state
    ON storage_buckets (tenant_id, state, updated_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_storage_buckets_tenant_create_idempotency
    ON storage_buckets (tenant_id, create_idempotency_key)
    WHERE create_idempotency_key IS NOT NULL;

CREATE TABLE IF NOT EXISTS storage_bucket_lifecycle_rules (
    tenant_id                   UUID        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    rule_id                     TEXT        NOT NULL,
    bucket_id                   TEXT        NOT NULL,
    name                        TEXT        NOT NULL,
    prefix                      TEXT,
    expire_days                 INTEGER     CHECK (expire_days IS NULL OR expire_days > 0),
    to_infrequent_days          INTEGER     CHECK (to_infrequent_days IS NULL OR to_infrequent_days > 0),
    enabled                     BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at                  TIMESTAMPTZ,
    create_idempotency_key      TEXT,
    create_request_fingerprint  TEXT,
    PRIMARY KEY (tenant_id, rule_id),
    FOREIGN KEY (tenant_id, bucket_id)
        REFERENCES storage_buckets(tenant_id, bucket_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_storage_bucket_lifecycle_rules_bucket
    ON storage_bucket_lifecycle_rules (tenant_id, bucket_id)
    WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_storage_bucket_lifecycle_rules_create_idempotency
    ON storage_bucket_lifecycle_rules (tenant_id, create_idempotency_key)
    WHERE create_idempotency_key IS NOT NULL;

-- Optional additive FK for Core object metadata -> bucket identity.
-- Keep legacy bucket name column; do not fail old rows that only have names.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'storage_objects_bucket_fk'
    ) THEN
        ALTER TABLE storage_objects
            ADD CONSTRAINT storage_objects_bucket_fk
            FOREIGN KEY (tenant_id, bucket_id)
            REFERENCES storage_buckets(tenant_id, bucket_id)
            NOT VALID;
    END IF;
END $$;

-- ── Vector stores (brownfield ALTER; rename store_id -> vector_store_id) ─────

CREATE TABLE IF NOT EXISTS vector_stores (
    tenant_id                   UUID        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    vector_store_id             TEXT        NOT NULL,
    name                        TEXT        NOT NULL,
    dimension                   INTEGER     NOT NULL CHECK (dimension > 0),
    metric                      TEXT        NOT NULL DEFAULT 'cosine'
        CHECK (metric IN ('cosine', 'l2', 'ip')),
    state                       TEXT        NOT NULL DEFAULT 'pending'
        CHECK (state IN ('pending', 'ready', 'failed', 'deleting', 'deleted')),
    reason                      TEXT,
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, vector_store_id)
);

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'public'
          AND table_name = 'vector_stores'
          AND column_name = 'store_id'
    ) AND NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'public'
          AND table_name = 'vector_stores'
          AND column_name = 'vector_store_id'
    ) THEN
        ALTER TABLE vector_stores RENAME COLUMN store_id TO vector_store_id;
    END IF;
END $$;

ALTER TABLE vector_stores
    ADD COLUMN IF NOT EXISTS embedding_model TEXT,
    ADD COLUMN IF NOT EXISTS vector_count BIGINT,
    ADD COLUMN IF NOT EXISTS index_status TEXT,
    ADD COLUMN IF NOT EXISTS last_indexed_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS provider TEXT,
    ADD COLUMN IF NOT EXISTS provider_refs JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS last_observed_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS create_idempotency_key TEXT,
    ADD COLUMN IF NOT EXISTS create_request_fingerprint TEXT;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'public'
          AND table_name = 'vector_stores'
          AND column_name = 'idempotency_key'
    ) THEN
        UPDATE vector_stores
        SET create_idempotency_key = COALESCE(
            create_idempotency_key,
            NULLIF(idempotency_key, '')
        )
        WHERE create_idempotency_key IS NULL;
    END IF;
END $$;

DO $$
BEGIN
    ALTER TABLE vector_stores
        ADD CONSTRAINT vector_stores_vector_count_check
        CHECK (vector_count IS NULL OR vector_count >= 0);
EXCEPTION
    WHEN duplicate_object THEN NULL;
END $$;

DO $$
BEGIN
    ALTER TABLE vector_stores
        ADD CONSTRAINT vector_stores_index_status_check
        CHECK (index_status IS NULL OR index_status IN ('building', 'ready', 'failed'));
EXCEPTION
    WHEN duplicate_object THEN NULL;
END $$;

CREATE INDEX IF NOT EXISTS idx_vector_stores_tenant_state
    ON vector_stores (tenant_id, state, updated_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_vector_stores_tenant_create_idempotency
    ON vector_stores (tenant_id, create_idempotency_key)
    WHERE create_idempotency_key IS NOT NULL;

CREATE TABLE IF NOT EXISTS vector_store_knowledge_base_links (
    tenant_id                   UUID        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    vector_store_id             TEXT        NOT NULL,
    knowledge_base_id           TEXT        NOT NULL,
    knowledge_base_name         TEXT,
    source                      TEXT,
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at                  TIMESTAMPTZ,
    PRIMARY KEY (tenant_id, vector_store_id, knowledge_base_id),
    FOREIGN KEY (tenant_id, vector_store_id)
        REFERENCES vector_stores(tenant_id, vector_store_id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_vector_store_kb_links_one_active
    ON vector_store_knowledge_base_links (tenant_id, vector_store_id)
    WHERE deleted_at IS NULL;

-- ── Grants and RLS ───────────────────────────────────────────────────────────

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'ani_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON
            storage_volumes,
            storage_volume_auto_snapshot_policies,
            storage_volume_mount_events,
            storage_volume_snapshots,
            storage_filesystems,
            storage_filesystem_mount_targets,
            storage_filesystem_attachments,
            storage_buckets,
            storage_bucket_lifecycle_rules,
            storage_objects,
            vector_stores,
            vector_store_knowledge_base_links
        TO ani_app;
    END IF;
END $$;

ALTER TABLE storage_volumes ENABLE ROW LEVEL SECURITY;
ALTER TABLE storage_volumes FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON storage_volumes;
CREATE POLICY tenant_isolation ON storage_volumes
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

ALTER TABLE storage_volume_auto_snapshot_policies ENABLE ROW LEVEL SECURITY;
ALTER TABLE storage_volume_auto_snapshot_policies FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON storage_volume_auto_snapshot_policies;
CREATE POLICY tenant_isolation ON storage_volume_auto_snapshot_policies
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

ALTER TABLE storage_volume_mount_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE storage_volume_mount_events FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON storage_volume_mount_events;
CREATE POLICY tenant_isolation ON storage_volume_mount_events
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

ALTER TABLE storage_volume_snapshots ENABLE ROW LEVEL SECURITY;
ALTER TABLE storage_volume_snapshots FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON storage_volume_snapshots;
CREATE POLICY tenant_isolation ON storage_volume_snapshots
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

ALTER TABLE storage_filesystems ENABLE ROW LEVEL SECURITY;
ALTER TABLE storage_filesystems FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON storage_filesystems;
CREATE POLICY tenant_isolation ON storage_filesystems
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

ALTER TABLE storage_filesystem_mount_targets ENABLE ROW LEVEL SECURITY;
ALTER TABLE storage_filesystem_mount_targets FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON storage_filesystem_mount_targets;
CREATE POLICY tenant_isolation ON storage_filesystem_mount_targets
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

ALTER TABLE storage_filesystem_attachments ENABLE ROW LEVEL SECURITY;
ALTER TABLE storage_filesystem_attachments FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON storage_filesystem_attachments;
CREATE POLICY tenant_isolation ON storage_filesystem_attachments
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

ALTER TABLE storage_buckets ENABLE ROW LEVEL SECURITY;
ALTER TABLE storage_buckets FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON storage_buckets;
CREATE POLICY tenant_isolation ON storage_buckets
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

ALTER TABLE storage_bucket_lifecycle_rules ENABLE ROW LEVEL SECURITY;
ALTER TABLE storage_bucket_lifecycle_rules FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON storage_bucket_lifecycle_rules;
CREATE POLICY tenant_isolation ON storage_bucket_lifecycle_rules
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

ALTER TABLE storage_objects ENABLE ROW LEVEL SECURITY;
ALTER TABLE storage_objects FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON storage_objects;
CREATE POLICY tenant_isolation ON storage_objects
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

ALTER TABLE vector_stores ENABLE ROW LEVEL SECURITY;
ALTER TABLE vector_stores FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON vector_stores;
CREATE POLICY tenant_isolation ON vector_stores
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

ALTER TABLE vector_store_knowledge_base_links ENABLE ROW LEVEL SECURITY;
ALTER TABLE vector_store_knowledge_base_links FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON vector_store_knowledge_base_links;
CREATE POLICY tenant_isolation ON vector_store_knowledge_base_links
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

