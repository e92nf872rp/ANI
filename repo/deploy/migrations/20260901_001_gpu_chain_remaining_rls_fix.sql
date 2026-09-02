-- Fix remaining GPU/instance-chain RLS tables: replace broken policies with
-- the PERMISSIVE dual-policy pattern (platform_bypass + self), aligned with
-- workload_instances (20260825_001) and 20260828_001.
--
-- Background (found by full pg_policy audit on the K8s test env):
--   Three tables remained broken after 20260828_001:
--     1. network_load_balancers: single RESTRICTIVE tenant_isolation policy,
--        no PERMISSIVE policy -> denies all rows. Part of the network resource
--        family (POST /networks/load-balancers, UpsertLoadBalancer persistence),
--        same chain as network_vpcs/network_subnets fixed in 20260828_001.
--     2. k8s_cluster_proxy_targets: single RESTRICTIVE tenant_isolation policy,
--        no PERMISSIVE policy -> denies all rows. Read by
--        ResolveK8sClusterProxyTarget when instances run on external clusters
--        (k8s cluster proxy forwarding service).
--     3. network_routes: had a PERMISSIVE tenant_isolation policy but it reads
--        the wrong GUC ('ani.tenant_id'); no Go code sets that GUC anywhere
--        (the platform sets 'app.current_tenant_id') -> the policy never
--        matches -> equivalent to deny-all. Same network resource family.
--
--   metering_records is RESTRICTIVE-only too but is a dead table: no Go code
--   references it (the real usage table is metering_usage_records, written via
--   SET ROLE ani_metering_writer with BYPASSRLS by metering-service). It is
--   intentionally NOT touched here.
--
-- Fix (per table, same three-step pattern as 20260825_001 / 20260828_001):
--   1. DROP the old broken policy.
--   2. CREATE PERMISSIVE platform_bypass: app.current_tenant_id NULL -> all rows.
--   3. CREATE PERMISSIVE self: tenant_id matches current_setting -> own rows.

BEGIN;

-- ---------------------------------------------------------------------------
-- network_load_balancers
-- ---------------------------------------------------------------------------
DROP POLICY IF EXISTS tenant_isolation ON network_load_balancers;

CREATE POLICY network_load_balancers_platform_bypass
  ON network_load_balancers FOR ALL
  USING (current_setting('app.current_tenant_id', true) IS NULL);

CREATE POLICY network_load_balancers_self
  ON network_load_balancers FOR ALL
  USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

-- ---------------------------------------------------------------------------
-- network_routes
-- ---------------------------------------------------------------------------
DROP POLICY IF EXISTS tenant_isolation ON network_routes;

CREATE POLICY network_routes_platform_bypass
  ON network_routes FOR ALL
  USING (current_setting('app.current_tenant_id', true) IS NULL);

CREATE POLICY network_routes_self
  ON network_routes FOR ALL
  USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

-- ---------------------------------------------------------------------------
-- k8s_cluster_proxy_targets
-- ---------------------------------------------------------------------------
DROP POLICY IF EXISTS tenant_isolation ON k8s_cluster_proxy_targets;

CREATE POLICY k8s_cluster_proxy_targets_platform_bypass
  ON k8s_cluster_proxy_targets FOR ALL
  USING (current_setting('app.current_tenant_id', true) IS NULL);

CREATE POLICY k8s_cluster_proxy_targets_self
  ON k8s_cluster_proxy_targets FOR ALL
  USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

COMMIT;

-- Rollback:
--   BEGIN;
--   DROP POLICY IF EXISTS network_load_balancers_platform_bypass ON network_load_balancers;
--   DROP POLICY IF EXISTS network_load_balancers_self ON network_load_balancers;
--   CREATE POLICY tenant_isolation ON network_load_balancers FOR ALL
--     USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);
--   (repeat the same three statements for network_routes and
--    k8s_cluster_proxy_targets; for network_routes the old policy used
--    current_setting('ani.tenant_id', true) which never matched.)
--   COMMIT;
