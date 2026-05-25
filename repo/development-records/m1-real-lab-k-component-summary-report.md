# M1-REAL-LAB-K — REAL-K8S-LAB Component Summary Report

完成日期：2026-05-25
对应 Sprint：Sprint 5（收敛中；真实底座验证线）
验证结果：TDD RED 已确认 `validate_real_k8s_profile.py` 缺少 `component_summary_report()` 和 `--component-report` CLI；GREEN 后目标测试通过。

## 实现了什么

`scripts/validate_real_k8s_profile.py` 现在支持读取已有 component preflight 或 live summary JSON，并生成失败/阻塞 gate 的复查与复跑命令：

```bash
python scripts/validate_real_k8s_profile.py --component-report \
  repo/development-records/live/real-k8s-component-summary.json \
  --component-env-file repo/development-records/live/real-k8s-component-live.env \
  --component-evidence-dir repo/development-records/live/component-gates \
  --evidence-output repo/development-records/live/real-k8s-component-report.json
```

报告会输出：

- `failed_gates`：validator 已启动但 live 检查失败的 gate。
- `blocked_gates`：缺少 `required_env`、尚未启动 validator 的 gate。
- `next_commands`：每个失败或阻塞 gate 的 `--component-preflight --component-gate <id>` 和 `--component-live --component-gate <id>` 命令。

`--component-report` 是诊断模式，即使 summary 中有失败项也返回 0，方便真实 lab 首轮执行后生成下一步操作清单。本批次不代表任何 component live gate 已在真实 lab 执行成功。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `scripts/validate_real_k8s_profile.py` | 修改 | 新增 `component_summary_report()`、summary JSON loader、rerun command builder 和 `--component-report` CLI |
| `scripts/validate_real_k8s_profile_test.py` | 修改 | 新增 failed/blocked 分类和 CLI report 输出的 RED-GREEN 测试 |
| `repo/development-records/m1-real-lab-k-component-summary-report.md` | 新增 | 记录本批次边界、用法和验证命令 |

## 完工标准达成

- [x] 先写失败测试并确认 RED：缺少 report helper 和 CLI 参数
- [x] report 可把 `missing_env` gate 分类为 blocked
- [x] report 可把 validator returncode/error 失败分类为 failed
- [x] report 为每个 failed/blocked gate 生成 selected preflight 和 live 命令
- [x] `--component-report` 可读取 summary JSON 并通过 `--evidence-output` 写出 report JSON
- [x] 文档明确该能力不等同于真实 lab live 执行成功
- [ ] 在 REAL-K8S-LAB-A 三台云 VM 上对真实 component summary 生成 report 并按 report 复跑失败 gate

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
