# 网络管理 — 负载均衡

## 页面定位

`负载均衡` 是网络管理下负载均衡资源的独立明细页。

父级：`network-management.md`。

## 文档管理规则

- 本文是负载均衡子模块主维护源
- 一级权威源：`ANI-main/repo/api/openapi/v1.yaml`

## Core 层要求

| 方法 | 路径 | operationId | RBAC |
|---|---|---|---|
| GET | `/api/v1/networks/load-balancers` | `listNetworkLoadBalancers` | `scope:networks:read` |
| POST | `/api/v1/networks/load-balancers` | `createNetworkLoadBalancer` | `scope:networks:create` |
| GET | `/api/v1/networks/load-balancers/{load_balancer_id}` | `getNetworkLoadBalancer` | `scope:networks:read` |
| DELETE | `/api/v1/networks/load-balancers/{load_balancer_id}` | `deleteNetworkLoadBalancer` | `scope:networks:delete` |

Schema：`NetworkLoadBalancer`（含 `listeners[]`、`scheme`、`vip`）。

POST 创建必须带 `idempotency_key`。

## 页面职责

- 负载均衡 CRUD；监听器摘要展示
- 关联 VPC / 子网跳转

## 创建前置条件

| 依赖项 | 要求状态 | 未满足时的 HTTP 响应 |
|---|---|---|
| 用户登录 | 已认证 | `401 UNAUTHORIZED` |
| 读/写权限 | 对应 networks scope | `403 FORBIDDEN` |
| POST 必填 | `name`、`vpc_id`、`idempotency_key` | `400 BAD_REQUEST` |
| `vpc_id` | 存在且租户可见 | `404 NOT_FOUND` |
| `subnet_id` 若提供 | 属于该 VPC | 具体语义待 Core 冻结 |
| `scheme` | 仅 `internal` / `public` | `400 BAD_REQUEST` |

**当前 YAML 未在 create 声明 `422`**。

## 操作可用性矩阵

| 操作 | 只读用户 | 网络管理员 |
|---|---|---|
| 列表/详情 | ✅ | ✅ |
| 创建/删除 | ❌ | ✅ |
| 编辑 listeners 子资源 | ❌ | ❌（首版只读摘要） |

## 接口冻结规则

### `GET /api/v1/networks/load-balancers`

- 成功：`200 + NetworkLoadBalancerListResponse`
- 错误：`401`、`403`

### `POST /api/v1/networks/load-balancers`

- 成功：`201 + NetworkLoadBalancer`
- 错误：`400`、`401`、`403`、`404`

### `GET /api/v1/networks/load-balancers/{load_balancer_id}`

- 成功：`200 + NetworkLoadBalancer`
- 错误：`401`、`403`、`404`

### `DELETE /api/v1/networks/load-balancers/{load_balancer_id}`

- 成功：`200 + NetworkLoadBalancer`
- 错误：`401`、`403`、`404`

## 待补边界

- listeners 独立 CRUD 子路径 — **TODO-YAML**
- 后端目标组 / 健康检查 — **TODO-YAML**
- 负载均衡更新 PATCH — **当前 YAML 未声明**

## 验收标准

- [ ] `listeners[]` 首版只读摘要，不扩展未声明子路径
- [ ] 接口冻结规则逐 operation 列出成功码与错误码
