# 新增 Services OpenAPI 端点（reparse/config/rebuild/models）

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md` (US-002)
- UX: `repo/services/tasks/modules/ux/console/knowledge/ux-console-knowledge-base-platform.md` (§4.3 概览配置, §5 概览组件)
- SPEC: `repo/services/tasks/modules/spec/core/knowledge/spec-services-kb-service.md` (§4, §5)

## Description
作为平台开发者，我需要新增 reparse/config/rebuild/models 端点以支撑前端概览页配置与重建能力。

## Scope
- Product line: core (Services 契约层)
- Code paths allowed: `repo/api/openapi/services/v1.yaml` only

## Acceptance Criteria
- [ ] [SPEC] 新增 `POST /knowledge-bases/{kb_id}/documents/{doc_id}/reparse`（重新解析，202，SPEC §5 US-002 表）
- [ ] [SPEC] 新增 `GET /knowledge-bases/{kb_id}/config`（读取 KB 配置，200 + KBConfig，SPEC §5）
- [ ] [SPEC] 新增 `PUT /knowledge-bases/{kb_id}/config`（更新 KB 配置，200 + KnowledgeBase，SPEC §5）
- [ ] [SPEC] 新增 `POST /knowledge-bases/{kb_id}/rebuild`（全库重建，202，SPEC §5）
- [ ] [SPEC] 新增 `GET /knowledge-bases/{kb_id}/models`（可用嵌入/推理模型列表，200 + ModelList，SPEC §5）
- [ ] [SPEC] 新增端点写入 `repo/api/openapi/services/v1.yaml`，不写入 Core `v1.yaml`（SPEC §5 Frozen Paths）
- [ ] `make validate-services` 通过

## Dependencies
#1 (US-001 contract fix) — per SPEC §11.2 (US-002 depends on US-001).

## Type
core (contract)

## Priority
high

## Labels
core, contract

## Batch
M2.1-TASK-A

## References
- SPEC: spec-services-kb-service §5 (US-002 新增端点表, Frozen Facts Table)
- UX: §4.3 概览配置, §5 概览组件
