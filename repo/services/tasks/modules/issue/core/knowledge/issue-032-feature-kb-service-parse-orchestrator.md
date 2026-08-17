# [功能] kb-service parse_orchestrator 文档解析管线编排 (步骤 5 功能)

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md`
- UX: N/A — backend-only
- Plan: `repo/services/tasks/modules/plan/plan-rag-architecture-compliance-design.md` (步骤 5)

## Description
作为 Services 层开发者，我需要实现 kb-service 文档解析管线编排器 `ParseOrchestrator`，保证与 rag-engine parse_worker 等价。编排：获取 download_url → rag-engine.Parse RPC → 图片上传 Core API → Embed RPC → Core 向量插入 → PG kb_chunks 写入 → 摘要生成 → 状态更新。跑通全部测试。

## Scope
- Product line: core (Services / kb-service)
- Code paths allowed: `repo/services/kb-service/app/services/parse_orchestrator.py`

## Acceptance Criteria
- [ ] [Plan 步骤5] 实现 `ParseOrchestrator`：实现 `ParseOrchestratorProtocol` 接口，编排完整管线：pending → parsing → rag-engine.Parse → 图片上传 → indexing → 摘要 → Embed → Core 向量插入 → kb_chunks 写入 → ready
- [ ] [Plan 步骤5] 从 Core API 获取 `download_url` 传给 rag-engine.Parse RPC（kb-service 不下载文档 bytes）
- [ ] [Plan 步骤5] 图片上传到 Core API，替换占位符为 object_id
- [ ] [Plan 步骤5] Embed RPC 嵌入 child chunks + summary（分别嵌入，summary 不加入 child_chunks 避免双重写入）
- [ ] [Plan 步骤5] Core API 插入向量（传预计算 vector），metadata 包含 chunk_id / chunk_type / parent_content / parent_chunk_id / page_number / content_type / doc_id / file_name
- [ ] [Plan 步骤5] `write_chunks` 分开传 parents + children + summaries（避免双重写入）
- [ ] [Plan 步骤5] 状态机：pending → parsing → indexing → ready | failed（异常时 failed + sanitized error_msg）
- [ ] [Plan 步骤5] `_generate_summary`：best-effort，取前 3 个 parent blocks 拼接，调 Generate RPC（prompt = "请总结以下内容为 200-500 字的摘要"），失败返回 None 不阻塞
- [ ] [Plan 步骤5] `_sanitize_error`：移除敏感路径/凭据，截断 500 字符
- [ ] 新增 `test_parse_orchestrator.py` — mock Core API + rag-engine gRPC + PG
- [ ] **等价性测试**：同一文档，对比 kb_chunks 行数和内容一致

## Dependencies
#027 (接口) + #030 (kb-service infra) — 实现依赖接口定义 + CoreClient/gRPC 客户端 + chunk_repo。与 #031 可并行。#032 依赖此项。

## Type
core (feature)

## Priority
high

## Labels
core, feature

## Batch
RAG-REFACTOR-STEP-5-FEATURE

## References
- Plan: 步骤 5 (parse_orchestrator 设计), §2.1 (图片 Core API 上传等价), §0.1 (Parse 管线等价性)
