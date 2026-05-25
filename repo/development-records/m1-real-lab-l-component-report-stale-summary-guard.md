# M1-REAL-LAB-L — REAL-K8S-LAB Component Report Stale Summary Guard

完成日期：2026-05-25
对应 Sprint：Sprint 5（收敛中；真实底座验证线）
验证结果：TDD RED 已确认 `--component-report` 会接受包含未知 gate id 的旧 summary 并生成无效复跑命令；GREEN 后目标测试通过。

## 实现了什么

`scripts/validate_real_k8s_profile.py --component-report` 现在会用当前 `deploy/real-k8s-lab/profile.yaml` 的 `contract_gates` 校验 summary 中的 `component_gates[].id`：

- summary 引用当前 profile 不存在的 gate id 时直接失败。
- summary 中 `component_gates` 不是列表、gate entry 不是对象或缺少 `id` 时直接失败。
- 有效 summary 仍会正常生成 `failed_gates`、`blocked_gates` 和 selected preflight/live 复跑命令。

该守卫防止真实 lab 首轮执行后使用过期 summary 或手工拼错 gate id，避免生成无法执行或误导的 `--component-gate` 命令。本批次不代表任何 component live gate 已在真实 lab 执行成功。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `scripts/validate_real_k8s_profile.py` | 修改 | 新增 `validate_component_summary_gate_ids()`，`component_summary_report()` 在生成 report 前校验 summary gate id |
| `scripts/validate_real_k8s_profile_test.py` | 修改 | 新增 helper 和 CLI 对 stale summary gate id 的 RED-GREEN 测试 |
| `repo/development-records/m1-real-lab-l-component-report-stale-summary-guard.md` | 新增 | 记录本批次边界、用法和验证命令 |

## 完工标准达成

- [x] 先写失败测试并确认 RED：stale summary gate id 未被拒绝
- [x] `component_summary_report()` 校验 summary gate id 必须存在于当前 profile
- [x] `--component-report` 对 stale summary 以非零状态失败且不写 report 文件
- [x] 有效 summary 的 failed/blocked 分类和复跑命令能力保持不变
- [x] 文档明确该能力不等同于真实 lab live 执行成功
- [ ] 在 REAL-K8S-LAB-A 三台云 VM 上对真实 component summary 使用该守卫并按 report 复跑失败 gate

## 验证命令

```bash
python scripts/validate_real_k8s_profile_test.py
python scripts/validate_real_k8s_profile.py --component-report /tmp/ani-real-k8s-component-summary.json --component-env-file /tmp/ani-real-k8s-component-live.env --component-evidence-dir /tmp/ani-component-gates --evidence-output /tmp/ani-real-k8s-component-report.json
make validate-real-k8s-profile
make validate-doc-entrypoints
python scripts/validate_yaml.py api/openapi/v1.yaml api/openapi/services/v1.yaml deploy/real-k8s-lab/profile.yaml deploy/real-k8s-lab/vcluster-live-gate.yaml deploy/real-k8s-lab/vcluster-upgrade-live-gate.yaml deploy/real-k8s-lab/k8s-node-pool-live-gate.yaml deploy/real-k8s-lab/kubeovn-network-live-gate.yaml deploy/real-k8s-lab/kubevirt-vm-live-gate.yaml deploy/real-k8s-lab/reconcile-ha-live-gate.yaml deploy/real-k8s-lab/kms-sm4-live-gate.yaml deploy/real-k8s-lab/secrets-live-gate.yaml
make validate-architecture
make test
git diff --check
```
