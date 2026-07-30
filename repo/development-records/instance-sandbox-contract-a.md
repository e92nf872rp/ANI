# INSTANCE-SANDBOX-CONTRACT-A

> 日期: 2026-07-28
> 类型: Core API 契约批次
> 状态: 个人仓库 CI passed，契约已确认

## 目标

在统一实例主契约之后，独立补齐原型中 Sandbox 的短期访问令牌、预览端口、
文件、checkpoint 和代码执行子资源。该批次只扩展公开 Core v1 契约，不实现
handler、port、adapter、Console 页面或真实 runtime。

## 契约

- 新增 Sandbox token 签发接口，冻结 15 分钟默认有效期、1 小时上限和过期幂等回放冲突语义。
- 新增 runtime 短期预览端口的创建和关闭接口，不复用产品语义的 Kubernetes Ingress。
- 新增文件列表、写入和删除接口，约束 base64/upload 二选一、覆盖冲突和 413 大小限制。
- 新增 checkpoint 列表、创建、恢复和克隆接口；创建/恢复使用
  `202 + AsyncTask + Location`，克隆返回新的 `CreateInstanceResponse`。
- 新增异步 code-run 接口及结果 schema，约束输出截断并禁止 code/stdin/output
  进入普通日志或普通审计。
- `AsyncTask` 增加 Sandbox checkpoint/code-run 的 task type 与 resource type。
- 所有写操作固定幂等输入；DELETE 使用必填 `Idempotency-Key` header。
- 所有接口固定当前租户与 `kind=sandbox` 边界：跨租户按 404，kind、状态或
  provider 能力不满足按 422。

## 兼容性

- 现有 path、HTTP method、字段和 enum 值均保留。
- 新 path、schema 和 enum 值均为 additive v1 变更。
- 不复制 Registry、Network、Storage 或 GPU Spec 的 CRUD。

## 生成物

- Core Go/Python/Java/TypeScript SDK。
- Console Core TypeScript schema。
- Core 静态 API 文档和 SDK metadata。

## 聚焦测试

`validate_openapi_spec_test.py` 固定 11 个 Sandbox operationId、请求/响应 schema、
DELETE 幂等 header，以及 code-run 的 `202 + AsyncTask + Location` 语义。

## 验收

已通过:

```text
python3 scripts/validate_openapi_spec_test.py
make validate-openapi-spec
make validate-core-api-compatibility
make validate-sdk-alpha
make validate-doc-api
make validate-instance-contracts
make validate-instance-lifecycle-ops
make validate-doc-entrypoints
make test
make build
make validate-architecture
make validate-services
npm --prefix frontends/console audit --audit-level=high
npm --prefix frontends/console run type-check
npm --prefix frontends/console run lint
npm --prefix frontends/console run build
git diff --check
```

个人仓库 GitHub Actions run `30351691537` 已通过。

## 未包含

- Sandbox handler、ports、service、state store 或 adapters。
- token 签发、文件 IO、checkpoint 或 code-run 的真实 runtime。
- Console Sandbox 页面。
- real-provider、runtime-ready 或 production-ready 声明。
