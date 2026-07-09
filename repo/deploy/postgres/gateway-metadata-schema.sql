-- ANI Gateway metadata schema (idempotent).
-- Bundles Gateway metadata migrations and the instance network-selection column alignment.
-- Use on EXISTING PostgreSQL volumes; fresh `make deps` already includes this via ani-dev-database-init.sql.
--
--   psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f deploy/postgres/gateway-metadata-schema.sql
--   make db-upgrade-gateway-metadata

BEGIN;

ALTER TABLE workload_instances
    ADD COLUMN IF NOT EXISTS vpc_id TEXT,
    ADD COLUMN IF NOT EXISTS subnet_id TEXT,
    ADD COLUMN IF NOT EXISTS private_ip TEXT;

CREATE INDEX IF NOT EXISTS idx_workload_instances_reconcile
    ON workload_instances (state, updated_at);

CREATE TABLE IF NOT EXISTS metering_records (
    id              BIGSERIAL,
    tenant_id       UUID NOT NULL,
    az_name         TEXT NOT NULL,
    resource_type   TEXT NOT NULL,
    resource_id     UUID,
    quantity        NUMERIC(20, 6) NOT NULL,
    unit            TEXT NOT NULL,
    recorded_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (id, recorded_at)
);

CREATE INDEX IF NOT EXISTS idx_metering_tenant_time ON metering_records(tenant_id, recorded_at DESC);
CREATE INDEX IF NOT EXISTS idx_metering_type ON metering_records(tenant_id, resource_type, recorded_at DESC);

ALTER TABLE metering_records ENABLE ROW LEVEL SECURITY;
ALTER TABLE metering_records FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON metering_records;
CREATE POLICY tenant_isolation ON metering_records
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

CREATE TABLE IF NOT EXISTS vector_stores (
    tenant_id         UUID NOT NULL,
    store_id          TEXT NOT NULL,
    name              TEXT NOT NULL,
    dimension         INT NOT NULL CHECK (dimension > 0),
    metric            TEXT NOT NULL DEFAULT 'cosine'
        CHECK (metric IN ('cosine', 'l2', 'ip')),
    state             TEXT NOT NULL DEFAULT 'pending'
        CHECK (state IN ('pending', 'ready', 'failed', 'deleting', 'deleted')),
    reason            TEXT,
    idempotency_key   TEXT,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, store_id)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_vector_stores_idempotency
    ON vector_stores (tenant_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL AND idempotency_key <> '';

CREATE INDEX IF NOT EXISTS idx_vector_stores_tenant_updated
    ON vector_stores (tenant_id, updated_at DESC)
    WHERE state <> 'deleted';

ALTER TABLE vector_stores ENABLE ROW LEVEL SECURITY;
ALTER TABLE vector_stores FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON vector_stores;
CREATE POLICY tenant_isolation ON vector_stores
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

CREATE TABLE IF NOT EXISTS metering_token_reports (
    id              TEXT PRIMARY KEY,
    tenant_id       UUID NOT NULL,
    idempotency_key TEXT NOT NULL,
    source          TEXT NOT NULL,
    model           TEXT NOT NULL,
    input_tokens    BIGINT NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
    output_tokens   BIGINT NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
    request_id      TEXT,
    instance_id     TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS idx_metering_token_reports_tenant_created
    ON metering_token_reports (tenant_id, created_at DESC);

ALTER TABLE metering_token_reports ENABLE ROW LEVEL SECURITY;
ALTER TABLE metering_token_reports FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON metering_token_reports;
CREATE POLICY tenant_isolation ON metering_token_reports
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

CREATE TABLE IF NOT EXISTS storage_buckets (
    tenant_id         UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    bucket_id         TEXT NOT NULL,
    name              TEXT NOT NULL,
    region            TEXT,
    access_mode       TEXT NOT NULL DEFAULT 'private'
        CHECK (access_mode IN ('private', 'public_read')),
    idempotency_key   TEXT,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, bucket_id),
    UNIQUE (tenant_id, name)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_storage_buckets_idempotency
    ON storage_buckets (tenant_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL AND idempotency_key <> '';

CREATE INDEX IF NOT EXISTS idx_storage_buckets_tenant_created
    ON storage_buckets (tenant_id, created_at DESC);

CREATE TABLE IF NOT EXISTS volume_snapshots (
    tenant_id         UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    snapshot_id       TEXT NOT NULL,
    volume_id         TEXT NOT NULL,
    name              TEXT NOT NULL,
    description       TEXT,
    status            TEXT NOT NULL DEFAULT 'available'
        CHECK (status IN ('creating', 'available', 'error', 'deleting')),
    size_bytes        BIGINT NOT NULL DEFAULT 0 CHECK (size_bytes >= 0),
    idempotency_key   TEXT,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, snapshot_id),
    FOREIGN KEY (tenant_id, volume_id) REFERENCES storage_volumes(tenant_id, volume_id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_volume_snapshots_idempotency
    ON volume_snapshots (tenant_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL AND idempotency_key <> '';

CREATE INDEX IF NOT EXISTS idx_volume_snapshots_tenant_volume
    ON volume_snapshots (tenant_id, volume_id, created_at DESC);

CREATE TABLE IF NOT EXISTS filesystem_mount_targets (
    tenant_id         UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    mount_target_id   TEXT NOT NULL,
    filesystem_id     TEXT NOT NULL,
    subnet_id         TEXT NOT NULL,
    ip_address        TEXT NOT NULL,
    status            TEXT NOT NULL DEFAULT 'available'
        CHECK (status IN ('creating', 'available', 'deleting', 'error')),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, mount_target_id),
    UNIQUE (tenant_id, filesystem_id),
    FOREIGN KEY (tenant_id, filesystem_id) REFERENCES storage_filesystems(tenant_id, filesystem_id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_filesystem_mount_targets_tenant_fs
    ON filesystem_mount_targets (tenant_id, filesystem_id);

ALTER TABLE storage_buckets ENABLE ROW LEVEL SECURITY;
ALTER TABLE storage_buckets FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON storage_buckets;
CREATE POLICY tenant_isolation ON storage_buckets
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

ALTER TABLE volume_snapshots ENABLE ROW LEVEL SECURITY;
ALTER TABLE volume_snapshots FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON volume_snapshots;
CREATE POLICY tenant_isolation ON volume_snapshots
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

ALTER TABLE filesystem_mount_targets ENABLE ROW LEVEL SECURITY;
ALTER TABLE filesystem_mount_targets FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON filesystem_mount_targets;
CREATE POLICY tenant_isolation ON filesystem_mount_targets
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

ALTER TABLE network_routes ENABLE ROW LEVEL SECURITY;
ALTER TABLE network_routes FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON network_routes;
CREATE POLICY tenant_isolation ON network_routes
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

CREATE TABLE IF NOT EXISTS registry_projects (
    tenant_id         UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    project_id        TEXT NOT NULL,
    name              TEXT NOT NULL,
    public            BOOLEAN NOT NULL DEFAULT FALSE,
    provider_mode     TEXT NOT NULL DEFAULT 'local'
        CHECK (provider_mode IN ('local', 'harbor')),
    idempotency_key   TEXT,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, project_id),
    UNIQUE (tenant_id, name)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_registry_projects_idempotency
    ON registry_projects (tenant_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL AND idempotency_key <> '';

CREATE INDEX IF NOT EXISTS idx_registry_projects_tenant_created
    ON registry_projects (tenant_id, created_at DESC);

CREATE TABLE IF NOT EXISTS registry_repository_permissions (
    tenant_id         UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    project           TEXT NOT NULL,
    repository        TEXT NOT NULL,
    subject           TEXT NOT NULL,
    actions           JSONB NOT NULL DEFAULT '[]'::jsonb,
    state             TEXT NOT NULL DEFAULT 'active'
        CHECK (state IN ('active', 'duplicate')),
    idempotency_key   TEXT,
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, project, repository, subject)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_registry_repository_permissions_idempotency
    ON registry_repository_permissions (tenant_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL AND idempotency_key <> '';

CREATE INDEX IF NOT EXISTS idx_registry_repository_permissions_tenant_project
    ON registry_repository_permissions (tenant_id, project, repository);

CREATE TABLE IF NOT EXISTS registry_pull_secrets (
    tenant_id         UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    project           TEXT NOT NULL,
    name              TEXT NOT NULL,
    secret_ref        TEXT NOT NULL,
    registry_host     TEXT NOT NULL,
    username          TEXT NOT NULL,
    namespace         TEXT,
    state             TEXT NOT NULL DEFAULT 'active'
        CHECK (state IN ('active', 'duplicate')),
    idempotency_key   TEXT,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, project, name)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_registry_pull_secrets_idempotency
    ON registry_pull_secrets (tenant_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL AND idempotency_key <> '';

CREATE INDEX IF NOT EXISTS idx_registry_pull_secrets_tenant_project
    ON registry_pull_secrets (tenant_id, project, created_at DESC);

ALTER TABLE registry_projects ENABLE ROW LEVEL SECURITY;
ALTER TABLE registry_projects FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON registry_projects;
CREATE POLICY tenant_isolation ON registry_projects
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

ALTER TABLE registry_repository_permissions ENABLE ROW LEVEL SECURITY;
ALTER TABLE registry_repository_permissions FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON registry_repository_permissions;
CREATE POLICY tenant_isolation ON registry_repository_permissions
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

ALTER TABLE registry_pull_secrets ENABLE ROW LEVEL SECURITY;
ALTER TABLE registry_pull_secrets FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON registry_pull_secrets;
CREATE POLICY tenant_isolation ON registry_pull_secrets
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

CREATE TABLE IF NOT EXISTS k8s_clusters (
    tenant_id         UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    cluster_id        TEXT NOT NULL,
    name              TEXT NOT NULL,
    version           TEXT NOT NULL DEFAULT '',
    state             TEXT NOT NULL DEFAULT 'provisioning'
        CHECK (state IN ('provisioning', 'running', 'deleting')),
    reason            TEXT,
    provider          TEXT NOT NULL DEFAULT 'local',
    real_provider     BOOLEAN NOT NULL DEFAULT FALSE,
    provider_refs     JSONB NOT NULL DEFAULT '[]'::jsonb,
    idempotency_key   TEXT,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, cluster_id),
    UNIQUE (tenant_id, name)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_k8s_clusters_idempotency
    ON k8s_clusters (tenant_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL AND idempotency_key <> '';

CREATE INDEX IF NOT EXISTS idx_k8s_clusters_tenant_created
    ON k8s_clusters (tenant_id, created_at ASC);

CREATE TABLE IF NOT EXISTS k8s_cluster_node_pools (
    tenant_id           UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    cluster_id          TEXT NOT NULL,
    node_pool_id        TEXT NOT NULL,
    name                TEXT NOT NULL,
    node_count          INT NOT NULL CHECK (node_count >= 0),
    instance_type       TEXT NOT NULL,
    gpu_vendor          TEXT,
    gpu_model           TEXT,
    gpu_count           INT NOT NULL DEFAULT 0,
    gpu_resource_name   TEXT,
    state               TEXT NOT NULL DEFAULT 'running'
        CHECK (state IN ('running', 'deleting')),
    reason              TEXT,
    provider            TEXT NOT NULL DEFAULT 'local',
    real_provider       BOOLEAN NOT NULL DEFAULT FALSE,
    provider_refs       JSONB NOT NULL DEFAULT '[]'::jsonb,
    idempotency_key     TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, node_pool_id),
    UNIQUE (tenant_id, cluster_id, name),
    FOREIGN KEY (tenant_id, cluster_id) REFERENCES k8s_clusters(tenant_id, cluster_id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_k8s_cluster_node_pools_idempotency
    ON k8s_cluster_node_pools (tenant_id, cluster_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL AND idempotency_key <> '';

CREATE INDEX IF NOT EXISTS idx_k8s_cluster_node_pools_tenant_cluster
    ON k8s_cluster_node_pools (tenant_id, cluster_id, created_at ASC);

ALTER TABLE k8s_clusters ENABLE ROW LEVEL SECURITY;
ALTER TABLE k8s_clusters FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON k8s_clusters;
CREATE POLICY tenant_isolation ON k8s_clusters
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

ALTER TABLE k8s_cluster_node_pools ENABLE ROW LEVEL SECURITY;
ALTER TABLE k8s_cluster_node_pools FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON k8s_cluster_node_pools;
CREATE POLICY tenant_isolation ON k8s_cluster_node_pools
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

CREATE TABLE IF NOT EXISTS secrets (
    tenant_id         UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    secret_id         TEXT NOT NULL,
    name              TEXT NOT NULL,
    type              TEXT NOT NULL DEFAULT 'opaque'
        CHECK (type IN ('opaque', 'dockerconfigjson', 'tls')),
    keys              JSONB NOT NULL DEFAULT '[]'::jsonb,
    state             TEXT NOT NULL DEFAULT 'active'
        CHECK (state IN ('active', 'deleted')),
    provider          TEXT NOT NULL DEFAULT 'local',
    real_provider     BOOLEAN NOT NULL DEFAULT FALSE,
    provider_refs     JSONB NOT NULL DEFAULT '[]'::jsonb,
    idempotency_key   TEXT,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, secret_id),
    UNIQUE (tenant_id, name)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_secrets_idempotency
    ON secrets (tenant_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL AND idempotency_key <> '';

CREATE INDEX IF NOT EXISTS idx_secrets_tenant_created
    ON secrets (tenant_id, created_at ASC)
    WHERE state <> 'deleted';

CREATE TABLE IF NOT EXISTS secret_bindings (
    tenant_id         UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    binding_id        TEXT NOT NULL,
    secret_id         TEXT NOT NULL,
    target_type       TEXT NOT NULL,
    target_id         TEXT NOT NULL,
    mount_path        TEXT,
    env_prefix        TEXT,
    state             TEXT NOT NULL DEFAULT 'bound'
        CHECK (state IN ('bound', 'released')),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, binding_id),
    FOREIGN KEY (tenant_id, secret_id) REFERENCES secrets(tenant_id, secret_id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_secret_bindings_tenant_secret
    ON secret_bindings (tenant_id, secret_id, created_at ASC);

ALTER TABLE secrets ENABLE ROW LEVEL SECURITY;
ALTER TABLE secrets FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON secrets;
CREATE POLICY tenant_isolation ON secrets
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

ALTER TABLE secret_bindings ENABLE ROW LEVEL SECURITY;
ALTER TABLE secret_bindings FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON secret_bindings;
CREATE POLICY tenant_isolation ON secret_bindings
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

CREATE TABLE IF NOT EXISTS encryption_keys (
    tenant_id         UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    key_id            TEXT NOT NULL,
    name              TEXT NOT NULL,
    algorithm         TEXT NOT NULL DEFAULT 'SM4'
        CHECK (algorithm IN ('SM4', 'AES256')),
    state             TEXT NOT NULL DEFAULT 'active'
        CHECK (state IN ('active', 'rotated', 'revoked', 'deleted')),
    provider          TEXT NOT NULL DEFAULT 'local',
    real_provider     BOOLEAN NOT NULL DEFAULT FALSE,
    provider_refs     JSONB NOT NULL DEFAULT '[]'::jsonb,
    idempotency_key   TEXT,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, key_id)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_encryption_keys_idempotency
    ON encryption_keys (tenant_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL AND idempotency_key <> '';

CREATE INDEX IF NOT EXISTS idx_encryption_keys_tenant_created
    ON encryption_keys (tenant_id, created_at ASC);

ALTER TABLE encryption_keys ENABLE ROW LEVEL SECURITY;
ALTER TABLE encryption_keys FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON encryption_keys;
CREATE POLICY tenant_isolation ON encryption_keys
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

COMMIT;
