# Issue #2: Core Queue port + Volcano adapter + handler

> **Priority:** high
> **Depends On:** #1
> **Product line:** Core
> **Document Links:**
> - SPEC: `repo/services/tasks/modules/spec/core/gpu-inventory/spec-core-gpu-scheduling.md` §2.2, §3.2, §5.1

## Scope

- `repo/pkg/ports/gpu_scheduling.go`（NEW）
- `repo/pkg/adapters/runtime/volcano_queue_store.go`（NEW）
- `repo/pkg/adapters/runtime/volcano_queue_store_test.go`（NEW）
- `repo/services/ani-gateway/internal/handlers/gpu_scheduling_handler.go`（NEW）
- `repo/services/ani-gateway/internal/handlers/gpu_scheduling_handler_test.go`（NEW）

## Description

实现 GPU 调度队列的 Core 后端：port 接口 + Volcano Queue CRD adapter + Gateway handler 5 端点。队列数据持久化在 Volcano Queue CRD（K8s etcd），不在 PostgreSQL。

## Acceptance Criteria

- [x] `GPUSchedulingQueueStore` port 接口定义（List/Get/Create/Update/Delete）
- [x] `VolcanoQueueStore` adapter 实现（CRUD → Volcano Queue CRD `scheduling.volcano.sh/v1beta1`）
- [x] CRD metadata.labels 含 `ani.kubercloud.io/tenant=<tenant_id>`，adapter 按 tenant 过滤
- [x] 平台默认队列 `is_platform_default=true` 不可 PATCH/DELETE → `403 PlatformDefaultProtected`
- [x] 租户隔离：所有查询自动注入 `tenant_id` from JWT
- [x] `POST` 幂等性（`idempotency_key`）
- [x] handler 5 端点 + RBAC scope 校验（`scope:gpu-scheduling:read` / `write`）
- [x] 队列名校验：`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`，1-63 字符
- [x] 单元测试：CRUD + 租户隔离 + 默认队列保护 + idempotency + 跨租户 404

## Validation

```bash
cd repo
make test
make validate-architecture
```
