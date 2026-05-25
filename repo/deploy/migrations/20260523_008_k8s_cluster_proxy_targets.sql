-- ANI Platform · Migration 008
-- Description: M1-K8S-PROXY-C K8s cluster proxy target persistence
-- Depends on: 20260520_007_workload_identity_api_keys.sql

BEGIN;

CREATE TABLE IF NOT EXISTS k8s_cluster_proxy_targets (
    tenant_id       UUID        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    cluster_id      TEXT        NOT NULL,
    server          TEXT        NOT NULL,
    bearer_token    TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, cluster_id)
);

CREATE INDEX IF NOT EXISTS idx_k8s_cluster_proxy_targets_tenant_updated
    ON k8s_cluster_proxy_targets (tenant_id, updated_at DESC);

GRANT SELECT, INSERT, UPDATE, DELETE ON
    k8s_cluster_proxy_targets
TO ani_app;

ALTER TABLE k8s_cluster_proxy_targets ENABLE ROW LEVEL SECURITY;
ALTER TABLE k8s_cluster_proxy_targets FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON k8s_cluster_proxy_targets;
CREATE POLICY tenant_isolation ON k8s_cluster_proxy_targets
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

COMMENT ON TABLE k8s_cluster_proxy_targets IS
    'Tenant-scoped K8s/vCluster API Server targets used by the Core proxy forwarding adapter.';
COMMENT ON COLUMN k8s_cluster_proxy_targets.bearer_token IS
    'Current metadata-backed bridge token for proxy forwarding; production deployments should move this secret material behind KMS or a Kubernetes Secret provider.';

COMMIT;
