-- ANI Platform · Platform email/username uniqueness rules
-- Version: 20260901000100
-- Description:
--   1) 平台账号（tenant_id IS NULL）允许 email 重复（删除 idx_users_platform_email）
--   2) username 唯一仅约束未软删行，软删后可复用同名（重建 idx_users_platform_username）
-- Depends on: 20260707001400_platform_users.sql、20260827000200_users_display_name_soft_delete.sql
-- Rationale:
--   产品规则：平台运营账号 email 可重复；username（local: 前缀）对活跃账号唯一。
--   软删后 is_deleted=TRUE 的行不应继续占用 username 唯一槽位，否则重建同名会撞索引变 500。

DROP INDEX IF EXISTS idx_users_platform_email;

DROP INDEX IF EXISTS idx_users_platform_username;
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_platform_username
    ON users(username) WHERE tenant_id IS NULL AND is_deleted = FALSE;

-- ===========================================================================
-- Rollback
-- ===========================================================================
-- DROP INDEX IF EXISTS idx_users_platform_username;
-- CREATE UNIQUE INDEX IF NOT EXISTS idx_users_platform_username
--     ON users(username) WHERE tenant_id IS NULL;
-- CREATE UNIQUE INDEX IF NOT EXISTS idx_users_platform_email
--     ON users(email) WHERE tenant_id IS NULL;
