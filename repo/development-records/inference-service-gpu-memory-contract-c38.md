# INFERENCE-SERVICE-GPU-MEMORY-CONTRACT-C38

> 日期：2026-08-20  
> 状态：Core + Services 契约本地验证完成，待人工评审与独立契约 PR  
> 前置：`INFERENCE-SERVICE-ENGINE-VGPU-C36`  
> 范围：Core/Services OpenAPI、专项语义门禁、SDK/API 文档/Console 类型生成物、进度记录；不含 Gateway handler、proto、runtime 渲染、inventory 广告实现、live、Console 表单

## 目标

把加速器申请从 `-full` / `-Nx` 等份切片改成：`spec_id` 只表示 GPU 型号；`count` / `count_per_replica` 是申请卡数（整卡和 vGPU 都必填）；可选 `memory` 是申请显存。不填 `memory` 为整卡，填写为 vGPU。形态不写死在型号上，也不另加 `gpu_mode`。

## 契约结果

- Core `PlatformWorkloadAcceleratorResources` 与 Services `InferenceServiceAccelerator` 新增可选 `memory`（整数，最小 1，单位 MiB）。`required` 固定为型号与卡数：Core 是 `spec_id`+`count`，Services 是 `spec_id`+`count_per_replica`。整卡和 vGPU 都必须带卡数。
- 规范 `spec_id` 只表示型号，例如 `gpu-nvidia-geforce-rtx-4090`。同一型号每次创建可整卡或 vGPU，形态只看有没有 `memory`。
- 不填 `memory` 表示整卡；填写 `memory` 表示 vGPU。不另加 `gpu_mode`。
- 历史 `-full` / `-Nx` ID 仍可提交，实现剥掉后缀后按型号处理；不再用后缀表示整卡或 vGPU。
- 契约不把 vGPU 卡数写死为 1。
- 不改 GPUSpec 目录写契约、`/gpu-specs` 或实例 GPU 选择字段。
- 不新增 capabilities 广告字段；`PlatformWorkloadAcceleratorCapability` 仍只有 `spec_id`、`available`、`max_single_node_count`。
- 不新增错误码。容量不足仍走既有 `422 INSUFFICIENT_CAPACITY` / `ACCELERATOR_SPEC_UNAVAILABLE`。

## 强制边界

- 本批次不改 Gateway handler、`InferenceControl` proto、inventory/`gpuSpecID`、Kubernetes 渲染、`engine.Launch`、Console 创建表单。契约 PR 合入或明确批准前，不得按新字段下发 `volcano.sh/vgpu-memory`，也不得停止广告 `-full` / `-Nx`。
- 无新 live。不得标记 GPU ready / runtime ready。

## 验证证据

```text
cd /root/kubercon/ANI/repo
python3 scripts/validate_inference_service_contract_test.py                  PASS（17 tests）
python3 scripts/validate_inference_service_contract.py                       PASS
python3 scripts/validate_yaml.py api/openapi/v1.yaml api/openapi/services/v1.yaml  PASS
PATH=/tmp/ani-pybin:$PATH make validate-openapi-spec                         PASS（15 tests）
PATH=/tmp/ani-pybin:$PATH make validate-core-api-compatibility               PASS
PATH=/tmp/ani-pybin:$PATH make validate-services-contract                    PASS
PATH=/tmp/ani-pybin:$PATH make validate-doc-entrypoints                      PASS
git diff --check                                                              PASS
```

`make validate-services` 会刷新 SDK/API docs 并要求生成物相对提交后 HEAD 无漂移。本批次未提交时，该命令的生成物漂移检查会按预期停在未提交生成物上；提交后必须以个人仓库 GitHub Actions 为独立契约 PR 证据。

## 下一关

1. 人工评审：型号 + 必填卡数 + 可选 `memory`（不填整卡、填写 vGPU）。
2. 只提交本批契约、门禁、生成物和进度记录；个人仓库 CI 全绿后再创建上游独立契约 PR。
3. 契约批准后，实现层才按 `memory` 申请 volcano vGPU 显存，并广告型号级 capabilities。
