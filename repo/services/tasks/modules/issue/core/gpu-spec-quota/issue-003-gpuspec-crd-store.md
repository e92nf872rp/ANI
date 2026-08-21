# GPUSpec CRD 定义 + CRD Store 实现

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/gpu-spec-quota/prd-gpu-spec-quota.md`
- UX: N/A（非前端）
- SPEC: `repo/services/tasks/modules/spec/core/gpu-spec-quota/spec-gpu-spec-quota.md`

## Description

作为开发者，定义 GPUSpec CRD 的 K8s manifest 并实现 CRD Store adapter，完成规格目录的持久化读写，为 Volcano 翻译和前端规格管理提供基础。

## Scope

- Product line: core
- Code paths allowed: `repo/deploy/manifests/gpu-spec-a/`、`repo/pkg/adapters/runtime/crd_gpu_spec_store.go`

## Acceptance Criteria

- [ ] 新增 `deploy/manifests/gpu-spec-a/00-gpuspec-crd.yaml`，定义 GPUSpec CRD（集群级，含 spec.node_affinity + spec.volcano_resources 字段）
- [ ] 新增 `pkg/adapters/runtime/crd_gpu_spec_store.go`，实现 `GPUSpecStore` 接口（List/Get/Create/Delete）
- [ ] Create 时幂等用 idempotency_key 做 K8s label，重复请求返回已有 CRD
- [ ] Delete 前查 workload_instances 是否有引用，有引用时返回错误（不在 adapter 层查 PG，返回 spec 供 handler 判断）
- [ ] GPUSpecCRD Go 类型与 `pkg/ports/gpu_spec.go` 定义一致（node_affinity 含 gpu_spec/gpu_sharing_spec/gpu_sharing_policy/gpu_mode，volcano_resources 含 wholecard/vgpu 两个子 map）
- [ ] `go build ./pkg/adapters/runtime/...` 通过
- [ ] `go test ./pkg/adapters/runtime/ -run "TestGPUSpec" -count=1` 通过
- [ ] `make validate-architecture` 通过
- [ ] `git diff --check` 通过

## Dependencies

#2

## Type

core

## Priority

high

## Labels

core

## Batch

M2.1-TASK-A

## References

- SPEC: §3.2 (GPUSpec CRD yaml + Go 类型), §2.4 (File Structure)
- UX: N/A
