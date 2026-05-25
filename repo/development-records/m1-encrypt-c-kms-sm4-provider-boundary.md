# M1-ENCRYPT-C — KMS/SM4 Provider Code Boundary

完成日期：2026-05-24
对应 Sprint：Sprint 5（2026-07-16 ~ 07-31）
验证结果：TDD RED 已确认 Encryption service 缺少 provider port/evidence、Gateway 缺少 encryption runtime 选择；GREEN 后 targeted Go tests、`make validate-doc-entrypoints`、`python scripts/validate_yaml.py api/openapi/v1.yaml api/openapi/services/v1.yaml`、`make validate-real-k8s-profile`、`make validate-vcluster-live-gate`、`make validate-architecture`、`make test` 和 `git diff --check` 均通过。

## 实现了什么

把加密主链路从 local profile 扩展到可插拔 provider 边界：新增 `ports.EncryptionProvider`，覆盖 key material create/rotate/revoke/delete、seal object 和 unseal token 创建；`LocalEncryptionService` 在配置 provider 后会把生命周期和 seal/token 操作委托给 provider，并在 key/seal/token record 中保留 `Provider`、`RealProvider` 和 `ProviderRefs` evidence。

新增 `KMSSM4HTTPEncryptionProvider`，通过 HTTP 调用外部 KMS/SM4 provider 的 `/v1/keys`、`/v1/keys/{id}/rotate`、`/v1/keys/{id}/revoke`、`/v1/keys/{id}/delete`、`/v1/seal` 和 `/v1/unseal-token` 端点。Gateway 新增 `ENCRYPTION_PROVIDER_MODE=kms_sm4_http` runtime 选择，使用 `KMS_PROVIDER_BASE_URL`、`KMS_PROVIDER_BEARER_TOKEN` 和 `KMS_PROVIDER_NAME` 接入 provider，并把 `/api/v1/encryption/*` 路由注入到 provider-backed service。

该批次不是 live KMS 验证，也不是对象数据面 provider streaming 验证；它只完成 Core 加密 API 到外部 KMS/SM4 provider 的代码边界和可测试接线。对象内容 SM4-GCM 流式加解密代码边界后续由 `M1-ENCRYPT-D` 补齐。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `pkg/ports/encryption_resources.go` | 修改 | 新增 provider request/result/interface，以及 key/seal/token provider evidence 字段 |
| `pkg/adapters/runtime/local_encryption_service.go` | 修改 | 支持 `WithEncryptionProvider`，将 create/rotate/revoke/seal/unseal-token 委托给 provider |
| `pkg/adapters/runtime/kms_encryption_provider.go` | 新增 | KMS/SM4 HTTP provider adapter |
| `services/ani-gateway/encryption_runtime.go` | 新增 | Gateway `ENCRYPTION_PROVIDER_MODE=kms_sm4_http` runtime 选择 |
| `services/ani-gateway/internal/router/router.go` | 修改 | `RegisterOptions` 支持注入 EncryptionService |
| `services/ani-gateway/internal/router/encryption_resources.go` | 修改 | 支持注入 service，并把 provider-backed record 标记为 real dev profile |
| `services/ani-gateway/main.go` | 修改 | Gateway main 接入 encryption runtime |

## 完工标准达成

- [x] 先写失败测试并确认 RED：provider port/evidence 和 Gateway runtime 不存在
- [x] Encryption service 可委托 provider 创建 key material
- [x] Encryption service 可委托 provider rotate/revoke/delete key material
- [x] Encryption service 可委托 provider seal object 和创建 unseal token
- [x] Gateway 可通过 `ENCRYPTION_PROVIDER_MODE=kms_sm4_http` 接入 HTTP KMS/SM4 provider
- [x] Router response 对 provider-backed key 标记 real dev profile
- [ ] live KMS/SM4 backend 验证
- [x] 本地对象内容 SM4-GCM 流式加解密代码边界（由 `M1-ENCRYPT-D` 完成）
- [ ] 对象存储 + KMS/SM4 provider streaming 端到端验收

## 备注

该边界让 Core API 不再只能停留在 local seal/token 模拟：外部 KMS/SM4 provider 可接管密钥材料和 seal/token 结果。后续仍需要真实 KMS/SM4 服务、对象存储数据面和 live 验证记录共同证明可交付。
