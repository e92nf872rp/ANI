-- ANI Platform · Migration 20260707_014
-- Description: Unify platform admins into users table (tenant_id IS NULL) + refresh_tokens.user_id index
-- Depends on: 20260501_001_init_schema.sql
-- Rationale:
--   init_schema.users.tenant_id is NOT NULL with FK to tenants(id), preventing platform
--   admins (which have no tenant) from living in `users`. We previously maintained a
--   separate `platform_users` table; per the 2026-07-15 decision, platform admins now
--   live in `users` with `tenant_id IS NULL`, distinguished from tenant users by
--   `user_roles` (roles.tenant_id IS NULL = platform built-in role) rather than by
--   physical table separation.
--
--   This migration:
--     1. Drops the FK on users.tenant_id (so NULL is allowed) and makes tenant_id NULLABLE.
--     2. Drops the (tenant_id, username) and (tenant_id, email) UNIQUE constraints and
--        replaces them with:
--          - UNIQUE (tenant_id, username) and (tenant_id, email) for tenant users
--          - UNIQUE (username) and (email) partial indexes WHERE tenant_id IS NULL
--        so platform admins are globally unique by username/email while tenant users
--        remain unique only within their tenant.
--     3. Adds the missing idx_refresh_tokens_user_id index (used by RevokeAllForUser).
--
--   refresh_tokens.tenant_id/user_id FKs are NOT touched here. A later migration
--   (20260707_014_platform_refresh_tokens.sql) loosens refresh_tokens.tenant_id to
--   NULLABLE and restores the refresh_tokens.user_id FK to users(id).

BEGIN;

-- ===========================================================================
-- 1. USERS: allow tenant_id NULL for platform admins
-- ===========================================================================

-- 1a. Drop the FK on users.tenant_id (platform admins have no tenant).
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_tenant_id_fkey;

-- 1b. Allow tenant_id to be NULL.
ALTER TABLE users ALTER COLUMN tenant_id DROP NOT NULL;

-- 1c. Drop the old UNIQUE (tenant_id, username) and UNIQUE (tenant_id, email).
--     init_schema does not name these constraints; we drop by the column signature.
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_tenant_id_username_key;
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_tenant_id_email_key;

-- 1d. Re-create per-tenant uniqueness (NULLs are distinct by default in PostgreSQL,
--     so tenant users keep the same uniqueness semantics as before).
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_tenant_username
    ON users(tenant_id, username);

CREATE UNIQUE INDEX IF NOT EXISTS idx_users_tenant_email
    ON users(tenant_id, email);

-- 1e. Add global uniqueness for platform admins (tenant_id IS NULL).
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_platform_username
    ON users(username) WHERE tenant_id IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_users_platform_email
    ON users(email) WHERE tenant_id IS NULL;

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
-- DROP INDEX IF EXISTS idx_users_platform_email;
-- DROP INDEX IF EXISTS idx_users_platform_username;
-- DROP INDEX IF EXISTS idx_users_tenant_email;
-- DROP INDEX IF EXISTS idx_users_tenant_username;
-- ALTER TABLE users ALTER COLUMN tenant_id SET NOT NULL;
-- ALTER TABLE users ADD CONSTRAINT users_tenant_id_fkey
--     FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE;
-- ALTER TABLE users ADD CONSTRAINT users_tenant_id_username_key UNIQUE (tenant_id, username);
-- ALTER TABLE users ADD CONSTRAINT users_tenant_id_email_key UNIQUE (tenant_id, email);
