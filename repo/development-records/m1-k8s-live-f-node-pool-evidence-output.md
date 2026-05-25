# M1-K8S-LIVE-F — Node Pool Evidence Output

完成日期：2026-05-25
对应 Sprint：Sprint 5（2026-07-16 ~ 07-31）
验证结果：TDD RED 已确认 node pool live gate CLI 不支持 `--evidence-output`；GREEN 后目标测试、contract gate、真实底座 profile contract、文档入口门禁、YAML 校验、架构门禁、`make test` 和 `git diff --check` 通过。

## 实现了什么

`scripts/validate_k8s_node_pool_live_gate.py --live` 新增 `--evidence-output` 参数，并支持 `ANI_K8S_NODE_POOL_LIVE_EVIDENCE_OUTPUT` 环境变量。live 执行成功后会创建父目录并写出稳定 JSON，用于归档 node pool live gate 证据。

当前 JSON 证据来自既有 live 检查结果，包含：

- `status`
- `node_pool_id`
- `machine_deployment`
- `namespace`
- `scaled_replicas`
- `gpu_workload`

该批次只补齐证据输出能力，不代表 Core node pool create/update、Cluster API MachineDeployment 观测或 GPU Pod 调度已在真实 lab 执行成功。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `scripts/validate_k8s_node_pool_live_gate.py` | 修改 | live 模式支持 `--evidence-output` / `ANI_K8S_NODE_POOL_LIVE_EVIDENCE_OUTPUT` 并写出 JSON |
| `scripts/validate_k8s_node_pool_live_gate_test.py` | 修改 | 新增 CLI evidence output 单元测试 |
| `development-records/m1-k8s-live-b-node-pool-live-gate.md` | 修改 | live 使用入口补充 evidence 输出路径 |
| `CURRENT-SPRINT.md` / `ANI-06-开发计划.md` / `ANI-DOCS-INDEX.md` | 修改 | Sprint 状态、事实边界和下一步同步 |
| `repo/development-records/README.md` | 修改 | 批次索引追加 M1-K8S-LIVE-F |

## 使用方式

```bash
ANI_GATEWAY_URL=http://127.0.0.1:3000/api/v1 \
ANI_BEARER_TOKEN=<token> \
KUBECONFIG=<management-kubeconfig> \
python scripts/validate_k8s_node_pool_live_gate.py \
  --live \
  --tenant-id tenant-a \
  --cluster-id k8sclu-live \
  --node-pool-name gpu-pool \
  --scaled-node-count 3 \
  --evidence-output repo/development-records/live/k8s-node-pool-live-gate.json
```

如 GPU smoke Pod 需要独立 workload cluster kubeconfig，可同时设置 `ANI_WORKLOAD_KUBECONFIG` 或传入 `--workload-kubeconfig`。

## 完工标准达成

- [x] 先写失败测试并确认 RED：CLI 不接受 `--evidence-output`
- [x] live 模式支持 `--evidence-output`
- [x] live 模式支持 `ANI_K8S_NODE_POOL_LIVE_EVIDENCE_OUTPUT`
- [x] 输出路径自动创建父目录
- [x] 输出稳定 JSON，保留 node pool、MachineDeployment、namespace、scaled replicas 与 GPU workload 名称
- [x] 文档明确该能力不等于真实 lab live 执行完成
- [ ] 真实节点池 live 扩缩容验证
- [ ] GPU 节点池真实调度验证

## 验证命令

```bash
python scripts/validate_k8s_node_pool_live_gate_test.py
make validate-k8s-node-pool-live-gate
make validate-real-k8s-profile
make validate-doc-entrypoints
python scripts/validate_yaml.py api/openapi/v1.yaml api/openapi/services/v1.yaml deploy/real-k8s-lab/profile.yaml deploy/real-k8s-lab/vcluster-live-gate.yaml deploy/real-k8s-lab/vcluster-upgrade-live-gate.yaml deploy/real-k8s-lab/k8s-node-pool-live-gate.yaml deploy/real-k8s-lab/kubeovn-network-live-gate.yaml deploy/real-k8s-lab/kubevirt-vm-live-gate.yaml deploy/real-k8s-lab/reconcile-ha-live-gate.yaml deploy/real-k8s-lab/kms-sm4-live-gate.yaml deploy/real-k8s-lab/secrets-live-gate.yaml
make validate-architecture
make test
git diff --check
```

## 备注

`M1-K8S-LIVE-F` 是 `M1-K8S-LIVE-B` 的证据归档增强。后续真实 lab 执行时，应同时保留命令输出、JSON evidence 文件、Core node pool create/update 响应、Cluster API MachineDeployment 观测结果和 GPU smoke Pod 调度结果。
