# M1-RECONCILE-D · Independent Reconcile Worker

完成日期：2026-05-23
对应 Sprint：Sprint 5（2026-07-16 ~ 07-31）
验证结果：`go test ./pkg/bootstrap ./services/reconcile-worker/... -run 'TestRunWorkloadReconcileWorkerStartsControllerWithoutGRPC|TestLoadDefaultsToDedicatedReconcileWorker' -v` EXIT:0

## 实现了什么

本批次补齐 WorkloadReconcileController 的独立 worker 进程形态。新增 `bootstrap.RunWorkloadReconcileWorker`，可在不启动 gRPC server 的情况下启动 reconcile controller，并复用 bootstrap probe server 暴露 `/healthz`、`/readyz` 和 `/metrics`。

新增 `services/reconcile-worker` Go module 和入口程序。默认配置：

- `ServiceName=reconcile-worker`
- `HealthPort=9205`
- `WorkloadReconcileControllerEnabled=true`

`Makefile` 和 `go.work` 已接入该 worker，使 `make test` 和 `make build` 能覆盖新进程。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `pkg/bootstrap/server.go` | 修改 | 新增 `RunWorkloadReconcileWorker` 和可测试的 worker runtime helper |
| `pkg/bootstrap/server_test.go` | 修改 | 覆盖 worker 不启动 gRPC 也能启动并停止 controller |
| `services/reconcile-worker/main.go` | 新增 | 独立 reconcile worker 入口 |
| `services/reconcile-worker/internal/config/config.go` | 新增 | worker 默认配置 |
| `services/reconcile-worker/internal/config/config_test.go` | 新增 | 覆盖默认 service name、health port 和 controller enable |
| `services/reconcile-worker/go.mod` | 新增 | worker Go module |
| `go.work` | 修改 | 接入 worker module |
| `Makefile` | 修改 | `GO_PACKAGES`、`build` 和 `build-reconcile-worker` 接入 worker |

## 完工标准达成

- [x] worker 进程可不启动 gRPC server 而启动 reconcile controller。
- [x] worker context cancellation 会停止 controller 并退出。
- [x] worker 默认启用 reconcile controller。
- [x] worker 暴露与服务一致的 probe/metrics handler。
- [x] worker 被 `go.work`、`make test` 和 `make build` 覆盖。
- [x] targeted Go 测试通过。

## 备注

- 本批次不是 controller leader election；多副本 worker 的单活保证仍未完成。
- 本批次不改变真实 provider 能力，不代表 vCluster/KMS/Secret 注入真实链路完成。
