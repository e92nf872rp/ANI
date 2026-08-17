# [接口] kb-service 编排层抽象接口定义 (步骤 3-5 接口)

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md`
- UX: N/A — backend-only
- Plan: `repo/services/tasks/modules/plan/plan-rag-architecture-compliance-design.md` (§3.5, 步骤 3-5)

## Description
作为 Services 层开发者，我需要根据契约定义 kb-service 编排层的产品能力抽象接口（Protocol/ABC），声明 RetrieveService、ParseOrchestrator、QueryOrchestrator 的方法签名和依赖注入接口。**本 issue 仅定义抽象接口，不含任何具体实现。**

## Scope
- Product line: core (Services / kb-service)
- Code paths allowed: `repo/services/kb-service/app/services/__init__.py`, `app/services/contracts.py` (抽象接口定义文件)

## Acceptance Criteria
- [ ] [Plan §3.5] 新建 `app/services/` 目录 + `__init__.py`
- [ ] 新建 `app/services/contracts.py` — 定义以下抽象接口（Python Protocol / ABC）：
- [ ] `class RetrieveServiceProtocol`：声明 `async def retrieve(self, tenant_id, kb_id, question, top_k, score_threshold, retrieval_mode, vector_store_id) -> tuple[list[dict], float]` 签名（仅签名，无实现）
- [ ] `class ParseOrchestratorProtocol`：声明 `async def process_document(self, tenant_id, kb_id, doc_id, object_id, file_name, file_type, chunk_size, vector_store_id) -> None` 签名（仅签名，无实现）
- [ ] `class QueryOrchestratorProtocol`：声明 `async def query(self, tenant_id, kb_id, question, session_id, top_k, score_threshold, retrieval_mode, inference_service_name, vector_store_id, history) -> QueryResult` 签名（仅签名，无实现）
- [ ] `class RagEngineClientProtocol`：声明 `parse`/`embed`/`generate`/`generate_stream` 方法签名（仅签名，抽象 gRPC 客户端接口）
- [ ] `class CoreClientProtocol`：声明 `insert_vector_documents`/`search_vector_store`/`upload_object`/`request_download_url`/`delete_vector_store_documents` 方法签名（仅签名，抽象 Core API 客户端接口）
- [ ] `QueryResult` dataclass 定义（`answer`、`sources`、`session_id`、`input_tokens`、`output_tokens` 字段）
- [ ] 接口方法参数和返回值类型标注完整（type hints）
- [ ] 不包含任何具体实现代码（`RetrieveService`/`ParseOrchestrator`/`QueryOrchestrator`/`RagEngineGRPCClient`/`CoreClient` 实现在功能 issue 中完成）
- [ ] `python -m py_compile app/services/contracts.py` 通过（语法检查）

## Dependencies
#024 (Core 契约) + #025 (rag-engine 契约) — 接口依赖契约定义的类型。

## Type
core (interface)

## Priority
high

## Labels
core, interface

## Batch
RAG-REFACTOR-STEP-3-INTERFACE

## References
- Plan: §3.5 (app/services 目录), 步骤 4 (RetrieveService), 步骤 5 (ParseOrchestrator), 步骤 8A (QueryOrchestrator), §3.3 (CoreClient), §3.4 (RagEngineClient)
- CLAUDE.md §4.1 (先改 API 契约再写实现)
