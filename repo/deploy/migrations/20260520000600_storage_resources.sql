-- ANI Platform · Migration 006
-- Description: M1-STORAGE-A Core storage resource persistence
-- Depends on: 20260520000500_network_resources.sql


CREATE TABLE IF NOT EXISTS storage_volumes (
    tenant_id       UUID        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    volume_id       TEXT        NOT NULL,
    name            TEXT        NOT NULL,
    size_gib        BIGINT      NOT NULL CHECK (size_gib > 0),
    storage_class   TEXT        NOT NULL,
    state           TEXT        NOT NULL
        CHECK (state IN ('pending','available','failed','deleting','deleted')),
    reason          TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, volume_id)
);
CREATE INDEX IF NOT EXISTS idx_storage_volumes_tenant_state
    ON storage_volumes (tenant_id, state, updated_at DESC);

CREATE TABLE IF NOT EXISTS storage_filesystems (
    tenant_id       UUID        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    filesystem_id   TEXT        NOT NULL,
    name            TEXT        NOT NULL,
    protocol        TEXT        NOT NULL CHECK (protocol IN ('nfs','cephfs')),
    size_gib        BIGINT      NOT NULL CHECK (size_gib > 0),
    endpoint        TEXT,
    state           TEXT        NOT NULL
        CHECK (state IN ('pending','available','failed','deleting','deleted')),
    reason          TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, filesystem_id)
);
CREATE INDEX IF NOT EXISTS idx_storage_filesystems_tenant_state
    ON storage_filesystems (tenant_id, state, updated_at DESC);

CREATE TABLE IF NOT EXISTS storage_objects (
    tenant_id       UUID        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    object_id       TEXT        NOT NULL,
    bucket          TEXT        NOT NULL,
    object_key      TEXT        NOT NULL,
    size_bytes      BIGINT      NOT NULL DEFAULT 0 CHECK (size_bytes >= 0),
    content_type    TEXT        NOT NULL DEFAULT 'application/octet-stream',
    state           TEXT        NOT NULL
        CHECK (state IN ('pending','available','failed','deleting','deleted')),
    reason          TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, object_id),
    UNIQUE (tenant_id, bucket, object_key)
);
CREATE INDEX IF NOT EXISTS idx_storage_objects_tenant_bucket
    ON storage_objects (tenant_id, bucket, state, updated_at DESC);

GRANT SELECT, INSERT, UPDATE, DELETE ON
    storage_volumes,
    storage_filesystems,
    storage_objects
TO ani_app;

ALTER TABLE storage_volumes ENABLE ROW LEVEL SECURITY;
ALTER TABLE storage_volumes FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON storage_volumes;
CREATE POLICY tenant_isolation ON storage_volumes
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

ALTER TABLE storage_filesystems ENABLE ROW LEVEL SECURITY;
ALTER TABLE storage_filesystems FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON storage_filesystems;
CREATE POLICY tenant_isolation ON storage_filesystems
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

ALTER TABLE storage_objects ENABLE ROW LEVEL SECURITY;
ALTER TABLE storage_objects FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON storage_objects;
CREATE POLICY tenant_isolation ON storage_objects
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

