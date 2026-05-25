# M1-RECONCILE-E · Metadata Leader Election

完成日期：2026-05-24
对应 Sprint：Sprint 5（2026-07-16 ~ 07-31）
验证结果：`go test ./pkg/adapters/runtime ./pkg/bootstrap -run 'TestLeaderElectingWorkloadReconcileController|TestNewCapabilitiesCanWrapReconcileControllerWithMetadataLeaderElection|TestNewCapabilitiesRejectsLeaderElectionWithoutIdentity|TestConfigEnvironmentOverridesWorkloadReconcileController'` EXIT:0

## 实现了什么

本批次补齐 WorkloadReconcileController 的单活运行边界。新增 `ports.ReconcileLeaderElector`，并通过 `LeaderElectingWorkloadReconcileController` 包装现有 controller，使后台 reconcile loop 只在取得 leader lease 后运行；`ReconcileNow` 仍委托给底层 controller，不改变 API 请求路径行为。

新增 metadata-backed leader elector。它通过 `ports.MetadataStore` 在 platform transaction 中维护 `control_plane_leases`，按 lease name、holder identity、TTL 和 renew interval 获取/续租/释放 leader lease。bootstrap 支持以下显式配置：

- `WORKLOAD_RECONCILE_LEADER_ELECTION_ENABLED`
- `WORKLOAD_RECONCILE_LEADER_IDENTITY`
- `WORKLOAD_RECONCILE_LEADER_LEASE_NAME`
- `WORKLOAD_RECONCILE_LEADER_LEASE_TTL_SECONDS`
- `WORKLOAD_RECONCILE_LEADER_RENEW_INTERVAL_SECONDS`

默认不开启 leader election；开启时必须提供唯一 worker identity，避免多副本共享同一身份导致单活语义失真。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `pkg/ports/reconcile_controller.go` | 修改 | 新增 `ReconcileLeaderElector` port |
| `pkg/adapters/runtime/reconcile_leader_election.go` | 新增 | leader-electing controller wrapper 与 metadata-backed elector |
| `pkg/adapters/runtime/reconcile_controller_test.go` | 修改 | 覆盖 wrapper 必须通过 elector 启动 delegate |
| `pkg/bootstrap/deps.go` | 修改 | `NewCapabilitiesWithConfig` 在显式配置下包装 reconcile controller |
| `pkg/bootstrap/deps_test.go` | 修改 | 覆盖 leader election wiring 与缺失 identity 拒绝 |
| `pkg/bootstrap/server.go` | 修改 | 新增 leader election 环境变量解析 |
| `pkg/bootstrap/server_test.go` | 修改 | 覆盖 leader election 环境变量覆盖 |
| `deploy/migrations/20260524_001_control_plane_leases.sql` | 新增 | 建立 control plane lease 表与过期索引 |

## 完工标准达成

- [x] controller 具备可插拔 leader election port。
- [x] controller 后台循环可被 leader elector gate。
- [x] metadata-backed elector 可通过 Core metadata transaction 维护 leader lease。
- [x] bootstrap 默认不改变现有单进程运行方式。
- [x] bootstrap 在显式开启 leader election 时要求 worker identity。
- [x] targeted Go 测试通过。

## 备注

- 本批次完成 controller leader election 的代码边界与 metadata-backed 实现，但尚未在多副本真实部署中执行 failover/live HA 验证。
- 本批次不改变 K8s/vCluster、KMS/SM4 或 Secret 注入真实 provider 状态。
