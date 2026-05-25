# M1-REAL-LAB-C — REAL-K8S-LAB Live Evidence Output

完成日期：2026-05-25
对应 Sprint：Sprint 5（收敛中；真实底座验证线）
验证结果：本地 contract / 单测 / 文档 / 回归门禁均通过；REAL-K8S-LAB-A `--live` 尚未在真实 lab 执行。

## 实现了什么

在 `REAL-K8S-LAB-A` 总入口中为 `--live` 模式增加结构化 evidence 输出能力。真实 lab 就绪后，执行者可以用 `--evidence-output` 将总入口的 kubectl live 检查结果写成 JSON，作为后续真实执行批次的归档材料。

该批次只改变证据输出能力，不改变 live 检查集合：总入口仍只执行 `components.*.required=true` 的 Kubernetes、Kube-OVN、KubeVirt 和 vCluster 基础 kubectl 检查；组件级 vCluster upgrade、node pool、reconcile HA、KMS/SM4 和 Secrets live gate 由各自 validator 执行。后续 `M1-REAL-LAB-D` 已补充 `--component-live` 统一 runner，用于按 `contract_gates` 逐个启动组件 validator 并归档各自 evidence JSON。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `scripts/validate_real_k8s_profile.py` | 修改 | `validate_live` 返回 JSON-ready evidence；新增 `--evidence-output` 和 `ANI_REAL_K8S_EVIDENCE_OUTPUT` 输出路径 |
| `scripts/validate_real_k8s_profile_test.py` | 修改 | 覆盖 live evidence 结构、必需检查集合和 CLI JSON 写出 |
| `repo/CURRENT-SPRINT.md` | 修改 | 记录 M1-REAL-LAB-C，并保留 `--live` 尚未执行的事实边界 |
| `ANI-DOCS-INDEX.md` / `ANI-06-开发计划.md` | 修改 | 同步 `--live --evidence-output` 作为真实记录输出入口 |
| `development-records/README.md` | 修改 | 追加 M1-REAL-LAB-C 批次索引 |

## Evidence JSON 结构

`--live --evidence-output` 会写出以下字段：

- `profile`：当前 profile 名称，固定为 `REAL-K8S-LAB-A`
- `status`：固定为 `live`
- `minimum_nodes`：profile 要求的最小 Ready 节点数
- `kubeconfig_provided`：执行时是否提供 kubeconfig
- `checks`：每个必需 live check 的 `component`、`id`、`command`、`pass_condition` 和 `passed`

## 使用方式

```bash
KUBECONFIG=/path/to/real-lab.kubeconfig python scripts/validate_real_k8s_profile.py --live --evidence-output repo/development-records/live/real-k8s-lab-a.json
```

## 完工标准达成

- [x] `validate_live` 在全部检查通过后返回可序列化 evidence
- [x] `--evidence-output` 会创建父目录并写出稳定 JSON
- [x] 单测覆盖 evidence 结构、必需 live check 和 CLI 写出
- [x] 文档明确该能力不代表真实 lab `--live` 已经执行

## 本批次验证

以下命令均在 2026-05-25 于本地工作区执行并返回 `EXIT:0`：

- `python scripts/validate_real_k8s_profile_test.py`
- `python scripts/validate_real_k8s_profile.py`
- `make validate-real-k8s-profile`
- `python scripts/validate_yaml.py api/openapi/v1.yaml api/openapi/services/v1.yaml deploy/real-k8s-lab/profile.yaml deploy/real-k8s-lab/vcluster-live-gate.yaml deploy/real-k8s-lab/vcluster-upgrade-live-gate.yaml deploy/real-k8s-lab/k8s-node-pool-live-gate.yaml deploy/real-k8s-lab/kubeovn-network-live-gate.yaml deploy/real-k8s-lab/kubevirt-vm-live-gate.yaml deploy/real-k8s-lab/reconcile-ha-live-gate.yaml deploy/real-k8s-lab/kms-sm4-live-gate.yaml deploy/real-k8s-lab/secrets-live-gate.yaml`
- `make validate-doc-entrypoints`
- `make validate-sprint4-closure`
- `make validate-architecture`
- `make test`
- `git diff --check`

## 尚未完成

- [ ] 在 REAL-K8S-LAB-A 三台云 VM 上执行总入口 `--live --evidence-output`
- [ ] 执行并归档各组件 gate 的真实 lab `--live` 结果
