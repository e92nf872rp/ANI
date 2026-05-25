# M1-REAL-LAB-B — REAL-K8S-LAB Component Gate Index

完成日期：2026-05-25
对应 Sprint：Sprint 5（收敛中；真实底座验证线）
验证结果：本地 contract / 单测 / 文档 / 回归门禁均通过；REAL-K8S-LAB-A `--live` 尚未执行。

## 实现了什么

在 `REAL-K8S-LAB-A` profile 中新增 `contract_gates` 索引，固定记录 Sprint 5 已建立的组件级真实底座验证入口：vCluster live、vCluster upgrade、Cluster API node pool、Kube-OVN network、KubeVirt VM、reconcile HA、KMS/SM4 和 Secret live gates。

该索引只用于 contract 模式和文档闭环，不放入 `components.*.live_checks`，因此不会让 `--live` 模式执行非 kubectl 的 Make contract gate；真实 lab live 模式仍只执行基础 Kubernetes/Kube-OVN/KubeVirt/vCluster kubectl 检查。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `deploy/real-k8s-lab/profile.yaml` | 修改 | 新增 `contract_gates`，索引所有 Sprint 5 组件级 live gate 的 profile、Make target、manifest 和 validator |
| `scripts/validate_real_k8s_profile.py` | 修改 | contract 模式校验 `contract_gates` 必填项、Make target、manifest 和 validator 文件存在 |
| `scripts/validate_real_k8s_profile_test.py` | 新增 | 覆盖总 profile 必须索引所有组件级 contract gate，以及缺失 gate 时必须失败 |
| `Makefile` | 修改 | `make validate-real-k8s-profile` 同时运行 validator 和单测 |

## 完工标准达成

- [x] `REAL-K8S-LAB-A` 总入口显式索引所有 Sprint 5 组件级真实底座 contract gate
- [x] validator 会拒绝缺少任一组件级 gate 的 profile
- [x] contract gate 索引不改变 `--live` kubectl 检查语义
- [x] 文档闭环仍保留真实 lab 尚未执行的事实边界

## 本批次验证

以下命令均在 2026-05-25 于本地工作区执行并返回 `EXIT:0`：

- `python scripts/validate_real_k8s_profile_test.py`
- `python scripts/validate_real_k8s_profile.py`
- `make validate-real-k8s-profile`
- `python scripts/validate_yaml.py api/openapi/v1.yaml api/openapi/services/v1.yaml deploy/real-k8s-lab/profile.yaml deploy/real-k8s-lab/vcluster-live-gate.yaml deploy/real-k8s-lab/vcluster-upgrade-live-gate.yaml deploy/real-k8s-lab/k8s-node-pool-live-gate.yaml deploy/real-k8s-lab/kubeovn-network-live-gate.yaml deploy/real-k8s-lab/kubevirt-vm-live-gate.yaml deploy/real-k8s-lab/reconcile-ha-live-gate.yaml deploy/real-k8s-lab/kms-sm4-live-gate.yaml deploy/real-k8s-lab/secrets-live-gate.yaml`
- `make validate-doc-entrypoints`
- `make validate-vcluster-live-gate`
- `make validate-vcluster-upgrade-live-gate`
- `make validate-k8s-node-pool-live-gate`
- `make validate-kubeovn-network-live-gate`
- `make validate-kubevirt-vm-live-gate`
- `make validate-reconcile-ha-live-gate`
- `make validate-kms-sm4-live-gate`
- `make validate-secrets-live-gate`
- `make validate-sprint4-closure`
- `make validate-architecture`
- `make test`
- `git diff --check`

## 尚未完成

- [ ] 在 REAL-K8S-LAB-A 三台云 VM 上执行各组件 gate 的 `--live` 模式
- [ ] 形成 vCluster、Kube-OVN、KubeVirt、node pool、reconcile HA、KMS/SM4 和 Secret 的真实执行记录
