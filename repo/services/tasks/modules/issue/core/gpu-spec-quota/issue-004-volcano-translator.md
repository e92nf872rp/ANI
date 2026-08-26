# Volcano 资源翻译 Adapter

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/gpu-spec-quota/prd-gpu-spec-quota.md`
- UX: N/A（非前端）
- SPEC: `repo/services/tasks/modules/spec/core/gpu-spec-quota/spec-gpu-spec-quota.md`

## Description

作为开发者，实现 VolcanoResourceTranslator，将 spec_id 翻译为 nodeSelector + schedulerName:volcano + Pod 资源请求 + queue annotation，为创建实例走规格模式提供 K8s Pod spec 生成能力。

## Scope

- Product line: core
- Code paths allowed: `repo/pkg/adapters/runtime/volcano_resource_translator.go`

## Acceptance Criteria

- [ ] 新增 `pkg/adapters/runtime/volcano_resource_translator.go`，实现 `Translate(spec_id, queue_name, count)` 方法
- [ ] 从 GPUSpecStore.Get(spec_id) 获取规格定义
- [ ] nodeSelector 构建规则：公共 `ani.kubercloud.io/gpu-mode` 物理隔离；整卡模式用 `ani.kubercloud.io/gpu-spec`；vGPU 模式用 `ani.kubercloud.io/gpu-sharing-spec` + `ani.kubercloud.io/gpu-sharing-policy`
- [ ] schedulerName 固定为 `"volcano"`
- [ ] resourceRequests 构建：整卡模式从 VolcanoResources.Wholecard map 选取（nvidia.com/gpu / huawei.com/Ascend910）；vGPU 模式从 VolcanoResources.VGPU map 选取（volcano.sh/vgpu-memory 由 Adapter 算出不能传空字符串 + volcano.sh/vgpu-number）
- [ ] annotations 构建：`scheduling.volcano.sh/queue-name: queue_name`
- [ ] spec_id 不存在时返回明确错误
- [ ] `go build ./pkg/adapters/runtime/...` 通过
- [ ] `go test ./pkg/adapters/runtime/ -run "TestVolcanoTranslator" -count=1` 通过
- [ ] `make validate-architecture` 通过
- [ ] `git diff --check` 通过

## Dependencies

#3

## Type

core

## Priority

high

## Labels

core

## Batch

M2.1-TASK-A

## References

- SPEC: §5.1 (Volcano 翻译算法), §3.2 (GPUSpecNodeAffinity/VolcanoResources)
- UX: N/A
