-- ANI Platform · Migration 20260707_014
-- Description: Allow NULL tenant_id on refresh_tokens for platform admin refresh tokens
-- Depends on: 20260501_001_init_schema.sql, 20260707_014_platform_users.sql
-- Rationale:
--   init_schema.refresh_tokens.tenant_id is NOT NULL with FK to tenants(id).
--   init_schema.refresh_tokens.user_id has FK to users(id).
--   Platform admins live in platform_users (separate table, no tenant).
--   This migration loosens tenant_id to NULLABLE so platform refresh tokens
--   can be stored with tenant_id=NULL.
--
--   We intentionally do NOT add a second FK from refresh_tokens.user_id to
--   platform_users(id). PostgreSQL FKs with MATCH SIMPLE fire independently:
--   a non-NULL user_id would have to satisfy both FKs simultaneously, which
--   is impossible when users and platform_users are independent UUID spaces.
--   Tenant vs platform refresh token isolation is enforced at the application
--   layer (auth-service platformLoginManager / passwordLoginManager), which
--   writes user_id referencing the correct table per scope.

BEGIN;

-- 1. Make tenant_id nullable to allow platform refresh tokens (tenant_id=NULL).
ALTER TABLE refresh_tokens ALTER COLUMN tenant_id DROP NOT NULL;

ALTER TABLE refresh_tokens DROP CONSTRAINT IF EXISTS refresh_tokens_user_id_fkey;

COMMIT;

-- ===========================================================================
-- Rollback
-- ===========================================================================
-- ALTER TABLE refresh_tokens ALTER COLUMN tenant_id SET NOT NULL;
