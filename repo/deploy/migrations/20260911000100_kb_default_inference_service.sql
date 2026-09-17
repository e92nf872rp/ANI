-- ANI Platform · Migration 20260911000100
-- Description: knowledge_bases 新增 default_inference_service 列。
--              M3（模型动态切换）第二阶段：建库时可选定默认推理模型
--              （served_model_name，由 AI Gateway 路由）。问答请求
--              QueryRequest/RetrieveRequest 的 inference_service_name
--              为空时回落到此列，再为空则回落 ""（rag-engine 端由
--              settings.vllm_model 接管，见 20260911000200 同批审查修复）。
-- Depends on: 20260501000100_init_schema.sql（knowledge_bases 建表）
-- Rationale:
--   推理模型只影响生成，不影响向量索引，因此该列是普通 TEXT 配置列，
--   修改无需重建索引（区别于 embedding_model）。存量行 NULL = 未设置，
--   服务层回落 ""，由 rag-engine 的 settings.vllm_model 兜底。
--   对齐 services/kb-service/migrations 双目录策略：knowledge_bases 的
--   retrieval_mode/vector_store_id 变更走 kb-service 本地迁移；本次列
--   与 B1/B2 一样属于 deploy 共享集（knowledge_bases 表在 deploy 基线中）。

ALTER TABLE knowledge_bases
    ADD COLUMN IF NOT EXISTS default_inference_service TEXT;

-- ===========================================================================
-- Rollback
-- ===========================================================================
-- ALTER TABLE knowledge_bases DROP COLUMN IF EXISTS default_inference_service;
