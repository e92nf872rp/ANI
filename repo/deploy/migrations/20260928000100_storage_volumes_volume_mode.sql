-- ANI Platform · Migration 20260928000100
-- Description: storage_volumes 新增 volume_mode 列（Kubernetes volumeMode），区分
--              Block（VM 数据盘裸设备在线热插）与 Filesystem（容器/GPU 容器按
--              目录挂载）两种模式。存量卷统一回填为 filesystem。
-- Depends on: 20260803000100_storage_control_plane_state.sql
-- Background:
--   现状 storage_volumes 没有 volume_mode，storage_renderer 一律把 PVC 渲染为
--   volumeMode: Filesystem。VM 数据盘需要在 guest 内以裸设备呈现并支持在线热插，
--   而 KubeVirt 对 Filesystem 卷热插要求卷内已存在 disk.img，空盘必然失败（现场
--   表现为 virt-handler 反复重试）。本迁移固化 volume_mode 列，代码据此渲染 PVC
--   的 volumeMode。volumeMode 在 Kubernetes 上不可变，存量 Filesystem 卷无法原地
--   切换，只能删除重建；因此历史行统一回填 filesystem，与既有 PVC 保持一致。

-- ===========================================================================
-- 1. 新增 volume_mode 列（可空，兼容历史行与空值兜底）
-- ===========================================================================
ALTER TABLE storage_volumes
    ADD COLUMN IF NOT EXISTS volume_mode TEXT;

-- ===========================================================================
-- 2. 回填历史行为 filesystem
--    历史 PVC 全部以 volumeMode: Filesystem 创建，回填保证控制面与 provider 一致。
--    重放安全：只更新 NULL 行。
-- ===========================================================================
UPDATE storage_volumes
SET volume_mode = 'filesystem'
WHERE volume_mode IS NULL;

-- ===========================================================================
-- 3. 取值约束（与 OpenAPI CreateStorageVolumeRequest.volume_mode 枚举对齐）
--    CHECK 允许 NULL：控制面渲染与归一都会把空值兜底为 filesystem。
-- ===========================================================================
DO $$
BEGIN
    ALTER TABLE storage_volumes
        ADD CONSTRAINT storage_volumes_volume_mode_check
        CHECK (volume_mode IS NULL OR volume_mode IN ('block', 'filesystem'));
EXCEPTION
    WHEN duplicate_object THEN NULL;
END $$;

-- ===========================================================================
-- 验证（手动执行，不在迁移内）
-- ===========================================================================
-- SELECT column_name, is_nullable, data_type
--   FROM information_schema.columns
--  WHERE table_name = 'storage_volumes' AND column_name = 'volume_mode';
-- SELECT conname FROM pg_constraint
--  WHERE conname = 'storage_volumes_volume_mode_check';
-- SELECT volume_mode, count(*) FROM storage_volumes GROUP BY volume_mode;
--
-- ===========================================================================
-- Rollback
-- ===========================================================================
-- ALTER TABLE storage_volumes DROP CONSTRAINT IF EXISTS storage_volumes_volume_mode_check;
-- ALTER TABLE storage_volumes DROP COLUMN IF EXISTS volume_mode;