-- ANI Platform · Migration 20260825_001
-- Description: tenant_admin_invitation 加部分唯一索引，防止同一租户下同一用户存在多条 pending 邀请。
--              并发邀请竞态防护：DB 层兜底，service 层无需事务/幂等键。
-- Depends on: 20260821_001_tenant_admin_invitation.sql
-- Rationale:
--   HasPendingInvitation 与 InsertInvitation 是两次独立连接，存在 TOCTOU 竞态窗口。
--   部分唯一索引 (tenant_id, user_id) WHERE status='inviting' 让并发 INSERT 第二条直接冲突，
--   service 层捕获 23505 后返回 TENANT_INVITATION_PENDING。
--   该索引同时覆盖 HasPendingInvitation 查询（前缀 tenant_id+user_id + status 条件），
--   替换原 idx_tenant_admin_invitation_user_status。

BEGIN;

-- 1. 删除旧索引（user_id, status)——不含 tenant_id，查询需回表过滤
DROP INDEX IF EXISTS idx_tenant_admin_invitation_user_status;

-- 2. 新建部分唯一索引：同一租户下同一用户仅允许一条 inviting 邀请
CREATE UNIQUE INDEX IF NOT EXISTS uk_tenant_admin_invitation_pending
    ON tenant_admin_invitation (tenant_id, user_id)
    WHERE status = 'inviting';

COMMIT;

-- ===========================================================================
-- Rollback
-- ===========================================================================
-- DROP INDEX IF EXISTS uk_tenant_admin_invitation_pending;
-- CREATE INDEX idx_tenant_admin_invitation_user_status
--     ON tenant_admin_invitation(user_id, status);
