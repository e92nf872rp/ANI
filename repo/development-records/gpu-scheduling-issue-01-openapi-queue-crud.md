# GPU-SCHEDULING-ISSUE-01-A：OpenAPI 新增队列 CRUD + InstanceGPU 扩展

> **批次类型：** Feature batch（GPU 调度功能流 Issue #1）
> **完成日期：** 2026-07-03
> **Scope：** `repo/api/openapi/v1.yaml`、`repo/frontends/console/src/api/core-schema.d.ts`
> **依赖：** 无（功能流第一个 Issue）
> **Product line：** Core

## 交付内容

在 Core OpenAPI 契约中新增 GPU 调度队列 CRUD 5 端点 + 4 schema + 2 RBAC scope，并扩展 `InstanceRecord.gpu` 调度字段。这是 PRD §8.2 强制的第一步：先改契约，再实现 handler/adapter。

### 新增端点（5 个）

| operationId | Method | Path | RBAC scope |
|---|---|---|---|
| `listGPUSchedulingQueues` | GET | `/gpu-scheduling/queues` | `scope:gpu-scheduling:read` |
| `createGPUSchedulingQueue` | POST | `/gpu-scheduling/queues` | `scope:gpu-scheduling:write` |
| `getGPUSchedulingQueue` | GET | `/gpu-scheduling/queues/{queue_name}` | `scope:gpu-scheduling:read` |
| `updateGPUSchedulingQueue` | PUT | `/gpu-scheduling/queues/{queue_name}` | `scope:gpu-scheduling:write` |
| `deleteGPUSchedulingQueue` | DELETE | `/gpu-scheduling/queues/{queue_name}` | `scope:gpu-scheduling:write` |

POST 端点支持 `Idempotency-Key` header。

### 新增 Schema（4 个）

- `GPUSchedulingQueue`
- `GPUSchedulingQueueListResponse`
- `GPUSchedulingQueueCreateRequest`
- `GPUSchedulingQueueUpdateRequest`

### InstanceRecord.gpu 扩展字段

- `queue_name`
- `resource_name`
- `scheduling_reason`

### 新增错误码（5 个）

- `QueueNameConflict`（409）
- `QueueNotFound`（404）
- `PlatformDefaultProtected`（409）
- `InsufficientGPU`（422）
- `GPUNodeIncompatible`（422）

## Acceptance Criteria 验证

| AC | 证据 | 结果 |
|---|---|---|
| 5 端点 | Grep `GPUSchedulingQueue` in v1.yaml → 16 处匹配；operationId 行 4115/4129/4163/4179/4211 | ✅ |
| 4 schema | schema 定义行 1748/1763/1771/1781 | ✅ |
| 2 RBAC scope | Grep `scope:gpu-scheduling:(read\|write)` → 5 处匹配 | ✅ |
| Idempotency-Key | POST /gpu-scheduling/queues 行 4135 | ✅ |
| InstanceRecord.gpu 扩展 | 行 473-475 | ✅ |
| 5 错误码 | Grep 确认 5 个错误码存在 | ✅ |
| validate-architecture | `python scripts/validate_component_imports.py --root .` → exit 0 | ✅ |
| gen-core-schema | Grep `GPUSchedulingQueue` in core-schema.d.ts → 21 处匹配 | ✅ |

## 额外修复

- 修复 `/branding` endpoint schema 缺少 `type: object`（pre-existing bug，阻塞 gen-core-schema）
- 修复 `secondary_color:` YAML 格式问题（缺少空格）

## 验证命令

```bash
cd repo
python scripts/validate_component_imports.py --root .
python -c "import yaml; yaml.safe_load(open('api/openapi/v1.yaml').read()); print('YAML valid')"
```

## 边界声明

- 本批次只修改 Core OpenAPI 契约和前端类型生成产物，不涉及 handler/adapter 实现。
- Handler/adapter 实现属于 Issue #2。
- 本批次不声明 runtime ready 或 production ready。
