-- ANI Platform · Migration 20260911000200
-- Description: 存量 knowledge_bases.embedding_model 归一化为全名
--              "BAAI/bge-m3"。代码审查（M3 批次）发现模型名三值不一致：
--              CreateKB 曾默认落库无前缀 "bge-m3"，而实际 embedding 端点
--              （SiliconFlow OpenAI 兼容）只注册了全名 "BAAI/bge-m3"，
--              无前缀名会触发 model_not_found。此迁移把存量行修正为全名，
--              并与新代码默认值（grpc_server.py / config.py）对齐。
-- Depends on: 20260501000100_init_schema.sql（knowledge_bases 建表）
-- Rationale:
--   embedding_model 影响查询侧向量计算——修正名字不会重建索引（向量维度
--   bge-m3 = BAAI/bge-m3 = 1024，同一模型的两种写法，向量空间一致），
--   仅让后续 embed 调用使用端点可识别的模型名。
--   新建 KB 默认值已在应用层收敛为 "BAAI/bge-m3"；本迁移仅覆盖存量行。

UPDATE knowledge_bases
SET    embedding_model = 'BAAI/bge-m3'
WHERE  embedding_model = 'bge-m3';

-- ===========================================================================
-- Rollback
-- ===========================================================================
-- UPDATE knowledge_bases
-- SET    embedding_model = 'bge-m3'
-- WHERE  embedding_model = 'BAAI/bge-m3';
