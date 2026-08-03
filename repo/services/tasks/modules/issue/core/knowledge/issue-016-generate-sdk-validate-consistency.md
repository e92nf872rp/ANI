# 生成 SDK 生成物并校验一致性

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md` (US-007)
- UX: N/A — backend-only
- SPEC: `repo/services/tasks/modules/spec/core/knowledge/spec-services-kb-service.md` (§11.1)

## Description
作为平台开发者，我需要重新生成 SDK 并校验 proto/OpenAPI 一致性，确保契约变更落地。

## Scope
- Product line: core (SDK generation)
- Code paths allowed: SDK generation artifacts only

## Acceptance Criteria
- [ ] [SPEC] 基于 A1/A2/A3/A4 变更重新生成 SDK 生成物（SPEC §11.1 phase 3）
- [ ] `make validate-sdk-beta` 通过
- [ ] `make validate-spec-split` 通过
- [ ] SDK 无漂移

## Dependencies
#1 + #2 + #3 + #15 (all contract changes: US-001/002/003/004) — per SPEC §11.2 (US-007 depends on US-001,002,003,004).

## Type
core (sdk)

## Priority
high

## Labels
core, sdk

## Batch
M2.1-TASK-A

## References
- SPEC: spec-services-kb-service §11.1 (phase 3), §11.2
