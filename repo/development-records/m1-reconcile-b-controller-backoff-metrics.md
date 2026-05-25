# M1-RECONCILE-B · Controller Backoff And Metrics

完成日期：2026-05-23
对应 Sprint：Sprint 5（2026-07-16 ~ 07-31）
验证结果：`go test ./pkg/adapters/runtime ./pkg/bootstrap -run 'TestLocalWorkloadReconcileController|TestNewCapabilitiesDefaults|TestConfigEnvironmentOverridesWorkloadReconcileController|TestStartWorkloadReconcileControllerRequiresOptIn' -v` EXIT:0

## 实现了什么

本批次补齐 WorkloadReconcileController 的目标级失败退避和本地计数快照。单个 workload target 的 provider 观察失败不会终止整轮扫描，失败 target 在退避窗口内跳过并在窗口后重试；controller 暴露 tick、成功、失败和退避跳过计数，便于后续接入生产指标导出。

该批次仍不代表 controller leader election、Prometheus 指标导出或独立 worker 部署形态已经完成。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `pkg/ports/reconcile_controller.go` | 修改 | 新增 `FailureBackoffSeconds` 配置和 `ReconcileControllerMetrics` 计数快照 |
| `pkg/adapters/runtime/reconcile_controller.go` | 修改 | 新增目标级失败退避、失败后继续扫描、成功清除退避和 `Metrics()` |
| `pkg/adapters/runtime/reconcile_controller_test.go` | 修改 | 新增失败 target 退避、继续扫描、退避跳过和窗口后重试测试 |
| `pkg/bootstrap/server.go` | 修改 | 新增 `WORKLOAD_RECONCILE_FAILURE_BACKOFF_SECONDS` 环境变量读取 |
| `pkg/bootstrap/deps.go` | 修改 | 将 bootstrap 配置传入 controller config |
| `pkg/bootstrap/server_test.go` | 修改 | 覆盖退避环境变量和 controller config 接线 |

## 完工标准达成

- [x] 目标级 provider 错误不会中断同一轮中其它 target 的 reconcile。
- [x] 失败 target 会按 `FailureBackoffSeconds` 退避，窗口内跳过，窗口后重试。
- [x] controller 可读取 tick、成功、失败、退避跳过计数快照。
- [x] `WORKLOAD_RECONCILE_FAILURE_BACKOFF_SECONDS` 可配置退避秒数。
- [x] targeted Go 测试通过。

## 备注

- 后续仍需补 leader election、Prometheus 指标导出和独立 worker 部署形态。
- 本批次不触碰 K8s/vCluster、KMS/SM4 或 K8s Secret 注入真实 provider。
