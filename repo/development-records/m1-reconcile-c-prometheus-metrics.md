# M1-RECONCILE-C · Controller Prometheus Metrics

完成日期：2026-05-23
对应 Sprint：Sprint 5（2026-07-16 ~ 07-31）
验证结果：`go test ./pkg/bootstrap -run 'TestProbeHandlerMetricsExportsReconcileControllerCounters|TestProbeHandlerHealthz|TestRunProbeChecksDegradesOnDependencyFailure' -v` EXIT:0

## 实现了什么

本批次把 `M1-RECONCILE-B` 已有的 WorkloadReconcileController 计数快照接入 bootstrap probe server 的 Prometheus text endpoint。启用 `HealthPort` 的服务现在除 `/healthz` 和 `/readyz` 外，还可通过 `/metrics` 暴露：

- `ani_workload_reconcile_ticks_total`
- `ani_workload_reconcile_successes_total`
- `ani_workload_reconcile_failures_total`
- `ani_workload_reconcile_backoff_skips_total`

指标包含 `service` label，值来自实现 `ports.ReconcileControllerMetricsReader` 的 controller。未配置 controller 或 controller 不支持 metrics reader 时，端点仍返回 0 值，保持 fail-open 的 scrape 行为。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `pkg/ports/reconcile_controller.go` | 修改 | 新增 `ReconcileControllerMetricsReader` port |
| `pkg/bootstrap/probes.go` | 修改 | 新增 `/metrics` Prometheus text handler |
| `pkg/bootstrap/probes_test.go` | 修改 | 覆盖 reconcile controller counters 的 Prometheus 输出 |
| `pkg/bootstrap/server.go` | 修改 | probe server 启动时接入 controller metrics reader |

## 完工标准达成

- [x] `/metrics` 返回 Prometheus text format。
- [x] 输出 controller tick/success/failure/backoff skip counters。
- [x] 指标包含 service label。
- [x] 未配置 metrics reader 时保持 0 值输出，不影响 health/readiness。
- [x] Gateway/API path 不直接依赖 controller 或 Prometheus SDK。
- [x] targeted Go 测试通过。

## 备注

- 本批次不是 controller leader election。
- 本批次不是独立 worker 部署形态。
- 本批次不引入 Prometheus Go SDK；当前仅导出标准 text format，后续可在 observability 收敛时接入更完整的 metrics registry。
