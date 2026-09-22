-- ANI Platform · Migration 20260903000200
-- Description: 删除 async_tasks 上与表级 UNIQUE 约束重复的显式唯一索引，
--              消除同列双唯一索引带来的无谓写放大。
-- Depends on: 20260802000100_async_tasks.sql
-- Rationale:
--   init_schema 的表级 UNIQUE (tenant_id, idempotency_key) 已隐式建立唯一索引
--   （async_tasks_tenant_id_idempotency_key_key），20260802000100 又显式建了
--   同列的 idx_async_tasks_tenant_idempotency。两个唯一索引对同一保证重复收费：
--   每次 INSERT / UPDATE 多维护一份索引，无额外收益。
--   保留表级约束（schema 声明的锚点，代码以泛捕获 SQLSTATE 23505 方式使用，
--   无按索引名引用），删除显式索引。
--   不使用 DROP INDEX CONCURRENTLY：Atlas 每个迁移文件包裹独立事务，
--   CONCURRENTLY 不能在事务块内执行；async_tasks 表的短暂排他锁可接受。

DROP INDEX IF EXISTS idx_async_tasks_tenant_idempotency;

-- ===========================================================================
-- Rollback
-- ===========================================================================
-- CREATE UNIQUE INDEX IF NOT EXISTS idx_async_tasks_tenant_idempotency
--     ON async_tasks(tenant_id, idempotency_key);
