# 推理服务调用测试

## 页面定位

`推理服务调用测试` 是 `Console / 推理服务` 下针对单个推理服务的**调试页**，用于在 Console 内发送测试 prompt 并查看输出与延迟，验证服务 endpoint 可用。

本模块属于 **Services / InferenceServices** 子能力，一级权威源为 `services/v1.yaml`。

## 文档管理规则

- 本文是 `推理服务调用测试` 的主维护文档
- 与 `inference-service.md` 主模块互补：主模块负责 CRUD，本文负责 `test` 子路径交互
- OpenAI 兼容 Gateway 路径不属于本页正式契约（见 `openai-compatible-api.md`）
- OpenAPI 已声明 ≠ handler 已实现

## Services 层要求

- 正式路径：`POST /api/v1/svc/inference-services/{service_id}/test`
- `operationId`: `testInferenceService`
- 请求 schema：`InferenceServiceTestRequest`
- 响应 schema：`InferenceServiceTestResponse`
- 路径前缀 `/api/v1/svc/*`
- 错误结构统一为 `{"code":"UPPER_SNAKE","message":"...","request_id":"..."}`

<!-- ADDED-TO-YAML: POST /api/v1/svc/inference-services/{service_id}/test (Services v1.yaml, Phase 2 2026-06-17) -->

### 冻结请求字段

| 字段 | 必填 | 说明 |
|---|---|---|
| `idempotency_key` | 是 | UUID 格式幂等键 |
| `prompt` | 是 | 测试输入文本 |
| `max_tokens` | 否 | 最大生成 token |

### 冻结响应字段

| 字段 | 说明 |
|---|---|
| `output` | 模型输出文本 |
| `latency_ms` | 调用延迟，可空 |

## 页面职责

- 在推理服务详情上下文中提供测试表单
- 展示最近一次测试输出与延迟
- 服务不可测试时展示明确错误（含 `422`）
- 不替代生产流量监控或 OpenAI 兼容 API 文档

## 页面结构

```text
调用测试
├── 服务上下文（名称、status、endpoint_url 只读）
├── 测试表单（prompt、max_tokens、提交）
├── 结果区（output、latency_ms）
└── 返回推理服务详情
```

## 创建前置条件

| 依赖项 | 要求 | 未满足响应 |
|---|---|---|
| 用户登录 | 已认证 | `401 UNAUTHORIZED` |
| 服务存在 | `service_id` 有效 | `404 NOT_FOUND` |
| 写/测试权限 | Services RBAC | `403 FORBIDDEN` |
| 服务可调用 | 建议 `InferenceService.status=running` | `422 PRECONDITION_FAILED`（具体 `code` 待 Services 冻结；建议语义：服务未就绪） |
| 请求体合法 | `prompt` + `idempotency_key` | `400 BAD_REQUEST` |

**当前 YAML 已为 test 声明 `422`**；`PreconditionFailed.description` 中举例 `code` 待 Services 团队补充。

## 操作可用性矩阵

### 按服务状态

| 操作 | pending | deploying | running | stopping | stopped | failed |
|---|---|---|---|---|---|---|
| 打开测试页 | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| 提交测试 | ❌ | ❌ | ✅ | ❌ | ❌ | ❌ |

产品建议：仅 `running` 允许提交；其他状态返回 `422`。

### 按角色

| 操作 | 只读用户 | 服务管理员 |
|---|---|---|
| 查看页与历史结果 | ✅ | ✅ |
| 提交测试 | ❌ | ✅ |

## 接口冻结规则

### `POST /api/v1/svc/inference-services/{service_id}/test`

| 项 | 值 |
|---|---|
| operationId | `testInferenceService` |
| requestBody.required | `idempotency_key`、`prompt` |
| success | `200 + InferenceServiceTestResponse` |
| error responses | `400`、`401`、`403`、`404`、`422` |

## 待补边界

- `422` 的 `code` 枚举（如 `SERVICE_NOT_RUNNING`）— 待 Services 在 YAML description 举例
- 流式测试 — 本页仅非流式 JSON；外部流式调用走独立 Envoy AI Gateway `/v1/chat/completions`
- 测试历史持久化 — **当前 YAML 未声明** list test history API

## 与推理服务主模块的关系

- 列表 / 部署 / 删除见 `inference-service.md`
- 日志与指标见 `inference-observability.md`
- 测试不产生新的 `InferenceService` 资源，只读调用现有 endpoint

## 验收标准

- [ ] 路径与 schema 名称与 `services/v1.yaml` 一致
- [ ] 不把 `ani-gateway` 控制面写成 OpenAI `/v1/chat/completions` 数据面
- [ ] 标注 handler 仍为 stub（契约已声明 ≠ 已实现）
- [ ] 接口冻结规则逐 operation 列出成功码与错误码
