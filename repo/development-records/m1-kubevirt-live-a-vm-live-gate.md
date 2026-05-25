# M1-KUBEVIRT-LIVE-A — KubeVirt VM Live Gate

完成日期：2026-05-25
对应 Sprint：Sprint 5（收敛中；真实底座验证线）
验证结果：本地 contract / 单测 / 文档 / 回归门禁均通过；REAL-K8S-LAB-A `--live` 尚未执行。

## 实现了什么

新增 KubeVirt VM 真实底座验证门禁，固定 `validate-kubevirt-vm-live-gate` 入口。contract 模式校验门禁定义和文档闭环；live 模式使用 `kubectl` 验证 KubeVirt CRD、KubeVirt control plane 可用性、KubeVirt `VirtualMachine` 创建、启动、`VirtualMachineInstance` Ready/Running、VNC/console subresource 可达、停止和 VM 删除。后续 `M1-KUBEVIRT-LIVE-B` 为该 live 模式补充 `--evidence-output` / `ANI_KUBEVIRT_VM_LIVE_EVIDENCE_OUTPUT` JSON 证据归档能力。

本批次证明的是 KubeVirt VM 真实环境验证入口已经固化，不代表三台云 VM 已部署完成，也不代表 Core VM runtime 已经在真实 KubeVirt 上完成端到端执行记录。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `deploy/real-k8s-lab/kubevirt-vm-live-gate.yaml` | 新增 | M1-KUBEVIRT-LIVE-A live gate contract，列出 VM 创建、启动、停止、console/VNC 和删除检查 |
| `scripts/validate_kubevirt_vm_live_gate.py` | 新增 | contract validator 与可选 `--live` kubectl runner；后续支持 `--evidence-output` / `ANI_KUBEVIRT_VM_LIVE_EVIDENCE_OUTPUT` |
| `scripts/validate_kubevirt_vm_live_gate_test.py` | 新增 | 覆盖 contract gate、live runner、CLI live 配置失败路径和后续 evidence JSON 输出 |
| `Makefile` | 修改 | 新增 `make validate-kubevirt-vm-live-gate` |

## 完工标准达成

- [x] `M1-KUBEVIRT-LIVE-A` 固定 profile 和 YAML contract 已建立
- [x] contract gate 覆盖 KubeVirt CRD/control-plane、VM create/start/readiness/stop、console/VNC subresource 和 delete 检查步骤
- [x] live runner 只通过 `kubectl` 与真实 Kubernetes/KubeVirt API 交互，不绕过既有 runtime adapter 边界宣称 Core VM provider 完成
- [x] 文档闭环引用 `validate-kubevirt-vm-live-gate`
- [x] 后续 evidence 输出闭环可通过 `--evidence-output` 或 `ANI_KUBEVIRT_VM_LIVE_EVIDENCE_OUTPUT` 归档 live 结果 JSON

## 使用示例

```bash
KUBECONFIG=<management-kubeconfig> python scripts/validate_kubevirt_vm_live_gate.py --live --tenant-id tenant-a --namespace ani-tenant-tenant-a --vm-name ani-live-vm --evidence-output repo/development-records/live/kubevirt-vm-live-gate.json
```

## 本批次验证

以下命令均在 2026-05-25 于本地工作区执行并返回 `EXIT:0`：

- `python scripts/validate_kubevirt_vm_live_gate.py`
- `python scripts/validate_kubevirt_vm_live_gate_test.py`
- `make validate-kubevirt-vm-live-gate`
- `make validate-doc-entrypoints`
- `python scripts/validate_yaml.py api/openapi/v1.yaml api/openapi/services/v1.yaml deploy/real-k8s-lab/profile.yaml deploy/real-k8s-lab/vcluster-live-gate.yaml deploy/real-k8s-lab/vcluster-upgrade-live-gate.yaml deploy/real-k8s-lab/k8s-node-pool-live-gate.yaml deploy/real-k8s-lab/kubeovn-network-live-gate.yaml deploy/real-k8s-lab/kubevirt-vm-live-gate.yaml deploy/real-k8s-lab/reconcile-ha-live-gate.yaml deploy/real-k8s-lab/kms-sm4-live-gate.yaml deploy/real-k8s-lab/secrets-live-gate.yaml`
- `make validate-real-k8s-profile`
- `make validate-vcluster-live-gate`
- `make validate-vcluster-upgrade-live-gate`
- `make validate-k8s-node-pool-live-gate`
- `make validate-kubeovn-network-live-gate`
- `make validate-reconcile-ha-live-gate`
- `make validate-kms-sm4-live-gate`
- `make validate-secrets-live-gate`
- `make validate-sprint4-closure`
- `make validate-architecture`
- `make test`
- `git diff --check`

2026-05-25 追加校准：按 Sprint 5 KubeVirt 真实底座目标补齐 VM stop 覆盖。先运行 `python scripts/validate_kubevirt_vm_live_gate_test.py` 观察到缺少 `kubevirt-vm-stopped` 与 stop patch 的 RED 失败，再补充 contract 与 live runner，并重新执行上述 KubeVirt gate、YAML、文档入口、Sprint 4 闭环、架构、全量测试和 diff 检查。

## 尚未完成

- [ ] 在 REAL-K8S-LAB-A 三台云 VM 上执行 `python scripts/validate_kubevirt_vm_live_gate.py --live --evidence-output repo/development-records/live/kubevirt-vm-live-gate.json`
- [ ] 形成 KubeVirt VM start/stop lifecycle、console/VNC 和 delete 的真实执行记录
- [ ] 若后续需要证明 Core Instance API 直接驱动 KubeVirt provider，还需补 Core API 级 live gate
