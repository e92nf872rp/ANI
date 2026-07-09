# GPU-SCHEDULING-ISSUE-02-A：Core Queue port + Volcano adapter + handler

> **批次类型：** Feature batch（GPU 调度功能流 Issue #2）
> **完成日期：** 2026-07-06
> **Scope：** `repo/pkg/ports/gpu_scheduling.go`、`repo/pkg/adapters/runtime/volcano_queue_store.go`、`repo/pkg/adapters/runtime/volcano_queue_store_test.go`、`repo/services/ani-gateway/internal/router/gpu_scheduling_resources.go`、`repo/services/ani-gateway/internal/router/gpu_scheduling_resources_test.go`、`repo/services/ani-gateway/internal/router/router.go`
> **依赖：** Issue #1（OpenAPI 契约）
> **Product line：** Core

## 交付内容

实现 GPU 调度队列的 Core 后端：port 接口 + Volcano Queue CRD adapter + Gateway handler 5 端点。队列数据持久化在 Volcano Queue CRD（K8s etcd），不在 PostgreSQL。

### 新建文件

| 文件 | 内容 |
|---|---|
| `pkg/ports/gpu_scheduling.go` | `GPUSchedulingQueueStore` port 接口 + `GPUSchedulingQueue`/`WorkloadClass` 类型 + 4 个队列错误 sentinel |
| `pkg/adapters/runtime/volcano_queue_store.go` | `VolcanoQueueStore` adapter，CRUD → Volcano Queue CRD `scheduling.volcano.sh/v1beta1`，通过 K8s REST API 直接操作 |
| `pkg/adapters/runtime/volcano_queue_store_test.go` | 14 个 adapter 单测，覆盖 CRUD + 租户隔离 + 默认队列保护 + idempotency + 跨租户 404 + 无效队列名 + store 不可用 |
| `services/ani-gateway/internal/router/gpu_scheduling_resources.go` | 5 端点 handler（list/create/get/update/delete），含 Idempotency-Key 校验和 tenant 注入 |
| `services/ani-gateway/internal/router/gpu_scheduling_resources_test.go` | 12 个 handler 单测，含 HTTP 级别端点测试 + 错误映射 |

### 修改文件

| 文件 | 改动 |
|---|---|
| `services/ani-gateway/internal/router/router.go` | `RegisterOptions` 新增 `GPUSchedulingQueueStore` 字段；`RegisterWithOptions` 新增 `registerGPUSchedulingResourcesWithStore` 调用 |

## Acceptance Criteria 验证

| AC | 证据 | 结果 |
|---|---|---|
| `GPUSchedulingQueueStore` port 接口（List/Get/Create/Update/Delete） | `pkg/ports/gpu_scheduling.go` L53-59 | ✅ |
| `VolcanoQueueStore` adapter（CRUD → Volcano Queue CRD） | `pkg/adapters/runtime/volcano_queue_store.go`；const `scheduling.volcano.sh/v1beta1` | ✅ |
| CRD labels 含 `ani.kubercloud.io/tenant`，按 tenant 过滤 | adapter `labelSelectorTenant` + `collectionURL` labelSelector | ✅ |
| 平台默认队列不可 PATCH/DELETE → 403 | `isPlatformDefaultCRD` + `ErrPlatformDefaultProtected` | ✅ |
| 租户隔离：tenant_id from JWT | handler `middleware.GetTenantID(c)` 注入所有查询 | ✅ |
| POST 幂等性 | handler 校验 `Idempotency-Key` header required | ✅ |
| handler 5 端点 + RBAC scope | `registerGPUSchedulingResourcesWithStore` 注册 5 端点；v1.yaml 已定义 scope | ✅ |
| 队列名校验 `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$` | `validateQueueName` 函数 | ✅ |
| 单元测试覆盖 | adapter 14 个 + handler 12 个 = 26 个测试全通过 | ✅ |

## 验证命令

```bash
cd repo
python scripts/validate_component_imports.py --root .
go test ./pkg/adapters/runtime/ -run TestVolcanoQueueStore -count=1
go test ./services/ani-gateway/internal/router/ -run TestGPUScheduling -count=1
```

## 设计决策

- **handler 目录**：SPEC 标注 `internal/handlers/`，但现有代码库所有 handler 统一在 `internal/router/`。遵循现有代码风格（Karpathy 原则 #3），handler 放在 `internal/router/gpu_scheduling_resources.go`。
- **K8s REST 调用**：adapter 通过 `VolcanoHTTPDoer` 接口调用 K8s API，不引入 `k8s.io/client-go` 依赖。生产环境由 gateway runtime 注入 doer，测试用 `DoerFunc`。
- **队列 ID**：UUID 生成后存入 CRD label `ani.kubercloud.io/queue-id`，通过 labelSelector 查询。
- **Volcano Queue CRD 是 cluster-scoped**：在 `volcano-system` namespace 内，同名 CRD 跨租户会冲突。adapter 的 List-based 唯一性检查是 tenant-scoped，K8s POST 返回 409 时映射为 `ErrQueueNameConflict`。生产环境建议租户队列名前缀化。

## 边界声明

- 本批次只实现 Core 后端 port/adapter/handler，不涉及真实 K8s/Volcano 部署验证。
- adapter 的 `VolcanoHTTPDoer` 生产实现（桥接 `KubernetesRESTClient`）属于 gateway runtime 配置，不在本批次 scope。
- 本批次不声明 runtime ready 或 production ready。
- `TestDemoInstanceServiceRealShellExecutesCommand` 测试在 Windows 上失败（pre-existing，与本批次无关）。
