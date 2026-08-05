# STORAGE-ASYNC-CORRECTNESS-A

> 日期：2026-08-03
> 范围：ANI Core v1 / Vector 文档写入异步任务
> 状态：live passed

## 契约依据

- 保留现有 Core v1 `POST /vector-stores/{vector_store_id}/documents` 的 `202` 和 `VectorStoreDocumentInsertResponse`，不改为 `200`，不改变现有 Body 字段。
- `Location` 必须指向 `/api/v1/tasks/{task_id}`，且该任务必须可按租户查询。
- 新任务类型为 `vector_store.document.insert`；任务使用现有 `AsyncTaskStore`，不新增表或 migration。

## 实现

- Core OpenAPI 为文档写入 `202` 声明 `Location`，并扩展 `AsyncTask.task_type` enum。
- Gateway 使用服务返回的 task ID 创建 completed AsyncTask，结果包含 `vector_store_id` 和 `inserted_count`。
- 相同幂等键已存在 PG 任务时，以 `AsyncTaskStore.Create` 返回的任务 ID 和状态为权威，避免响应指向未落库的新 ID。
- 保留原 `VectorStoreDocumentInsertResponse`，兼容现有 SDK、CLI 和 Console。
- OpenAPI 回归测试新增通用规则：返回 `AsyncTask` 或必填 `task_id` 的 `202` 必须声明 `Location`。

## 真实 E2E

Gateway 镜像：

```text
docker.changqingyun.cn/ani/ani-gateway:storage-async-correctness-20260803-v1
sha256:9f8063fa6e94a9048ad114f150d4559845cf78bc9fb43452a2eede1322a8963b
```

真实环境通过 Auth/Dex bearer 调用 Core v1，经 Milvus 创建 Vector Store 并写入一条文档：

- create 返回 201；document insert 返回 202，`Location` 与 Body `task_id` 一致；
- Gateway 轮询任务返回 200，任务类型、状态、进度和结果正确；
- PostgreSQL `async_tasks` 存在唯一 tenant-scoped 记录；
- rollout restart 后 Pod 已替换且 Ready，原 task ID 再次查询返回 200，关键字段与重启前一致；
- 临时 Milvus collection 已删除，PG 任务行保留为审计证据。

脱敏证据：`development-records/live-evidence/storage-async-vector-task-live-20260803.json`。证据不包含 token、数据库连接、内部端点、IP 或业务 payload。

## 验证

```text
make test
make validate-architecture
make validate-openapi-spec
make validate-core-api-compatibility
make validate-sdk-beta
go test -race ./services/ani-gateway/internal/router -run 'TestVectorStoreHTTPDocumentInsertPersistsPollableTask|TestStorageHTTPAsyncTasksKeepOperationTypeAndLocation' -count=1
git diff --check
```

## 边界

- 本结果证明 Vector 文档写入任务已落 PG，并可跨 Gateway 重启查询；不外推为 full platform production ready。
- Vector Store 控制面元数据的跨 Gateway 重启持久化不属于本批范围。
