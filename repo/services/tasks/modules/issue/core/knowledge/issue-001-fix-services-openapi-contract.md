# 修复 Services OpenAPI 与 proto 契约一致性

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md` (US-001)
- UX: `repo/services/tasks/modules/ux/console/knowledge/ux-console-knowledge-base-platform.md` (§5 字段命名规则)
- SPEC: `repo/services/tasks/modules/spec/core/knowledge/spec-services-kb-service.md` (§4, §5)

## Description
作为平台开发者，我需要修复 Services OpenAPI 与 proto 的契约不一致，使前后端契约对齐。这是契约优先（contract-first）基础，必须先于任何服务骨架落地。

## Scope
- Product line: core (Services 契约层)
- Code paths allowed: `repo/api/proto/kb/v1/kb_service.proto`, `repo/api/openapi/services/v1.yaml` only

## Acceptance Criteria
- [ ] `KBDocument` 字段名统一为 `parse_status`，枚举值对齐 proto（`pending | parsing | indexing | ready | failed`）
- [ ] 文档上传契约改为两步式 pre-signed URL（`getDocumentUploadURL` + `notifyDocumentUploaded`），对齐 proto
- [ ] `KBQueryRequest` 补齐 `score_threshold`、`inference_service_name`、`idempotency_key` 字段
- [ ] 文档上传新增 `custom_metadata`（JSONB）字段到 proto 与 OpenAPI
- [ ] `make validate-services` 通过
- [ ] proto 与 services/v1.yaml 一致性校验通过

## Dependencies
None — this is the contract-first foundation; MUST land before any service skeleton.

## Type
core (contract)

## Priority
high

## Labels
core, contract

## Batch
M2.1-TASK-A (contract phase)

## References
- SPEC: spec-services-kb-service §4.1, §4.2, §5 (US-001 修复表)
- UX: §5 字段命名规则（`parse_status` 非 `status`）
