-- ANI Platform · BOSS 预留账本表
-- Description: resource_reservation_allocations stores tenant-level allocated_gpu_count
--              (single dimension, no per-spec split). PK tenant_id, CHECK >= 0.
--              RLS enabled for tenant isolation (consistent with resource_quota / resource_reservations).
--              RLS 模式对齐 20260810000100_resource_quota.sql：
--                - PERMISSIVE platform_bypass: 平台上下文（app.current_tenant_id 为 NULL）→ 全部可见
--                - PERMISSIVE tenant_self: 租户上下文 → 仅自己的行
--                - 无 RESTRICTIVE 策略（与 resource_quota 一致，避免无 PERMISSIVE 导致全拒）
--              GRANT DML 权限给 ani_app_user（与 resource_quota / resource_reservations 一致）。
-- Rollback: DROP TABLE resource_reservation_allocations

CREATE TABLE IF NOT EXISTS resource_reservation_allocations (
    tenant_id           UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    allocated_gpu_count BIGINT NOT NULL DEFAULT 0 CHECK (allocated_gpu_count >= 0),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id)
);

-- Row Level Security: 对齐 resource_quota 的 PERMISSIVE 双策略模式
ALTER TABLE resource_reservation_allocations ENABLE ROW LEVEL SECURITY;
ALTER TABLE resource_reservation_allocations FORCE ROW LEVEL SECURITY;

-- 平台上下文（app.current_tenant_id 未设置/为空）→ 全部可见（BOSS 管理端 PutReservation / GetReservation）
CREATE POLICY resource_reservation_allocations_platform_bypass
  ON resource_reservation_allocations FOR ALL
  USING (current_setting('app.current_tenant_id', true) IS NULL);

-- 租户上下文 → 仅自己的行（普通用户创建实例时 GetReservationTx 闸 2 预留检查）
CREATE POLICY resource_reservation_allocations_self
  ON resource_reservation_allocations FOR ALL
  USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

-- Grants — 应用用户权限分配（对齐 20260810000100 resource_quota / resource_reservations）
GRANT SELECT, INSERT, UPDATE, DELETE ON resource_reservation_allocations TO ani_app_user;

