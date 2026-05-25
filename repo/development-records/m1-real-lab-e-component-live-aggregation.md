# M1-REAL-LAB-E — REAL-K8S-LAB Component Live Failure Aggregation

完成日期：2026-05-25
对应 Sprint：Sprint 5（收敛中；真实底座验证线）
验证结果：TDD RED 已确认 `validate_real_k8s_profile.py --component-live` 会在首个失败 gate 处提前退出，且 CLI 对失败 summary 不会返回非零状态；GREEN 后目标测试通过。

## 实现了什么

`scripts/validate_real_k8s_profile.py --component-live` 现在会执行 `profile.yaml` 中所有 `contract_gates` 对应的组件 validator，并在 summary evidence 中记录：

- `passed`
- `summary.total`
- `summary.passed`
- `summary.failed`
- 每个 `component_gates[]` 的 `id`、`profile`、`validator_script`、`evidence_output` 和 `passed`
- 失败 gate 的 `returncode` 和 `error`

CLI 行为：

- 先执行所有 indexed component live gate。
- 如传入 `--evidence-output`，无论成功或失败都会写出汇总 JSON。
- 任一 gate 失败时，写出 summary 后以非零状态退出，并列出失败 gate id。

本批次只改进真实 lab 首轮执行时的诊断能力：一次运行可以知道 Sprint 5 哪些 component live gate 仍失败。它不代表任一组件 gate 已在真实 lab 执行成功。

后续 `M1-REAL-LAB-F` 已补充 required env preflight：`--component-live` 会在启动 validators 前检查 `contract_gates[].required_env`，缺少环境变量时先写出 preflight summary 并以非零状态退出。

## 使用示例

```bash
python scripts/validate_real_k8s_profile.py --component-live \
  --component-evidence-dir repo/development-records/live/component-gates \
  --evidence-output repo/development-records/live/real-k8s-component-summary.json
```

失败时仍应保留 `real-k8s-component-summary.json`，用其中的 `summary.failed` 和失败 gate `error` 作为下一轮修复依据。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `scripts/validate_real_k8s_profile.py` | 修改 | `validate_component_live_gates` 改为执行所有 gate 并聚合 pass/fail；CLI 写出 summary 后对失败 gate 返回非零 |
| `scripts/validate_real_k8s_profile_test.py` | 修改 | 新增失败聚合和失败 summary 写出 RED-GREEN 测试 |
| `repo/development-records/m1-real-lab-e-component-live-aggregation.md` | 新增 | 记录本批次边界、用法和验证命令 |

## 完工标准达成

- [x] 先写失败测试并确认 RED：首个 component gate 失败会中断后续 gate，失败 summary 不触发 CLI 非零退出
- [x] `--component-live` 会执行所有 indexed component gate
- [x] summary JSON 记录 total/passed/failed
- [x] 失败 gate 记录 returncode 和 error
- [x] 任一 gate 失败时仍写出 summary，并以非零状态退出
- [x] 文档明确该聚合能力不等同于真实 lab live 执行成功
- [ ] 在 REAL-K8S-LAB-A 三台云 VM 上执行 `--component-live` 并归档真实组件 evidence 与汇总 JSON

## 验证命令

```bash
python scripts/validate_real_k8s_profile_test.py
make validate-real-k8s-profile
make validate-doc-entrypoints
python scripts/validate_yaml.py api/openapi/v1.yaml api/openapi/services/v1.yaml deploy/real-k8s-lab/profile.yaml deploy/real-k8s-lab/vcluster-live-gate.yaml deploy/real-k8s-lab/vcluster-upgrade-live-gate.yaml deploy/real-k8s-lab/k8s-node-pool-live-gate.yaml deploy/real-k8s-lab/kubeovn-network-live-gate.yaml deploy/real-k8s-lab/kubevirt-vm-live-gate.yaml deploy/real-k8s-lab/reconcile-ha-live-gate.yaml deploy/real-k8s-lab/kms-sm4-live-gate.yaml deploy/real-k8s-lab/secrets-live-gate.yaml
make validate-architecture
make test
git diff --check
```
