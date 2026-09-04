-- ANI Platform · Migration 20260811000300
-- Description: 配额变更申请表 — tenant_quota_change
--              支撑 US-012（提交配额变更申请）/ US-013（查询申请列表）/ US-014（审批申请）
--              以及 GetApprovedQuotaChanges（绑定套餐/修改限额同步时跳过 approved 维度）
-- Depends on: 20260501000100_init_schema.sql (tenants, users), 20260810000100_resource_quota.sql (resource_quota_meta)
-- Rationale:
--   BOSS 租户配额变更审批流（plan v3.0 §4.1.6）：
--     - tenant_quota_change  记录某租户某配额维度的变更申请；一次 HTTP 提交的多维度
--                          共享同一 request_id（复合主键含 request_id + resource_type）。
--                          status: pending | approved | rejected
--                          审批按 request_id 整批处理（同批全部维度一并通过/驳回）。
--                          approved 状态的维度在套餐绑定/限额修改同步时被跳过
--                         （保留 approved 值不被套餐模板覆盖）。
--     - 外键 → tenants (ON DELETE CASCADE，租户删除时清理申请记录)
--              users (requested_by NOT NULL / reviewed_by 可空)
--     - resource_type 不建外键（Core resource_quota_meta 与 BOSS 表跨库）
--     - RLS: 平台操作绕过 RLS；租户上下文只能看自己的申请（Console 自助场景）
--   平台治理数据，应用层 RBAC 限制为 platform-admin/ops；
--   tenant-service 以 ani_app_user 连接 DB，故仍需表级 GRANT。


-- ===========================================================================
-- 1. tenant_quota_change — 配额变更申请表
-- ===========================================================================
CREATE TABLE tenant_quota_change (
    tenant_id       UUID        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    request_id      UUID        NOT NULL,                    -- 同一次提交共享；审批路径 /quota-requests/{reqId} 对齐此列
    resource_type   TEXT        NOT NULL,                    -- 配额维度（如 storage_gb / gpu_count / cpu_core / memory_gb）
    old_value       BIGINT,                                  -- 变更前配额值（NULL=首次设置）
    new_value       BIGINT      NOT NULL,                    -- 申请变更为的配额值
    requested_by    UUID        NOT NULL REFERENCES users(id) ON DELETE RESTRICT,  -- 申请人 user_id
    status          TEXT        NOT NULL DEFAULT 'pending',  -- pending / approved / rejected
    reviewed_by     UUID        REFERENCES users(id) ON DELETE RESTRICT,           -- 审核人 user_id（NULL=未审核）
    reviewed_at     TIMESTAMPTZ,                             -- 审核时间（NULL=未审核）
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, request_id, resource_type),
    CONSTRAINT chk_quota_change_status CHECK (status IN ('pending', 'approved', 'rejected'))
);

CREATE INDEX idx_tenant_quota_change_tenant ON tenant_quota_change(tenant_id);
CREATE INDEX idx_tenant_quota_change_status ON tenant_quota_change(status);
CREATE INDEX idx_tenant_quota_change_requested_by ON tenant_quota_change(requested_by);
CREATE INDEX idx_tenant_quota_change_request ON tenant_quota_change(tenant_id, request_id);

-- ===========================================================================
-- 2. RLS — 平台操作绕过 / 租户只能看自己
-- ===========================================================================
ALTER TABLE tenant_quota_change ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_quota_change FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_quota_change_platform_bypass
    ON tenant_quota_change
    USING (current_setting('app.current_tenant_id', true) IS NULL);

CREATE POLICY tenant_quota_change_self_read
    ON tenant_quota_change
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

-- GRANT 表级读写给 ani_app_user
GRANT SELECT, INSERT, UPDATE, DELETE ON tenant_quota_change TO ani_app_user;


-- ===========================================================================
-- Rollback
-- ===========================================================================
-- DROP POLICY IF EXISTS tenant_quota_change_self_read ON tenant_quota_change;
-- DROP POLICY IF EXISTS tenant_quota_change_platform_bypass ON tenant_quota_change;
-- ALTER TABLE tenant_quota_change NO FORCE ROW LEVEL SECURITY;
-- ALTER TABLE tenant_quota_change DISABLE ROW LEVEL SECURITY;
-- DROP TABLE IF EXISTS tenant_quota_change;
