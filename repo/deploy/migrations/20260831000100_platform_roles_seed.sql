-- ANI Platform · Platform ops/readonly role seed
-- Version: 20260831000100
-- Description: 向 roles 表插入平台内置角色种子 platform-ops / platform-readonly（tenant_id NULL）
-- Depends on: 20260502000300_permissions_schema.sql（roles_permissions_schema CHECK + platform-admin 种子）
-- Rationale:
--   平台运营账号模块需要三个平台角色：platform-admin（已由 20260502000300 种子）、
--   platform-ops、platform-readonly。本迁移仅补齐后两者；不含 users 列扩展、索引或 RLS。
--
--   与早期 SPEC 草稿的差异：
--     1. 文件名按 Atlas 14 位版本号约定：20260831000100（对应 Issue 草稿 20260831_001）。
--     2. roles.permissions 中「无权限」用省略条目表达，不写 "actions":[]——
--        roles_permissions_schema CHECK 要求 actions 为非空数组。
--        平台运营角色 resource 维度：tenants（租户）/ resource_pool（资源池）/
--        users（平台账号）/ metering（计量）；无权限则不写入对应条目。
--     3. UNIQUE (tenant_id, name) 对 NULL tenant_id 不按重复处理（见 20260502000300 注释），
--        因此幂等写入使用固定 id 的 ON CONFLICT (id) DO UPDATE，而非 ON CONFLICT (tenant_id, name)。
--        若库中已存在同名不同 id 的行（手工建库/半迁移状态），ON CONFLICT (id) 无法拦截，
--        故再加 WHERE NOT EXISTS (tenant_id IS NULL AND name = ...) 双保险（同 20260502000300 先例）。
--        注意：本分支与 DO UPDATE 互斥——已按固定 id 重放时走 UPDATE，同名异 id 时不插入。
--
-- 事务：Atlas 默认按文件包裹事务，本文件不写 BEGIN/COMMIT。
-- Run: atlas migrate apply  OR  psql $DATABASE_URL -f <this_file>

-- ===========================================================================
-- 1) platform-ops：租户/资源池全权限，计量只读，不可管理平台账号
-- ===========================================================================

INSERT INTO roles (id, tenant_id, name, permissions)
SELECT '00000000-0000-0000-0000-000000000006', NULL, 'platform-ops', '
    [
        {"resource":"tenants","actions":["*"],"scope":"platform"},
        {"resource":"resource_pool","actions":["*"],"scope":"platform"},
        {"resource":"metering","actions":["read","list"],"scope":"platform"}
    ]'::jsonb
WHERE NOT EXISTS (
    SELECT 1 FROM roles WHERE tenant_id IS NULL AND name = 'platform-ops'
)
ON CONFLICT (id) DO UPDATE
SET permissions = EXCLUDED.permissions,
    name = EXCLUDED.name;

-- ===========================================================================
-- 2) platform-readonly：租户/资源池/计量仅 read+list
-- ===========================================================================

INSERT INTO roles (id, tenant_id, name, permissions)
SELECT '00000000-0000-0000-0000-000000000007', NULL, 'platform-readonly', '
    [
        {"resource":"tenants","actions":["read","list"],"scope":"platform"},
        {"resource":"resource_pool","actions":["read","list"],"scope":"platform"},
        {"resource":"metering","actions":["read","list"],"scope":"platform"}
    ]'::jsonb
WHERE NOT EXISTS (
    SELECT 1 FROM roles WHERE tenant_id IS NULL AND name = 'platform-readonly'
)
ON CONFLICT (id) DO UPDATE
SET permissions = EXCLUDED.permissions,
    name = EXCLUDED.name;

-- ===========================================================================
-- Rollback（手动执行；先确认无 user_roles 引用）
-- ===========================================================================
-- DELETE FROM roles
-- WHERE id IN ('00000000-0000-0000-0000-000000000006',
--              '00000000-0000-0000-0000-000000000007');
