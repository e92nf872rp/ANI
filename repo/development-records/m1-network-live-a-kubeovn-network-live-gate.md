# M1-NETWORK-LIVE-A — Kube-OVN Network Live Gate

完成日期：2026-05-25
对应 Sprint：Sprint 5（收敛中；真实底座验证线）
验证结果：python scripts/validate_kubeovn_network_live_gate_test.py EXIT:0，python scripts/validate_kubeovn_network_live_gate.py EXIT:0，make validate-kubeovn-network-live-gate EXIT:0，make validate-doc-entrypoints EXIT:0，python scripts/validate_yaml.py api/openapi/v1.yaml api/openapi/services/v1.yaml deploy/real-k8s-lab/profile.yaml deploy/real-k8s-lab/vcluster-live-gate.yaml deploy/real-k8s-lab/vcluster-upgrade-live-gate.yaml deploy/real-k8s-lab/k8s-node-pool-live-gate.yaml deploy/real-k8s-lab/kubeovn-network-live-gate.yaml deploy/real-k8s-lab/kubevirt-vm-live-gate.yaml deploy/real-k8s-lab/reconcile-ha-live-gate.yaml deploy/real-k8s-lab/kms-sm4-live-gate.yaml deploy/real-k8s-lab/secrets-live-gate.yaml EXIT:0，make validate-real-k8s-profile EXIT:0，make validate-vcluster-live-gate EXIT:0，make validate-vcluster-upgrade-live-gate EXIT:0，make validate-k8s-node-pool-live-gate EXIT:0，make validate-reconcile-ha-live-gate EXIT:0，make validate-kms-sm4-live-gate EXIT:0，make validate-secrets-live-gate EXIT:0，make validate-sprint4-closure EXIT:0，make validate-architecture EXIT:0，make test EXIT:0，git diff --check EXIT:0。

## 实现了什么

新增 Kube-OVN 网络真实底座验证门禁，固定 `validate-kubeovn-network-live-gate` 入口。contract 模式校验门禁定义和文档闭环；live 模式使用 `kubectl` 验证 Kube-OVN `Vpc/Subnet` CRD，并 apply/observe Kube-OVN `Vpc`、`Subnet`、Kubernetes `NetworkPolicy` 和 `Service` LoadBalancer 资源。后续 `M1-NETWORK-LIVE-B` 为该 live 模式补充 `--evidence-output` / `ANI_KUBEOVN_NETWORK_LIVE_EVIDENCE_OUTPUT` JSON 证据归档能力。

本批次证明的是 Kube-OVN 真实环境验证入口已经固化，不代表三台云 VM 已部署完成，也不代表 Core Network API 已经通过 Gateway 默认接线执行真实 provider。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `deploy/real-k8s-lab/kubeovn-network-live-gate.yaml` | 新增 | M1-NETWORK-LIVE-A live gate contract，列出 Kube-OVN CRD、Vpc/Subnet、NetworkPolicy 和 Service/LB 检查 |
| `scripts/validate_kubeovn_network_live_gate.py` | 新增 | contract validator 与可选 `--live` kubectl runner；后续支持 `--evidence-output` / `ANI_KUBEOVN_NETWORK_LIVE_EVIDENCE_OUTPUT` |
| `scripts/validate_kubeovn_network_live_gate_test.py` | 新增 | 覆盖 contract gate、live runner、CLI live 配置失败路径和后续 evidence JSON 输出 |
| `Makefile` | 修改 | 新增 `make validate-kubeovn-network-live-gate` |

## 完工标准达成

- [x] `M1-NETWORK-LIVE-A` 固定 profile 和 YAML contract 已建立
- [x] contract gate 覆盖 Kube-OVN `Vpc/Subnet`、Kubernetes `NetworkPolicy` 与 Service/LB live 检查步骤
- [x] live runner 只通过 `kubectl` 与真实 Kubernetes/Kube-OVN API 交互，不绕过既有 ports/adapters 边界宣称 Core provider 完成
- [x] 文档闭环引用 `validate-kubeovn-network-live-gate`
- [x] 后续 evidence 输出闭环可通过 `--evidence-output` 或 `ANI_KUBEOVN_NETWORK_LIVE_EVIDENCE_OUTPUT` 归档 live 结果 JSON

## 使用示例

```bash
KUBECONFIG=<management-kubeconfig> python scripts/validate_kubeovn_network_live_gate.py --live --tenant-id tenant-a --namespace ani-tenant-tenant-a --vpc-name ani-live-net --subnet-name ani-live-subnet --security-group-name ani-live-sg --load-balancer-name ani-live-lb --evidence-output repo/development-records/live/kubeovn-network-live-gate.json
```

## 尚未完成

- [ ] 在 REAL-K8S-LAB-A 三台云 VM 上执行 `python scripts/validate_kubeovn_network_live_gate.py --live --evidence-output repo/development-records/live/kubeovn-network-live-gate.json`
- [ ] 形成 Kube-OVN `Vpc/Subnet`、NetworkPolicy、Service/LB 的真实执行记录
- [ ] 若后续需要证明 Core Network API 直接驱动 Kube-OVN provider，还需补 Gateway/runtime 接线与对应 live gate
