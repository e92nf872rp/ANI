# M1-REAL-LAB-N — REAL-K8S-LAB Component Evidence Integrity Guard

完成日期：2026-05-25
对应 Sprint：Sprint 5（收敛中；真实底座验证线）
验证结果：TDD RED 已确认 component validator 返回 0 但未写出 per-gate evidence JSON 时，`--component-live` 会把该 gate 汇总为 passed；GREEN 后目标测试通过。

## 实现了什么

`scripts/validate_real_k8s_profile.py --component-live` 现在会在每个组件 validator 返回 0 后检查对应 `--evidence-output` 文件是否存在：

- evidence JSON 存在时，该 gate 才会计入 passed。
- validator 返回非零时，保持原有 failed 处理，记录 returncode 和 error。
- validator 返回 0 但 evidence JSON 缺失时，该 gate 会计入 failed，并记录 `returncode=0` 和 `missing evidence output: <path>`。

该守卫确保真实 lab component summary 的 passed gate 必须有实际 JSON 证据支撑，避免只有退出码、没有证据文件的误判。本批次不代表任何 component live gate 已在真实 lab 执行成功。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `scripts/validate_real_k8s_profile.py` | 修改 | `validate_component_live_gates()` 增加成功后 evidence 文件存在性检查 |
| `scripts/validate_real_k8s_profile_test.py` | 修改 | 新增 validator 成功但 evidence 缺失时应失败的 RED-GREEN 测试；成功路径 fake runner 写出 evidence |
| `repo/development-records/m1-real-lab-n-component-evidence-integrity-guard.md` | 新增 | 记录本批次边界、用法和验证命令 |

## 完工标准达成

- [x] 先写失败测试并确认 RED：validator 返回 0 但缺失 evidence 仍被汇总为 passed
- [x] `--component-live` 成功 gate 必须有 per-gate evidence JSON 文件
- [x] evidence 缺失会进入 failed summary，并保留 returncode 与 error
- [x] 原有 validator 非零失败聚合和 preflight 行为保持不变
- [x] 文档明确该能力不等同于真实 lab live 执行成功
- [ ] 在 REAL-K8S-LAB-A 三台云 VM 上用真实 component live run 产出所有 per-gate evidence JSON

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
