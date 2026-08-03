# INSTANCE-CONTRACT-A

> 日期: 2026-07-28
> 类型: Core API 契约批次
> 状态: 个人仓库 CI passed，契约已确认

## 目标

在现有统一 `/instances` API 上补齐原型所需的 VM、Container、GPU Container 和 Sandbox
创建、列表、详情与生命周期契约。该批次只扩展公开 Core v1 契约，不实现
handler、port、adapter、Console 页面或真实 provider。

## 契约

- `CreateInstanceRequest` 增加 description、labels、image_id 和 image_ref。
- 新增实例网络、磁盘、卷/文件系统挂载、端口、环境变量和 Workload Identity 配置 schema。
- 扩展 VM、Container、GPU Container 与 Sandbox kind config，引用既有 Registry、Network、
  Storage 和 GPU Spec 资源，不复制关联模块 CRUD。
- `InstanceRecord` 增加 image、compute、network、access 和 storage attachment 稳定摘要。
- `GET /instances` 增加通用和 kind-specific 过滤、稳定 cursor 输入与排序参数。
- events 和 security-events 增加 cursor query，与既有 next_cursor 响应对齐。
- 生命周期增加文件系统挂载、扩缩容、镜像更新、Secret、安全组、终止保护和 Sandbox
  pause/resume/extend/touch_idle 动作及结构化 payload。
- `InstanceOperation` 同步动作 enum，并允许 operation step 关联 Storage task/resource。

## 兼容性

- 现有 path、HTTP method、字段和 lifecycle action 均保留。
- `image`、`boot_image`、扁平 GPU/replicas 等兼容字段继续接受并标记 deprecated。
- 新字段、schema、query 参数、响应字段和 enum 值均为 additive v1 变更。
- GPU quota check/acquire/release 仍不属于本批次。
- Sandbox token、ports、files、checkpoints 和 code-runs 子资源由后续
  `INSTANCE-SANDBOX-CONTRACT-A` 独立提交。

## 生成物

- Core Go/Python/Java/TypeScript SDK。
- Console Core TypeScript schema。
- Core 静态 API 文档和 SDK metadata。

## 聚焦测试

`validate_openapi_spec_test.py` 固定以下契约:

- 四类创建配置和实例详情摘要。
- 实例列表过滤、排序及观测 cursor。
- lifecycle action/payload 与 operation step task/resource 关联。

## 验收

已通过:

```text
python3 scripts/validate_openapi_spec_test.py
python3 scripts/validate_yaml.py api/openapi/v1.yaml
make validate-openapi-spec
make validate-core-api-compatibility
make validate-instance-contracts
make validate-instance-lifecycle-ops
```

完整本地 CI 和个人仓库 GitHub Actions run `30348851947` 已通过。

## 未包含

- 实例 handler、ports、service、state store 或 adapters。
- Registry、Network、Storage、GPU Spec 的重复 CRUD。
- Sandbox 子资源接口。
- GPU 配额扣减、占用或释放。
- real-provider、runtime-ready 或 production-ready 声明。
