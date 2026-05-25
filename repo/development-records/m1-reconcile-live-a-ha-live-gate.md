# M1-RECONCILE-LIVE-A — Controller HA Live Validation Gate

完成日期：2026-05-25
对应 Sprint：Sprint 5（2026-07-16 ~ 07-31）
验证结果：TDD RED 已确认缺少 `validate_reconcile_ha_live_gate` 模块；GREEN 后目标测试、contract gate、文档入口门禁、YAML 校验、`make test` 和 `git diff --check` 通过。

## 实现了什么

新增 WorkloadReconcileController 多副本 HA live 验证门禁：`deploy/real-k8s-lab/reconcile-ha-live-gate.yaml` 固定两副本 reconcile worker、metadata-backed `control_plane_leases` active holder、Prometheus metrics、删除 leader pod 和 follower 接管 HA failover 检查步骤。

新增 `scripts/validate_reconcile_ha_live_gate.py` 和 `scripts/validate_reconcile_ha_live_gate_test.py`。默认 contract 模式只校验 gate 结构和文档闭环；`--live` 模式需要 `DATABASE_URL`、metrics URL 配置、真实 K8s 访问和至少两个带唯一 `ani.kubercloud.io/reconcile-identity` label 的 reconcile worker Pod，用于真实执行：

- `kubectl get pods -n {namespace} -l {worker_selector}`
- 查询 `control_plane_leases` 的 active holder
- 抓取 reconcile worker `/metrics`
- `kubectl delete pod {leader_pod}`
- 再次查询 `control_plane_leases`，确认 holder 发生切换
- 再次抓取 `/metrics`，确认 failover 后 metrics 仍可观测

新增 `make validate-reconcile-ha-live-gate`，纳入 Sprint 5 固定 contract gate。后续 `M1-RECONCILE-LIVE-B` 已补充 `--live --evidence-output` 证据归档能力；该批次不代表 controller 多副本 live HA failover 已经完成，真实结果仍需后续 `--live` 执行记录证明。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `deploy/real-k8s-lab/reconcile-ha-live-gate.yaml` | 新增 | M1-RECONCILE-LIVE-A contract gate 定义 |
| `scripts/validate_reconcile_ha_live_gate.py` | 新增 | contract/live validator |
| `scripts/validate_reconcile_ha_live_gate_test.py` | 新增 | validator 单元测试 |
| `Makefile` | 修改 | 新增 `validate-reconcile-ha-live-gate` |
| `CURRENT-SPRINT.md` / `ANI-06-开发计划.md` / `ANI-DOCS-INDEX.md` | 修改 | Sprint 状态和下一步边界同步 |

## 完工标准达成

- [x] 先写失败测试并确认 RED：缺少 `validate_reconcile_ha_live_gate`
- [x] Contract gate 覆盖两副本 reconcile worker 检查
- [x] Contract gate 覆盖 `control_plane_leases` leader holder 观测
- [x] Contract gate 覆盖 `/metrics` Prometheus counters 检查
- [x] Contract gate 覆盖删除 leader pod 后 follower 接管 HA failover
- [x] Makefile 暴露固定入口 `make validate-reconcile-ha-live-gate`
- [ ] controller 多副本 live HA failover 真实执行验证并归档 evidence JSON

## 验证命令

```bash
python scripts/validate_reconcile_ha_live_gate_test.py
python scripts/validate_reconcile_ha_live_gate.py
make validate-reconcile-ha-live-gate
make validate-doc-entrypoints
python scripts/validate_yaml.py api/openapi/v1.yaml api/openapi/services/v1.yaml deploy/real-k8s-lab/profile.yaml deploy/real-k8s-lab/vcluster-live-gate.yaml deploy/real-k8s-lab/k8s-node-pool-live-gate.yaml deploy/real-k8s-lab/reconcile-ha-live-gate.yaml deploy/real-k8s-lab/kms-sm4-live-gate.yaml deploy/real-k8s-lab/secrets-live-gate.yaml
make test
git diff --check
```

真实 lab 执行时可额外归档 evidence JSON：

```bash
python scripts/validate_reconcile_ha_live_gate.py --live \
  --database-url "$DATABASE_URL" \
  --metrics-url "$METRICS_URL" \
  --evidence-output repo/development-records/live/reconcile-ha-live-gate.json
```

## 备注

`M1-RECONCILE-LIVE-A` 是 live 验证入口，不替代真实执行。真实 lab 需要 reconcile worker 使用 `WORKLOAD_RECONCILE_LEADER_ELECTION_ENABLED=true`、唯一 `WORKLOAD_RECONCILE_LEADER_IDENTITY`、同一个 `WORKLOAD_RECONCILE_LEADER_LEASE_NAME`，并把 identity 暴露为 Pod label 供验证脚本定位 leader Pod。
