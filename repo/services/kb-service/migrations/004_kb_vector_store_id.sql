-- 004_kb_vector_store_id.sql
-- 新增 knowledge_bases.vector_store_id 列：持久化 Core API 返回的向量库 ID。
-- 改造后 kb-service 作为唯一编排者，需记录 Core 侧 vector_store id 以便
-- 后续 insert/search/delete 向量操作 (Plan §3.1)。
-- 幂等：列已存在则跳过。仅用于已有库升级；新装库见 scripts/apply_kb_migration.py。

ALTER TABLE knowledge_bases
    ADD COLUMN IF NOT EXISTS vector_store_id TEXT;
