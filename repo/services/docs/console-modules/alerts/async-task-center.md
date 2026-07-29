# 异步任务中心

## 页面定位

`异步任务中心` 是 `Console` 全局任务查询入口，用于统一展示由 `202 + AsyncTask` 触发的异步操作进度（模型导入、知识库解析、推理部署、对象上传等）。

本页以 **Core `AsyncTask`** 为统一任务模型，Services 层 `202` 响应体同样引用该 schema。

## 文档管理规则

- 本文是 `异步任务中心` 的主维护文档
- `tasks/modules/prd/console/alerts/prd-console-async-task-center.md` 与 `tasks/modules/spec/console/alerts/spec-console-async-task-center.md` 为辅助材料
- 一级权威源：`ANI-main/repo/api/openapi/v1.yaml`（`AsyncTask` schema 与 `/tasks` 查询/取消接口）
- Services 侧 `202` 引用同一 `AsyncTask` 定义（见 `services/v1.yaml` components）
- OpenAPI 已声明 ≠ handler 已实现

## Core 层要求

- 正式路径：`GET /api/v1/tasks`、`GET /api/v1/tasks/{task_id}`、
  `POST /api/v1/tasks/{task_id}/cancel`
- 响应 schema：`AsyncTask`
- 页面不要求前端显式传 `tenant_id`
- 错误结构统一为 `{"code":"UPPER_SNAKE","message":"...","request_id":"..."}`

### AsyncTask 冻结字段（展示用）

| 字段 | 说明 |
|---|---|
| `id` | 任务 UUID |
| `task_type` | `model.import` / `kb.parse` / `kb.index` / `inference.deploy` 等；Phase 3 规划：`kb.document.analyze` / `kb.meeting.ingest` / `kb.video.ingest`（见 `knowledge/README.md`） |
| `resource_type` | 关联资源类型，可空 |
| `resource_id` | 关联资源 ID，可空 |
| `status` | `pending` / `running` / `completed` / `failed` / `cancelled` / `dead_letter` |
| `progress_pct` | 0–100 |
| `error_message` | 失败原因，可空 |
| `resource_name` | 任务创建时的资源名称快照，可空 |
| `instance_id` / `operation_id` | 实例任务与实例操作时间线关联，可空 |
| `created_at` / `started_at` / `completed_at` | 时间戳 |

### 任务状态枚举

- `pending`：已创建，等待执行
- `running`：执行中
- `completed`：成功完成
- `failed`：失败（可重试策略由后端决定）
- `cancelled`：已取消
- `dead_letter`：超过最大重试进入死信

## Services 层要求

- 以下操作成功返回 `202 + AsyncTask`（任务详情仍通过 Core `GET /tasks/{task_id}` 查询）：
  - 推理服务部署 / 删除（`services/v1.yaml`）
  - 知识库文档上传（`202`）
  - 对象上传 `POST /api/v1/objects/upload`（`202 + AsyncTask`）
  - 向量写入 `POST /api/v1/vector-stores/{id}/documents`（`202`）
  - 模型导入（若返回 AsyncTask）
  - Phase 3 规划：`kb.document.analyze` / `kb.meeting.ingest` / `kb.video.ingest` / `billing.export`（见各域 README）
- Services 路径前缀 `/api/v1/svc/*`；任务查询统一走 Core `/api/v1/tasks/{task_id}`

## 页面职责

- 展示当前租户异步任务列表
- 支持按 `task_type`、`resource_type`、`status`、`instance_id` 和时间筛选
- 展示任务详情：进度、错误信息、关联资源跳转
- 提供轮询刷新（推荐间隔 ≥ 2s，避免风暴）

## 页面结构

```text
任务中心
├── 筛选器（task_type / status / 时间）
├── 任务列表
│   └── 行：类型、状态、进度、资源、创建时间
├── 任务详情抽屉
│   ├── AsyncTask 全字段只读
│   └── 跳转关联资源
└── 空态 / 错误态
```

## 数据来源与分层约束

| 能力 | 路径 | 状态 |
|---|---|---|
| 查询单任务 | `GET /api/v1/tasks/{task_id}` | YAML 已声明；handler stub |
| 列出任务 | `GET /api/v1/tasks` | YAML 已声明；handler 待实现 |
| 取消任务 | `POST /api/v1/tasks/{task_id}/cancel` | YAML 已声明；handler 待实现 |

### 关键边界

- 列表响应必须使用 `AsyncTaskListResponse`
- tenant_id 从认证上下文获取，不提供跨租户 query
- `404` 表示 task_id 不存在或无权访问

## 创建前置条件

| 依赖项 | 要求状态 | 未满足时的 HTTP 响应 |
|---|---|---|
| 用户登录 | 已认证 | `401 UNAUTHORIZED` |
| 查询 task_id | 来自合法 `202` 响应或会话缓存 | `404 NOT_FOUND` |

取消请求必须提供 `idempotency_key`；重复请求回放原结果。

## 操作可用性矩阵

### 按任务状态

| 操作 | pending | running | completed | failed | cancelled | dead_letter |
|---|---|---|---|---|---|---|
| 查看详情 | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| 轮询刷新 | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| 取消任务 | ✅ | ❌ | ❌ | ❌ | ❌ | ❌ |

running 或任一终态任务取消返回 409。

### 按角色

| 操作 | 只读用户 | 租户成员 |
|---|---|---|
| 查看已知任务 | ✅ | ✅ |
| 查看全量租户任务 | ✅ | ✅ |

## 接口冻结规则

### `GET /api/v1/tasks/{task_id}`

- 成功：`200 + AsyncTask`
- 错误：`404 NOT_FOUND`
- **当前 YAML 未声明** `401`/`403`/`422`

## 待补边界

- list/get/cancel handler、持久化 task-service 和执行取消传播
- 任务与租户成员权限的实现验证
- 跨模块 task_type 枚举完整清单以 YAML 为准，新增须先扩 YAML

## 与相关模块的关系

- `alerts-pending-items.md`：引用本页统计失败 / 处理中任务
- `inference-service.md` / `knowledge-base.md` / `model-center.md`：产生 `202` 任务的来源模块
- `object-storage-upload.md` / `vector-store-write.md`：Core `202` 任务来源

## 验收标准

- [ ] 任务状态枚举与 `v1.yaml` `AsyncTask.status` 一致
- [ ] list/get/cancel 均消费 YAML schema，不自造客户端任务模型
- [ ] 不宣称 handler 已实现
- [ ] 取消按钮只对 pending 任务可用
