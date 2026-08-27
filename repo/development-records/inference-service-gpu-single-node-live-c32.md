# INFERENCE-SERVICE-GPU-SINGLE-NODE-LIVE-C32

> 日期：2026-08-18
> 状态：live passed（生产 `ani-system/ani-gateway` + `inference-service`，`ANI_AUTH_MODE=auth_service`）
> 前置：`INFERENCE-SERVICE-CREATE-IMAGE-HARBOR-LIVE-C31`、`INFERENCE-SERVICE-GPU-LWS-VOLCANO-C20`、`INFERENCE-SERVICE-INCLUSTER-E2E-C21`

## 完成范围

- 经现网 `ani-gateway` 租户 Bearer 创建单节点整卡 GPU `InferenceService`：digest-pinned CUDA vLLM `image_ref`、`accelerator.spec_id=gpu-nvidia-geforce-rtx-4090-full`、`count_per_replica=1`、`placement_mode=single_node`、模型 `pvc://vllm-model#/models/qwen`。
- create 返回 202，产品 GET 达到 `running`；runtime 为 Deployment + ClusterIP，`schedulerName=volcano`，`nvidia.com/gpu=1`。产品 logs 返回真实 Pod 行且无 replica 字段。`invocation_url` / `endpoint_url` 保持 null。
- 现网 `ANI_AUTH_MODE` 保持 `auth_service`。不部署第二条 Gateway，不装 Console。同一 `service_id` 完成 stop→`stopped`（runtime 释放）和 start→`running`。用户服务保留，未做 delete。
- 补齐 Gateway search_path 上缺失的 `platform_workloads` 表（官方 `20260815000100_platform_workloads.sql`），否则 Core POST 500、产品 create 503。
- 未改 OpenAPI。跨节点 LWS 仍 skip。不得标记 GPU ready / runtime ready。

## Design Decisions

- 产品 HTTP 只打现网 `ani-gateway`，不经 Console nginx。
- 单节点 GPU 按 C20：写 `schedulerName=volcano`，不绑 `ani-inference` Queue，不渲染 LWS。
- create 请求路径 Ensure 与 worker Observe 共用 Core 幂等键；worker 对宽限期内未绑定的 create 不再 Ensure。
- P0 不建设调用网关，所以 `invocation_url` / `endpoint_url` 固定 null。
- 用户要的是可留下的 GPU 服务，本批次做 stop/start，但不做 delete。

## Deviations

- C21/C30/C31 的 live 含 stop/start/delete。C32 做 stop/start，但保留用户服务，不做 delete。
- C31 走 Console nginx；C32 按产品口径只走 Gateway。
- C20 live gate 仍是 `status: contract`；本批次不把 C20 改成 LWS live passed。

## Tradeoffs

- 静态 `CORE_SERVICE_TOKEN` 继续给 inference-service 调 Core，而不是完整 C7 按租户 mint。这是单租户 live 权宜，不是终态。
- CUDA 镜像首次拉取后，把 inference-service `INFERENCE_DEPLOY_TIMEOUT_SECONDS` 调到 1800，避免 worker 在镜像拉取期间回收 runtime。
- start 后本地 Gateway port-forward 曾断开，产品轮询中断；随后独立 GET 确认同一 `service_id` 已回到 `running`。

## Open Questions

- NVIDIA GPU Operator 与 `volcano-device-plugin` 抢 `nvidia.com/gpu` 的回归风险由 `GPU-PLUGIN-NODE-PARTITION-C33` 按节点拆开。
- 跨节点 LWS / gang Queue `ani-inference` 仍未 live。
- 私有 Harbor `imagePullSecrets` 仍未做。

## 验证证据

```text
cd /root/kubercon/ANI/repo
PATH=/tmp/ani-pybin:$PATH make validate-inference-gpu-single-node-live-gate
PATH=/tmp/ani-pybin:$PATH make validate-architecture
PATH=/tmp/ani-pybin:$PATH make validate-doc-entrypoints
git diff --check
```

evidence：`development-records/live-evidence/inference-gpu-single-node-live-20260818.json`
gate：`deploy/real-k8s-lab/inference-gpu-single-node-live-gate.yaml`（`status: live`）

## 明确未完成

- delete 产品 ops live（服务已保留）。
- 跨节点 LWS runtime live。
- 私有仓库节点 pull。
- 不得把本批次标为 GPU ready / runtime ready。
