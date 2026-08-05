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
| GET/POST | `/api/v1/networks/load-balancers/{load_balancer_id}/listeners` | `list/createNetworkLoadBalancerListener` | `scope:networks:read/create` |
| GET/PUT/DELETE | `/api/v1/networks/load-balancers/{load_balancer_id}/listeners/{listener_id}` | `get/update/deleteNetworkLoadBalancerListener` | `scope:networks:read/create/delete` |
| GET/POST | `/api/v1/networks/load-balancers/{load_balancer_id}/backend-groups` | `list/createNetworkLoadBalancerBackendGroup` | `scope:networks:read/create` |
| GET/PUT/DELETE | `/api/v1/networks/load-balancers/{load_balancer_id}/backend-groups/{backend_group_id}` | `get/update/deleteNetworkLoadBalancerBackendGroup` | `scope:networks:read/create/delete` |
| GET/POST | `/api/v1/networks/load-balancers/{load_balancer_id}/backend-groups/{backend_group_id}/members` | `list/createNetworkLoadBalancerBackendMember` | `scope:networks:read/create` |
| GET/PUT/DELETE | `/api/v1/networks/load-balancers/{load_balancer_id}/backend-groups/{backend_group_id}/members/{member_id}` | `get/update/deleteNetworkLoadBalancerBackendMember` | `scope:networks:read/create/delete` |

Schema：`NetworkLoadBalancer`（含 `listeners[]`、`scheme`、`vip`）。

POST 创建必须带 `idempotency_key`。

## 页面职责

- 创建负载均衡后管理监听器、后端组、后端成员和健康检查
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

公网负载均衡在平台不具备 EIP 能力时返回 `422 EIPNotAvailable`。

## 操作可用性矩阵

| 操作 | 只读用户 | 网络管理员 |
|---|---|---|
| 列表/详情 | ✅ | ✅ |
| 创建/删除 | ❌ | ✅ |
| 管理 listeners / backend groups / members | ❌ | ✅ |

## 接口冻结规则

### `GET /api/v1/networks/load-balancers`

- 成功：`200 + NetworkLoadBalancerListResponse`
- 错误：`401`、`403`

### `POST /api/v1/networks/load-balancers`

- 成功：`201 + NetworkLoadBalancer`
- 错误：`400`、`401`、`403`、`404`、`409`、`422`

### `GET /api/v1/networks/load-balancers/{load_balancer_id}`

- 成功：`200 + NetworkLoadBalancer`
- 错误：`401`、`403`、`404`

### `DELETE /api/v1/networks/load-balancers/{load_balancer_id}`

- 成功：`200 + NetworkLoadBalancer`
- 错误：`401`、`403`、`404`

## 当前边界

- listener 创建必须关联 `backend_group_id`
- 后端成员支持 `instance` / `ip`、端口、权重和只读健康状态
- 后端组承载轮询算法与健康检查配置
- 负载均衡主资源更新 PATCH 当前未声明
- UDP、HTTPS 证书、静态 VIP 和独立 EIP 资源不在本批 7.29 P0 范围

## 验收标准

- [ ] 创建负载均衡后可独立管理 listener、backend group 和 member
- [ ] listener 可唯一关联后端组，member 健康状态与资源生命周期分离
- [ ] 接口冻结规则逐 operation 列出成功码与错误码
