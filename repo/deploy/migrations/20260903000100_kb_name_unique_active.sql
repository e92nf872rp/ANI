-- ANI Platform · Migration 20260903000100
-- Description: knowledge_bases 的 (tenant_id, name) 唯一约束改为部分唯一索引，
--              软删行（status='deleted'）不再占用名称，删除后可同名重建。
-- Depends on: 20260501000100_init_schema.sql
-- Rationale:
--   原 UNIQUE (tenant_id, name) 是普通约束，软删行仍占名。导致两条坏路径：
--     1. DeleteKB 后无法用同名重建 KB（对用户表现为"删除失败后名字被锁死"）；
--     2. CreateKB 在 Core 创建失败时执行 soft_delete_kb 清理，客户端重试同名
--        时 INSERT 撞 UNIQUE(tenant_id, name) —— 软删行占名使重试永远失败。
--   改为 partial unique index WHERE status <> 'deleted'：
--   - 与 get_kb / list_kbs / update_kb 读路径的过滤条件完全一致；
--   - status='rebuilding' 的 KB 继续占名（重建中不可被同名抢占）。
--   无需数据回填：迁移只是放宽约束，存量软删行之间不会冲突。

ALTER TABLE knowledge_bases
    DROP CONSTRAINT IF EXISTS knowledge_bases_tenant_id_name_key;

CREATE UNIQUE INDEX IF NOT EXISTS idx_knowledge_bases_tenant_name_active
    ON knowledge_bases(tenant_id, name)
    WHERE status <> 'deleted';

-- ===========================================================================
-- Rollback
-- ===========================================================================
-- DROP INDEX IF EXISTS idx_knowledge_bases_tenant_name_active;
-- ALTER TABLE knowledge_bases
--     ADD CONSTRAINT knowledge_bases_tenant_id_name_key UNIQUE (tenant_id, name);
-- （回滚前提：不存在同名软删行与 active 行并存的情况，否则需先清理。）
