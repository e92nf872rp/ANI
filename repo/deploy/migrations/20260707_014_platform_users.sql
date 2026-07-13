-- ANI Platform · Migration 20260707_001
-- Description: Platform admin users (independent from tenant users) + refresh_tokens.user_id index
-- Depends on: 20260501_001_init_schema.sql
-- Rationale:
--   init_schema.users.tenant_id is NOT NULL, so platform admins cannot live in `users`.
--   Use a separate `platform_users` table to keep platform/tenant storage fully isolated.
--   `users.password_hash` already exists (init_schema line 55); no change to `users` here.
--   `refresh_tokens` already has tenant_id and expires_at indexes (init_schema lines 119-120),
--   but no user_id index; add one for RevokeAllForUser and similar lookups.

BEGIN;

-- ===========================================================================
-- 1. PLATFORM_USERS
--    Platform administrators. Stored independently from tenant `users`.
--    No FK to `tenants` — platform scope has no tenant.
-- ===========================================================================

CREATE TABLE IF NOT EXISTS platform_users (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    username        TEXT        NOT NULL UNIQUE,
    email           TEXT        NOT NULL UNIQUE,
    password_hash   TEXT        NOT NULL,                       -- bcrypt; platform admins always have a password
    status          TEXT        NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'disabled')),
    last_login_at   TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_platform_users_status ON platform_users(status);

-- ===========================================================================
-- 2. REFRESH_TOKENS.USER_ID INDEX
--    init_schema indexes tenant_id and expires_at; user_id is missing.
--    Used by RevokeAllForUser and related lookups.
-- ===========================================================================

CREATE INDEX IF NOT EXISTS idx_refresh_tokens_user_id ON refresh_tokens(user_id);

COMMIT;

-- ===========================================================================
-- Rollback
-- ===========================================================================
-- DROP INDEX IF EXISTS idx_refresh_tokens_user_id;
-- DROP TABLE IF EXISTS platform_users;
