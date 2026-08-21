# Ports 层 — GPUSpecStore + WorkloadInstanceStoreTx + Inventory 扩展接口

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/gpu-spec-quota/prd-gpu-spec-quota.md`
- UX: N/A（非前端）
- SPEC: `repo/services/tasks/modules/spec/core/gpu-spec-quota/spec-gpu-spec-quota.md`

## Description

作为开发者，定义 Ports 层接口：新增 `GPUSpecStore` 接口、`WorkloadInstanceStoreTx` 小接口、扩展 `GPUInventory` 的 `ListSpecAvailability` 方法签名，复用已有 QuotaService/QuotaStoreService 接口。

## Scope

- Product line: core
- Code paths allowed: `repo/pkg/ports/` only
- 例外：因扩展 `GPUInventory` 接口新增 `ListSpecAvailability` 方法，所有现有 `GPUInventory` 实现者（6 处：`NotConfigured`、`KubernetesGPUInventory`、`LocalGPUInventory`、`fakeGPUInventory`×2、`fallbackGPUInventory`）需补充空桩方法（仅 `return ErrUnsupported`/`ErrNotConfigured`），不含任何实现逻辑；完整实现在 issue-003 Adapters 批次

## Acceptance Criteria

- [ ] 新增 `pkg/ports/gpu_spec.go`，定义 `GPUSpecStore` 接口（List/Get/Create/Delete）+ GPUSpecCRD/GPUSpecNodeAffinity/GPUSpecVolcanoResources Go 类型
- [ ] 扩展 `pkg/ports/workload_runtime.go`，新增 `WorkloadInstanceStoreTx` 小接口（以 `UpsertStatusTx`），不破坏现有 6 个 mock
- [ ] 扩展 `pkg/ports/gpu_inventory.go`，新增 `ListSpecAvailability(tenant_id)` 方法签名（按租户配额余量 + 节点标签匹配查询）
- [ ] 复用已有 `pkg/ports/quota.go` 的 `QuotaService`/`QuotaStoreService` 接口，不新建
- [ ] `go build ./pkg/ports/...` 通过
- [ ] `go test ./pkg/ports/...` 通过
- [ ] `go build ./pkg/adapters/...` 通过（依赖空桩编译）
- [ ] `go build ./services/ani-gateway/...` 通过（依赖空桩编译）
- [ ] `gofmt -l` 对所有修改文件无输出
- [ ] `make validate-architecture` 通过
- [ ] `git diff --check` 通过

## Dependencies

#1

## Type

core

## Priority

high

## Labels

core

## Batch

M2.1-TASK-A

## References

- SPEC: §3.2 (Entity Definitions), §2.4 (File Structure)
- UX: N/A
