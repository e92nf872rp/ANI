# R-P0-4 · Data-Plane Readyz Health

> 日期：2026-06-23
> 分支：`feature/sprint14-core-resilience-semantics`
> 状态：Completed（local/logic verified）
> 范围：ANI Core only；不触碰 ANI Services 业务逻辑，不修改 `api/openapi/services/v1.yaml`
> 后续补齐：`SPRINT14-CORE-RESILIENCE-LIVE-GATE` 已在隔离 fixture 中覆盖 strong backend kill 后 readyz fail / recovery；production-ready 范围仅限该 fixture。

## 目标

把 Core 数据面依赖接入 bootstrap `/readyz`：ObjectStore、VectorStore、Kubernetes API 任一已配置后端 health 返回错误时，dependency probe 能暴露对应失败，并使 readyz 进入 degraded/503 路径。

本批没有执行真实后端 kill / fault-injection，只证明本地逻辑、接口和 adapter health 调用可复跑；单批次不声明 production ready。后续 aggregate live gate 已补齐 strong backend kill → readyz fail → recovery 的隔离 fixture 证据。

## 实现

- 新增 `ports.HealthChecker`，用于 bootstrap 保存通用 Kubernetes API health checker。
- `ports.ObjectStore` 新增 `Health(ctx) error`：
  - MinIO 使用签名 `GET /` 作为轻量后端探测。
  - NotConfigured 返回 `ports.ErrNotConfigured`。
- `ports.VectorStore` 拆分：
  - 新增 backend `Health(ctx) error`，Milvus 调 `/v2/vectordb/collections/list`。
  - 原集合级健康改名为 `CollectionHealth(ctx, ref)`，保留原语义。
- `ports.K8sClusterService` 新增 `Health(ctx) error`：
  - local service 返回 nil。
  - forwarding service 代理到底层 base service。
- `KubernetesRESTClient.Health(ctx)` 调 Kubernetes `/version`。
- `pkg/bootstrap/deps.go` 保存 `Capabilities.KubernetesAPI`。
- `pkg/bootstrap/probes.go` 追加：
  - `object-store`
  - `vector-store`
  - `kubernetes-api`
  - `ports.ErrNotConfigured` 被视为未启用，不让默认 local profile readiness 误失败。
- `Makefile` 新增 `validate-readyz-dataplane-live-gate`。当前 target 执行 local gate，并在输出中明确“未执行真实后端 kill”。

## TDD 证据

红灯：

```bash
go test ./pkg/bootstrap -run TestDependencyProbeChecksReportsObjectStoreUnavailable -v
```

失败原因：`object-store probe check missing`。

红灯：

```bash
go test ./pkg/bootstrap -run TestDependencyProbeChecksReportsVectorStoreUnavailable -v
```

失败原因：`VectorStore` 仍是集合级 `Health(ctx, ref)`，尚无 backend `Health(ctx)`。

红灯：

```bash
go test ./pkg/bootstrap -run 'TestDependencyProbeChecksReportsKubernetesAPIUnavailable|TestDependencyProbeChecksIgnoreNotConfiguredDataPlane' -v
```

失败原因：`Capabilities.KubernetesAPI` 字段不存在。

红灯：

```bash
go test ./pkg/adapters/runtime -run TestKubernetesRESTClientHealthCallsVersion -v
```

失败原因：`KubernetesRESTClient.Health` 未定义。

绿灯：

```bash
make validate-readyz-dataplane-live-gate
```

结果：PASS（local/logic gate；未执行真实后端 kill）。

相关包回归：

```bash
go test ./pkg/bootstrap ./pkg/ports ./pkg/adapters/runtime ./pkg/adapters/objectstore ./pkg/adapters/vectorstore ./services/ani-gateway/...
```

结果：PASS。

## 边界

- 单批次未执行 `kill` MinIO/Milvus/Kubernetes API 等真实后端；后续 Sprint14 aggregate live gate 仅证明隔离 fixture 内的 strong backend kill / recovery，不外推到现有后端自身 HA。
- R-P1-6 会进一步区分 strong/weak dependency 的 degraded 策略；本批只接入数据面健康信号。
- Gateway router 自身 `/readyz` 仍是独立轻量 health endpoint；本批按 Sprint14 主计划接 `pkg/bootstrap/probes.go`。
