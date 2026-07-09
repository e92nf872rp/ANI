-- ANI local dev + Auth/Dex production gate database bootstrap.
-- Mounted by deploy/docker/docker-compose.yml on first `make deps` (empty PG volume only).
-- Canonical source: deploy/postgres/ani-dev-database-init.sql
-- Gateway metadata tables: same as deploy/postgres/gateway-metadata-schema.sql (inlined below).

CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS tenants (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT NOT NULL UNIQUE,
    display_name    TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'suspended', 'deleted')),
    max_gpu_count   INT  NOT NULL DEFAULT 0,
    max_cpu_cores   INT  NOT NULL DEFAULT 0,
    max_memory_gb   INT  NOT NULL DEFAULT 0,
    settings        JSONB NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS users (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    username        TEXT NOT NULL,
    email           TEXT NOT NULL,
    password_hash   TEXT,
    status          TEXT NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'disabled')),
    last_login_at   TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, username),
    UNIQUE (tenant_id, email)
);
CREATE INDEX IF NOT EXISTS idx_users_tenant_id ON users(tenant_id);

CREATE TABLE IF NOT EXISTS roles (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID REFERENCES tenants(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    permissions JSONB NOT NULL DEFAULT '[]',
    UNIQUE (tenant_id, name)
);

CREATE TABLE IF NOT EXISTS user_roles (
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_id     UUID NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    granted_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, role_id)
);

CREATE TABLE IF NOT EXISTS api_keys (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id         UUID REFERENCES users(id) ON DELETE SET NULL,
    name            TEXT NOT NULL,
    key_hash        TEXT NOT NULL UNIQUE,
    key_prefix      TEXT NOT NULL,
    scopes          TEXT[] NOT NULL DEFAULT '{}',
    rate_limit_rpm  INT NOT NULL DEFAULT 60,
    instance_id     TEXT,
    expires_at      TIMESTAMPTZ,
    last_used_at    TIMESTAMPTZ,
    revoked_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_api_keys_tenant_id ON api_keys(tenant_id);
CREATE INDEX IF NOT EXISTS idx_api_keys_instance
    ON api_keys (tenant_id, instance_id)
    WHERE instance_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS jwt_blocklist (
    jti         TEXT PRIMARY KEY,
    expires_at  TIMESTAMPTZ NOT NULL,
    revoked_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_jwt_blocklist_expires ON jwt_blocklist(expires_at);

CREATE TABLE IF NOT EXISTS refresh_tokens (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash      TEXT NOT NULL UNIQUE,
    roles           TEXT[] NOT NULL DEFAULT '{}',
    expires_at      TIMESTAMPTZ NOT NULL,
    last_used_at    TIMESTAMPTZ,
    revoked_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_tenant_id ON refresh_tokens(tenant_id);
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_expires ON refresh_tokens(expires_at);

-- ---------------------------------------------------------------------------
-- Core runtime metadata (Gateway / reconcile-worker local profile)
-- No RLS or ani_app grants: local docker postgres uses owner role "ani".
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS instance_plan_audits (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id             UUID,
    instance_id         TEXT,
    instance_name       TEXT NOT NULL,
    workload_kind       TEXT NOT NULL
        CHECK (workload_kind IN ('vm','container','gpu_container','inference','notebook','agent_sandbox','batch_job')),
    vpc_id              TEXT,
    subnet_id           TEXT,
    private_ip          TEXT,
    provider            TEXT,
    manifest_count      INT NOT NULL DEFAULT 0,
    rendered_manifests  JSONB NOT NULL DEFAULT '[]',
    admission_allowed   BOOLEAN NOT NULL DEFAULT FALSE,
    admission_reason    TEXT,
    admission_warnings  JSONB NOT NULL DEFAULT '[]',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_instance_plan_audits_tenant
    ON instance_plan_audits(tenant_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_instance_plan_audits_instance
    ON instance_plan_audits(tenant_id, instance_name, created_at DESC);

CREATE TABLE IF NOT EXISTS workload_instances (
    tenant_id           UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    instance_id         TEXT NOT NULL,
    name                TEXT NOT NULL,
    workload_kind       TEXT NOT NULL
        CHECK (workload_kind IN ('vm','container','gpu_container','inference','notebook','agent_sandbox','batch_job')),
    vpc_id              TEXT,
    subnet_id           TEXT,
    private_ip          TEXT,
    provider            TEXT,
    audit_id            UUID REFERENCES instance_plan_audits(id),
    provider_id         TEXT,
    resource_refs       JSONB NOT NULL DEFAULT '[]',
    state               TEXT NOT NULL
        CHECK (state IN ('pending','provisioning','running','starting','stopping','stopped','failed','deleting','deleted')),
    endpoint            TEXT,
    node_name           TEXT,
    reason              TEXT,
    networks            JSONB NOT NULL DEFAULT '[]',
    storage             JSONB NOT NULL DEFAULT '[]',
    lifecycle_policy    JSONB NOT NULL DEFAULT '{}',
    ssh_connection      JSONB NOT NULL DEFAULT '{}',
    snapshots           JSONB NOT NULL DEFAULT '[]',
    container_status    JSONB NOT NULL DEFAULT '{}',
    gpu_status          JSONB NOT NULL DEFAULT '{}',
    idempotency_key     TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, instance_id)
);
CREATE INDEX IF NOT EXISTS idx_workload_instances_tenant
    ON workload_instances(tenant_id, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_workload_instances_kind
    ON workload_instances(tenant_id, workload_kind, state);
CREATE INDEX IF NOT EXISTS idx_workload_instances_audit
    ON workload_instances(tenant_id, audit_id);
CREATE INDEX IF NOT EXISTS idx_workload_instances_reconcile
    ON workload_instances (state, updated_at);
CREATE UNIQUE INDEX IF NOT EXISTS idx_workload_instances_idem
    ON workload_instances (tenant_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;

CREATE TABLE IF NOT EXISTS workload_instance_operations (
    id                      UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id               UUID        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    instance_id             TEXT        NOT NULL,
    operation               TEXT        NOT NULL
        CHECK (operation IN (
            'create','start','stop','restart','resize','rebuild','delete',
            'snapshot','attach_volume','detach_volume','rollback','console_session'
        )),
    status                  TEXT        NOT NULL DEFAULT 'accepted'
        CHECK (status IN ('accepted','in_progress','succeeded','failed','cancelled')),
    idempotency_key         TEXT,
    requested_by            TEXT        NOT NULL,
    precheck_json           JSONB       NOT NULL DEFAULT '{}',
    destructive_impact_json JSONB       NOT NULL DEFAULT '{}',
    before_spec_json        JSONB       NOT NULL DEFAULT '{}',
    after_spec_json         JSONB       NOT NULL DEFAULT '{}',
    provider_refs_json      JSONB       NOT NULL DEFAULT '[]',
    failure_reason          TEXT,
    failure_message         TEXT,
    retry_eligible          BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_wio_tenant_instance
    ON workload_instance_operations (tenant_id, instance_id);
CREATE INDEX IF NOT EXISTS idx_wio_active_status
    ON workload_instance_operations (tenant_id, status)
    WHERE status NOT IN ('succeeded','failed','cancelled');
CREATE UNIQUE INDEX IF NOT EXISTS idx_wio_idempotency
    ON workload_instance_operations (tenant_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;

CREATE TABLE IF NOT EXISTS workload_instance_operation_steps (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    UUID        NOT NULL,
    operation_id UUID        NOT NULL REFERENCES workload_instance_operations(id) ON DELETE CASCADE,
    step_name    TEXT        NOT NULL,
    status       TEXT        NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending','running','succeeded','failed','skipped')),
    message      TEXT,
    started_at   TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_wios_operation
    ON workload_instance_operation_steps (operation_id);

CREATE TABLE IF NOT EXISTS network_vpcs (
    tenant_id       UUID        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    vpc_id          TEXT        NOT NULL,
    name            TEXT        NOT NULL,
    cidr            TEXT        NOT NULL,
    state           TEXT        NOT NULL
        CHECK (state IN ('pending','available','failed','deleting','deleted')),
    reason          TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, vpc_id)
);
CREATE INDEX IF NOT EXISTS idx_network_vpcs_tenant_state
    ON network_vpcs (tenant_id, state, updated_at DESC);

CREATE TABLE IF NOT EXISTS network_subnets (
    tenant_id       UUID        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    subnet_id       TEXT        NOT NULL,
    vpc_id          TEXT        NOT NULL,
    name            TEXT        NOT NULL,
    cidr            TEXT        NOT NULL,
    gateway         TEXT,
    state           TEXT        NOT NULL
        CHECK (state IN ('pending','available','failed','deleting','deleted')),
    reason          TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, subnet_id),
    FOREIGN KEY (tenant_id, vpc_id) REFERENCES network_vpcs(tenant_id, vpc_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_network_subnets_tenant_vpc
    ON network_subnets (tenant_id, vpc_id, state, updated_at DESC);

CREATE TABLE IF NOT EXISTS network_security_groups (
    tenant_id           UUID        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    security_group_id   TEXT        NOT NULL,
    name                TEXT        NOT NULL,
    description         TEXT,
    rules               JSONB       NOT NULL DEFAULT '[]',
    state               TEXT        NOT NULL
        CHECK (state IN ('pending','available','failed','deleting','deleted')),
    reason              TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, security_group_id)
);
CREATE INDEX IF NOT EXISTS idx_network_security_groups_tenant_state
    ON network_security_groups (tenant_id, state, updated_at DESC);

CREATE TABLE IF NOT EXISTS network_load_balancers (
    tenant_id           UUID        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    load_balancer_id    TEXT        NOT NULL,
    name                TEXT        NOT NULL,
    vpc_id              TEXT        NOT NULL,
    subnet_id           TEXT,
    scheme              TEXT        NOT NULL DEFAULT 'internal'
        CHECK (scheme IN ('internal','public')),
    vip                 TEXT,
    listeners           JSONB       NOT NULL DEFAULT '[]',
    state               TEXT        NOT NULL
        CHECK (state IN ('pending','available','failed','deleting','deleted')),
    reason              TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, load_balancer_id),
    FOREIGN KEY (tenant_id, vpc_id) REFERENCES network_vpcs(tenant_id, vpc_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_network_load_balancers_tenant_vpc
    ON network_load_balancers (tenant_id, vpc_id, state, updated_at DESC);

CREATE TABLE IF NOT EXISTS network_routes (
    tenant_id        UUID        NOT NULL,
    route_id         TEXT        NOT NULL,
    vpc_id           TEXT        NOT NULL,
    destination_cidr TEXT        NOT NULL,
    next_hop_type    TEXT        NOT NULL,
    next_hop_id      TEXT        NOT NULL,
    description      TEXT,
    state            TEXT        NOT NULL,
    provider         TEXT,
    real_provider    BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, route_id),
    FOREIGN KEY (tenant_id, vpc_id) REFERENCES network_vpcs(tenant_id, vpc_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_network_routes_tenant_vpc
    ON network_routes (tenant_id, vpc_id, state, updated_at DESC);

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

CREATE TABLE IF NOT EXISTS k8s_cluster_proxy_targets (
    tenant_id               UUID        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    cluster_id              TEXT        NOT NULL,
    server                  TEXT        NOT NULL,
    bearer_token            TEXT,
    ca_data                 TEXT,
    client_certificate_data TEXT,
    client_key_data         TEXT,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, cluster_id)
);
CREATE INDEX IF NOT EXISTS idx_k8s_cluster_proxy_targets_tenant_updated
    ON k8s_cluster_proxy_targets (tenant_id, updated_at DESC);

CREATE TABLE IF NOT EXISTS control_plane_leases (
    lease_name  TEXT PRIMARY KEY,
    holder_id   TEXT NOT NULL,
    lease_until TIMESTAMPTZ NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_control_plane_leases_until
    ON control_plane_leases (lease_until);

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
CREATE INDEX IF NOT EXISTS idx_async_tasks_tenant
    ON async_tasks(tenant_id, status);

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
CREATE INDEX IF NOT EXISTS idx_outbox_unpublished
    ON outbox_events(created_at) WHERE NOT published;

CREATE TABLE IF NOT EXISTS platform_branding (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    platform_name   TEXT NOT NULL DEFAULT 'KuberCloud ANI',
    logo_light_url  TEXT,
    logo_dark_url   TEXT,
    favicon_url     TEXT,
    primary_color   TEXT NOT NULL DEFAULT '#1677FF',
    secondary_color TEXT NOT NULL DEFAULT '#13C2C2',
    login_bg_url    TEXT,
    icp_number      TEXT,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_branding_single ON platform_branding ((TRUE));

CREATE TABLE IF NOT EXISTS platform_settings (
    key         TEXT PRIMARY KEY,
    value       JSONB NOT NULL,
    description TEXT,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ---------------------------------------------------------------------------
-- Gateway metadata persistence (P1/P3 + network_routes RLS alignment)
-- Keep in sync with deploy/postgres/gateway-metadata-schema.sql
-- ---------------------------------------------------------------------------

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

INSERT INTO roles (tenant_id, name, permissions) VALUES
    (NULL, 'platform-admin', '["*"]'),
    (NULL, 'tenant-admin', '["tenant:read","networks:*","storage:*","gpu-inventory:*","k8s-clusters:*"]'),
    (NULL, 'user', '["networks:read","storage:read","gpu-inventory:read","k8s-clusters:read","scope:instances:exec"]'),
    (NULL, 'auditor', '["audit:read","metering:read","networks:read","storage:read"]')
ON CONFLICT (tenant_id, name) DO NOTHING;

INSERT INTO tenants (id, name, display_name, status)
VALUES
    ('00000000-0000-0000-0000-000000000001', 'tenant-a', 'Tenant A', 'active'),
    ('11111111-1111-1111-1111-111111111111', 'default', 'Default Tenant', 'active')
ON CONFLICT (name) DO UPDATE
SET status='active',
    updated_at=NOW();

INSERT INTO platform_settings (key, value, description) VALUES
    ('metering.collection_interval_seconds', '60', '计量数据采集间隔'),
    ('inference.default_max_tokens', '4096', '推理默认最大 Token 数'),
    ('kb.default_top_k', '5', '知识库默认召回数量'),
    ('kb.default_score_threshold', '0.3', '知识库最低相关性分数')
ON CONFLICT (key) DO NOTHING;

INSERT INTO platform_branding (platform_name, primary_color, secondary_color)
VALUES ('KuberCloud ANI', '#1677FF', '#13C2C2')
ON CONFLICT DO NOTHING;
