-- ANI Platform · Migration 20260707_014
-- Description: Allow NULL tenant_id on refresh_tokens for platform admin refresh tokens
--              and restore refresh_tokens.user_id FK to users(id)
-- Depends on: 20260501_001_init_schema.sql, 20260707_014_platform_users.sql
-- Rationale:
--   init_schema.refresh_tokens.tenant_id is NOT NULL with FK to tenants(id).
--   init_schema.refresh_tokens.user_id has FK to users(id).
--   Platform admins now live in `users` with tenant_id IS NULL (see migration
--   20260707_014_platform_users.sql). Platform refresh tokens are stored with
--   tenant_id=NULL, user_id referencing the platform admin's row in `users`.


BEGIN;

-- 1. Make tenant_id nullable to allow platform refresh tokens (tenant_id=NULL).
ALTER TABLE refresh_tokens ALTER COLUMN tenant_id DROP NOT NULL;

-- 2. 修改 RLS 策略：允许平台 refresh token (tenant_id IS NULL) 通过。
--    原 RESTRICTIVE 策略 `tenant_id = NULLIF(current_setting(...))::uuid` 对 NULL 行
--    求值为 NULL（非 TRUE），导致平台管理员 INSERT 被 RLS 拒绝（生产 ani_app_user
--    无 BYPASSRLS；dev superuser 绕过 RLS 掩盖了此 bug）。
--    修改后：tenant_id IS NULL 的行（平台）直接放行；tenant_id NOT NULL 的行
--    仍要求 app.current_tenant_id 匹配（租户隔离不变）。
--    该 USING 同时作为 INSERT 的 WITH CHECK（无独立 WITH CHECK 子句），
--    所以平台 INSERT 也会通过。
DROP POLICY IF EXISTS tenant_isolation ON refresh_tokens;
CREATE POLICY tenant_isolation ON refresh_tokens
    AS RESTRICTIVE
    USING (tenant_id IS NULL
           OR tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);


COMMIT;

-- ===========================================================================
-- Rollback
-- ===========================================================================
-- ALTER TABLE refresh_tokens DROP CONSTRAINT IF EXISTS refresh_tokens_user_id_fkey;
-- ALTER TABLE refresh_tokens ALTER COLUMN tenant_id SET NOT NULL;
-- DROP POLICY IF EXISTS tenant_isolation ON refresh_tokens;
-- CREATE POLICY tenant_isolation ON refresh_tokens
--     AS RESTRICTIVE
--     USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);
