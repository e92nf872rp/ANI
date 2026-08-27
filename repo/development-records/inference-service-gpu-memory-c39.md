# INFERENCE-SERVICE-GPU-MEMORY-C39

> 日期：2026-08-21
> 状态：live passed；2026-08-21 二次 live 按用户要求保留整卡/vGPU 服务并做 ClusterIP 压测（`ANI_AUTH_MODE=auth_service`）
> 前置：`INFERENCE-SERVICE-GPU-MEMORY-CONTRACT-C38`（上游 PR #114 已合入）
> 范围：Gateway handler、`InferenceControl` proto、inference-service domain/createBody、Core capabilities 型号广告与 Kubernetes 渲染、现网产品 live；不含 Console 表单、不含 `/gpu-specs` 目录改写

## 目标

按已批准的 C38 契约实现加速器申请：`spec_id` 只表示 GPU 型号；`count` / `count_per_replica` 是申请卡数；可选 `memory`（MiB）不填即整卡，填写即 vGPU。并在现网产品路径证明 `memory` 会落到 volcano vGPU 资源。

## 完成范围

- Gateway 解析 Core `platform-workloads` 与 Services `InferenceService` 的可选 `accelerator.memory`，经内部 proto 冻结到 domain，再映射到 Core `AcceleratorMemoryMB`。
- Capabilities 广告型号 ID，例如 `gpu-nvidia-geforce-rtx-4090`，不再广告 `-full` / `-Nx`。历史后缀在准入时剥掉后仍按型号匹配。
- 有 `memory`：申请 `volcano.sh/vgpu-number=count` 与 `volcano.sh/vgpu-memory=ceil(memory_mib/10)`（现网 `gpuMemoryFactor=10`）。无 `memory`：申请 `nvidia.com/gpu=count`，即使 `spec_id` 带 `-8x`。
- 不再按 advertised `MemoryPerShareMB` 自动补显存。省略 `memory` 就是整卡。
- Leader-worker 角色缺卡数时默认每 Pod 1 卡，不把汇总 `AcceleratorCount` 抄进每个 role。
- Capabilities 对外仍只广告 `spec_id` / `available` / `max_single_node_count`；内部拆开整卡与 vGPU 容量，准入按有没有 `memory` 分别校验。
- JSON 显式 `memory: 0` 或负数在 Gateway 返回 400；内部 0 仍表示未填整卡。OpenAPI 描述改动不在本批，只保留代码注释。
- 现网滚动 `ani-gateway` / `inference-service` 到 `gpu-memory-c39-20260821`。live 先清理残留推理测试服务（含旧整卡 `inf-gpu-qwen` 与 `inf-c36/c37/c39` 残留），再证明：capabilities 广告 `gpu-nvidia-geforce-rtx-4090`；`memory: 0` 返回 `400 INVALID_ARGUMENT`；省略 `memory` 申请 `nvidia.com/gpu=1` 且无 volcano vGPU；填写 `memory=12280` 申请 `volcano.sh/vgpu-number=1` 与 `volcano.sh/vgpu-memory=1228`，无 `nvidia.com/gpu`；首次 live 两条产品路径 GET `running` 后均删除。CPU InferenceService 只临时 scale 到 0。为让整卡落到可调度节点，测试期间临时 scale 占用整卡的 instance 工作负载。用户 vGPU instance 未删。`ANI_AUTH_MODE` 保持 `auth_service`。
- 2026-08-21 按用户要求二次 live：`--keep --load-seconds 60`。整卡服务 `inf-c39-whole-f3cbfa4a` 与 vGPU 服务 `inf-c39-vgpu-f3cbfa4a` 均 GET `running` 后保留。模型 PVC 是 RWO，vGPU 改挂克隆卷 `vllm-model-c39-vgpu`（来自现网 snapshot class），整卡继续挂 `vllm-model`。产品无 `invocation_url`，压测走同租户 ClusterIP `/v1/chat/completions`，每条服务 60 秒、并发 4：整卡 2456/2456 成功、约 40.9 QPS、p50 92ms / p99 101ms；vGPU 2373/2373 成功、约 39.6 QPS、p50 95ms / p99 107ms。压测客户端已删。占用整卡 GPU 的 instance 仍保持 0 副本，避免挤掉整卡 InferenceService。不得标记 GPU ready / runtime ready。keep/load evidence：`development-records/live-evidence/inference-gpu-memory-keep-load-20260821.json`。
- live 前对现网 PostgreSQL 补了已有 additive migration `20260820000100_platform_workload_intent_status.sql`（`platform_workload_intents.status`），否则当前 Gateway 二进制无法写 Core platform-workload 变更。
- 不改 `/gpu-specs` 的 `-full` / `-Nx` 目录 ID。不新增 capabilities 广告字段。无 Console 表单。不得标记 GPU ready / runtime ready。

## Design Decisions

- 整卡/vGPU 只看有没有 `memory`，不再看 `spec_id` 后缀。
- volcano 显存换算固定 factor 10，与现网 volcano-device-plugin 一致；产品字段单位保持 MiB。
- 一张卡仍是一个显存池加有限槽位；`count` 是申请卡/槽数，不是等份切片。
- 为释放 RWO 模型 PVC，live 只 scale CPU runtime，不走产品 stop：现网对该 CPU 服务的 Core lifecycle 幂等键仍缓存着迁移前的失败结果。
- 整卡 live 必须让出唯一可调度的 `nvidia.com/gpu` 工作节点；控制面节点有 NoSchedule 污点，不能承载整卡 InferenceService。

## 验证证据

```text
cd /root/kubercon/ANI/repo
PATH=/tmp/ani-pybin:$PATH make test
PATH=/tmp/ani-pybin:$PATH make validate-architecture
PATH=/tmp/ani-pybin:$PATH make validate-doc-entrypoints
PATH=/tmp/ani-pybin:$PATH make validate-inference-control-plane
PATH=/tmp/ani-pybin:$PATH make validate-inference-gpu-memory-live-gate
git diff --check
```

evidence：`development-records/live-evidence/inference-gpu-memory-live-20260821.json`

keep/load evidence：`development-records/live-evidence/inference-gpu-memory-keep-load-20260821.json`

## 明确未完成

- Console 创建表单仍不暴露 `memory`。
- `/gpu-specs` 仍广告 `-full` / `-Nx`。
- 跨节点 LWS runtime live。
- 不得把本批次标为 GPU ready / runtime ready。
