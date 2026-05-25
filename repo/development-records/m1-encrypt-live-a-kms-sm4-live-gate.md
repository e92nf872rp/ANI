# M1-ENCRYPT-LIVE-A — KMS/SM4 Live Validation Gate

完成日期：2026-05-24
对应 Sprint：Sprint 5（2026-07-16 ~ 07-31）
验证结果：TDD RED 已确认缺少 `validate_kms_sm4_live_gate` 模块和 KMS/SM4 live gate 定义；GREEN 后 `python scripts/validate_kms_sm4_live_gate.py`、`python scripts/validate_kms_sm4_live_gate_test.py`、`make validate-kms-sm4-live-gate`、文档/API/SDK/Mock/真实底座 contract gate、架构校验、全量 `make test` 和 `git diff --check` 均通过。

## 实现了什么

新增 KMS/SM4 live backend 与对象存储 provider streaming 端到端验收的固定门禁：`deploy/real-k8s-lab/kms-sm4-live-gate.yaml` 定义 Core key/seal/token、KMS streaming seal/open、对象存储 sealed content 写入/读取的 live 检查。

新增 `scripts/validate_kms_sm4_live_gate.py`，默认 contract 模式校验 gate 和文档闭环；`--live` 模式通过 `ANI_GATEWAY_URL`、`ANI_BEARER_TOKEN`、`KMS_PROVIDER_BASE_URL`、`KMS_PROVIDER_BEARER_TOKEN`、`OBJECTSTORE_LIVE_PUT_URL` 和 `OBJECTSTORE_LIVE_GET_URL` 执行真实 Gateway/KMS/objectstore round trip。

后续 `M1-ENCRYPT-LIVE-B` 已补充 `--live --evidence-output` 证据归档能力，输出不包含 bearer token 或 presigned URL。

该批次不是 live KMS/SM4 backend 验证结果；它只建立固定入口和可执行校验逻辑，真实 lab 就绪后仍需用 `--live` 形成验证记录。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `deploy/real-k8s-lab/kms-sm4-live-gate.yaml` | 新增 | KMS/SM4 live backend、provider streaming 和对象存储 round trip gate 定义 |
| `scripts/validate_kms_sm4_live_gate.py` | 新增 | contract/live 校验脚本 |
| `scripts/validate_kms_sm4_live_gate_test.py` | 新增 | validator 单元测试和 fake live round trip |
| `Makefile` | 修改 | 新增 `validate-kms-sm4-live-gate` 入口 |

## 完工标准达成

- [x] 先写失败测试并确认 RED：KMS/SM4 live gate validator 不存在
- [x] Gate 定义包含 Core create SM4 key、Core seal、Core unseal-token、KMS stream seal/open 和 objectstore write/read
- [x] Contract 模式校验 gate 与文档闭环
- [x] Live 模式支持真实 Gateway、KMS/SM4 provider streaming 和对象存储 presigned URL round trip
- [ ] 真实 KMS/SM4 backend live 验证并归档 evidence JSON
- [ ] 真实对象存储 + provider streaming live 结果记录

## Live 证据归档示例

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

## 备注

Live 模式使用对象存储 presigned PUT/GET URL，避免 validator 直接绑定 MinIO/S3 SDK。真实环境中应由 lab 准备一次性验证对象路径和对应 presigned URL。
