# KuberCloud ANI · 数据模型设计

> 版本 V1 | 广州常青云科技有限公司 | 内部产品规划文件  
> 本文档是 Spec-First 开发的数据基础，OpenAPI Schema 和 gRPC Message 均从此派生

---

## 阅读说明

- 所有 PostgreSQL 表默认含 `created_at TIMESTAMPTZ DEFAULT NOW()` 和 `updated_at TIMESTAMPTZ`
- 多租户隔离通过 PostgreSQL RLS（Row-Level Security）实现，所有业务表含 `tenant_id UUID NOT NULL`
- UUID 使用 `gen_random_uuid()`（PG 13+ 内置）
- 枚举类型使用 PostgreSQL `ENUM` 或 `TEXT` + CHECK 约束（优先后者，便于扩展）
- 索引命名：`idx_{表名}_{字段名}`

---

## 零、RLS 安全基线（数据库部署规范）

> GPT 审查 P0 修复：原设计缺少 DB 角色权限、fail-closed 行为和连接池事务模式的明确规定。

### 0.1 数据库角色与权限

```sql
-- 应用连接账号（所有微服务使用此账号）
-- 关键约束：非 owner、非 superuser、非 bypassrls，受 RLS 约束
CREATE ROLE ani_app NOLOGIN;
CREATE USER ani_app_user WITH PASSWORD '...' IN ROLE ani_app;

-- 授权：只读/写业务表，不可 DDL
GRANT CONNECT ON DATABASE ani TO ani_app_user;
GRANT USAGE ON SCHEMA public TO ani_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO ani_app;
-- 注意：不授予 UPDATE ON pg_catalog 或 DDL 权限

-- 迁移账号（仅 CI/CD 时使用，不是应用账号）
CREATE ROLE ani_migrator;
GRANT ALL PRIVILEGES ON DATABASE ani TO ani_migrator;
```

### 0.2 RLS 全局策略（Fail-Closed）

**所有含 tenant_id 的业务表必须满足：**

```sql
-- 模板：每张业务表都执行以下三条命令

-- 1. 启用 RLS
ALTER TABLE {table_name} ENABLE ROW LEVEL SECURITY;

-- 2. 强制对表 owner 也生效（防止应用代码用 owner 账号绕过）
ALTER TABLE {table_name} FORCE ROW LEVEL SECURITY;

-- 3. Fail-Closed 策略：tenant 上下文为空时拒绝所有行（不返回空集，而是报错）
--    使用 current_setting(..., true) 的 missing_ok=true 参数，为空时返回 NULL
--    NULL::uuid 比较会让 USING 条件为 false，配合默认拒绝策略实现 fail-closed
CREATE POLICY tenant_isolation ON {table_name}
    AS RESTRICTIVE
    USING (
        tenant_id = NULLIF(
            current_setting('app.current_tenant_id', true), ''
        )::uuid
    );
```

**应用代码在每个事务开始时必须设置租户上下文：**

```sql
-- 每个数据库事务的第一条语句
SET LOCAL app.current_tenant_id = '<tenant_uuid>';

-- 验证：未设置时的行为
-- current_setting('app.current_tenant_id', true) 返回 ''
-- NULLIF('', '') 返回 NULL
-- NULL::uuid 导致 USING 条件为 false → 返回零行
-- 这是 fail-closed 行为：未设置上下文时看不到任何数据
```

### 0.3 PgBouncer 连接池配置要求

```ini
; PgBouncer 必须使用 transaction pooling，不能用 session pooling
; 原因：session pooling 下，SET LOCAL 可能在连接归还后残留给下一个租户
pool_mode = transaction

; 每次事务从池中取连接时，connection_init_query 不能替代 SET LOCAL
; 应用层必须在每个事务第一条 SQL 之前显式设置上下文
; 不要依赖 server_reset_query 来清理上下文（transaction mode 下不执行）
```

### 0.4 已应用 RLS 的表清单

| 表名 | ENABLE RLS | FORCE RLS | 策略名 |
|---|---|---|---|
| knowledge_bases | ✓ | ✓ | tenant_isolation |
| kb_documents | ✓ | ✓ | tenant_isolation |
| kb_sessions | ✓ | ✓ | tenant_isolation |
| kb_messages | ✓ | ✓ | tenant_isolation |
| models | ✓ | ✓ | tenant_isolation |
| model_versions | 通过 models JOIN | — | — |
| inference_services | ✓ | ✓ | tenant_isolation |
| async_tasks | ✓ | ✓ | tenant_isolation |
| api_keys | ✓ | ✓ | tenant_isolation |
| audit_logs | ✓ | ✓ | tenant_isolation |
| metering_records | ✓ | ✓ | tenant_isolation |
| outbox_events | ✓ | ✓ | tenant_isolation |

**注意：** outbox_events publisher 服务需要跨租户读取（扫描所有未发布事件），该服务使用专用角色 `ani_outbox_publisher`（bypassrls），并在 K8s NetworkPolicy 中严格限制其只能从专用 Pod 访问。

---

## 一、核心元数据库（PostgreSQL 17）

### 1.1 租户与用户

```sql
-- 租户（平台顶层隔离单元）
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

-- 用户
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

-- 角色定义
CREATE TABLE roles (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID REFERENCES tenants(id) ON DELETE CASCADE, -- NULL=平台内置角色
    name        TEXT NOT NULL,   -- platform-admin | tenant-admin | user | auditor
    permissions JSONB NOT NULL DEFAULT '[]',
    UNIQUE (tenant_id, name)
);

-- 用户角色关联
CREATE TABLE user_roles (
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_id     UUID NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    granted_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, role_id)
);

-- API Key
CREATE TABLE api_keys (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id         UUID REFERENCES users(id) ON DELETE SET NULL,
    name            TEXT NOT NULL,
    key_hash        TEXT NOT NULL UNIQUE,           -- SHA256(原始key)，不存明文
    key_prefix      TEXT NOT NULL,                  -- 展示用前缀，如 "ani_prod_xxxx"
    scopes          TEXT[] NOT NULL DEFAULT '{}',   -- 权限范围
    rate_limit_rpm  INT NOT NULL DEFAULT 60,        -- 每分钟请求限制
    expires_at      TIMESTAMPTZ,                    -- NULL = 永不过期
    last_used_at    TIMESTAMPTZ,
    revoked_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_api_keys_tenant_id ON api_keys(tenant_id);

-- JWT 黑名单（吊销的 Token）
CREATE TABLE jwt_blocklist (
    jti         TEXT PRIMARY KEY,                   -- JWT ID
    expires_at  TIMESTAMPTZ NOT NULL,               -- Token 原始过期时间（用于清理）
    revoked_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_jwt_blocklist_expires ON jwt_blocklist(expires_at);
```

### 1.2 模型仓库

```sql
-- 模型
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

-- 模型版本
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

-- 模型导入任务
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
```

### 1.3 推理服务

```sql
-- 推理服务实例
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

-- 推理调用审计日志（高写入量，可考虑分区）
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
) PARTITION BY RANGE (created_at);                  -- 按月分区

-- 创建当月和下月分区（由定时任务自动维护）
CREATE TABLE inference_audit_logs_2026_05
    PARTITION OF inference_audit_logs
    FOR VALUES FROM ('2026-05-01') TO ('2026-06-01');

CREATE INDEX idx_inference_audit_tenant ON inference_audit_logs(tenant_id, created_at);
```

### 1.4 知识库

```sql
-- 知识库
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

-- 知识库文档
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

-- 知识库问答会话
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
-- （GPT 审查 P0 修复：kb_messages 只靠 session 间接隔离会在 join/缓存查询中漏条件）
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

ALTER TABLE kb_messages ENABLE ROW LEVEL SECURITY;
ALTER TABLE kb_messages FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON kb_messages
    USING (tenant_id = current_setting('app.current_tenant_id', true)::uuid);
```

### 1.5 异步任务（Saga 最小模型）

> GPT 审查 P0 修复：原表缺少幂等键、租约、重试计数、死信、补偿动作和对账字段。
> 现采用 Outbox Pattern + 任务租约保证跨组件操作的最终一致性。

```sql
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
```

**状态对账器（Reconciler）设计要点：**
```
每 60 秒运行一次，检查：
1. status='running' AND lease_until < NOW() - INTERVAL '2 minutes'
   → 任务僵死（worker 崩溃），重置为 pending，attempt_count+1
2. status='running' AND last_heartbeat_at < NOW() - INTERVAL '5 minutes'
   → worker 失活，同上处理
3. attempt_count >= max_attempts
   → 设置 status='dead_letter'，dead_letter_at=NOW()，触发告警
4. DB status='running' 但 K8s CRD 不存在
   → 执行 compensating_action（如删除孤儿 DB 记录或重建 CRD）
```

### 1.6 Region/AZ（多集群）

```sql
-- Region
CREATE TABLE regions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT NOT NULL UNIQUE,           -- cn-east | cn-north
    display_name    TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'maintenance', 'disabled')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 可用区（对应 Karmada 成员集群）
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
```

### 1.7 计量（Metering）

```sql
-- 计量记录（TimescaleDB 超表，按时间自动分区）
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

-- 转换为 TimescaleDB 超表（安装时执行）
-- SELECT create_hypertable('metering_records', 'recorded_at', chunk_time_interval => INTERVAL '1 day');

CREATE INDEX idx_metering_tenant_time ON metering_records(tenant_id, recorded_at DESC);
CREATE INDEX idx_metering_type ON metering_records(tenant_id, resource_type, recorded_at DESC);
```

### 1.8 平台配置

```sql
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

-- 审计日志（操作层面，与推理层面分开存储）
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

CREATE INDEX idx_audit_tenant ON audit_logs(tenant_id, created_at DESC);
```

---

## 二、Milvus 向量数据库 Collection 设计

### 2.1 知识库向量 Collection

每个知识库对应一个独立的 Milvus Collection，命名规则：`kb_{kb_id 去掉横杠}`

```python
# Collection Schema（Python SDK 定义）
from pymilvus import CollectionSchema, FieldSchema, DataType

kb_collection_schema = CollectionSchema(
    fields=[
        FieldSchema(name="chunk_id",     dtype=DataType.VARCHAR, max_length=64, is_primary=True),
        FieldSchema(name="doc_id",       dtype=DataType.VARCHAR, max_length=64),
        FieldSchema(name="kb_id",        dtype=DataType.VARCHAR, max_length=64),
        FieldSchema(name="tenant_id",    dtype=DataType.VARCHAR, max_length=64),
        FieldSchema(name="content",      dtype=DataType.VARCHAR, max_length=4096),  # 原文内容
        FieldSchema(name="file_name",    dtype=DataType.VARCHAR, max_length=512),
        FieldSchema(name="page_number",  dtype=DataType.INT32),
        FieldSchema(name="chunk_index",  dtype=DataType.INT32),
        FieldSchema(name="embedding",    dtype=DataType.FLOAT_VECTOR, dim=1024),    # BGE-M3 维度
    ],
    description="Knowledge base document chunks"
)

# 索引配置（HNSW，平衡召回率与性能）
index_params = {
    "metric_type": "COSINE",
    "index_type": "HNSW",
    "params": {"M": 16, "efConstruction": 200}
}

# 查询参数
search_params = {
    "metric_type": "COSINE",
    "params": {"ef": 64}     # 召回时的搜索广度
}
```

**分区策略：** 每个 Collection 按 `tenant_id` 分区，确保查询时过滤到本租户数据。

### 2.2 Milvus 资源规划

| 资源 | 开发环境 | 生产环境 |
|---|---|---|
| 部署模式 | Standalone（单节点）| Cluster（3 QueryNode + 3 DataNode）|
| Collection 数量 | 与知识库数量相同 | 同左 |
| 向量维度 | 1024（BGE-M3）| 同左 |
| 单 Collection 向量上限 | 5M（~100 万文档）| 5M（可扩展）|

---

## 三、Redis 数据结构

### 3.1 Key 命名规范

```
ani:{env}:{module}:{key}

env    = prod | staging | dev
module = auth | ratelimit | cache | session
```

### 3.2 数据结构详细设计

```
# JWT 黑名单（吊销的 Token）
Key:   ani:prod:auth:blocklist:{jti}
Type:  String（值为空字符串，仅检查存在性）
TTL:   Token 原始过期时间（自动过期清理，无需定时任务）

# 限流计数（令牌桶）
Key:   ani:prod:ratelimit:tenant:{tenant_id}:{window_minute}
Type:  String（计数器）
TTL:   60 秒（滑动窗口自动清理）

# API Key 限流
Key:   ani:prod:ratelimit:apikey:{key_id}:{window_minute}
Type:  String（计数器）
TTL:   60 秒

# 推理服务路由缓存（service_name → endpoint URL）
Key:   ani:prod:cache:inference_route:{tenant_id}:{service_name}
Type:  String（JSON）
TTL:   30 秒（推理服务状态变化时主动删除）

# 知识库 RAG 会话上下文（多轮对话历史）
Key:   ani:prod:session:kb:{session_id}
Type:  List（RPUSH 追加，LRANGE 读取最近 N 条）
TTL:   24 小时（不活跃会话自动清理）
Max:   保留最近 20 条消息（LTRIM）

# 平台品牌化配置缓存（高频读取，低频变化）
Key:   ani:prod:cache:branding
Type:  String（JSON）
TTL:   5 分钟（品牌配置修改后主动清除）

# 异步任务状态订阅（SSE 进度推送）
Key:   ani:prod:task:progress:{task_id}
Type:  Pub/Sub Channel（ANI Gateway 订阅，向 SSE 客户端推送）
```

---

## 四、MinIO Bucket 规划

| Bucket 名 | 用途 | 访问控制 | 生命周期策略 |
|---|---|---|---|
| `ani-models` | 模型文件（明文/加密）| 私有，仅后端访问 | 无自动删除 |
| `ani-datasets` | 训练/微调数据集 | 私有，仅后端访问 | 无 |
| `ani-kb-docs` | 知识库原始文档 | 私有，仅后端访问 | 无 |
| `ani-branding` | 品牌图片（Logo/Favicon）| 公开读（presigned URL）| 无 |
| `ani-patches` | 升级 Patch 包 | 私有，仅 upgrade-operator | 7 天后自动删除 |
| `ani-exports` | 用户导出文件（账单/报表）| 私有，短期 presigned URL | 3 天后自动删除 |
| `ani-backups` | 数据库备份（Velero）| 私有，仅备份系统 | 90 天后自动删除 |

**对象命名规范：**
```
ani-models:   {tenant_id}/{model_id}/{version}/{filename}
ani-kb-docs:  {tenant_id}/{kb_id}/{doc_id}/{filename}
ani-branding: branding/{config_key}/{filename}
```

---

## 五、NATS JetStream Subject 规划

### 5.1 Subject 命名规范

```
ani.{domain}.{action}

domain: tasks | events | alerts
```

### 5.2 Stream 与 Consumer 配置

```yaml
# 异步任务 Stream
Stream:
  name: ANI_TASKS
  subjects: [ani.tasks.>]
  retention: WorkQueuePolicy    # 消费后删除
  max_age: 24h
  storage: File
  replicas: 3

# Subject 清单
ani.tasks.model.import          # 模型导入任务
ani.tasks.kb.parse              # 文档解析任务
ani.tasks.kb.index              # 向量化任务
ani.tasks.model.finetune        # 模型微调任务
ani.tasks.model.encrypt         # 模型加密任务
ani.tasks.export.generate       # 报表生成任务

# 平台事件 Stream（用于 Webhook 回调和 SSE 推送）
Stream:
  name: ANI_EVENTS
  subjects: [ani.events.>]
  retention: InterestPolicy
  max_age: 1h

# Subject 清单
ani.events.task.{task_id}.progress     # 任务进度更新
ani.events.task.{task_id}.completed    # 任务完成
ani.events.task.{task_id}.failed       # 任务失败
ani.events.inference.{svc_id}.status   # 推理服务状态变化
```

---

## 六、数据库迁移规范

**工具：** Atlas（Go 原生，支持声明式 Schema 管理）

**版本策略：** 数据库迁移兼容性判定以 `ANI-12-版本管理策略.md` 为准。

**迁移文件命名：**
```
migrations/
├── 20260501_001_init_schema.sql
├── 20260601_002_add_regions.sql
└── 20260701_003_add_metering.sql
```

**迁移原则：**
- 每次发布只做加法（新增表/列）和改名，不做破坏性删除
- 删除操作分两步：先废弃（应用层停止使用），下个版本再物理删除
- 所有迁移脚本必须是幂等的（可重复执行）
- 大表加列使用 `ALTER TABLE ... ADD COLUMN ... DEFAULT ...`（PG 11+ 支持在线加列）
- `PATCH` 和 `MINOR` 版本默认只允许向后兼容迁移；需要人工处理、停机窗口或不可自动回滚的迁移必须升级 MAJOR 或延期。

---

## 七、初始化数据

安装完成后自动写入：

```sql
-- 系统内置角色
INSERT INTO roles (id, tenant_id, name, permissions) VALUES
    (gen_random_uuid(), NULL, 'platform-admin',
     '["*"]'),
    (gen_random_uuid(), NULL, 'tenant-admin',
     '["tenant:read","models:*","kb:*","inference:*","users:*","audit:read"]'),
    (gen_random_uuid(), NULL, 'user',
     '["models:read","kb:read","kb:query","inference:invoke"]'),
    (gen_random_uuid(), NULL, 'auditor',
     '["audit:read","metering:read","models:read"]');

-- 默认品牌配置
INSERT INTO platform_branding (platform_name, primary_color, secondary_color)
VALUES ('KuberCloud ANI', '#1677FF', '#13C2C2');

-- 平台默认设置
INSERT INTO platform_settings (key, value, description) VALUES
    ('metering.collection_interval_seconds', '60', '计量数据采集间隔'),
    ('inference.default_max_tokens', '4096', '推理默认最大 Token 数'),
    ('kb.default_top_k', '5', '知识库默认召回数量'),
    ('kb.default_score_threshold', '0.3', '知识库最低相关性分数');
```
