# INSTANCE-TASK-CENTER-CONTRACT-A

> 日期: 2026-07-29
> 类型: Core API 契约批次
> 状态: 本地契约门禁通过，等待个人仓库 CI 和契约评审

## 目标

补齐首页统一任务中心和实例长操作的公开 Core v1 契约。该批次只定义
`AsyncTask`、任务列表/取消以及实例 `202` 响应，不实现 handler、task-service、
reconcile-worker、controller、数据库迁移或 Console 页面。

## 契约

- 新增租户内 `GET /tasks` cursor 列表，支持 status、task_type、resource_type、
  instance_id、创建时间和排序筛选。
- 新增 `POST /tasks/{task_id}/cancel`；请求必须携带 `idempotency_key`，仅
  pending 任务可取消，running 或终态返回 409，跨租户按 404 处理。
- `AsyncTask` 增加 resource_name、instance_id、operation_id 和 started_at，
  让任务中心可展示并下钻实例操作时间线。
- 新增 `InstanceAsyncTask`，要求实例 202 响应在接受任务前已分配 instance_id
  和 operation_id，避免客户端丢失任务或实例追踪入口。
- 增加实例长操作 task type；Storage attach/detach 继续使用真实
  `volume.*` / `filesystem.*` task type，不伪造为实例任务。
- `POST /instances`、`POST /instances/{instance_id}/lifecycle` 和 Sandbox
  checkpoint clone 增加 `202 + AsyncTask + Location`。
- 保留既有 `201 CreateInstanceResponse` 和 `200 InstanceLifecycleResponse`，
  供兼容同步 profile 和快速元数据操作使用。

## 兼容性

- 现有 path、HTTP method、成功响应和 schema 字段均保留。
- 新 path、可选响应、可选字段、schema 和 enum 值均为 additive v1 变更。
- `resource_id` 的 UUID 约束不变；非 UUID 的实例标识使用新增 instance_id。

## 生成物

- Core Go/Python/Java/TypeScript SDK。
- Console Core TypeScript schema。
- Core 静态 API 文档和 SDK metadata。

## 聚焦测试

`validate_openapi_spec_test.py` 固定任务类型、关联字段、租户任务列表、幂等取消，
以及三个实例入口的 `202 + InstanceAsyncTask + Location` 响应。

## 验收

已通过:

```text
make validate-openapi-spec
make validate-core-api-compatibility
make validate-sdk-alpha
make validate-doc-api
make validate-instance-contracts
make validate-instance-lifecycle-ops
make validate-doc-entrypoints
make validate-ci-workflow
make build
make test
make validate-architecture
npm --prefix frontends/console audit --audit-level=high
npm --prefix frontends/console run type-check
npm --prefix frontends/console run lint
npm --prefix frontends/console run build
git diff --check
```

Console lint 保留 1 条既有 `react-hooks/exhaustive-deps` warning、0 error；构建保留
既有 chunk size warning。提交后生成物漂移检查和个人仓库 GitHub Actions 待本批次
后续步骤验证。

## 未包含

- task-service 持久化、队列、租约、进度上报和取消执行。
- controller/reconcile-worker 的实例执行或状态回写。
- Gateway list/get/cancel handler 与实例 handler 异步改造。
- Console 首页任务中心。
- real-provider、runtime-ready 或 production-ready 声明。
