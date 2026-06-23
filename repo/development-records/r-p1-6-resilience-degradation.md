# R-P1-6 · Resilience Degradation Policy

> 记录类型：Execution / Sprint 14 Core resilience
> 完成日期：2026-06-23
> 分支：`feature/sprint14-core-resilience-semantics`
> 状态：local/logic verified；未执行真实后端 down / live gate；不声明 production ready
> 后续补齐：`SPRINT14-CORE-RESILIENCE-LIVE-GATE` 已在隔离 fixture 中覆盖 weak dependency down → degraded / HTTP 200 → recovery。

## 范围

本批把 readyz 从“任何依赖失败都 HTTP 503”细化为 strong/weak dependency 策略：

- 新增 `pkg/adapters/resilience/degradation.go`，显式声明依赖等级：
  - strong：`postgres`、`nats`、`redis`、`kubernetes-api` 以及未知依赖。
  - weak：`object-store`、`vector-store`。
- `pkg/bootstrap/probes.go` 的 `runProbeChecks` 根据依赖等级映射状态：
  - strong 失败：整体 `status: "fail"`，`/readyz` 返回 HTTP 503。
  - weak 失败：整体 `status: "degraded"`，`/readyz` 返回 HTTP 200，单项 check 标记 `degraded` 并保留 error。
- 新增 `make validate-resilience-degradation` 本地门禁，覆盖依赖等级表、strong fail、weak degraded 行为。

## 非范围 / 未完成

- 本批未执行真实 MinIO/Milvus backend down live gate；后续 aggregate live gate 已覆盖 object-store weak dependency down 的 readyz degraded 语义。
- 未把 degradation policy 扩展为用户业务 API 的新错误码；本批只改变 readyz/健康语义。
- production-ready 结论仅限 Sprint14 隔离 fixture；不声明后端自身 HA 或 full platform production ready。

## 验证

已通过：

```bash
make validate-resilience-degradation
```

批次收口还需与本分支常规门禁一起执行：

```bash
make test
make validate-architecture
make validate-doc-entrypoints
git diff --check
```
