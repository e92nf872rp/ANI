# M1-K8S-LIVE-E — vCluster Upgrade Evidence Output

完成日期：2026-05-25
对应 Sprint：Sprint 5（2026-07-16 ~ 07-31）
验证结果：TDD RED 已确认 vCluster upgrade live gate CLI 不支持 `--evidence-output`；GREEN 后目标测试、contract gate、真实底座 profile contract、文档入口门禁、YAML 校验、架构门禁、`make test` 和 `git diff --check` 通过。

## 实现了什么

`scripts/validate_vcluster_upgrade_live_gate.py --live` 新增 `--evidence-output` 参数，并支持 `ANI_VCLUSTER_UPGRADE_LIVE_EVIDENCE_OUTPUT` 环境变量。live 执行成功后会创建父目录并写出稳定 JSON，用于归档 vCluster upgrade live gate 证据。

当前 JSON 证据来自既有 live 检查结果，包含：

- `status`
- `target_version`
- `kubeconfig`
- `proxy_status`

该批次只补齐证据输出能力，不代表 Core upgrade、Helm values、kubectl 或 Core proxy 已在真实 lab 执行成功。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `scripts/validate_vcluster_upgrade_live_gate.py` | 修改 | live 模式支持 `--evidence-output` / `ANI_VCLUSTER_UPGRADE_LIVE_EVIDENCE_OUTPUT` 并写出 JSON |
| `scripts/validate_vcluster_upgrade_live_gate_test.py` | 修改 | 新增 CLI evidence output 单元测试 |
| `CURRENT-SPRINT.md` / `ANI-06-开发计划.md` / `ANI-DOCS-INDEX.md` | 修改 | Sprint 状态、事实边界和下一步同步 |
| `repo/development-records/README.md` | 修改 | 批次索引追加 M1-K8S-LIVE-E |

## 使用方式

```bash
ANI_GATEWAY_URL=http://127.0.0.1:3000/api/v1 \
ANI_BEARER_TOKEN=<token> \
python scripts/validate_vcluster_upgrade_live_gate.py \
  --live \
  --tenant-id tenant-a \
  --cluster-id k8sclu-live \
  --target-version v1.31.0 \
  --vcluster-server https://k8sclu-live.example \
  --evidence-output repo/development-records/live/vcluster-upgrade-live-gate.json
```

## 完工标准达成

- [x] 先写失败测试并确认 RED：CLI 不接受 `--evidence-output`
- [x] live 模式支持 `--evidence-output`
- [x] live 模式支持 `ANI_VCLUSTER_UPGRADE_LIVE_EVIDENCE_OUTPUT`
- [x] 输出路径自动创建父目录
- [x] 输出稳定 JSON，保留 target version、kubeconfig 路径和 Core proxy HTTP 状态
- [x] 文档明确该能力不等于真实 lab live 执行完成
- [ ] live vCluster upgrade 真实执行验证

## 验证命令

```bash
python scripts/validate_vcluster_upgrade_live_gate_test.py
make validate-vcluster-upgrade-live-gate
make validate-real-k8s-profile
make validate-doc-entrypoints
python scripts/validate_yaml.py api/openapi/v1.yaml api/openapi/services/v1.yaml deploy/real-k8s-lab/profile.yaml deploy/real-k8s-lab/vcluster-live-gate.yaml deploy/real-k8s-lab/vcluster-upgrade-live-gate.yaml deploy/real-k8s-lab/k8s-node-pool-live-gate.yaml deploy/real-k8s-lab/kubeovn-network-live-gate.yaml deploy/real-k8s-lab/kubevirt-vm-live-gate.yaml deploy/real-k8s-lab/reconcile-ha-live-gate.yaml deploy/real-k8s-lab/kms-sm4-live-gate.yaml deploy/real-k8s-lab/secrets-live-gate.yaml
make validate-architecture
make test
git diff --check
```

## 备注

`M1-K8S-LIVE-E` 是 `M1-K8S-LIVE-C` 的证据归档增强。后续真实 lab 执行时，应同时保留命令输出、JSON evidence 文件和底层 Core/Helm/kubectl/Core proxy 结果。
