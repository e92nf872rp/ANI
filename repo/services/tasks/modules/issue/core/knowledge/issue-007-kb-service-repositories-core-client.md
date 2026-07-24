# kb-service repositories 与 Core API client

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md` (US-009)
- UX: N/A — backend-only
- SPEC: `repo/services/tasks/modules/spec/core/knowledge/spec-services-kb-service.md` (§2, §6)

## Description
作为平台开发者，我需要实现 kb-service 的数据访问层与 Core API 客户端。

## Scope
- Product line: core (Services / kb-service)
- Code paths allowed: `repo/services/kb-service/app/repositories/`, `app/core_api/`, `app/rag_engine/` only

## Acceptance Criteria
- [ ] [SPEC] 实现 repositories 覆盖 4 张已迁移表 + kb_chunks（含 RLS 过滤），文件：`knowledge_base.py`/`document.py`/`message.py`/`async_task.py`/`outbox.py`/`chunk.py`（SPEC §2.4）
- [ ] Core API client 实现 `/vector-stores` 集合级 CRUD、`/objects/upload`、`/objects/{id}/download`、`/vector-stores/{id}/documents` 删除
- [ ] rag-engine gRPC client 实现 Query 调用
- [ ] CreateKB 调 Core `POST /vector-stores` 创建向量集合
- [ ] DeleteKB 软删 + 调 Core `DELETE /vector-stores/{id}` 删集合
- [ ] `make test` 通过

## Dependencies
#6 (kb-service skeleton) — per SPEC §11.2 (US-009 depends on US-008).

## Type
core (services)

## Priority
high

## Labels
core, services

## Batch
M2.1-TASK-B

## References
- SPEC: spec-services-kb-service §2.2, §2.4, §6.1
