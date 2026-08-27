# M1-STORAGE-A — Storage Persistence Boundary

完成日期：2026-05-20
对应 Sprint：Sprint 3（2026-05-20 提前启动；计划窗口 2026-06-16 ~ 06-30）
验证结果：make validate-storage-alpha EXIT:0，make validate-network-alpha EXIT:0，make validate-core-alpha EXIT:0，make validate-architecture EXIT:0，python scripts/validate_yaml.py api/openapi/v1.yaml api/openapi/services/v1.yaml EXIT:0，make test EXIT:0，git diff --check EXIT:0

## 实现了什么

补齐存储资源持久化边界：volumes、filesystems、objects 现在具备 `StorageResourceStore` port、metadata adapter、RLS 迁移和 bootstrap capability。Gateway dev/local profile 创建与删除时会同步写入 metadata 表，后续真实 provider 可以复用同一持久化边界。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `pkg/ports/storage_resources.go` | 修改 | 新增 `StorageResourceStore` port |
| `pkg/adapters/runtime/storage_store.go` | 新增 | 新增 metadata-backed storage store |
| `pkg/adapters/runtime/storage_service.go` | 修改 | 创建/删除存储资源时写入 store |
| `deploy/migrations/20260520000600_storage_resources.sql` | 新增 | 新增 storage resource 表、索引、RLS 和 grants |
| `pkg/bootstrap/deps.go` | 修改 | 新增 `StorageStore` capability，并注入 `LocalStorageService` |
| `scripts/validate_storage_alpha_contract.py` | 修改 | 合同守卫覆盖持久化边界和 RLS 迁移 |
| `Makefile` | 修改 | `make validate-storage-alpha` 纳入持久化单元测试 |

## 完工标准达成

- [x] storage metadata 表覆盖 volumes/filesystems/objects
- [x] 表启用 tenant RLS，按 `app.current_tenant_id` 做隔离
- [x] `StorageResourceStore` 隔离 DB 写入细节，Gateway 与 Services 不直接操作存储表
- [x] dev/local profile 创建和删除会同步持久化
- [x] 合同守卫覆盖 port、adapter、migration 和 bootstrap capability

## 备注

后续切片 `m1-storage-a-provider-renderer.md` 与 `m1-storage-a-provider-dry-run-apply-gate.md` 已补齐 provider 渲染和执行门控；对象内容读写仍应继续走既有 `ObjectStore` port，不在本批次里直接暴露底层 MinIO SDK。
