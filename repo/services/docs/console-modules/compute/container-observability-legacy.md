# 容器实例可观测性

## 页面定位

容器实例详情下的 **日志 / 事件 / 指标 / 终端 exec** 能力页，适用于 `kind=container`（及同类统一实例）。

父级：`container-instance-management.md`。

## 文档管理规则

- 本文是容器可观测性子模块主维护源
- Handler 契约：`tasks/execution/CORE-HANDLER-IMPLEMENTATION-GUIDE.md` §TASK-CORE-005
- SPEC：`tasks/modules/spec/console/compute/spec-console-container-observability.md`
- 一级权威源：`ANI-main/repo/api/openapi/v1.yaml`
- OpenAPI 已声明 ≠ handler 已实现

## Core 层要求

<!-- ADDED-TO-YAML: instances 观测子路径 (Core v1.yaml, Phase 2 2026-06-17) -->

| 方法 | 路径 | operationId | RBAC |
|---|---|---|---|
| GET | `/api/v1/instances/{instance_id}/logs` | `listInstanceLogs` | `scope:instances:read` |
| GET | `/api/v1/instances/{instance_id}/events` | `listInstanceEvents` | `scope:instances:read` |
| GET | `/api/v1/instances/{instance_id}/metrics` | `getInstanceMetrics` | `scope:instances:read` |
| POST | `/api/v1/instances/{instance_id}/exec` | `createInstanceExecSession` | `scope:instances:exec` |

Console 会话：`POST /api/v1/instances/{instance_id}/console`（见 VM/容器主模块）。

## 页面职责

- Tab：日志（cursor 分页）、事件、指标、终端（exec）
- 仅 `running` 或等价态允许 exec
- 不提供独立 Dashboard 路径（YAML 未声明）

## 创建前置条件

| 依赖项 | 要求状态 | 未满足时的 HTTP 响应 |
|---|---|---|
| 用户登录 | 已认证 | `401 UNAUTHORIZED` |
| 实例存在 | 当前租户可访问 | `404 NOT_FOUND` |
| 读权限 | `scope:instances:read` | `403 FORBIDDEN` |
| exec 权限 | `scope:instances:exec` | `403 FORBIDDEN` |
| exec 实例状态 | `state=running`（产品语义） | `422 PRECONDITION_FAILED`（exec 已声明） |
| exec 请求 | `idempotency_key` + `command[]` | `400 BAD_REQUEST` |

## 操作可用性矩阵

| 操作 | stopped | running | deleted | 只读用户 | 运维角色 |
|---|---|---|---|---|---|
| logs/events/metrics | ✅ | ✅ | ❌ | ✅ | ✅ |
| exec | ❌ | ✅ | ❌ | ❌ | ✅（exec scope） |
| console/vnc | ❌ | ✅ | ❌ | 视 console scope | 视 console scope |

## 接口冻结规则

### `GET /api/v1/instances/{instance_id}/logs`

- 成功：`200 + InstanceLogListResponse`
- 错误：`401`、`403`、`404`
- Query：`limit`、`cursor`

### `GET /api/v1/instances/{instance_id}/events`

- 成功：`200 + InstanceEventListResponse`
- 错误：`401`、`403`、`404`

### `GET /api/v1/instances/{instance_id}/metrics`

- 成功：`200 + InstanceMetricsResponse`
- 错误：`401`、`403`、`404`

### `POST /api/v1/instances/{instance_id}/exec`

- 成功：`200 + InstanceExecSession`
- 错误：`400`、`401`、`403`、`404`、`422`
- requestBody.required：`idempotency_key`、`command`

## 待补边界

- 独立 Web Terminal UI 长连接协议 — exec 返回 session 后前端对接方式待产品定义
- Dashboard — **YAML 未声明** `/dashboard`
- K8s 工作负载级观测 — 归属 `k8s-workloads.md`

## 验收标准

- [ ] 路径与 Phase 2 Core YAML 一致
- [ ] OpenAPI 已声明 ≠ handler 已实现
- [ ] 接口冻结规则逐 operation 列出成功码与错误码
