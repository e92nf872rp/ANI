# CORE-SVC-SUPPORT-OBJVEC-A — Core Services 支撑对象/向量 Handler

> 批次类型：Feature batch
> 完成日期：2026-06-19
> 范围：仅 ANI Core，Tier1 local profile

## 背景

Sprint 12 目标是闭合 `api/openapi/v1.yaml` 已声明但网关尚未实现的 Core handler 缺口。本批覆盖 B3：对象存储 bucket / 预签名 object URL，以及向量文档写入。

## 完成内容

- 扩展 `ports.StorageService`，新增 `CreateStorageBucket` / `ListStorageBuckets` / `CreateStorageObjectUpload` / `GetStorageObjectDownload`。
- `runtime.LocalStorageService` 新增 bucket local 元数据、POST 幂等、tenant isolation，以及 `ports.ObjectStore.EnsureBucket` / `SignedUploadURL` / `SignedDownloadURL` 的可注入复用；未注入真实对象存储时只返回 local dev profile 预签名 URL。
- Gateway 注册并实现 4 个 ObjectStore operationId：
  - `GET /api/v1/buckets`
  - `POST /api/v1/buckets`
  - `POST /api/v1/objects/upload`
  - `GET /api/v1/objects/{object_id}/download`
- 扩展 `ports.VectorStoreService`，新增 `InsertDocuments`；`runtime.LocalVectorStoreService` 校验 store ready、文档数量与内容，生成 local embedding 向量并复用 `ports.VectorStore.Upsert`。
- Gateway 注册并实现 `POST /api/v1/vector-stores/{vector_store_id}/documents`，返回 `v1.yaml` 当前定义的 `VectorStoreDocumentInsertResponse`（`inserted_count`、`task_id`、`status`）并设置 `Location: /api/v1/tasks/{task_id}`。
- 复审收口：`StorageBucketListResponse` 与 handler 统一为 `{items,total,next_cursor}`，避免列表 schema 与实际响应分叉。
- 本批全部为 Tier1 local profile，不声明 real-provider、runtime ready 或 production ready；真实 MinIO/S3-compatible 与 Milvus/Qdrant provider 仍属于 Sprint 13 后续 live gate。

## 验证

TDD 红测先行：

```bash
go test ./pkg/adapters/runtime ./services/ani-gateway/internal/router
```

红测阶段失败原因是缺少 B3 storage/vector port 类型、local adapter 方法和 router 转换函数；实现后 targeted tests 通过。

完整门禁与 curl smoke 结果见本批提交记录和最终执行输出。

## 关键文件

- `pkg/ports/storage_resources.go`
- `pkg/ports/object_store.go`
- `pkg/ports/vector_store.go`
- `pkg/adapters/runtime/storage_service.go`
- `pkg/adapters/runtime/vector_store_service.go`
- `services/ani-gateway/internal/router/storage_resources.go`
- `services/ani-gateway/internal/router/vector_store_resources.go`
- `services/ani-gateway/internal/router/storage_resources_test.go`
- `services/ani-gateway/internal/router/vector_store_resources_test.go`
- `pkg/adapters/runtime/storage_service_test.go`
- `pkg/adapters/runtime/vector_store_service_test.go`

## 后续真实环境门禁关联

本批只完成 Tier1 local profile。Sprint 13 若推进真实 provider，必须沿用本批已建立的 port/handler 边界：

- 对象存储 bucket / upload / download：从 `ports.ObjectStore` 接 MinIO/S3-compatible adapter，并新增 object-store live gate 验证 bucket ensure 与预签名 URL。
- 向量文档写入：从 `ports.VectorStore.Upsert` 接 Milvus/Qdrant 或选定向量后端，并新增 vector write live gate。

未执行 live gate 前，本批能力不得标记为 real-provider、runtime ready 或 production ready。
