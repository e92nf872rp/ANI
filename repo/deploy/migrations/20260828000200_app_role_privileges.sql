-- ANI Platform · Application Role Privileges
-- Version: 20260828000200
-- Description: 补齐应用角色权限，并分离 metering-service 的跨租户写侧权限
-- Depends on: 20260828000100_database_roles_hardening.sql
--
-- 说明：
--   1. ani_app 是 NOLOGIN 组角色，业务表权限统一授给它；
--      ani_app_user 通过继承 ani_app 获得普通应用权限。
--   2. 不在 migration 中写密码。
--   3. 不在 migration 中手写 BEGIN/COMMIT，Atlas 默认按文件管理事务。

-- ===========================================================================
-- 1) 普通应用角色：认证、模型、租户套餐和 Core 租户路径
-- ===========================================================================

GRANT USAGE ON SCHEMA public TO ani_app;

-- auth-service：登录前查询和登录后的 token / API key 持久化。
GRANT SELECT, INSERT, UPDATE ON tenants TO ani_app;
GRANT SELECT, INSERT, UPDATE ON users TO ani_app;
GRANT SELECT ON roles TO ani_app;
GRANT SELECT, INSERT ON user_roles TO ani_app;
GRANT SELECT, INSERT, UPDATE ON refresh_tokens TO ani_app;
GRANT SELECT, INSERT, UPDATE ON jwt_blocklist TO ani_app;
GRANT SELECT, INSERT, UPDATE ON api_keys TO ani_app;

-- model-service：模型和模型版本的租户范围读写。
GRANT SELECT, INSERT, UPDATE ON models TO ani_app;
GRANT SELECT, INSERT ON model_versions TO ani_app;

-- tenant-service：套餐、套餐限额和套餐域审计。
GRANT SELECT, INSERT, UPDATE ON tenant_plans TO ani_app;
GRANT SELECT, INSERT, UPDATE ON plan_quota_limits TO ani_app;
GRANT SELECT ON tenant_quota_change TO ani_app;
GRANT SELECT, INSERT ON audit_logs TO ani_app;

-- Core：租户范围资源、任务、操作记录和 outbox 写入。
GRANT SELECT, INSERT, UPDATE ON async_tasks TO ani_app;
GRANT SELECT, INSERT, UPDATE ON outbox_events TO ani_app;
GRANT INSERT ON instance_plan_audits TO ani_app;
GRANT SELECT, INSERT, UPDATE ON control_plane_leases TO ani_app;
GRANT SELECT, INSERT, UPDATE ON network_routes TO ani_app;
GRANT SELECT, INSERT, UPDATE ON workload_instances TO ani_app;
GRANT SELECT, INSERT, UPDATE ON workload_instance_operations TO ani_app;
GRANT SELECT, INSERT ON workload_instance_operation_steps TO ani_app;

-- inference-service：租户池的控制面 CRUD；后台跨租户 worker 另见第 4 节。
GRANT SELECT, INSERT, UPDATE, DELETE
ON inference_services, inference_operations
TO ani_app;

-- ===========================================================================
-- 2) 把历史上直接授给 ani_app_user 的权限归一到 ani_app
-- ===========================================================================

GRANT SELECT, INSERT, UPDATE, DELETE
ON resource_quota,
   resource_reservations,
   resource_quota_meta,
   resource_reservation_allocations
TO ani_app;

GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO ani_app;

REVOKE SELECT, INSERT, UPDATE, DELETE
ON resource_quota,
   resource_reservations,
   resource_quota_meta,
   resource_reservation_allocations
FROM ani_app_user;

REVOKE USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public FROM ani_app_user;

-- 其余历史直授表也统一通过 ani_app 继承获得权限。
GRANT SELECT, INSERT, UPDATE ON tenant_plans, plan_quota_limits TO ani_app;
GRANT SELECT ON tenant_quota_change TO ani_app;

REVOKE SELECT, INSERT, UPDATE, DELETE
ON tenant_plans, plan_quota_limits, tenant_quota_change
FROM ani_app_user;

-- ===========================================================================
-- 3) 未来由 ani 创建的序列自动给 ani_app 使用
-- ===========================================================================

-- 表权限仍要求在每个 migration 中显式 GRANT，避免新建的内部表被全库暴露。
ALTER DEFAULT PRIVILEGES FOR ROLE ani IN SCHEMA public
GRANT USAGE, SELECT ON SEQUENCES TO ani_app;

-- ===========================================================================
-- 4) metering-service：独立登录账号，不再继承普通应用角色
-- ===========================================================================

REVOKE ani_app FROM ani_metering_user;
GRANT USAGE ON SCHEMA public TO ani_metering_writer;
GRANT SELECT, INSERT, UPDATE, DELETE
ON metering_usage_records
TO ani_metering_writer;

-- metering-service 启动重建 ticker 时只需跨租户读取 running 实例。
GRANT SELECT ON workload_instances TO ani_metering_user;

-- metering_usage_records 使用 BIGSERIAL，写侧必须能够调用 nextval。
GRANT USAGE, SELECT
ON SEQUENCE metering_usage_records_id_seq
TO ani_metering_writer;

-- ===========================================================================
-- 5) outbox publisher 角色预留权限
-- ===========================================================================

GRANT USAGE ON SCHEMA public TO ani_outbox_publisher;
GRANT SELECT, UPDATE ON outbox_events TO ani_outbox_publisher;

