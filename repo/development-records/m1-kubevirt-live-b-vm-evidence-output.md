# M1-KUBEVIRT-LIVE-B — KubeVirt VM Evidence Output

完成日期：2026-05-25
对应 Sprint：Sprint 5（收敛中；真实底座验证线）
验证结果：python scripts/validate_kubevirt_vm_live_gate_test.py EXIT:0，make validate-kubevirt-vm-live-gate EXIT:0，make validate-real-k8s-profile EXIT:0，make validate-doc-entrypoints EXIT:0，python scripts/validate_yaml.py api/openapi/v1.yaml api/openapi/services/v1.yaml deploy/real-k8s-lab/profile.yaml deploy/real-k8s-lab/vcluster-live-gate.yaml deploy/real-k8s-lab/vcluster-upgrade-live-gate.yaml deploy/real-k8s-lab/k8s-node-pool-live-gate.yaml deploy/real-k8s-lab/kubeovn-network-live-gate.yaml deploy/real-k8s-lab/kubevirt-vm-live-gate.yaml deploy/real-k8s-lab/reconcile-ha-live-gate.yaml deploy/real-k8s-lab/kms-sm4-live-gate.yaml deploy/real-k8s-lab/secrets-live-gate.yaml EXIT:0，make validate-architecture EXIT:0，make test EXIT:0，git diff --check EXIT:0。

## 实现了什么

为 `scripts/validate_kubevirt_vm_live_gate.py --live` 增加结构化 evidence JSON 输出。调用方可以通过 `--evidence-output` 或 `ANI_KUBEVIRT_VM_LIVE_EVIDENCE_OUTPUT` 指定输出路径，validator 会创建父目录并写出稳定 JSON。

JSON 当前固定包含：

- `status`
- `namespace`
- `vm`

本批次只证明 KubeVirt VM live gate 具备证据归档能力，不代表 KubeVirt VM start/stop lifecycle、console/VNC 或 delete 已在真实 lab 执行成功。

## 使用示例

```bash
KUBECONFIG=<management-kubeconfig> python scripts/validate_kubevirt_vm_live_gate.py --live --tenant-id tenant-a --namespace ani-tenant-tenant-a --vm-name ani-live-vm --evidence-output repo/development-records/live/kubevirt-vm-live-gate.json
```

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `scripts/validate_kubevirt_vm_live_gate.py` | 修改 | `--live` 模式支持 `--evidence-output` 和 `ANI_KUBEVIRT_VM_LIVE_EVIDENCE_OUTPUT` |
| `scripts/validate_kubevirt_vm_live_gate_test.py` | 修改 | 新增 CLI live evidence JSON 输出回归测试 |
| `repo/development-records/m1-kubevirt-live-b-vm-evidence-output.md` | 新增 | 记录本批次边界、用法和验证命令 |

## 完工标准达成

- [x] 先新增失败测试覆盖 `--live --evidence-output` 写出 JSON
- [x] CLI 支持 `--evidence-output`
- [x] 环境变量支持 `ANI_KUBEVIRT_VM_LIVE_EVIDENCE_OUTPUT`
- [x] 输出路径父目录会自动创建
- [x] JSON key 稳定，便于归档和后续审计
- [x] 文档明确该输出能力不等同于真实 lab live 执行成功
- [ ] 在 REAL-K8S-LAB-A 三台云 VM 上执行真实 KubeVirt VM live gate 并归档 evidence JSON

## 验证命令

```bash
python scripts/validate_kubevirt_vm_live_gate_test.py
make validate-kubevirt-vm-live-gate
make validate-real-k8s-profile
make validate-doc-entrypoints
python scripts/validate_yaml.py api/openapi/v1.yaml api/openapi/services/v1.yaml deploy/real-k8s-lab/profile.yaml deploy/real-k8s-lab/vcluster-live-gate.yaml deploy/real-k8s-lab/vcluster-upgrade-live-gate.yaml deploy/real-k8s-lab/k8s-node-pool-live-gate.yaml deploy/real-k8s-lab/kubeovn-network-live-gate.yaml deploy/real-k8s-lab/kubevirt-vm-live-gate.yaml deploy/real-k8s-lab/reconcile-ha-live-gate.yaml deploy/real-k8s-lab/kms-sm4-live-gate.yaml deploy/real-k8s-lab/secrets-live-gate.yaml
make validate-architecture
make test
git diff --check
```
