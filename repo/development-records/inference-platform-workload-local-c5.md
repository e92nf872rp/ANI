# INFERENCE-PLATFORM-WORKLOAD-LOCAL-C5

> 日期：2026-08-15
> 状态：local/logic verified
> 方案依据：`services/docs/console-modules/inference/inference-service-design.md` 阶段 A.4 / D 前置
> 前置：Core PR #99、Services PR #101、`INFERENCE-SERVICE-GATEWAY-GRPC-C4`

## 完成范围

- Core 新增 `ports.PlatformWorkloadService` 与 local adapter：只准入 CPU `single_node`；digest-pinned image；`cluster_internal`；立即收敛到 `running` 并返回内部 endpoint。
- leader_worker / accelerator 返回 `PRECONDITION_FAILED`；跨租户 Get 返回 404；delete 后 tombstone，Get 404；幂等 create/scale/lifecycle/delete。
- Gateway 注册 7 个 `platform-workloads*` 路由。只接受 `principal_kind=service` + `scope:platform-workloads:*`；租户 JWT / API key 为 403。dev 用 `X-Dev-Principal-Kind` / `X-Dev-Service-Scope`。
- mutation 返回 `202 AsyncTask`，`resource_id` 在接受时确定；logs 本地返回空列表。
- 平台工作负载使用独立 store，不写入 `/instances`。
- `WorkloadKindInference` 不再强制 GPU；GPU 准入改为 `RequiredCount > 0`，`gpu_container` 仍要求 GPU。
- inference-service 增加 Core SDK `InferenceRuntime` adapter（`CORE_API_BASE_URL`）；未配置时继续 fake。

## Design Decisions

- 先做 CPU local 纵切，不接 Deployment/LWS/vLLM，也不发 service JWT。
- Services 只通过 Core OpenAPI / `anisdk.Client` 调用，不 import `pkg/ports`。
- ModelCatalog 真实 adapter 仍留给后续批次。

## Deviations

- 无 PostgreSQL PlatformWorkload 表、无真实 K8s apply、无 service JWT 签发。
- local logs 为空，不伪造 runtime 日志。
- `CORE_API_BASE_URL` 未设置时 worker 仍走 fake，避免本地启动依赖 Gateway。

## 验证证据

```text
cd repo
go test ./pkg/adapters/runtime/ -count=1 -run 'PlatformWorkload|PlanningRuntimeAllowsInference'
go test ./services/ani-gateway/internal/router/ -count=1 -run PlatformWorkload
cd services/inference-service && GOWORK=off go test -race ./... -count=1
PATH=/tmp/ani-pybin:$PATH make validate-architecture
git diff --check
```

## 明确未完成

- 无真实 ModelCatalog、service JWT、PG PlatformWorkload store、Deployment/LWS/Volcano/vLLM 或 live evidence。
- 不得标记 control-plane ready、runtime ready 或 production ready。

## 下一批次边界

下一批次接真实 ModelCatalog，以及 CPU single-node PlatformWorkload 的真实 provider / live gate。
