# M1-K8S-LIVE-A — vCluster Live Validation Gate

完成日期：2026-05-24
对应 Sprint：Sprint 5（2026-07-16 ~ 07-31）
验证结果：TDD RED 已确认缺少 `validate_vcluster_live_gate` validator、`validate-vcluster-live-gate` Make target 和入口文档引用；GREEN 后 `make validate-vcluster-live-gate`、`make validate-doc-entrypoints`、`make validate-real-k8s-profile`、`python scripts/validate_yaml.py deploy/real-k8s-lab/vcluster-live-gate.yaml api/openapi/v1.yaml api/openapi/services/v1.yaml`、`make validate-architecture`、`make test` 和 `git diff --check` 均通过。

## 实现了什么

为 vCluster 真实主链路建立固定 live 验证门禁：新增 `deploy/real-k8s-lab/vcluster-live-gate.yaml`、`scripts/validate_vcluster_live_gate.py`、`scripts/validate_vcluster_live_gate_test.py` 和 `make validate-vcluster-live-gate`。

默认 contract 模式只校验门禁定义、文档闭环和本地 validator 单元测试。真实环境就绪后，可通过 `--live` 或环境变量执行四段验证：

1. `helm upgrade --install` 安装/更新 vCluster Helm release。
2. `vcluster connect <cluster_id> --namespace <tenant namespace> --print` 打印 kubeconfig。
3. `kubectl --kubeconfig <generated> get --raw /version` 验证租户 vCluster API Server 可访问。
4. 通过 Core `/api/v1/k8s-clusters/{cluster_id}/proxy` 请求 `/version`，验证 live proxy 路径。

该批次不是 live 验收结果，不代表 Helm、vCluster kubeconfig、kubectl 或 Core proxy 已在 REAL-K8S-LAB-A 环境中成功跑通；它只提供固定门禁和可重复验证入口。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `deploy/real-k8s-lab/vcluster-live-gate.yaml` | 新增 | vCluster live 验证门禁契约 |
| `scripts/validate_vcluster_live_gate.py` | 新增 | contract/live validator，支持 Helm、vCluster、kubectl 和 Core proxy 检查 |
| `scripts/validate_vcluster_live_gate_test.py` | 新增 | 覆盖门禁契约和 live 命令编排 |
| `Makefile` | 修改 | 新增 `validate-vcluster-live-gate` |
| `repo/CURRENT-SPRINT.md` | 修改 | 标记 M1-K8S-LIVE-A contract gate，live 结果仍未完成 |
| `ANI-06-开发计划.md` | 修改 | 同步 Sprint 5 状态和未完成边界 |
| `ANI-DOCS-INDEX.md` | 修改 | 同步当前结论 |
| `repo/development-records/README.md` | 修改 | 追加批次归档索引 |

## 完工标准达成

- [x] 先写失败测试并确认 RED：缺少 validator module
- [x] 先运行 Make target 并确认 RED：缺少 `validate-vcluster-live-gate`
- [x] vCluster live gate contract 可描述 Helm/kubeconfig/kubectl/proxy 四段检查
- [x] validator 可在 contract 模式检查门禁定义和文档引用
- [x] validator 的 live 编排可由单元测试注入 runner 验证命令和 Core proxy payload
- [ ] REAL-K8S-LAB-A live Helm 安装验证
- [ ] 返回 kubeconfig 的真实 `kubectl --kubeconfig` 可用性验证
- [ ] live proxy 访问租户 vCluster API Server 验证

## Live 使用入口

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
