# 推理服务

## 页面定位

`推理服务` 是 `Console` 的租户侧推理部署与运维页面，用于管理推理服务列表、部署、详情和删除。

当前模块属于 `Services / InferenceServices`，一级权威源为 [`repo/api/openapi/services/v1.yaml`](../../../../api/openapi/services/v1.yaml)，正式路径前缀为 `/api/v1/svc`。

## 文档管理规则

- 本文是 `推理服务` 的主维护文档，产品定义、边界约束与验收标准统一以本文为准
- 如 `PRD`、`SPEC`、HTML 摘要与本文不一致，先回到 [`repo/api/openapi/services/v1.yaml`](../../../../api/openapi/services/v1.yaml) 核对，再统一回写
- 未冻结的 OpenAI 兼容、限流策略、观测资源等内容只能保留边界说明，不得写成当前正式页面承诺
- 页面设计可补充状态流转与交互反馈，但不得扩写新的资源域或新增 `Core` 表述

## Services 层要求

- 推理服务属于 `Services`
- 正式路径使用 `/api/v1/svc/inference-services*`
- 页面不要求前端显式传 `tenant_id`
- 页面不把 OpenAI 兼容 API、限流策略、生命周期子页写成独立资源域
- 契约规定部署请求成功返回 `202 + InferenceService`；当前过渡 handler 尚未对齐该响应语义
- 契约规定删除请求成功返回 `202`；当前过渡 handler 尚未对齐该响应语义
- 错误结构统一为 `{"code":"UPPER_SNAKE","message":"...","request_id":"..."}`

## 页面职责

- 展示推理服务列表
- 提供服务部署入口
- 展示服务详情与状态摘要
- 提供服务删除能力

## 页面结构

- 列表页至少包含 `部署入口`、`服务列表`、`状态摘要`
- 详情页至少包含 `基础信息`、`当前状态`、`配置摘要`、`最近操作反馈`
- 删除确认与结果反馈必须留在当前页面上下文，不依赖未冻结的观测或测试子页

## 数据来源与分层约束

### 数据来源划分

- `Services` 数据
  - 推理服务列表
  - 推理服务部署请求
  - 推理服务详情
  - 推理服务删除请求
- `Core` 数据
  - 当前页面不承接 `Core` 推理服务资源契约
  - 如后续需展示任务、审计或监控摘要，只能通过跳转或引用方式承接

### 关键边界

- 推理服务不写成 `Core` 资源
- 部署成功 `202` 表示部署请求已提交，不等于服务已 `running`
- 删除成功 `202` 表示停止任务已提交，不等于资源已立即消失
- OpenAI 兼容 API、限流策略、观测资源、调用测试只保留待补边界说明

## 字段级定义

| 字段 | 来源 schema | 说明 |
|---|---|---|
| `id` | `InferenceService.id` | 服务唯一标识 |
| `name` | `InferenceService.name` | 服务名称 |
| `model` | `InferenceService.model` | 上游模型标识 |
| `replicas` | `InferenceService.replicas` | 副本数 |
| `gpu_type` | `InferenceService.gpu_type` | GPU 类型 |
| `gpu_count_per_pod` | `InferenceService.gpu_count_per_pod` | 每个 Pod 的 GPU 数 |
| `max_concurrency` | `InferenceService.max_concurrency` | 最大并发 |
| `status` | `InferenceService.status` | `pending / deploying / running / stopping / stopped / failed` |
| `endpoint_url` | `InferenceService.endpoint_url` | 服务访问地址 |
| `created_at` | `InferenceService.created_at` | 创建时间 |

## 字段展示规则

| 场景 | 展示规则 | 说明 |
|---|---|---|
| 空态 | 展示部署入口和模型准备说明 | 不伪造示例服务 |
| 列表正常态 | 展示服务名称、模型、状态、部署规模 | 字段必须来自 `InferenceService` |
| 部署进行态 | 展示 `pending / deploying` 过程反馈 | 不把部署请求提交写成部署完成 |
| 删除进行态 | 保留当前上下文并展示“停止任务已提交” | 不立即视为资源已消失 |
| 详情缺失态 | 返回 `NOT_FOUND` 时展示找不到服务 | 不伪造详情 |

## 字段口径与单位

| 字段 | 口径建议 | 单位/格式 |
|---|---|---|
| `name` | 服务名称 | 字符串 |
| `model` | 上游模型引用 | 字符串 |
| `replicas` | 副本规模 | 整数 |
| `gpu_count_per_pod` | 单 Pod GPU 数 | 整数 |
| `max_concurrency` | 最大并发能力 | 整数 |
| `endpoint_url` | 服务访问地址 | URL 或空值 |
| `created_at` | 服务端创建时间 | date-time |

## 状态与能力口径

### 服务状态

- `pending`：请求已提交，尚未进入正式部署
- `deploying`：部署中
- `running`：运行中
- `stopping`：停止中
- `stopped`：已停止
- `failed`：部署或运行失败

### 待补能力边界

- OpenAI 兼容 API <!-- ADDED-TO-YAML: Gateway POST /v1/chat/completions（非 services/v1.yaml） (Phase 2 2026-06-17) -->
- 限流与访问策略 <!-- ADDED-TO-YAML: PUT /api/v1/svc/inference-services/{service_id}/policies (Services v1.yaml, Phase 2 2026-06-17) -->
- 日志 / 事件 / 指标独立资源 <!-- ADDED-TO-YAML: GET /api/v1/svc/inference-services/{service_id}/logs (Services v1.yaml, Phase 2 2026-06-17) --> — 详文见 `inference-observability.md`
- 调用测试独立资源 <!-- ADDED-TO-YAML: POST /api/v1/svc/inference-services/{service_id}/test (Services v1.yaml, Phase 2 2026-06-17) --> — 详文见 `inference-call-test.md`
- 生命周期子页独立后端路径

## 创建前置条件

- 用户已登录并具备当前租户的推理服务查看权限
- 部署与删除请求需要具备对应写权限
- 部署请求必须包含有效 `idempotency_key`
- 推理服务创建前应已有可引用的上游模型，且模型版本处于 `ready` 状态

| 依赖项 | 要求状态 | 未满足时的 HTTP 响应 |
|---|---|---|
| `model` / 上游模型引用 | 对应 `Model.status=ready` | `422 PRECONDITION_FAILED`（Services YAML 已举例 `MODEL_NOT_READY`） |
| `name` | 租户内唯一 | `409 CONFLICT` |
| GPU 资源（若指定） | 可满足调度 | `422 PRECONDITION_FAILED`（具体 `code` 待 Services 冻结；建议语义：GPU 不可用） |

## 操作可用性矩阵

### 按服务状态（`InferenceService.status`）

| 操作 | pending | deploying | running | stopping | stopped | failed |
|---|---|---|---|---|---|---|
| 查看详情 | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| 部署（创建） | — | — | — | — | — | — |
| 扩缩容/变配 (`PATCH`) | ❌ | ❌ | ✅ | ❌ | ❌ | ❌ |
| 删除 | ✅ | ⚠️ | ⚠️ 二次确认 | ❌ | ✅ | ✅ |

当前 Gateway 已注册 `PATCH /api/v1/svc/inference-services/{service_id}` 过渡路由，但 Services OpenAPI 尚未声明该操作，因此它不能作为前端可依赖的正式能力。若要正式开放，必须先补齐 OpenAPI、生成物、handler 响应语义和对应门禁；部署/删除契约成功均为 `202`，表示任务已提交。

### 按角色（RBAC）

| 操作 | 只读用户 | 服务管理员 |
|---|---|---|
| 查看列表/详情 | ✅ | ✅ |
| 部署 / 变配 / 删除 | ❌ | ✅ |

## 删除前置校验与当前契约边界

- 当前正式契约仅确认 `DELETE /api/v1/svc/inference-services/{service_id}` 成功返回 `202`
- 当前权威源未声明删除请求的额外前置校验、引用冲突或软删除规则，正文不得自造这些逻辑
- 若后续权威源补充删除任务跟踪或冲突规则，需再更新本文

## 接口冻结规则

### `GET /api/v1/svc/inference-services`

- `operationId`: `listInferenceServices`
- `success`: `200 + {items: InferenceService[]}`
- 当前权威源未单独列出错误返回码，正文不得擅自补写

### `POST /api/v1/svc/inference-services`

- `operationId`: `createInferenceService`
- `success`: `202 + InferenceService`
- `requestBody.required`: `idempotency_key`、`name`、`model`
- `optional fields`: `replicas`、`gpu_type`、`gpu_count_per_pod`、`max_concurrency`
- 部署请求成功不等于服务已进入 `running`

### `GET /api/v1/svc/inference-services/{service_id}`

- `operationId`: `getInferenceService`
- `success`: `200 + InferenceService`
- `errors`: `404`

### `PATCH /api/v1/svc/inference-services/{service_id}`

- `operationId`: `patchInferenceService`
- `summary`: 更新推理服务配置（扩缩容/修改参数）
- `success`: `202 + InferenceService`
- `requestBody.required`: `idempotency_key`
- `optional fields`: `replicas`、`gpu_type`、`gpu_count_per_pod`、`max_concurrency`
- `errors`: `400`、`401`、`403`、`404`、`422`（非 `running` 状态）
- 仅 `status=running` 时可提交变配请求

### `DELETE /api/v1/svc/inference-services/{service_id}`

- `operationId`: `deleteInferenceService`
- `success`: `202`
- `success semantics`: 停止任务已提交
- `errors`: `404`

## 响应示例

### 部署请求提交成功

```json
{
  "id": "d91fbc43-4b37-4b68-8b61-014a48e8f1e5",
  "name": "llama3-prod",
  "model": "llama3-8b",
  "replicas": 1,
  "gpu_type": "A100",
  "gpu_count_per_pod": 1,
  "max_concurrency": 8,
  "status": "pending",
  "endpoint_url": null,
  "created_at": "2026-06-14T11:00:00Z"
}
```

### 删除请求提交成功

```json
{}
```

## 错误示例

### 服务不存在

```json
{
  "code": "NOT_FOUND",
  "message": "inference service not found",
  "request_id": "req-infer-404-001"
}
```

## 回填前置依赖

- 如未来补充 OpenAI 兼容协议、限流策略、观测资源，必须先在 `services/v1.yaml` 中冻结路径和 schema
- 如未来补充部署任务流或删除任务历史，必须先明确正式资源归属
- 如未来补充调用测试页，必须先确认它是否属于 `InferenceServices` 主资源扩展

## 待确认项

- 列表是否需要未来扩充健康检查摘要字段
- 删除请求提交后，是否需要未来独立任务跟踪页
- `endpoint_url` 为空时，是否需要统一的用户提示文案

## 回填验收标准

- 正文路径全部位于 `Services / InferenceServices`
- 成功返回码与 `services/v1.yaml` 保持一致：列表 `200`、部署 `202`、详情 `200`、删除 `202`
- 正文只引用权威源真实存在的 schema：`InferenceService`、`ErrorResponse`
- 正文不再把部署写成 `201`，也不再把删除写成 `204`
- HTML 摘要、PRD、SPEC 与本文件一致

## 产品经理补充定义

### 目标用户与权限视角

- 服务管理员：查看服务列表、部署服务、查看详情、删除服务
- 运维成员：查看运行状态、失败反馈和删除结果，具体写权限以后端授权为准
- 只读用户：仅查看服务列表和详情

### 页面结构补充

- 首屏至少包含 `部署入口`、`服务列表`、`状态摘要`
- 服务详情至少包含 `基础信息`、`当前状态`、`配置摘要`、`最近操作反馈`
- 页面不单独扩出 OpenAI、限流策略、日志/事件/指标子页，它们仍只属于待补边界

### 核心任务流

1. 用户从列表进入服务详情，确认当前服务的可用状态与基础配置摘要
2. 用户提交服务部署请求后，页面回流到新服务详情或当前列表中的新条目
3. 用户删除服务时，页面给出危险动作确认，并在结果返回后刷新列表或详情

### 页面状态与反馈

- 空态：当前无推理服务时，展示部署入口和模型准备说明
- 进行中态：部署完成前必须有明确的处理中反馈，不允许只靠页面刷新猜测
- 异常态：详情页必须保留失败信息与 `request_id`，便于排障
- 删除态：删除处理中与删除完成必须可区分，不让用户误判为列表瞬间消失

### 跨模块协同

- 与 `模型中心` 协同，部署服务前可回跳选择上游模型信息
- 与 `租户用量报表`、首页协同，只通过摘要与跳转连接，不重写统计契约
- OpenAI 兼容 API、访问策略、观测资源等未冻结能力仅保留边界说明

### 产品验收补充

- 用户必须能完成“查看服务 -> 部署新服务 -> 复核服务状态 -> 删除服务”的闭环
- 服务状态展示必须基于现有返回字段映射为可读文案，而不是新增资源域
- 空态、进行中态、异常态、删除态都必须具备
- 本页不得把推理服务写成 `Core` 资源，也不得引入未冻结子页承诺
