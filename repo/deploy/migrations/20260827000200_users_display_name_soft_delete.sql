-- ANI Platform · Users display name and soft-delete fields
-- Version: 20260827000200
-- Description: 扩展 users 表，支持展示名和软删除
-- Depends on: 20260501000100_init_schema.sql (users 表)

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS display_name TEXT;

COMMENT ON COLUMN users.display_name IS
    '租户管理员展示名（昵称）；NULL 表示使用 username';

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS is_deleted BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;

COMMENT ON COLUMN users.is_deleted IS
    '软删除标记，与 users.status(active/disabled) 独立';

COMMENT ON COLUMN users.deleted_at IS
    '软删除时间；未删除时为 NULL';
