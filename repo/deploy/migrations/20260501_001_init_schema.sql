-- ANI Platform · Initial Schema Migration
-- Version: 001
-- Description: Complete initial schema for all ANI services
-- Run with: psql $DATABASE_URL -f 20260501_001_init_schema.sql
-- Atlas: atlas migrate apply
BEGIN;

-- ===========================================================================
-- DATABASE ROLES & PERMISSIONS
-- ===========================================================================

-- 应用连接账号（所有微服务使用此账号）
-- 关键约束：非 owner、非 superuser、非 bypassrls，受 RLS 约束
CREATE ROLE ani_app NOLOGIN;
CREATE USER ani_app_user WITH PASSWORD '...' IN ROLE ani_app;

GRANT CONNECT ON DATABASE ani TO ani_app_user;
GRANT USAGE ON SCHEMA public TO ani_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO ani_app;

-- 迁移账号（仅 CI/CD 时使用，不是应用账号）
CREATE ROLE ani_migrator;
GRANT ALL PRIVILEGES ON DATABASE ani TO ani_migrator;

-- Outbox publisher 专用角色（bypassrls，仅跨租户扫描 outbox_events）
CREATE ROLE ani_outbox_publisher BYPASSRLS NOLOGIN;

-- ===========================================================================
-- SECTION 1: TENANTS
-- ===========================================================================

CREATE TABLE tenants (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT NOT NULL UNIQUE,           -- 租户唯一标识（英文，URL友好）
    display_name    TEXT NOT NULL,                  -- 显示名称
    status          TEXT NOT NULL DEFAULT 'active'  -- active | suspended | deleted
        CHECK (status IN ('active', 'suspended', 'deleted')),
    max_gpu_count   INT  NOT NULL DEFAULT 0,        -- GPU 配额上限，0=不限
    max_cpu_cores   INT  NOT NULL DEFAULT 0,
    max_memory_gb   INT  NOT NULL DEFAULT 0,
    settings        JSONB NOT NULL DEFAULT '{}',    -- 租户级别的配置（弹性扩展）
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ===========================================================================
-- SECTION 2: USERS, ROLES, USER_ROLES
-- ===========================================================================

CREATE TABLE users (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    username        TEXT NOT NULL,
    email           TEXT NOT NULL,
    password_hash   TEXT,                           -- bcrypt，外部OIDC用户可为NULL
    status          TEXT NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'disabled')),
    last_login_at   TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, username),
    UNIQUE (tenant_id, email)
);
CREATE INDEX idx_users_tenant_id ON users(tenant_id);

CREATE TABLE roles (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID REFERENCES tenants(id) ON DELETE CASCADE, -- NULL=平台内置角色
    name        TEXT NOT NULL,   -- platform-admin | tenant-admin | user | auditor
    permissions JSONB NOT NULL DEFAULT '[]',
    UNIQUE (tenant_id, name)
);

CREATE TABLE user_roles (
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_id     UUID NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    granted_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, role_id)
);

-- ===========================================================================
-- SECTION 3: API_KEYS, JWT_BLOCKLIST
-- ===========================================================================

CREATE TABLE api_keys (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id         UUID REFERENCES users(id) ON DELETE SET NULL,
    name            TEXT NOT NULL,
    key_hash        TEXT NOT NULL UNIQUE,           -- SHA256(原始key)，不存明文
    key_prefix      TEXT NOT NULL,                  -- 展示用前缀，如 "ani_prod_xxxx"
    scopes          TEXT[] NOT NULL DEFAULT '{}',   -- 权限范围
    rate_limit_rpm  INT NOT NULL DEFAULT 60,        -- 每分钟请求限制
    idempotency_key TEXT,                           -- 创建请求幂等键；明文仅短 TTL 加密缓存
    expires_at      TIMESTAMPTZ,                    -- NULL = 永不过期
    last_used_at    TIMESTAMPTZ,
    revoked_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_api_keys_tenant_id ON api_keys(tenant_id);
CREATE UNIQUE INDEX idx_api_keys_idempotency
    ON api_keys (tenant_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL AND idempotency_key <> '';

CREATE TABLE jwt_blocklist (
    jti         TEXT PRIMARY KEY,                   -- JWT ID
    expires_at  TIMESTAMPTZ NOT NULL,               -- Token 原始过期时间（用于清理）
    revoked_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_jwt_blocklist_expires ON jwt_blocklist(expires_at);

CREATE TABLE refresh_tokens (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash      TEXT NOT NULL UNIQUE,            -- SHA256(refresh token)，不存明文
    roles           TEXT[] NOT NULL DEFAULT '{}',
    expires_at      TIMESTAMPTZ NOT NULL,
    last_used_at    TIMESTAMPTZ,
    revoked_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_refresh_tokens_tenant_id ON refresh_tokens(tenant_id);
CREATE INDEX idx_refresh_tokens_expires ON refresh_tokens(expires_at);

-- ===========================================================================
-- SECTION 4: MODELS, MODEL_VERSIONS, MODEL_IMPORT_TASKS
-- ===========================================================================

CREATE TABLE models (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,                  -- 如 "qwen2.5-72b"
    display_name    TEXT NOT NULL,
    description     TEXT,
    source          TEXT NOT NULL DEFAULT 'upload'
        CHECK (source IN ('upload', 'huggingface', 'modelscope', 'builtin')),
    source_repo_id  TEXT,                           -- HF/MS 的 repo_id
    capabilities    TEXT[] NOT NULL DEFAULT '{}',   -- text-generation | embedding | speech-to-text
    status          TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'downloading', 'ready', 'error', 'deleted')),
    error_message   TEXT,
    total_size_bytes BIGINT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, name)
);
CREATE INDEX idx_models_tenant_id ON models(tenant_id);
CREATE INDEX idx_models_status ON models(tenant_id, status);

CREATE TABLE model_versions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    model_id        UUID NOT NULL REFERENCES models(id) ON DELETE CASCADE,
    version         TEXT NOT NULL,                  -- 如 "v1", "v2", "latest"
    is_encrypted    BOOLEAN NOT NULL DEFAULT FALSE,
    encrypt_algo    TEXT,                           -- sm4 | zuc | aes256gcm
    encrypt_hint    TEXT,                           -- 用户自定义密钥提示，非密钥本身
    format          TEXT NOT NULL DEFAULT 'safetensors'
        CHECK (format IN ('safetensors', 'gguf', 'pytorch')),
    size_bytes      BIGINT,
    checksum_sha256 TEXT,
    storage_path    TEXT NOT NULL,                  -- MinIO 对象路径
    config_json     JSONB NOT NULL DEFAULT '{}',    -- 模型配置（max_tokens等）
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (model_id, version)
);
CREATE INDEX idx_model_versions_model_id ON model_versions(model_id);

CREATE TABLE model_import_tasks (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    model_id        UUID REFERENCES models(id) ON DELETE CASCADE,
    source          TEXT NOT NULL CHECK (source IN ('huggingface', 'modelscope')),
    source_repo_id  TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'running', 'completed', 'failed', 'cancelled')),
    progress_pct    INT NOT NULL DEFAULT 0,
    downloaded_bytes BIGINT NOT NULL DEFAULT 0,
    total_bytes     BIGINT,
    error_message   TEXT,
    started_at      TIMESTAMPTZ,
    completed_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ===========================================================================
-- SECTION 5: INFERENCE_SERVICES, INFERENCE_AUDIT_LOGS (partitioned by month)
-- ===========================================================================

CREATE TABLE inference_services (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name                TEXT NOT NULL,
    model_version_id    UUID NOT NULL REFERENCES model_versions(id),
    replicas            INT NOT NULL DEFAULT 1,
    gpu_type            TEXT,                       -- A100 | A10 | H100 | 910B | null(CPU)
    gpu_count_per_pod   INT NOT NULL DEFAULT 1,
    max_concurrency     INT NOT NULL DEFAULT 8,
    placement_region    TEXT,                       -- Region 放置偏好
    placement_az        TEXT,                       -- AZ 放置偏好
    status              TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending','downloading','decrypting','deploying','running','stopping','stopped','failed')),
    endpoint_url        TEXT,                       -- 运行后填充的内部访问地址
    k8s_namespace       TEXT,
    k8s_deployment_name TEXT,
    error_message       TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, name)
);
CREATE INDEX idx_inference_services_tenant ON inference_services(tenant_id);
CREATE INDEX idx_inference_services_status ON inference_services(tenant_id, status);

CREATE TABLE instance_plan_audits (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id             UUID,
    instance_id         TEXT,
    instance_name       TEXT NOT NULL,
    workload_kind       TEXT NOT NULL
        CHECK (workload_kind IN ('vm','container','gpu_container','inference','notebook','sandbox','agent_sandbox','batch_job')),
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
CREATE INDEX idx_instance_plan_audits_tenant ON instance_plan_audits(tenant_id, created_at DESC);
CREATE INDEX idx_instance_plan_audits_instance ON instance_plan_audits(tenant_id, instance_name, created_at DESC);

CREATE TABLE workload_instances (
    tenant_id           UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    instance_id         TEXT NOT NULL,
    name                TEXT NOT NULL,
    workload_kind       TEXT NOT NULL
        CHECK (workload_kind IN ('vm','container','gpu_container','inference','notebook','sandbox','agent_sandbox','batch_job')),
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
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, instance_id)
);
CREATE INDEX idx_workload_instances_tenant ON workload_instances(tenant_id, updated_at DESC);
CREATE INDEX idx_workload_instances_kind ON workload_instances(tenant_id, workload_kind, state);
CREATE INDEX idx_workload_instances_audit ON workload_instances(tenant_id, audit_id);

-- 推理调用审计日志（高写入量，按月分区）
-- NOTE: 可转为 TimescaleDB hypertable，见文件末尾注释
CREATE TABLE inference_audit_logs (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL,
    service_id      UUID NOT NULL,
    user_id         UUID,
    api_key_id      UUID,
    request_id      TEXT NOT NULL,                  -- 全链路追踪ID
    model_name      TEXT NOT NULL,
    input_tokens    INT NOT NULL DEFAULT 0,
    output_tokens   INT NOT NULL DEFAULT 0,
    duration_ms     INT,
    status_code     INT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
) PARTITION BY RANGE (created_at);                  -- 按月分区，由定时任务自动维护

-- 创建当月和下月分区
CREATE TABLE inference_audit_logs_2026_05
    PARTITION OF inference_audit_logs
    FOR VALUES FROM ('2026-05-01') TO ('2026-06-01');

CREATE TABLE inference_audit_logs_2026_06
    PARTITION OF inference_audit_logs
    FOR VALUES FROM ('2026-06-01') TO ('2026-07-01');

CREATE INDEX idx_inference_audit_tenant ON inference_audit_logs(tenant_id, created_at);

-- ===========================================================================
-- SECTION 6: KNOWLEDGE_BASES, KB_DOCUMENTS, KB_SESSIONS, KB_MESSAGES
-- ===========================================================================

CREATE TABLE knowledge_bases (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    description     TEXT,
    embedding_model TEXT NOT NULL DEFAULT 'bge-m3',
    chunk_size      INT NOT NULL DEFAULT 512,
    top_k           INT NOT NULL DEFAULT 5,
    score_threshold FLOAT NOT NULL DEFAULT 0.3,
    status          TEXT NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'rebuilding', 'deleted')),
    doc_count       INT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, name)
);
CREATE INDEX idx_kb_tenant_id ON knowledge_bases(tenant_id);

CREATE TABLE kb_documents (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    kb_id           UUID NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
    tenant_id       UUID NOT NULL,
    file_name       TEXT NOT NULL,
    file_type       TEXT NOT NULL,                  -- pdf | docx | xlsx | txt | md
    file_size_bytes BIGINT NOT NULL,
    storage_path    TEXT NOT NULL,                  -- MinIO 路径
    checksum_sha256 TEXT NOT NULL,
    parse_status    TEXT NOT NULL DEFAULT 'pending'
        CHECK (parse_status IN ('pending', 'parsing', 'indexing', 'ready', 'failed')),
    chunk_count     INT NOT NULL DEFAULT 0,
    error_message   TEXT,
    parsed_at       TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_kb_docs_kb_id ON kb_documents(kb_id);
CREATE INDEX idx_kb_docs_parse_status ON kb_documents(kb_id, parse_status);

CREATE TABLE kb_sessions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    kb_id       UUID NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
    tenant_id   UUID NOT NULL,
    user_id     UUID,
    title       TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 问答消息
-- tenant_id 直接冗余在消息行，不依赖 JOIN session 来做 RLS 隔离
-- （ANI-09 P0 修复：kb_messages 只靠 session 间接隔离会在 join/缓存查询中漏条件）
CREATE TABLE kb_messages (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id      UUID NOT NULL REFERENCES kb_sessions(id) ON DELETE CASCADE,
    tenant_id       UUID NOT NULL,                  -- 直接冗余，RLS 直接过滤此列
    role            TEXT NOT NULL CHECK (role IN ('user', 'assistant')),
    content         TEXT NOT NULL,
    source_chunks   JSONB,                          -- [{doc_id, file_name, page, score}]
    input_tokens    INT,
    output_tokens   INT,
    duration_ms     INT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_kb_messages_session ON kb_messages(session_id, created_at);
CREATE INDEX idx_kb_messages_tenant ON kb_messages(tenant_id, created_at);

-- ===========================================================================
-- SECTION 7: ASYNC_TASKS, OUTBOX_EVENTS
-- ===========================================================================

-- 通用异步任务表（含 Saga 必需字段）
CREATE TABLE async_tasks (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           UUID NOT NULL,
    idempotency_key     TEXT NOT NULL,          -- 调用方提供，防重复提交（如 "model-import:{model_id}"）
    task_type           TEXT NOT NULL,          -- model.import | kb.parse | kb.index | inference.deploy
    resource_type       TEXT,                   -- inference_service | kb_document | model_version
    resource_id         UUID,                   -- 关联的业务资源 ID
    status              TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending','running','completed','failed','cancelled','dead_letter')),
    attempt_count       INT NOT NULL DEFAULT 0,
    max_attempts        INT NOT NULL DEFAULT 3,
    -- 租约：worker 持有任务的截止时间，过期后其他 worker 可抢占（防僵尸任务）
    lease_owner         TEXT,                   -- 当前持有租约的 worker 身份
    lease_until         TIMESTAMPTZ,
    last_heartbeat_at   TIMESTAMPTZ,            -- worker 每 30s 更新，用于 reconciler 检测失活
    progress_pct        INT NOT NULL DEFAULT 0 CHECK (progress_pct BETWEEN 0 AND 100),
    payload             JSONB NOT NULL DEFAULT '{}',    -- 任务参数（不可变）
    result              JSONB,                          -- 完成后的结果
    error_message       TEXT,
    compensating_action TEXT,                   -- 失败后执行的补偿动作描述（如 "delete_k8s_crd"）
    dead_letter_at      TIMESTAMPTZ,            -- 超过 max_attempts 后打入死信队列的时间
    webhook_url         TEXT,                   -- 完成后主动回调地址
    started_at          TIMESTAMPTZ,
    completed_at        TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, idempotency_key)         -- 同租户下同一幂等键只存一条
);
CREATE INDEX idx_async_tasks_tenant ON async_tasks(tenant_id, status);
CREATE INDEX idx_async_tasks_type ON async_tasks(task_type, status);
CREATE INDEX idx_async_tasks_lease_owner ON async_tasks(tenant_id, lease_owner) WHERE status = 'running';
-- 对账器查询：查找租约过期但仍在 running 的任务（用于重新分配）
CREATE INDEX idx_async_tasks_lease ON async_tasks(lease_until) WHERE status = 'running';
-- 死信队列监控
CREATE INDEX idx_async_tasks_dead_letter ON async_tasks(dead_letter_at) WHERE status = 'dead_letter';

-- Outbox 表：保证"写 DB + 发 NATS 消息"的原子性
-- 应用层：在同一个事务内写业务表 + 写 outbox，由 outbox publisher 轮询发布到 NATS
-- 这样即使 NATS 宕机，消息不丢失；NATS 消费侧幂等处理重复投递
CREATE TABLE outbox_events (
    id              BIGSERIAL PRIMARY KEY,
    aggregate_type  TEXT NOT NULL,              -- inference_service | kb_document | model_version
    aggregate_id    UUID NOT NULL,
    event_type      TEXT NOT NULL,              -- deployed | parse_requested | imported | failed
    tenant_id       UUID NOT NULL,
    payload         JSONB NOT NULL DEFAULT '{}',
    published       BOOLEAN NOT NULL DEFAULT FALSE,
    published_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
-- publisher 轮询此索引，只扫未发布的行
CREATE INDEX idx_outbox_unpublished ON outbox_events(created_at) WHERE NOT published;

-- ===========================================================================
-- SECTION 8: REGIONS, AVAILABILITY_ZONES
-- ===========================================================================

CREATE TABLE regions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT NOT NULL UNIQUE,           -- cn-east | cn-north
    display_name    TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'maintenance', 'disabled')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE availability_zones (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    region_id           UUID NOT NULL REFERENCES regions(id),
    name                TEXT NOT NULL UNIQUE,        -- cn-east-1a
    display_name        TEXT NOT NULL,
    karmada_cluster     TEXT NOT NULL UNIQUE,        -- Karmada 成员集群名
    status              TEXT NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'maintenance', 'disabled')),
    gpu_capacity        JSONB NOT NULL DEFAULT '{}', -- {"nvidia-a100": 8}
    gpu_used            JSONB NOT NULL DEFAULT '{}',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_az_region_id ON availability_zones(region_id);

-- ===========================================================================
-- SECTION 9: METERING_RECORDS
-- NOTE: 此表建议转换为 TimescaleDB hypertable，见文件末尾注释
-- ===========================================================================

CREATE TABLE metering_records (
    id              BIGSERIAL,
    tenant_id       UUID NOT NULL,
    az_name         TEXT NOT NULL,
    resource_type   TEXT NOT NULL,
        -- gpu_hours | cpu_hours | memory_gb_hours | storage_gb_days
        -- input_tokens | output_tokens | kb_queries
    resource_id     UUID,                           -- 关联的服务/知识库ID
    quantity        NUMERIC(20, 6) NOT NULL,
    unit            TEXT NOT NULL,                  -- hours | GB·hours | tokens | calls
    recorded_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (id, recorded_at)
);
CREATE INDEX idx_metering_tenant_time ON metering_records(tenant_id, recorded_at DESC);
CREATE INDEX idx_metering_type ON metering_records(tenant_id, resource_type, recorded_at DESC);

-- ===========================================================================
-- SECTION 10: PLATFORM_BRANDING, UPGRADE_HISTORY, PLATFORM_SETTINGS, AUDIT_LOGS
-- ===========================================================================

-- 白牌化品牌配置
CREATE TABLE platform_branding (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    platform_name   TEXT NOT NULL DEFAULT 'KuberCloud ANI',
    logo_light_url  TEXT,                           -- MinIO presigned URL
    logo_dark_url   TEXT,
    favicon_url     TEXT,
    primary_color   TEXT NOT NULL DEFAULT '#1677FF',
    secondary_color TEXT NOT NULL DEFAULT '#13C2C2',
    login_bg_url    TEXT,
    icp_number      TEXT,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
-- 单行表，确保只有一条记录
CREATE UNIQUE INDEX idx_branding_single ON platform_branding ((TRUE));

-- 平台升级历史
CREATE TABLE upgrade_history (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    from_version    TEXT NOT NULL,
    to_version      TEXT NOT NULL,
    status          TEXT NOT NULL
        CHECK (status IN ('pending', 'dry_run', 'upgrading', 'completed', 'failed', 'rolled_back')),
    patch_manifest  JSONB NOT NULL DEFAULT '{}',
    started_by      UUID,                           -- 操作人
    started_at      TIMESTAMPTZ,
    completed_at    TIMESTAMPTZ,
    error_message   TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 平台全局配置 KV
CREATE TABLE platform_settings (
    key         TEXT PRIMARY KEY,
    value       JSONB NOT NULL,
    description TEXT,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 审计日志（操作层面，与推理层面分开存储），按月分区
CREATE TABLE audit_logs (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID,
    user_id     UUID,
    request_id  TEXT NOT NULL,
    action      TEXT NOT NULL,                      -- user.login | model.deploy | kb.create ...
    resource    TEXT,                               -- 如 "models/qwen2.5-72b"
    result      TEXT NOT NULL CHECK (result IN ('success', 'failure')),
    details     JSONB NOT NULL DEFAULT '{}',
    ip_address  INET,
    user_agent  TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
) PARTITION BY RANGE (created_at);

CREATE TABLE audit_logs_2026_05
    PARTITION OF audit_logs
    FOR VALUES FROM ('2026-05-01') TO ('2026-06-01');

CREATE TABLE audit_logs_2026_06
    PARTITION OF audit_logs
    FOR VALUES FROM ('2026-06-01') TO ('2026-07-01');

CREATE INDEX idx_audit_tenant ON audit_logs(tenant_id, created_at DESC);

-- ===========================================================================
-- SECTION 11: ROW LEVEL SECURITY — ALL TABLES
-- ===========================================================================

-- knowledge_bases
ALTER TABLE knowledge_bases ENABLE ROW LEVEL SECURITY;
ALTER TABLE knowledge_bases FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON knowledge_bases
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

-- kb_documents
ALTER TABLE kb_documents ENABLE ROW LEVEL SECURITY;
ALTER TABLE kb_documents FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON kb_documents
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

-- kb_sessions
ALTER TABLE kb_sessions ENABLE ROW LEVEL SECURITY;
ALTER TABLE kb_sessions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON kb_sessions
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

-- kb_messages
ALTER TABLE kb_messages ENABLE ROW LEVEL SECURITY;
ALTER TABLE kb_messages FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON kb_messages
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

-- models
ALTER TABLE models ENABLE ROW LEVEL SECURITY;
ALTER TABLE models FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON models
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

-- inference_services
ALTER TABLE inference_services ENABLE ROW LEVEL SECURITY;
ALTER TABLE inference_services FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON inference_services
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

-- instance_plan_audits
ALTER TABLE instance_plan_audits ENABLE ROW LEVEL SECURITY;
ALTER TABLE instance_plan_audits FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON instance_plan_audits
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

-- workload_instances
ALTER TABLE workload_instances ENABLE ROW LEVEL SECURITY;
ALTER TABLE workload_instances FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON workload_instances
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

-- async_tasks
ALTER TABLE async_tasks ENABLE ROW LEVEL SECURITY;
ALTER TABLE async_tasks FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON async_tasks
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

-- api_keys
ALTER TABLE api_keys ENABLE ROW LEVEL SECURITY;
ALTER TABLE api_keys FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON api_keys
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

-- refresh_tokens
ALTER TABLE refresh_tokens ENABLE ROW LEVEL SECURITY;
ALTER TABLE refresh_tokens FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON refresh_tokens
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

-- audit_logs
ALTER TABLE audit_logs ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_logs FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON audit_logs
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

-- metering_records
ALTER TABLE metering_records ENABLE ROW LEVEL SECURITY;
ALTER TABLE metering_records FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON metering_records
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

-- outbox_events
-- NOTE: outbox publisher 服务使用专用角色 ani_outbox_publisher (bypassrls)
-- 跨租户扫描所有未发布事件，在 K8s NetworkPolicy 中严格限制访问来源
ALTER TABLE outbox_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE outbox_events FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON outbox_events
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

-- ===========================================================================
-- SECTION 12: SEED DATA — BUILT-IN ROLES
-- ===========================================================================

INSERT INTO roles (id, tenant_id, name, permissions) VALUES
    (gen_random_uuid(), NULL, 'platform-admin',
     '["*"]'),
    (gen_random_uuid(), NULL, 'tenant-admin',
     '["tenant:read","models:*","kb:*","inference:*","users:*","audit:read"]'),
    (gen_random_uuid(), NULL, 'user',
     '["models:read","kb:read","kb:query","inference:invoke"]'),
    (gen_random_uuid(), NULL, 'auditor',
     '["audit:read","metering:read","models:read"]');

INSERT INTO platform_settings (key, value, description) VALUES
    ('metering.collection_interval_seconds', '60', '计量数据采集间隔'),
    ('inference.default_max_tokens', '4096', '推理默认最大 Token 数'),
    ('kb.default_top_k', '5', '知识库默认召回数量'),
    ('kb.default_score_threshold', '0.3', '知识库最低相关性分数');

-- TimescaleDB hypertable conversion (run separately after extension is enabled)
-- SELECT create_hypertable('metering_records', 'recorded_at', chunk_time_interval => INTERVAL '1 day');
-- SELECT create_hypertable('inference_audit_logs', 'created_at', chunk_time_interval => INTERVAL '1 month');

-- Seed data: platform default settings
INSERT INTO platform_branding (platform_name, primary_color, secondary_color)
VALUES ('KuberCloud ANI', '#1677FF', '#13C2C2')
ON CONFLICT DO NOTHING;

COMMIT;
