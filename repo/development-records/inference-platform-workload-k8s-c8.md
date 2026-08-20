# INFERENCE-PLATFORM-WORKLOAD-K8S-C8

> 日期：2026-08-15
> 状态：local/logic verified
> 方案依据：`services/docs/console-modules/inference/inference-service-design.md` 阶段 D 前置
> 前置：Core PR #99、`INFERENCE-PLATFORM-WORKLOAD-LOCAL-C5`、`INFERENCE-SERVICE-JWT-C7`

## 完成范围

- Core `PlatformWorkloadService` 增加 CPU `single_node` Kubernetes adapter：自行渲染 Deployment + ClusterIP Service，经现有 `KubernetesRESTClient.ApplyManifests` / `Do(DELETE|GET)` 落地，不走 `/instances` 的 `WorkloadProviderApply`。
- 继续只准入 digest-pinned image、`cluster_internal`、无 accelerator、无 `leader_worker`。跨租户 Get 404；delete 先删 runtime 再 tombstone。
- `stop` 删除 Deployment/Service，保留 PlatformWorkload 记录并清空 endpoint；`start` 按已存 spec 再 apply；`scale` 对运行中负载再 apply。
- 标签至少包含 `ani.platform_workload=inference` 与租户/workload 身份；不写 `ani.kubercloud.io/instance`。内部 endpoint 为 `http://{name}.{namespace}.svc:{port}`。
- Gateway 默认仍 local。`PLATFORM_WORKLOAD_PROVIDER=kubernetes_rest` 才切 K8s；未配置 K8s host 时 fail-closed，不静默回落“立刻 running”。
- 未改 OpenAPI；无 PostgreSQL PlatformWorkload 表；logs 仍返回空列表。

## Design Decisions

- 产品 port 仍是 `ports.PlatformWorkloadService`。K8s apply/observe/delete 是 adapter 内部 runtime，不新增产品 port。
- 不复用实例 `KubernetesDryRunRenderer` / `WorkloadSpec`，避免带上 instance identity 与 volume 语义。
- 本批次只做 local/logic：用 fake runtime 与 HTTP fake API 证明状态机和 manifest，不宣称 live / real-provider ready。

## Deviations

- 无 LWS、GPU、Volcano schedulerName、artifact/secret mount、真实 Pod 日志或 live evidence。
- 未配置 `PLATFORM_WORKLOAD_PROVIDER` 时 Gateway 继续 C5 local，立即 running。
- 无 Core 后台 PlatformWorkload reconciler；运行中/provisioning 的 Get 会 Observe Deployment `readyReplicas`。

## 验证证据

```text
cd repo
go test ./pkg/adapters/runtime/ -count=1 -run 'PlatformWorkload|PlanningRuntimeAllowsInference'
go test ./services/ani-gateway/ -count=1 -run PlatformWorkload
go test ./services/ani-gateway/internal/router/ -count=1 -run PlatformWorkload
PATH=/tmp/ani-pybin:$PATH make validate-architecture
PATH=/tmp/ani-pybin:$PATH make validate-doc-entrypoints
git diff --check
```

## 明确未完成

- 无真实集群 create/重启恢复/日志/伸缩/停启/删除 live evidence。
- 无 LWS、GPU Deployment、vLLM、PG PlatformWorkload store 或调用网关。
- 不得标记 control-plane ready、runtime ready、real-provider ready 或 production ready。

## 下一批次边界

下一批次才是 CPU single-node 真实集群 live gate（需人工确认物理机/集群），并归档脱敏 evidence。C8 不得标 live passed。
