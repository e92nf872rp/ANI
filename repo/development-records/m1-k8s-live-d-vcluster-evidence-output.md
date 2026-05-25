# M1-K8S-LIVE-D — vCluster Live Evidence Output

完成日期：2026-05-25
对应 Sprint：Sprint 5（收敛中；真实底座验证线）
验证结果：本地 contract / 单测 / 文档门禁通过；vCluster `--live` 尚未在 REAL-K8S-LAB-A 执行。

## 实现了什么

为 `M1-K8S-LIVE-A` vCluster live gate 增加 JSON evidence 输出能力。真实 lab 就绪后，执行者可以在 `validate_vcluster_live_gate.py --live` 后追加 `--evidence-output`，把 Helm/vCluster/kubectl/Core proxy 检查通过后的结果归档到固定 JSON 文件。

该批次只增加证据落盘能力，不改变 live 检查语义，也不代表 Helm 安装、vCluster kubeconfig 或 Core proxy 已经在真实 lab 跑通。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `scripts/validate_vcluster_live_gate.py` | 修改 | 新增 `--evidence-output` 和 `ANI_VCLUSTER_LIVE_EVIDENCE_OUTPUT`，live 成功后写出 JSON evidence |
| `scripts/validate_vcluster_live_gate_test.py` | 修改 | 覆盖 CLI live 模式写 evidence JSON |
| `development-records/m1-k8s-live-a-vcluster-live-gate.md` | 修改 | live 使用入口补充 evidence 输出路径 |
| `repo/CURRENT-SPRINT.md` | 修改 | 记录 M1-K8S-LIVE-D，并保留真实 live 尚未执行的事实边界 |
| `ANI-DOCS-INDEX.md` / `ANI-06-开发计划.md` | 修改 | 同步 vCluster live evidence output 状态 |
| `development-records/README.md` | 修改 | 追加 M1-K8S-LIVE-D 批次索引 |

## 使用方式

```bash
ANI_GATEWAY_URL=http://127.0.0.1:3000/api/v1 \
ANI_BEARER_TOKEN=<token> \
python scripts/validate_vcluster_live_gate.py \
  --live \
  --tenant-id tenant-a \
  --cluster-id k8sclu-live \
  --vcluster-server https://k8sclu-live.example \
  --evidence-output repo/development-records/live/vcluster-live-gate.json
```

## 完工标准达成

- [x] 先写失败测试并确认 RED：`--evidence-output` 尚未被 CLI 接受，文件不会创建
- [x] CLI 支持 `--evidence-output` 和 `ANI_VCLUSTER_LIVE_EVIDENCE_OUTPUT`
- [x] evidence 输出会创建父目录并写出稳定 JSON
- [x] 文档明确该能力不代表真实 lab `--live` 已执行

## 本批次验证

以下命令均在 2026-05-25 于本地工作区执行并返回 `EXIT:0`：

- `python scripts/validate_vcluster_live_gate_test.py`
- `make validate-vcluster-live-gate`
- `make validate-real-k8s-profile`
- `make validate-doc-entrypoints`
- `python scripts/validate_yaml.py api/openapi/v1.yaml api/openapi/services/v1.yaml deploy/real-k8s-lab/profile.yaml deploy/real-k8s-lab/vcluster-live-gate.yaml deploy/real-k8s-lab/vcluster-upgrade-live-gate.yaml deploy/real-k8s-lab/k8s-node-pool-live-gate.yaml deploy/real-k8s-lab/kubeovn-network-live-gate.yaml deploy/real-k8s-lab/kubevirt-vm-live-gate.yaml deploy/real-k8s-lab/reconcile-ha-live-gate.yaml deploy/real-k8s-lab/kms-sm4-live-gate.yaml deploy/real-k8s-lab/secrets-live-gate.yaml`
- `make validate-architecture`
- `make test`
- `git diff --check`

## 尚未完成

- [ ] 在 REAL-K8S-LAB-A 上执行 `validate_vcluster_live_gate.py --live --evidence-output`
- [ ] 归档真实 Helm 安装、vCluster kubeconfig、kubectl `/version` 和 Core proxy `/version` 结果
