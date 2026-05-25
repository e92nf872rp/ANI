# M1-K8S-LIVE-C — vCluster Upgrade Live Validation Gate

完成日期：2026-05-25
对应 Sprint：Sprint 5（2026-07-16 ~ 07-31）
验证结果：TDD RED 已确认缺少 `validate_vcluster_upgrade_live_gate` 模块；GREEN 后目标测试、contract gate、文档入口门禁、YAML 校验、`make test` 和 `git diff --check` 通过。

## 实现了什么

新增 vCluster upgrade live 验证门禁：`deploy/real-k8s-lab/vcluster-upgrade-live-gate.yaml` 固定 Core upgrade API、Helm values 目标版本、升级后 kubeconfig、kubectl `/version` 和 Core proxy `/version` 检查步骤。

新增 `scripts/validate_vcluster_upgrade_live_gate.py` 和 `scripts/validate_vcluster_upgrade_live_gate_test.py`。默认 contract 模式只校验 gate 结构和文档闭环；`--live` 模式需要 `ANI_GATEWAY_URL`、`ANI_BEARER_TOKEN`、真实 vCluster Helm release 和可用 kubectl，可通过 `--evidence-output` 或 `ANI_VCLUSTER_UPGRADE_LIVE_EVIDENCE_OUTPUT` 归档 JSON 证据，用于真实执行：

- `POST /api/v1/k8s-clusters/{cluster_id}/upgrade`
- `helm get values {cluster_id} --namespace {namespace} -a -o json`
- 检查 Helm values 中的 `controlPlane.distro.k8s.version`
- `vcluster connect {cluster_id} --namespace {namespace} --print`
- `kubectl --kubeconfig <generated> get --raw /version`
- Core proxy `POST /api/v1/k8s-clusters/{cluster_id}/proxy` 请求 `/version`

新增 `make validate-vcluster-upgrade-live-gate`，纳入 Sprint 5 固定 contract gate。该批次不代表真实 vCluster upgrade 已经完成；真实结果仍需后续 `--live --evidence-output` 执行记录证明。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `deploy/real-k8s-lab/vcluster-upgrade-live-gate.yaml` | 新增 | M1-K8S-LIVE-C contract gate 定义 |
| `scripts/validate_vcluster_upgrade_live_gate.py` | 新增 | contract/live validator |
| `scripts/validate_vcluster_upgrade_live_gate_test.py` | 新增 | validator 单元测试 |
| `Makefile` | 修改 | 新增 `validate-vcluster-upgrade-live-gate` |
| `CURRENT-SPRINT.md` / `ANI-06-开发计划.md` / `ANI-DOCS-INDEX.md` | 修改 | Sprint 状态和下一步边界同步 |

## 完工标准达成

- [x] 先写失败测试并确认 RED：缺少 `validate_vcluster_upgrade_live_gate`
- [x] Contract gate 覆盖 Core upgrade API
- [x] Contract gate 覆盖 Helm `controlPlane.distro.k8s.version`
- [x] Contract gate 覆盖升级后 vCluster kubeconfig 和 kubectl `/version`
- [x] Contract gate 覆盖升级后 Core proxy `/version`
- [x] Makefile 暴露固定入口 `make validate-vcluster-upgrade-live-gate`
- [ ] live vCluster upgrade 真实执行验证

## 验证命令

```bash
python scripts/validate_vcluster_upgrade_live_gate_test.py
python scripts/validate_vcluster_upgrade_live_gate.py
make validate-vcluster-upgrade-live-gate
ANI_GATEWAY_URL=http://127.0.0.1:3000/api/v1 ANI_BEARER_TOKEN=<token> python scripts/validate_vcluster_upgrade_live_gate.py --live --tenant-id tenant-a --cluster-id k8sclu-live --target-version v1.31.0 --vcluster-server https://k8sclu-live.example --evidence-output repo/development-records/live/vcluster-upgrade-live-gate.json
make validate-doc-entrypoints
python scripts/validate_yaml.py api/openapi/v1.yaml api/openapi/services/v1.yaml deploy/real-k8s-lab/profile.yaml deploy/real-k8s-lab/vcluster-live-gate.yaml deploy/real-k8s-lab/vcluster-upgrade-live-gate.yaml deploy/real-k8s-lab/k8s-node-pool-live-gate.yaml deploy/real-k8s-lab/kubeovn-network-live-gate.yaml deploy/real-k8s-lab/kubevirt-vm-live-gate.yaml deploy/real-k8s-lab/reconcile-ha-live-gate.yaml deploy/real-k8s-lab/kms-sm4-live-gate.yaml deploy/real-k8s-lab/secrets-live-gate.yaml
make test
git diff --check
```

## 备注

`M1-K8S-LIVE-C` 是 live 验证入口，不替代真实执行。真实 lab 需要先由 `M1-K8S-LIVE-A` 或等价流程创建可访问的 vCluster Helm release，并通过 `K8S_CLUSTER_PROVIDER_MODE=vcluster_helm` 让 Core upgrade API 委托 vCluster Helm provider。
