# INFERENCE-SERVICE-GPU-LWS-RUNTIME-FIX-C37

> 日期：2026-08-19
> 状态：local/logic verified
> 前置：`INFERENCE-SERVICE-ENGINE-VGPU-C36`
> 范围：默认 GPU/LWS 启动与 Core PlatformWorkload 渲染；不含 OpenAPI、不含 Console、不含现网 rollout、不含新产品 live

## 目标

把 4090 整卡 1 卡 / 2 卡 / 同机 LWS 验证里已经复现的产品路径缺陷收进默认实现：LWS 第一次 chat 因 Ray compiled DAG 失败、单节点多卡 `/dev/shm` 过小、GPU Deployment RollingUpdate 卡死 RWO PVC、SGLang 或无 Ray 的租户 command 误进 `leader_worker`、直连 ClusterIP smoke 超时过短。

## 完成范围

- 平台默认 LWS leader/worker 增加 `VLLM_USE_RAY_COMPILED_DAG=0`（Launch `rayEnv`、Core worker `rayGPUProcessEnv`、LWS 容器 env）。不在启动命令里 `pip install`。
- GPU `CountPerReplica>1` 的默认 vLLM argv 追加 `--disable-custom-all-reduce`。
- `leader_worker` 或 `AcceleratorCount>=2` 时 `/dev/shm` Memory emptyDir 为 `12Gi`，其余保持 `1Gi`。
- PlatformWorkload Deployment `strategy.type=Recreate`，避免 RWO 模型 PVC 上 RollingUpdate 卡死。
- `PlanTopology` 拒绝 SGLang 的 `leader_worker`；租户 `engine.command` 走 LWS 时必须自身包含 `ray start` 或 `multi-node-serving.sh`，否则 `UNSUPPORTED_TOPOLOGY`。`LaunchLeader` 对冻结 command 仍不包 Ray（C36 行为保留）。
- inference-service 直连 ClusterIP 的默认 HTTP timeout 从 15s 改为 120s，与 kube proxy 一致。
- 未改 OpenAPI。无新 live，不得标记 GPU ready / runtime ready。

## Design Decisions

- compiled DAG 关闭是镜像缺 pyarrow 时的最小默认；不把 `pip install` 放进产品启动路径。
- 消费级 4090 无 NVLink，多卡 NCCL 走 `/dev/shm`；12Gi 来自同机 2 卡 / LWS 已跑通的临时验证，不是生产容量承诺。
- Recreate 作用于全部 PlatformWorkload Deployment（含 CPU），因为模型 PVC 也是 RWO。
- C36 冻结 command 原样下发不改；缺 Ray 的 LWS 在准入层拒绝，避免 worker 仍 `ray start`、leader 却不进集群。

## 验证证据

```text
cd /root/kubercon/ANI/repo
GOWORK=off go test -C pkg/adapters/runtime -count=1 .
GOWORK=off go test -C services/inference-service -count=1 ./internal/engine ./internal/runtime ./internal/runtime/coresdk
PATH=/tmp/ani-pybin:$PATH make validate-architecture
PATH=/tmp/ani-pybin:$PATH make validate-doc-entrypoints
git diff --check
```

## 明确未完成

- 未滚动现网 `ani-gateway` / `inference-service`。
- 无产品路径 GPU 2 卡 / LWS live evidence；临时整卡验证不外推 runtime ready。
- Console 创建页仍未绑定 `engine.env` / `engine.command`。
- 跨节点 4090 LWS 因无 NVLink/RDMA，不能当产品能力。
- 不得把本批次标为 GPU ready / runtime ready。
