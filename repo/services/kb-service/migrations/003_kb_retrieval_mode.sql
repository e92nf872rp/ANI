-- 003_kb_retrieval_mode.sql
-- 新增 knowledge_bases.retrieval_mode 列：指定 KB 的检索方式。
--   枚举：vector | hybrid | keyword
--     vector   向量检索（Milvus cosine）
--     hybrid   混合检索（向量 + 全文 pg_trgm + RRF，默认）
--     keyword  全文检索（pg_trgm）
-- 幂等：列已存在则跳过。仅用于已有库升级；新装库见 scripts/apply_kb_migration.py。

ALTER TABLE knowledge_bases
    ADD COLUMN IF NOT EXISTS retrieval_mode TEXT NOT NULL DEFAULT 'hybrid'
        CHECK (retrieval_mode IN ('vector', 'hybrid', 'keyword'));
