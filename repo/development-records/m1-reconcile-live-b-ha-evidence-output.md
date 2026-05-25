# M1-RECONCILE-LIVE-B — Controller HA Evidence JSON Output

完成日期：2026-05-25
对应 Sprint：Sprint 5（2026-07-16 ~ 07-31）
验证结果：TDD RED 已确认 `validate_reconcile_ha_live_gate.py --live` 不支持 `--evidence-output`；GREEN 后目标测试通过。

## 实现了什么

`scripts/validate_reconcile_ha_live_gate.py --live` 新增 evidence JSON 输出能力：

- `--evidence-output <path>`
- `ANI_RECONCILE_HA_LIVE_EVIDENCE_OUTPUT`

live 模式通过后会写出结构化 JSON，包含：

- `status`
- `namespace`
- `worker_selector`
- `lease_name`
- `metrics_url`
- `initial_leader`
- `new_leader`
- `deleted_pod`

未传 `--evidence-output` 且未设置环境变量时，原有 stdout JSON 行为保持不变。该批次只证明 evidence 文件输出能力，不证明 controller 多副本 HA failover 已在真实 lab 执行成功。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `scripts/validate_reconcile_ha_live_gate.py` | 修改 | live 结果补充 context 字段，新增 `write_live_evidence`、`--evidence-output` 和 `ANI_RECONCILE_HA_LIVE_EVIDENCE_OUTPUT` |
| `scripts/validate_reconcile_ha_live_gate_test.py` | 修改 | 新增 CLI evidence JSON 输出测试 |
| `development-records/m1-reconcile-live-a-ha-live-gate.md` | 修改 | 补充后续 evidence 输出说明和 live 使用示例 |
| `CURRENT-SPRINT.md` / `ANI-06-开发计划.md` / `ANI-DOCS-INDEX.md` / `development-records/README.md` | 修改 | Sprint 状态、事实边界和归档索引同步 |

## 完工标准达成

- [x] 先写失败测试并确认 RED：`--evidence-output` 未被 argparse 识别，输出文件不存在
- [x] `--live --evidence-output` 可写出 JSON 文件
- [x] `ANI_RECONCILE_HA_LIVE_EVIDENCE_OUTPUT` 可作为默认输出路径
- [x] live evidence 包含 namespace、worker selector、lease、metrics URL、holder 和 deleted pod
- [x] 未请求 evidence 文件时保持原 stdout JSON 行为
- [ ] controller 多副本 live HA failover 真实执行验证

## 验证命令

```bash
python scripts/validate_reconcile_ha_live_gate_test.py
make validate-reconcile-ha-live-gate
make validate-real-k8s-profile
make validate-doc-entrypoints
python scripts/validate_yaml.py api/openapi/v1.yaml api/openapi/services/v1.yaml deploy/real-k8s-lab/profile.yaml deploy/real-k8s-lab/vcluster-live-gate.yaml deploy/real-k8s-lab/vcluster-upgrade-live-gate.yaml deploy/real-k8s-lab/k8s-node-pool-live-gate.yaml deploy/real-k8s-lab/kubeovn-network-live-gate.yaml deploy/real-k8s-lab/kubevirt-vm-live-gate.yaml deploy/real-k8s-lab/reconcile-ha-live-gate.yaml deploy/real-k8s-lab/kms-sm4-live-gate.yaml deploy/real-k8s-lab/secrets-live-gate.yaml
make validate-architecture
make test
git diff --check
```

## 备注

真实 lab 执行时应使用：

```bash
python scripts/validate_reconcile_ha_live_gate.py --live \
  --database-url "$DATABASE_URL" \
  --metrics-url "$METRICS_URL" \
  --evidence-output repo/development-records/live/reconcile-ha-live-gate.json
```

该输出应作为真实执行记录的附件；没有 live 执行日志和 JSON 证据前，不得把 `M1-RECONCILE-LIVE-A/B` 标记为 real-provider 或 production ready。
