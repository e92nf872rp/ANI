-- GPU 设备台账两表：状态覆盖（reason/unavailable/maintenance 持久化）、
-- 设备事件流（BOSS 联动事件块）。
--
-- 设计依据 repo/design/gpu-pool-status-surface-gap-plan.md（D3/D4 决策）：
--   - D3：status enum 加 unavailable（fault=自动检测故障，unavailable=人工标记）
--   - D4：reason 持久化在 PG 平台账，不写节点 annotation
--
-- GPU 预留为数量型语义（PUT /admin/tenants/{tenant_id}/reservations，
-- 见 20260812000200_resource_reservation_allocations.sql），不做设备级分配。
--
-- 表性质：全部为平台级台账（BOSS 写读；租户路径不直接读这两张表）。
-- RLS 采用 platform_bypass 单策略：未设置 app.current_tenant_id（WithPlatformTx）
-- 时可见全部行；租户上下文（WithTenantTx）下不可见（平台数据不暴露给租户 RLS 读）。
--
-- 设备身份口径：device_id 是 K8s 节点 × 物理卡 index × 型号派生的稳定 UUID
-- （uuid.NewSHA1(NameSpaceOID, node/index/model)），与 GET /gpu-inventory 的
-- 记录 id 一致；节点级事件（集群切分）device_id 为 NULL。

BEGIN;

CREATE TABLE IF NOT EXISTS gpu_device_overlays (
    device_id   uuid PRIMARY KEY,
    status      text NOT NULL CHECK (status IN ('maintenance', 'unavailable')),
    reason      text,
    updated_by  text,
    created_at  timestamptz NOT NULL DEFAULT NOW(),
    updated_at  timestamptz NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS gpu_device_events (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id   uuid,
    node_name   text,
    gpu_type    text,
    event_type  text NOT NULL CHECK (event_type IN
        ('status_changed', 'partition_applied')),
    reason      text,
    actor       text,
    created_at  timestamptz NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS gpu_device_events_created_idx
    ON gpu_device_events (created_at DESC);
CREATE INDEX IF NOT EXISTS gpu_device_events_device_idx
    ON gpu_device_events (device_id, created_at DESC);

-- RLS：平台台账单策略（platform_bypass）。与 20260831_001 的双策略形式相比，
-- 这两张表没有租户自读路径，故不需要 self 策略；保留 NULLIF 形式以对齐
-- 池化连接 GUC 残留的既有口径。
ALTER TABLE gpu_device_overlays ENABLE ROW LEVEL SECURITY;
ALTER TABLE gpu_device_events ENABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS gpu_device_overlays_platform_bypass ON gpu_device_overlays;
CREATE POLICY gpu_device_overlays_platform_bypass
  ON gpu_device_overlays FOR ALL
  USING (NULLIF(current_setting('app.current_tenant_id', true), '') IS NULL);

DROP POLICY IF EXISTS gpu_device_events_platform_bypass ON gpu_device_events;
CREATE POLICY gpu_device_events_platform_bypass
  ON gpu_device_events FOR ALL
  USING (NULLIF(current_setting('app.current_tenant_id', true), '') IS NULL);

GRANT SELECT, INSERT, UPDATE, DELETE ON gpu_device_overlays TO ani_app;
GRANT SELECT, INSERT ON gpu_device_events TO ani_app;

COMMIT;

-- ===========================================================================
-- Rollback
-- ===========================================================================
-- DROP TABLE IF EXISTS gpu_device_events;
-- DROP TABLE IF EXISTS gpu_device_overlays;
