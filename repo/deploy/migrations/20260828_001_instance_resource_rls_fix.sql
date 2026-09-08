-- Fix instance-resource chain RLS: replace RESTRICTIVE-only policies with
-- PERMISSIVE dual-policy pattern (platform_bypass + self), aligned with
-- workload_instances (20260825_001) and resource_quota / resource_reservations.
--
-- Background:
--   The instance create chain depends on these tables:
--     - resolver: network_vpcs / network_subnets / network_security_groups
--       (VPC & subnet resolution), storage_filesystems /
--       storage_filesystem_attachments (filesystem_mounts resolution)
--     - kubernetes_rest provider: platform_workloads /
--       platform_workload_intents (workload intent persistence)
--   All of them had a single RESTRICTIVE tenant_isolation policy with NO
--   PERMISSIVE policy. PostgreSQL RLS denies all rows when there is no
--   PERMISSIVE policy to pass, regardless of current_setting value.
--   This was masked while services connected as superuser (ani, BYPASSRLS).
--   After switching the app connection to ani_app_user (non-superuser),
--   every INSERT/SELECT on these tables is rejected:
--     - VPC/subnet/filesystem creation silently fails to persist
--     - instance create resolver returns NOT_FOUND for never-persisted VPCs
--     - platform workload intent persistence fails -> instances end failed
--
-- Fix (per table, same as 20260825_001):
--   1. DROP the old RESTRICTIVE tenant_isolation policy.
--   2. CREATE PERMISSIVE platform_bypass: app.current_tenant_id NULL -> all rows.
--   3. CREATE PERMISSIVE self: tenant_id matches current_setting -> own rows.
--
-- Scope: instance create chain only. Other RESTRICTIVE-only tables
-- (refresh_tokens for auth login, api_keys, async_tasks, ...) are owned by
-- their respective chains and are intentionally NOT touched here.

BEGIN;

-- ---------------------------------------------------------------------------
-- network_vpcs
-- ---------------------------------------------------------------------------
DROP POLICY IF EXISTS tenant_isolation ON network_vpcs;

CREATE POLICY network_vpcs_platform_bypass
  ON network_vpcs FOR ALL
  USING (current_setting('app.current_tenant_id', true) IS NULL);

CREATE POLICY network_vpcs_self
  ON network_vpcs FOR ALL
  USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

-- ---------------------------------------------------------------------------
-- network_subnets
-- ---------------------------------------------------------------------------
DROP POLICY IF EXISTS tenant_isolation ON network_subnets;

CREATE POLICY network_subnets_platform_bypass
  ON network_subnets FOR ALL
  USING (current_setting('app.current_tenant_id', true) IS NULL);

CREATE POLICY network_subnets_self
  ON network_subnets FOR ALL
  USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

-- ---------------------------------------------------------------------------
-- network_security_groups
-- ---------------------------------------------------------------------------
DROP POLICY IF EXISTS tenant_isolation ON network_security_groups;

CREATE POLICY network_security_groups_platform_bypass
  ON network_security_groups FOR ALL
  USING (current_setting('app.current_tenant_id', true) IS NULL);

CREATE POLICY network_security_groups_self
  ON network_security_groups FOR ALL
  USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

-- ---------------------------------------------------------------------------
-- storage_filesystems
-- ---------------------------------------------------------------------------
DROP POLICY IF EXISTS tenant_isolation ON storage_filesystems;

CREATE POLICY storage_filesystems_platform_bypass
  ON storage_filesystems FOR ALL
  USING (current_setting('app.current_tenant_id', true) IS NULL);

CREATE POLICY storage_filesystems_self
  ON storage_filesystems FOR ALL
  USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

-- ---------------------------------------------------------------------------
-- storage_filesystem_attachments
-- ---------------------------------------------------------------------------
DROP POLICY IF EXISTS tenant_isolation ON storage_filesystem_attachments;

CREATE POLICY storage_filesystem_attachments_platform_bypass
  ON storage_filesystem_attachments FOR ALL
  USING (current_setting('app.current_tenant_id', true) IS NULL);

CREATE POLICY storage_filesystem_attachments_self
  ON storage_filesystem_attachments FOR ALL
  USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

-- ---------------------------------------------------------------------------
-- platform_workloads
-- ---------------------------------------------------------------------------
DROP POLICY IF EXISTS tenant_isolation ON platform_workloads;

CREATE POLICY platform_workloads_platform_bypass
  ON platform_workloads FOR ALL
  USING (current_setting('app.current_tenant_id', true) IS NULL);

CREATE POLICY platform_workloads_self
  ON platform_workloads FOR ALL
  USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

-- ---------------------------------------------------------------------------
-- platform_workload_intents
-- ---------------------------------------------------------------------------
DROP POLICY IF EXISTS tenant_isolation ON platform_workload_intents;

CREATE POLICY platform_workload_intents_platform_bypass
  ON platform_workload_intents FOR ALL
  USING (current_setting('app.current_tenant_id', true) IS NULL);

CREATE POLICY platform_workload_intents_self
  ON platform_workload_intents FOR ALL
  USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

-- ---------------------------------------------------------------------------
-- instance_plan_audits
-- ---------------------------------------------------------------------------
DROP POLICY IF EXISTS tenant_isolation ON instance_plan_audits;

CREATE POLICY instance_plan_audits_platform_bypass
  ON instance_plan_audits FOR ALL
  USING (current_setting('app.current_tenant_id', true) IS NULL);

CREATE POLICY instance_plan_audits_self
  ON instance_plan_audits FOR ALL
  USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

COMMIT;

-- ===========================================================================
-- Rollback (per table)
-- ===========================================================================
-- DROP POLICY IF EXISTS network_vpcs_self ON network_vpcs;
-- DROP POLICY IF EXISTS network_vpcs_platform_bypass ON network_vpcs;
-- CREATE POLICY tenant_isolation ON network_vpcs
--     AS RESTRICTIVE
--     USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);
-- (repeat the same shape for network_subnets, network_security_groups,
--  storage_filesystems, storage_filesystem_attachments, platform_workloads,
--  platform_workload_intents, instance_plan_audits)
