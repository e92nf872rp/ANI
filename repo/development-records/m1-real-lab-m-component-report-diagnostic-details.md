# M1-REAL-LAB-M — REAL-K8S-LAB Component Report Diagnostic Details

完成日期：2026-05-25
对应 Sprint：Sprint 5（收敛中；真实底座验证线）
验证结果：TDD RED 已确认 `--component-report` 只输出 failed/blocked gate id 和复跑命令，缺少真实 lab 首轮排障需要的 missing env、returncode 和 error 细节；GREEN 后目标测试通过。

## 实现了什么

`scripts/validate_real_k8s_profile.py --component-report <summary.json>` 现在会在 report 中输出 `gate_details`：

- blocked gate 记录 `id`、`status=blocked` 和 `missing_env`。
- failed gate 记录 `id`、`status=failed`、`missing_env`、`returncode` 和 `error`（如果原 summary 提供）。
- 原有 `failed_gates`、`blocked_gates`、`next_commands` 和 stale summary guard 保持不变。

该能力让真实 lab 首轮 `--component-live` 或 `--component-preflight` 失败后，report 同时给出复跑命令和失败原因摘要，避免操作员只看到 gate id 后还要手工回查 summary。本批次不代表任何 component live gate 已在真实 lab 执行成功。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `scripts/validate_real_k8s_profile.py` | 修改 | `component_summary_report()` 增加 `gate_details` 输出 |
| `scripts/validate_real_k8s_profile_test.py` | 修改 | 扩展 component report 测试，覆盖 blocked/failed gate diagnostic details |
| `repo/development-records/m1-real-lab-m-component-report-diagnostic-details.md` | 新增 | 记录本批次边界、用法和验证命令 |

## 完工标准达成

- [x] 先写失败测试并确认 RED：report 缺少 `gate_details`
- [x] `--component-report` 对 blocked gate 输出 `missing_env`
- [x] `--component-report` 对 failed gate 输出 `returncode` 和 `error`
- [x] 原有 failed/blocked 分类、复跑命令和 stale summary guard 保持可用
- [x] 文档明确该能力不等同于真实 lab live 执行成功
- [ ] 在 REAL-K8S-LAB-A 三台云 VM 上对真实 component summary 使用 `gate_details` 完成排障和复跑

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
