# 全量验收（make test / validate-architecture / validate-services / git diff --check）

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/quota/prd-quota-service.md`
- UX: N/A
- SPEC: `repo/services/tasks/modules/spec/core/quota/spec-quota-service.md`
- Plan: `repo/services/tasks/modules/plan/plan-quota-service.md`

## Description

全量验收门禁。所有 Issue 完成后，执行全量校验命令，确保 ports/adapters 边界、Services boundary、SDK 漂移、OpenAPI 契约、代码格式全部通过。

## Scope

- Product line: core
- Code paths allowed: 全仓库（校验命令）

## Acceptance Criteria

- [ ] `make test` 通过（含所有单元测试）
- [ ] `make validate-architecture` 通过（ports/adapters 边界）
- [ ] `make validate-services` 通过（Services boundary + SDK 漂移 + OpenAPI 契约）
- [ ] `make gen-core-sdk && git diff --check -- sdks/core` 无漂移无空白错误
- [ ] `git diff --check` 无空白错误
- [ ] `python scripts/validate_yaml.py api/openapi/v1.yaml` 通过
- [ ] 集成测试通过 `go test ./pkg/adapters/runtime/ -v -run Integration -tags integration`（若 PG 实例可用）

## Dependencies

All（#0~#11）

## Type

core

## Priority

high

## Labels

core

## Batch

TBD

## References

- SPEC: §10.1 Phase 8
- Plan: §12
