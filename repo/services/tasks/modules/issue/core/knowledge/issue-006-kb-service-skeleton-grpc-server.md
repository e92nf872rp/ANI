# kb-service 骨架与 gRPC server

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md` (US-008)
- UX: N/A — backend-only
- SPEC: `repo/services/tasks/modules/spec/core/knowledge/spec-services-kb-service.md` (§2, §4)

## Description
作为平台开发者，我需要创建 kb-service 骨架并实现 gRPC server 承接 proto 10 个 RPC。

## Scope
- Product line: core (Services / kb-service)
- Code paths allowed: `repo/services/kb-service/` only

## Acceptance Criteria
- [ ] 新建 `repo/services/kb-service/`，含 Dockerfile/requirements/main.py
- [ ] [SPEC] 实现文件结构：`app/api/grpc_server.py` + `app/api/p1_rpcs.py` + `app/core/config.py`（SPEC §2.4）
- [ ] 实现 `kb_service.proto` 10 个 RPC：CreateKB/GetKB/ListKBs/DeleteKB/GetDocumentUploadURL/NotifyDocumentUploaded/GetDocument/ListDocuments/DeleteDocument/Query
- [ ] Phase A 新增 3 个权限/审计 RPC 声明，P0 返回 `UNIMPLEMENTED`（ListKBCitations/ListKBSessions/UpdateKBPermissions，SPEC §4.1）
- [ ] gRPC server 可启动并响应 RPC
- [ ] `make test` 通过

## Dependencies
#1 (contract fix, proto alignment) + #4 (kb_chunks migration) — per SPEC §11.2 (US-008 depends on US-001, US-005).

## Type
core (services)

## Priority
high

## Labels
core, services

## Batch
M2.1-TASK-B

## References
- SPEC: spec-services-kb-service §2.2, §2.4, §4.1
