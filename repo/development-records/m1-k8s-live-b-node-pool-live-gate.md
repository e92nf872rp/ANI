# M1-K8S-LIVE-B — Node Pool Live Validation Gate

完成日期：2026-05-25
对应 Sprint：Sprint 5（2026-07-16 ~ 07-31）
验证结果：TDD RED 已确认缺少 `validate_k8s_node_pool_live_gate` 模块和 `M1-K8S-LIVE-B` gate；GREEN 后目标测试、contract gate、文档入口门禁、YAML 校验、`make test` 和 `git diff --check` 通过。

## 实现了什么

新增 K8s node pool live 验证门禁：`deploy/real-k8s-lab/k8s-node-pool-live-gate.yaml` 固定 Core node pool create/update、Cluster API `MachineDeployment` 观测和 GPU workload 调度检查步骤。

新增 `scripts/validate_k8s_node_pool_live_gate.py` 和 `scripts/validate_k8s_node_pool_live_gate_test.py`。默认 contract 模式只校验 gate 结构和文档闭环；`--live` 模式需要 `ANI_GATEWAY_URL`、`ANI_BEARER_TOKEN`、`KUBECONFIG`、真实 provider cluster 和可选 `ANI_WORKLOAD_KUBECONFIG`，可通过 `--evidence-output` 或 `ANI_K8S_NODE_POOL_LIVE_EVIDENCE_OUTPUT` 归档 JSON 证据，用于真实执行：

- `POST /api/v1/k8s-clusters/{cluster_id}/node-pools`
- `kubectl get machinedeployment {node_pool_name}`
- `PATCH /api/v1/k8s-clusters/{cluster_id}/node-pools/{node_pool_id}`
- 再次观测 `MachineDeployment.spec.replicas`
- 创建 GPU smoke Pod 并等待 `PodScheduled`

新增 `make validate-k8s-node-pool-live-gate`，纳入 Sprint 5 固定 contract gate。该批次不代表真实节点池 live 扩缩容或 GPU 调度已经完成；真实结果仍需后续 `--live --evidence-output` 执行记录证明。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `deploy/real-k8s-lab/k8s-node-pool-live-gate.yaml` | 新增 | M1-K8S-LIVE-B contract gate 定义 |
| `scripts/validate_k8s_node_pool_live_gate.py` | 新增 | contract/live validator |
| `scripts/validate_k8s_node_pool_live_gate_test.py` | 新增 | validator 单元测试 |
| `Makefile` | 修改 | 新增 `validate-k8s-node-pool-live-gate` |
| `CURRENT-SPRINT.md` / `ANI-06-开发计划.md` / `ANI-DOCS-INDEX.md` | 修改 | Sprint 状态和下一步边界同步 |

## 完工标准达成

- [x] 先写失败测试并确认 RED：缺少 `validate_k8s_node_pool_live_gate`
- [x] Contract gate 覆盖 Core node pool create/update
- [x] Contract gate 覆盖 Cluster API `MachineDeployment` 创建与扩缩容观测
- [x] Contract gate 覆盖 GPU workload 调度验证步骤
- [x] Makefile 暴露固定入口 `make validate-k8s-node-pool-live-gate`
- [ ] 真实节点池 live 扩缩容验证
- [ ] GPU 节点池真实调度验证

## 验证命令

```bash
python scripts/validate_k8s_node_pool_live_gate_test.py
python scripts/validate_k8s_node_pool_live_gate.py
make validate-k8s-node-pool-live-gate
ANI_GATEWAY_URL=http://127.0.0.1:3000/api/v1 ANI_BEARER_TOKEN=<token> KUBECONFIG=<management-kubeconfig> python scripts/validate_k8s_node_pool_live_gate.py --live --tenant-id tenant-a --cluster-id k8sclu-live --node-pool-name gpu-pool --scaled-node-count 3 --evidence-output repo/development-records/live/k8s-node-pool-live-gate.json
make validate-doc-entrypoints
python scripts/validate_yaml.py api/openapi/v1.yaml api/openapi/services/v1.yaml deploy/real-k8s-lab/profile.yaml deploy/real-k8s-lab/vcluster-live-gate.yaml deploy/real-k8s-lab/vcluster-upgrade-live-gate.yaml deploy/real-k8s-lab/k8s-node-pool-live-gate.yaml deploy/real-k8s-lab/kubeovn-network-live-gate.yaml deploy/real-k8s-lab/kubevirt-vm-live-gate.yaml deploy/real-k8s-lab/reconcile-ha-live-gate.yaml deploy/real-k8s-lab/kms-sm4-live-gate.yaml deploy/real-k8s-lab/secrets-live-gate.yaml
make test
git diff --check
```

## 备注

`M1-K8S-LIVE-B` 是 live 验证入口，不替代真实执行。真实 lab 需要同时具备 Cluster API provider、Core Gateway real provider runtime、管理集群 kubeconfig，以及用于 GPU smoke Pod 的 workload kubeconfig 或等价访问路径。
