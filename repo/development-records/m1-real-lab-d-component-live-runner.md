# M1-REAL-LAB-D — REAL-K8S-LAB Component Live Runner

完成日期：2026-05-25
对应 Sprint：Sprint 5（收敛中；真实底座验证线）
验证结果：TDD RED 已确认 `validate_real_k8s_profile.py` 只能校验总 profile contract / 基础 `--live` kubectl checks，不能按 `contract_gates` 统一启动组件级 live validators；GREEN 后目标测试通过。

## 实现了什么

为 `scripts/validate_real_k8s_profile.py` 增加 `--component-live` 模式。该模式会读取 `deploy/real-k8s-lab/profile.yaml` 的 `contract_gates`，逐个执行对应 validator：

```bash
python <validator_script> --live --evidence-output <component-evidence-dir>/<gate-id>.json
```

执行成功后，REAL-K8S-LAB-A 会生成汇总 evidence，包含：

- `profile`
- `status`，固定为 `component_live`
- `component_gates[]`
- 每个 gate 的 `id`、`profile`、`validator_script`、`evidence_output` 和 `passed`

本批次只提供统一启动和证据汇总能力，不代表 vCluster、vCluster upgrade、node pool、Kube-OVN、KubeVirt、reconcile HA、KMS/SM4 或 Secrets 任一组件 gate 已在真实 lab 执行成功。

后续 `M1-REAL-LAB-E` 已补充失败聚合：`--component-live` 会执行所有 indexed component gate 后再汇总 total/passed/failed 和失败原因，而不是在第一个失败处提前停止。

## 使用示例

```bash
python scripts/validate_real_k8s_profile.py --component-live \
  --component-evidence-dir repo/development-records/live/component-gates \
  --evidence-output repo/development-records/live/real-k8s-component-summary.json
```

真实执行前仍需按各组件 gate 要求提供 `KUBECONFIG`、`ANI_GATEWAY_URL`、`ANI_BEARER_TOKEN`、KMS/ObjectStore 等环境变量。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `scripts/validate_real_k8s_profile.py` | 修改 | 新增 `validate_component_live_gates`、`--component-live`、`--component-evidence-dir` 和 component summary evidence 写出 |
| `scripts/validate_real_k8s_profile_test.py` | 修改 | 新增 component live runner RED-GREEN 测试和 CLI summary 写出测试 |
| `repo/development-records/m1-real-lab-d-component-live-runner.md` | 新增 | 记录本批次边界、用法和验证命令 |

## 完工标准达成

- [x] 先写失败测试并确认 RED：profile validator 没有 component live runner
- [x] `--component-live` 按 `contract_gates` 逐个执行组件 validator
- [x] 每个组件 validator 使用 `--live --evidence-output`
- [x] `--component-evidence-dir` 控制组件 evidence 输出目录
- [x] `--evidence-output` 可写出 component live 汇总 JSON
- [x] 文档明确该 runner 不等同于真实 lab live 执行成功
- [ ] 在 REAL-K8S-LAB-A 三台云 VM 上执行 `--component-live` 并归档组件 evidence 与汇总 JSON

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
