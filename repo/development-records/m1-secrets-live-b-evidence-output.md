# M1-SECRETS-LIVE-B — Kubernetes Secret Evidence JSON Output

完成日期：2026-05-25
对应 Sprint：Sprint 5（收敛中；真实底座验证线）
验证结果：TDD RED 已确认 `validate_secrets_live_gate.py --live` 不支持 `--evidence-output`，且 live 结果缺少归档上下文字段；GREEN 后目标测试通过。

## 实现了什么

为 `scripts/validate_secrets_live_gate.py --live` 增加结构化 evidence JSON 输出。调用方可以通过以下方式指定输出路径：

- `--evidence-output <path>`
- `ANI_SECRETS_LIVE_EVIDENCE_OUTPUT`

live 执行成功后，validator 会创建父目录并写出稳定 JSON。当前 evidence 字段包含：

- `status`
- `tenant_id`
- `gateway_url`
- `secret_id`
- `namespace`
- `pod`
- `vm`

输出不会归档 `ANI_BEARER_TOKEN`、Secret password/token 明文或其它 Secret data 值。

本批次只证明 Secret live gate 具备证据归档能力，不代表 Kubernetes Secret 写入、Pod env/file 可见性或 VM Secret volume 已在真实 lab 执行成功。

## 使用示例

```bash
KUBECONFIG=<management-kubeconfig> python scripts/validate_secrets_live_gate.py --live \
  --gateway-url "$ANI_GATEWAY_URL" \
  --ani-bearer-token "$ANI_BEARER_TOKEN" \
  --tenant-id tenant-a \
  --secret-name live-secret \
  --pod-name ani-secret-live-pod \
  --vm-name ani-secret-live-vm \
  --evidence-output repo/development-records/live/secrets-live-gate.json
```

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `scripts/validate_secrets_live_gate.py` | 修改 | live 结果补充安全上下文字段，新增 `write_live_evidence`、`--evidence-output` 和 `ANI_SECRETS_LIVE_EVIDENCE_OUTPUT` |
| `scripts/validate_secrets_live_gate_test.py` | 修改 | 新增 live result 上下文字段断言和 CLI evidence JSON 输出回归测试 |
| `repo/development-records/m1-secrets-live-b-evidence-output.md` | 新增 | 记录本批次边界、用法和验证命令 |

## 完工标准达成

- [x] 先写失败测试并确认 RED：CLI 不接受 `--evidence-output`，输出文件不存在，live 结果缺少上下文字段
- [x] `--live --evidence-output` 可写出 JSON 文件
- [x] `ANI_SECRETS_LIVE_EVIDENCE_OUTPUT` 可作为默认输出路径
- [x] 输出路径父目录会自动创建
- [x] JSON key 稳定，便于归档和后续审计
- [x] 不归档 bearer token 或 Secret 明文
- [x] 文档明确该输出能力不等同于真实 lab live 执行成功
- [ ] 在 REAL-K8S-LAB-A 三台云 VM 上执行真实 Secret live gate 并归档 evidence JSON

## 验证命令

```bash
python scripts/validate_secrets_live_gate_test.py
make validate-secrets-live-gate
make validate-real-k8s-profile
make validate-doc-entrypoints
python scripts/validate_yaml.py api/openapi/v1.yaml api/openapi/services/v1.yaml deploy/real-k8s-lab/profile.yaml deploy/real-k8s-lab/vcluster-live-gate.yaml deploy/real-k8s-lab/vcluster-upgrade-live-gate.yaml deploy/real-k8s-lab/k8s-node-pool-live-gate.yaml deploy/real-k8s-lab/kubeovn-network-live-gate.yaml deploy/real-k8s-lab/kubevirt-vm-live-gate.yaml deploy/real-k8s-lab/reconcile-ha-live-gate.yaml deploy/real-k8s-lab/kms-sm4-live-gate.yaml deploy/real-k8s-lab/secrets-live-gate.yaml
make validate-architecture
make test
git diff --check
```
