# INFERENCE-SERVICE-ENGINE-VGPU-C36

> 日期：2026-08-19
> 状态：live passed（生产 `ani-system/ani-gateway` + `inference-service`，`ANI_AUTH_MODE=auth_service`；vGPU Pod Ready 未要求）
> 前置：`INFERENCE-SERVICE-ENGINE-EXTRA-ARGS-CONTRACT-C35`、`GPU-PLUGIN-NODE-PARTITION-C33`
> 范围：Gateway handler、`InferenceControl` proto、inference-service Launch/createBody、Core `platform-workloads` 可选 `env`、volcano vGPU inventory/capabilities/资源请求；不含 Console 表单、不含跨节点 LWS live

## 目标

按已批准的 C35 契约实现创建时冻结的 `engine.env` 与完整 `engine.command` argv，并把 volcano-device-plugin 的 vGPU 节点广告成可申请的 accelerator 规格。

## 完成范围

- Gateway 解析 `engine`，校验 POSIX 名、上限、保留 env 名；命中保留名返回 `400 INVALID_ARGUMENT`，不进入 gRPC。PATCH 仍只有 replicas。
- 内部 proto 增加 `InferenceServiceEngine`；创建请求与产品投影只读回显冻结的 env/command。
- `engine.command` 原样作为容器 `command`，`args` 为空，不拼接、不追加平台默认 Launch。省略 `engine` 时 Launch 行为与 C34 相同。
- `engine.env` 经 Core `PlatformWorkloadCreateRequest.env`（additive 可选字段）写入容器环境变量，不是 `sh -c` / `env NAME=VALUE` 拼进 argv。
- LWS leader 若带租户 `engine.command`，同样原样下发，不再包 Ray `sh -c`。
- GPU inventory 识别 `volcano.sh/vgpu-number` / `volcano.sh/vgpu-memory`。Capabilities 同时广告整卡 `-full` 与 vGPU `-Nx`。渲染时 `-Nx` 申请 `volcano.sh/vgpu-*`，不再误写 `nvidia.com/gpu`。
- Services OpenAPI `spec_id` 描述改为接受整卡与 vGPU 规格。
- 现网滚动 `ani-gateway` / `inference-service` 到 `engine-vgpu-c36-20260819`。live 证明保留 env 400、capabilities 广告 `gpu-nvidia-geforce-rtx-4090-8x`、租户 command/env 原样下发、vGPU Deployment 申请 `volcano.sh/vgpu-number=1` 与 `volcano.sh/vgpu-memory=1228` 且无 `nvidia.com/gpu`。测试服务已删除；用户整卡服务保留。`ANI_AUTH_MODE` 保持 `auth_service`。
- 无 Console 表单。不得标记 GPU ready / runtime ready。未要求 vGPU Pod Ready：RWO 模型 PVC 仍被保留的整卡服务占用。

## Design Decisions

- Core 增加可选 `env` 是为了兑现 C35「不是 shell 赋值」。未改 command allowlist；command 仍只要求非空。
- vGPU 规格 ID 与既有 `gpuSpecID(model, shares)` 一致：allocatable `vgpu-number` 为 2/4/8 时用实际份数，否则默认 4。现网 vGPU 节点广告 `8x`。`count_per_replica` 对应 `volcano.sh/vgpu-number`；显存按节点 `vgpu-memory / vgpu-number` 写入请求。
- 保留 env 名在 Gateway 与 inference-service 各校验一次。平台 LWS CUDA/Ray env 仍由 Core 注入。
- 同租户 `served_model_name` 仍唯一；live 测试用独立 served name，避免与保留整卡服务冲突。

## 验证证据

```text
cd /root/kubercon/ANI/repo
GOWORK=off go test -C pkg/adapters/runtime -count=1 .
GOWORK=off go test -C services/inference-service -count=1 ./...
GOWORK=off go test -C services/ani-gateway -count=1 ./internal/router -run 'TestInferenceCreate|TestInferenceRoutes'
PATH=/tmp/ani-pybin:$PATH python3 scripts/validate_openapi_spec_test.py
PATH=/tmp/ani-pybin:$PATH python3 scripts/validate_core_api_compatibility.py
PATH=/tmp/ani-pybin:$PATH python3 scripts/validate_inference_service_contract.py
PATH=/tmp/ani-pybin:$PATH make validate-inference-engine-vgpu-live-gate
PATH=/tmp/ani-pybin:$PATH make validate-architecture
PATH=/tmp/ani-pybin:$PATH make validate-doc-entrypoints
git diff --check
```

evidence：`development-records/live-evidence/inference-engine-vgpu-live-20260819.json`

## 明确未完成

- Console 创建页尚未绑定 `engine.env` / `engine.command`。
- vGPU Pod Ready / runtime `/health`：RWO 模型 PVC 被保留整卡服务占用，且 vGPU 与整卡不在同一节点分区。
- 跨节点 LWS runtime live。
- 不得把本批次标为 GPU ready / runtime ready。
