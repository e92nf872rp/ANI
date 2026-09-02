# 异步任务中心

## 页面定位

`异步任务中心` 是 `Console` 全局任务查询入口，用于统一展示由 `202 + AsyncTask` 触发的异步操作进度（模型导入、知识库解析、推理部署、对象上传等）。

本页以 **Core `AsyncTask`** 为统一任务模型，Services 层 `202` 响应体同样引用该 schema。

## 文档管理规则

- 本文是 `异步任务中心` 的主维护文档
- `tasks/modules/prd/console/alerts/prd-console-async-task-center.md` 与 `tasks/modules/spec/console/alerts/spec-console-async-task-center.md` 为辅助材料
- 一级权威源：`ANI-main/repo/api/openapi/v1.yaml`（`AsyncTask` / `TaskListResponse` schema 与 `GET /tasks`、`GET /tasks/{task_id}`）
- Services 侧 `202` 引用同一 `AsyncTask` 定义（见 `services/v1.yaml` components）
- OpenAPI 已声明 ≠ handler 已实现

## Core 层要求

- 正式路径：`GET /api/v1/tasks`（list，cursor 分页）与 `GET /api/v1/tasks/{task_id}`（单查）
- 响应 schema：`AsyncTask` / `TaskListResponse`（items + next_cursor）
- list 排序冻结为 `created_at DESC, id DESC`；筛选 `status` / `task_type` / `resource_type`；`limit` 1–100 默认 20；cursor 为不透明 token
- 实例任务（`instance.*`）为真进度模式：受理即 `running/10`，`GET /tasks/{task_id}` 单查触发读时懒同步按实例 state 推进（映射表见 v1.yaml `AsyncTask` description）；list 只返回库内快照不做懒同步
- 页面不要求前端显式传 `tenant_id`
- 错误结构统一为 `{"code":"UPPER_SNAKE","message":"...","request_id":"..."}`

### AsyncTask 冻结字段（展示用）

| 字段 | 说明 |
|---|---|
| `id` | 任务 UUID |
| `task_type` | `instance.create` / `instance.start` / `instance.stop` / `instance.restart` / `instance.delete`（Core 实例域，C1 已入契约）及 `model.import` / `kb.parse` / `kb.index` / `inference.deploy` 等；Phase 3 规划：`kb.document.analyze` / `kb.meeting.ingest` / `kb.video.ingest`（见 `knowledge/README.md`） |
| `resource_type` | 关联资源类型，可空；实例任务为 `instance` |
| `resource_id` | 关联资源 ID，可空 |
| `status` | `pending` / `running` / `completed` / `failed` / `cancelled` / `dead_letter` |
| `progress_pct` | 0–100 |
| `error_message` | 失败原因，可空 |
| `dead_letter_at` | 死信时间，可空 |
| `created_at` / `completed_at` | 时间戳 |

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

- 展示当前租户的异步任务列表（`GET /api/v1/tasks`，list 契约已冻结、handler 已实现）
- 支持按 `task_type`、`status`、`resource_type` 筛选（服务端筛选 + UI 筛选器）
- 展示任务详情：进度、错误信息、关联资源跳转（详情抽屉轮询单查可获得实例任务实时进度）
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
| 列出任务 | `GET /api/v1/tasks` | YAML 已声明；handler 已实现（TASKCENTER-A1） |
| 查询单任务 | `GET /api/v1/tasks/{task_id}` | YAML 已声明；handler 已实现（含实例任务懒同步） |
| 取消任务 | **无** | 待补（归延后方案 V2-3） |

### 关键边界

- `TaskListResponse` 已在 v1.yaml 声明（items: AsyncTask[] + next_cursor）
- list 只读库内快照，不做懒同步——实例任务的实时进度由详情抽屉轮询单查承担
- `404` 表示 task_id 不存在或无权访问
- kb 域噪声（已知状态，非缺陷）：`kb.parse` 任务由 kb-service 写入、状态推进依赖延后方案的 NATS consumer 闭环，`pending` 任务可能长期停留；Gateway 对 kb 域不写、不推进、不懒同步；CreateKB 任务为同步 completed 无噪声

## 创建前置条件

| 依赖项 | 要求状态 | 未满足时的 HTTP 响应 |
|---|---|---|
| 用户登录 | 已认证 | `401 UNAUTHORIZED`（list 与单查均已声明 401/403） |
| 无 `tasks` 读权限 | 角色含 `scope:tasks:read` | `403 FORBIDDEN` |
| 查询 task_id | 来自合法 `202` 响应或会话缓存 | `404 NOT_FOUND` |

本页无 POST/PUT；`idempotency_key` 由产生任务的来源写操作携带。

## 操作可用性矩阵

### 按任务状态

| 操作 | pending | running | completed | failed | cancelled | dead_letter |
|---|---|---|---|---|---|---|
| 查看详情 | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| 轮询刷新 | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| 取消任务 | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ |

取消能力待 Core 冻结前，UI 不展示取消按钮。

### 按角色

| 操作 | 只读用户 | 租户成员 |
|---|---|---|
| 查看已知任务 | ✅ | ✅ |
| 查看全量租户任务 | ✅（list 已上线，按 `scope:tasks:read` 授权） | ✅ |

## 接口冻结规则

### `GET /api/v1/tasks`

- 成功：`200 + TaskListResponse`（items: AsyncTask[]、next_cursor: string|null）
- 错误：`400 BAD_REQUEST`（非法 cursor / 非法 status 枚举值 / limit 越界）、`401 UNAUTHORIZED`、`403 FORBIDDEN`
- `task_type` / `resource_type` 筛选无枚举约束，未匹配值返回空列表而非 400

### `GET /api/v1/tasks/{task_id}`

- 成功：`200 + AsyncTask`
- 错误：`401 UNAUTHORIZED`、`403 FORBIDDEN`、`404 NOT_FOUND`

## 待补边界

- 任务取消 `POST /tasks/{task_id}/cancel` — **待冻结**（归延后方案 V2-3，本批明确不做取消契约）
- 任务与租户成员权限绑定规则 — 以 `scope:tasks:read` 契约声明为准（V2 链路 pilot 切换前的 auth-service 权限数据注册待部署期决策）
- 跨模块 task_type 枚举完整清单 — 以 YAML `AsyncTask.task_type` 为准，新增须先扩 YAML

## 与相关模块的关系

- Core 后端集成方案（活跃）：仓库根目录 `async-task-core-integration-migration.md`（Core 层范围：`instance.*` task_type 扩展、list API、实例任务审计接入的 C1/A1 两批次）
- Services 层集成（推理桥接、kb 域推进闭环、model.import）：已延后，设计存档于仓库根目录 `async-task-services-integration-deferred.md`，后续另立方案重新评审——页面展示推理任务进度暂不可依赖
- `alerts-pending-items.md`：引用本页统计失败 / 处理中任务
- `inference-service.md` / `knowledge-base.md` / `model-center.md`：产生 `202` 任务的来源模块（其中推理/知识库的任务进 Core 任务库的链路已延后）
- `object-storage-upload.md` / `vector-store-write.md`：Core `202` 任务来源

## 验收标准

- [ ] 任务状态枚举与 `v1.yaml` `AsyncTask.status` 一致
- [x] list API 已冻结并实现（`GET /api/v1/tasks`，TASKCENTER-C1 契约 + A1 handler）
- [x] 响应字段与 `AsyncTask` schema 对齐（含 `resource_id` / `error_message` / `dead_letter_at`，A1 修复）
- [x] kb 域噪声已声明（`kb.parse` pending 长期停留为已知状态）
- [ ] 不宣称取消能力（取消归延后方案 V2-3）
- [ ] 接口冻结规则逐 operation 列出成功码与错误码
