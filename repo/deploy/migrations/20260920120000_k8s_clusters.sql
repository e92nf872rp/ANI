-- ANI Platform · Migration: k8s_clusters
-- Description: M1-K8S-K K8s cluster record persistence
-- Depends on: 20260523000800_k8s_cluster_proxy_targets.sql


CREATE TABLE IF NOT EXISTS k8s_clusters (
    tenant_id               UUID        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    cluster_id              TEXT        NOT NULL,
    name                    TEXT        NOT NULL,
    version                 TEXT        NOT NULL DEFAULT '',
    state                   TEXT        NOT NULL,
    reason                  TEXT        NOT NULL DEFAULT '',
    provider                TEXT        NOT NULL DEFAULT '',
    real_provider           BOOLEAN     NOT NULL DEFAULT FALSE,
    provider_refs           JSONB       NOT NULL DEFAULT '[]'::jsonb,
    create_idempotency_key  TEXT,
    upgrade_idempotency_key TEXT,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, cluster_id),
    CONSTRAINT k8s_clusters_state_check
        CHECK (state IN ('provisioning', 'running', 'deleting'))
);

-- ANI-02 §2.1.2 定义 v1.0.0 多租户隔离为「每租户一个 vCluster」，vcluster syncer 亦拒绝
-- 同一 namespace 内的多个虚拟集群。该唯一索引把产品约束沉淀为数据库层不可绕过的约束，
-- 与 localK8sClusterService 内存校验、vCluster Helm provider 底座守卫形成三层防护。
CREATE UNIQUE INDEX IF NOT EXISTS idx_k8s_clusters_tenant_unique
    ON k8s_clusters (tenant_id);

-- 幂等键随集群行保存：网关重启后同幂等键重放仍能返回原记录，而不是被唯一约束顶成冲突。
CREATE UNIQUE INDEX IF NOT EXISTS idx_k8s_clusters_tenant_create_idem
    ON k8s_clusters (tenant_id, create_idempotency_key)
    WHERE create_idempotency_key IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_k8s_clusters_tenant_upgrade_idem
    ON k8s_clusters (tenant_id, upgrade_idempotency_key)
    WHERE upgrade_idempotency_key IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_k8s_clusters_tenant_updated
    ON k8s_clusters (tenant_id, updated_at DESC);

GRANT SELECT, INSERT, UPDATE, DELETE ON
    k8s_clusters
TO ani_app;

ALTER TABLE k8s_clusters ENABLE ROW LEVEL SECURITY;
ALTER TABLE k8s_clusters FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS k8s_clusters_platform_bypass ON k8s_clusters;
CREATE POLICY k8s_clusters_platform_bypass ON k8s_clusters
    AS PERMISSIVE
    FOR ALL
    USING (current_setting('app.current_tenant_id', true) IS NULL);
DROP POLICY IF EXISTS k8s_clusters_self ON k8s_clusters;
CREATE POLICY k8s_clusters_self ON k8s_clusters
    AS PERMISSIVE
    FOR ALL
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);
DROP POLICY IF EXISTS tenant_isolation ON k8s_clusters;
CREATE POLICY tenant_isolation ON k8s_clusters
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

COMMENT ON TABLE k8s_clusters IS
    'Tenant-scoped K8s/vCluster control-plane records. Persisted so gateway restarts keep cluster visibility; UNIQUE (tenant_id) enforces the ANI-02 §2.1.2 one-vCluster-per-tenant rule.';
COMMENT ON COLUMN k8s_clusters.create_idempotency_key IS
    'Client idempotency key of the create request that produced this row; lets a replay after gateway restart return the original record instead of a conflict.';
COMMENT ON COLUMN k8s_clusters.upgrade_idempotency_key IS
    'Client idempotency key of the last accepted in-place upgrade request.';