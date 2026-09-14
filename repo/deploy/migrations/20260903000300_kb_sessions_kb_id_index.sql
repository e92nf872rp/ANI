-- ANI Platform · Migration 20260903000300
-- Description: 为 kb_sessions(kb_id, created_at DESC) 补建二级索引。
--              ListKBSessions / ListKBCitations 均以 kb_id 过滤 + created_at
--              排序（键集分页），此前 kb_sessions 仅有主键索引，查询需
--              全表扫描 + 排序。对齐 kb_documents.idx_kb_docs_kb_id 先例。
-- Depends on: 20260815000100_kb_service.sql（kb_sessions 建表）
-- Rationale:
--   kb-service B2（issue-045）新增 ListKBSessions（kb_id = $1 … ORDER BY
--   created_at DESC, id DESC）与 ListKBCitations（s.kb_id = $1 … ORDER BY
--   m.created_at DESC）。补建复合索引使两查询可走索引扫描并消排序。
--   created_at DESC 列同时支撑键集分页谓词 (created_at, id) < ($2, $3)。
--   不使用 CREATE INDEX CONCURRENTLY：Atlas 每个迁移文件包裹独立事务，
--   CONCURRENTLY 不能在事务块内执行；kb_sessions 体量小，短暂排他锁可接受。

CREATE INDEX IF NOT EXISTS idx_kb_sessions_kb_id_created_at
    ON kb_sessions(kb_id, created_at DESC);

-- ===========================================================================
-- Rollback
-- ===========================================================================
-- DROP INDEX IF EXISTS idx_kb_sessions_kb_id_created_at;
