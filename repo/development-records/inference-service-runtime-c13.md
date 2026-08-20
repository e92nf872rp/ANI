# INFERENCE-SERVICE-RUNTIME-C13

> 日期：2026-08-15
> 状态：local/logic verified
> 方案依据：`services/docs/console-modules/inference/inference-service-design.md` 阶段 D（单节点 CPU/GPU 同一入口）
> 前置：`INFERENCE-PLATFORM-WORKLOAD-K8S-LIVE-C12`

## 完成范围

- CPU 与 GPU 继续共用同一产品入口和同一条 Core `platform-workloads` 创建链路。差别只在 `resources.accelerator`：省略为 CPU Deployment，存在则在同一 Deployment 上申请 `nvidia.com/gpu`。
- Core local/Kubernetes provider 接受完整 accelerator（`spec_id` + 正数 count）；不完整规格 400；`leader_worker` 仍 422。未改 OpenAPI。
- inference-service 用同一套 vLLM OpenAI server 启动命令；CPU 追加 `--device cpu`，多卡 GPU 追加 `--tensor-parallel-size`。Core SDK 把 accelerator、artifacts 和启动参数传给 Core。
- `running` 前的 Health/Smoke 改为打 `runtime_endpoint` 的 `/health` 与有界 `/v1/chat/completions`。不注册产品 `/test`。
- 无真实 vLLM 镜像/模型挂载 live evidence，无 LWS，未 rollout in-cluster Gateway，不得标记 runtime ready。

## Design Decisions

- 不拆 CPU/GPU 两套 API、handler 或状态机。
- Core 仍不理解 vLLM；`spec_id` 写入保留标签，P0 整卡请求固定 `nvidia.com/gpu`。尚未做 GPUInventory 规格目录匹配。
- 模型 `object_ref` 已进入 Core create body；本批次不伪造 emptyDir 挂载。
- 内部 smoke 是 reconciler 条件，不是租户 HTTP `/test`。

## 验证证据

```text
cd repo
go test ./pkg/adapters/runtime/ -count=1 -run 'PlatformWorkload'
go test ./services/ani-gateway/internal/router/ -count=1 -run 'PlatformWorkloadHTTP'
go test ./services/inference-service/internal/engine/ ./services/inference-service/internal/runtime/coresdk/ -count=1
PATH=/tmp/ani-pybin:$PATH make validate-architecture
PATH=/tmp/ani-pybin:$PATH make validate-doc-entrypoints
git diff --check
```

## 明确未完成

- 无真实 vLLM CPU/GPU Pod live，无模型 artifact 挂载，无 GPUInventory 准入。
- 无 LWS / Volcano / 跨节点，不实现 `/test`。
- 不得把 in-cluster Gateway 或推理产品链路标为 runtime ready。

## 下一批次边界

有批准的 digest-pinned vLLM 镜像和可挂载小模型后，用同一入口跑 CPU/GPU 单节点 live。未明确要求前不滚动生产 Gateway。
