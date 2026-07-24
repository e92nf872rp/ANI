# model proto 新增 OCR capability 标注

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md` (US-004)
- UX: N/A — backend-only
- SPEC: `repo/services/tasks/modules/spec/core/knowledge/spec-services-kb-service.md` (§4)

## Description
作为平台开发者，我需要在 model proto 中标注 OCR 能力，使模型列表可识别 OCR 服务。

## Scope
- Product line: core (proto)
- Code paths allowed: `repo/api/proto/model/v1/model_service.proto` only

## Acceptance Criteria
- [ ] model proto 新增 `ocr` 字符串值作为 capability 注释
- [ ] proto 生成物更新
- [ ] `make validate-services` 通过

## Dependencies
None

## Type
core (contract)

## Priority
medium

## Labels
core, contract

## Batch
M2.1-TASK-A

## References
- SPEC: spec-services-kb-service §4 (US-004)
