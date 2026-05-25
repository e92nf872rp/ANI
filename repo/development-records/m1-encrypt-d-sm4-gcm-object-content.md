# M1-ENCRYPT-D — SM4-GCM Object Content Streaming Boundary

完成日期：2026-05-24
对应 Sprint：Sprint 5（2026-07-16 ~ 07-31）
验证结果：TDD RED 已确认缺少 SM4 block cipher、对象内容 seal/open request type 和 `EncryptionService` 流式方法；GREEN 后 targeted Go tests、runtime package tests、Gateway package tests、文档入口校验、API YAML/API docs/SDK/Mock/真实底座 contract gate、架构校验、全量 `make test` 和 `git diff --check` 均通过。

## 实现了什么

新增对象内容级 SM4-GCM 流式加解密代码边界：`EncryptionService` 现在可通过 reader/writer 对对象内容执行分块 seal/open，返回 sealed URI、nonce、chunk metadata、明文/密文 SHA256 和 provider evidence。

本地 runtime 新增无外部依赖的 SM4 block cipher，并用国密 SM4 标准向量测试证明 block 实现；分块内容加密使用 SM4-GCM，每个 chunk 独立认证，open 时校验 GCM tag、明文 digest、密文 digest 和 frame 边界。

该批次不是 KMS/SM4 live backend 验证，也没有把对象数据面接入真实 MinIO/KMS provider；provider-backed key 当前仍要求后续 provider streaming 支持。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `pkg/ports/encryption_resources.go` | 修改 | 新增对象内容 seal/open request、record 和 reader/writer service 方法 |
| `pkg/adapters/runtime/sm4.go` | 新增 | 本地 SM4 block cipher 实现，供 GCM 内容加密使用 |
| `pkg/adapters/runtime/local_encryption_service.go` | 修改 | 新增 SM4-GCM 分块 seal/open、frame/digest 校验和 provider-backed key 边界错误 |
| `pkg/adapters/runtime/local_encryption_service_test.go` | 修改 | 新增 SM4 标准向量测试和对象内容多 chunk seal/open 测试 |

## 完工标准达成

- [x] 先写失败测试并确认 RED：SM4 block cipher、内容加密 request type 和 service 方法不存在
- [x] SM4 block cipher 通过标准向量 `0123456789abcdeffedcba9876543210 -> 681edf34d206965e86b3e94f536e4246`
- [x] 本地 `EncryptionService` 可用 SM4-GCM 分块 seal 对象内容，密文不包含明文标记
- [x] 本地 `EncryptionService` 可用 seal metadata open 对象内容并还原原文
- [x] open 时校验 GCM tag、frame 边界、明文 SHA256 和密文 SHA256
- [ ] KMS/SM4 live backend 验证
- [ ] 真实对象存储 + provider streaming 端到端验收

## 备注

当前实现解决的是 Core 内部对象内容加密代码边界和本地真实算法验证，不新增 REST API；`/api/v1/encryption/seal` 仍是控制面的 URI seal/token 契约。后续需要把对象存储数据面与 KMS/SM4 provider streaming 组合起来，再用 live 环境证明可交付。
