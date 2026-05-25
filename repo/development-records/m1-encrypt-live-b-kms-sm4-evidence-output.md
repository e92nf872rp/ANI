# M1-ENCRYPT-LIVE-B — KMS/SM4 Evidence JSON Output

完成日期：2026-05-25
对应 Sprint：Sprint 5（2026-07-16 ~ 07-31）
验证结果：TDD RED 已确认 `validate_kms_sm4_live_gate.py --live` 不支持 `--evidence-output`，且 live 结果缺少归档上下文字段；GREEN 后目标测试通过。

## 实现了什么

`scripts/validate_kms_sm4_live_gate.py --live` 新增 evidence JSON 输出能力：

- `--evidence-output <path>`
- `ANI_KMS_SM4_LIVE_EVIDENCE_OUTPUT`

live 模式通过后会写出结构化 JSON，包含：

- `status`
- `tenant_id`
- `gateway_url`
- `kms_base_url`
- `object_uri`
- `provider`
- `key_id`
- `sealed_object_uri`
- `object_round_trip_bytes`

为避免把敏感凭据写入批次证据，输出不会包含 ANI bearer token、KMS bearer token、objectstore presigned PUT URL 或 presigned GET URL。该批次只证明 evidence 文件输出能力，不证明 KMS/SM4 live backend、objectstore write/read 或 provider streaming round trip 已在真实 lab 执行成功。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `scripts/validate_kms_sm4_live_gate.py` | 修改 | live 结果补充安全上下文字段，新增 `write_live_evidence`、`--evidence-output` 和 `ANI_KMS_SM4_LIVE_EVIDENCE_OUTPUT` |
| `scripts/validate_kms_sm4_live_gate_test.py` | 修改 | 新增 CLI evidence JSON 输出测试，并断言输出不包含 token/presigned URL |
| `development-records/m1-encrypt-live-a-kms-sm4-live-gate.md` | 修改 | 补充后续 evidence 输出说明和 live 使用示例 |
| `CURRENT-SPRINT.md` / `ANI-06-开发计划.md` / `ANI-DOCS-INDEX.md` / `development-records/README.md` | 修改 | Sprint 状态、事实边界和归档索引同步 |

## 完工标准达成

- [x] 先写失败测试并确认 RED：`--evidence-output` 未被 argparse 识别，输出文件不存在，live 结果缺少上下文字段
- [x] `--live --evidence-output` 可写出 JSON 文件
- [x] `ANI_KMS_SM4_LIVE_EVIDENCE_OUTPUT` 可作为默认输出路径
- [x] live evidence 包含 tenant、Gateway/KMS 地址、object URI、provider、key、sealed URI 和 round-trip bytes
- [x] live evidence 不包含 bearer token 或 presigned URL
- [x] 未请求 evidence 文件时保持原 stdout JSON 行为
- [ ] 真实 KMS/SM4 backend live 验证
- [ ] 真实对象存储 + provider streaming live 结果记录

## 验证命令

```bash
python scripts/validate_kms_sm4_live_gate_test.py
make validate-kms-sm4-live-gate
make validate-real-k8s-profile
make validate-doc-entrypoints
python scripts/validate_yaml.py api/openapi/v1.yaml api/openapi/services/v1.yaml deploy/real-k8s-lab/profile.yaml deploy/real-k8s-lab/vcluster-live-gate.yaml deploy/real-k8s-lab/vcluster-upgrade-live-gate.yaml deploy/real-k8s-lab/k8s-node-pool-live-gate.yaml deploy/real-k8s-lab/kubeovn-network-live-gate.yaml deploy/real-k8s-lab/kubevirt-vm-live-gate.yaml deploy/real-k8s-lab/reconcile-ha-live-gate.yaml deploy/real-k8s-lab/kms-sm4-live-gate.yaml deploy/real-k8s-lab/secrets-live-gate.yaml
make validate-architecture
make test
git diff --check
```

## 备注

真实 lab 执行时应使用：

```bash
python scripts/validate_kms_sm4_live_gate.py --live \
  --gateway-url "$ANI_GATEWAY_URL" \
  --ani-bearer-token "$ANI_BEARER_TOKEN" \
  --kms-base-url "$KMS_PROVIDER_BASE_URL" \
  --kms-bearer-token "$KMS_PROVIDER_BEARER_TOKEN" \
  --object-put-url "$OBJECTSTORE_LIVE_PUT_URL" \
  --object-get-url "$OBJECTSTORE_LIVE_GET_URL" \
  --evidence-output repo/development-records/live/kms-sm4-live-gate.json
```

没有 live 执行日志和 JSON 证据前，不得把 `M1-ENCRYPT-LIVE-A/B` 标记为 real-provider 或 production ready。
