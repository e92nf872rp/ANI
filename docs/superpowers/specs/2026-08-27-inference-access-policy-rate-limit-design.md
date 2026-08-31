# 推理限流与访问策略设计

> 日期：2026-08-27
> 阶段：`INFERENCE-SERVICE-C42`
> 状态：草案

## 背景

原型中的“推理服务”已经不仅是控制面生命周期页，还包含面向租户开发者的 OpenAI 兼容调用闭环：选择 API Key、在 Playground 调用、查看调用地址，并把推理服务绑定到“限流与访问策略”。本阶段只处理后端服务、契约、数据面鉴权和真实门禁；Console 前端不在 C42 实施范围内，只作为后续消费者保留接口形态。

当前 ANI 推理服务已经完成控制面与运行时主链路：模型版本解析、镜像冻结、CPU/GPU/vGPU vLLM 部署、日志、生命周期、真实集群门禁，以及 Envoy AI Gateway C40 静态数据面方案。C40 只复用 auth-service 现有 AK 每分钟请求数限制，并明确不做租户聚合限流、服务级并发、计费或 token 用量上报。

因此本设计补齐原型中的“限流与访问策略”产品能力。它不是替代 Envoy AI Gateway，也不是把 OpenAI 请求重新代理回 ANI Gateway；它是在 Envoy AI Gateway 数据面前置授权链路中增加 ANI 可管理、可审计、可演进到计费的推理访问策略。

## 已确认决策

1. Envoy AI Gateway 继续是唯一推理调用数据面；ANI Gateway 只承担控制面 API，不代理 `/v1/chat/completions`、`/v1/embeddings` 或 SSE。
2. 访问策略和限流策略是 ANI Services 产品能力，契约归属 `repo/api/openapi/services/v1.yaml`。
3. 第一版只做请求级访问控制、QPS/RPM 限流和保守并发限制；不做 token 账单、余额扣减或套餐计费。
4. auth-service 继续是 AK 的唯一认证来源；策略服务不保存 AK 明文，不重新实现 AK 校验。
5. `envoy-authz-adapter` 在完成 AK 校验和租户匹配后调用策略检查；策略拒绝时直接向 Envoy 返回 403/429/503。
6. Envoy Gateway/AI Gateway 的原生限流能力只作为平台兜底保护，不作为 ANI 后端策略 API 的真实来源。
7. 跨租户访问继续返回 404，避免暴露目标推理服务是否存在；同租户但策略拒绝返回 403。
8. 命中记录只保存脱敏字段：tenant_id、inference_service_id、policy_id、key_id 或 key_prefix、reason、path、model、状态码、时间和 request_id；不保存 AK 原文、Authorization 头、prompt、completion 或 embedding 内容。
9. C42 不要求 C41 动态发布已完成，但需要兼容 C41：策略检查只依赖受信任路由上下文中的 tenant_id、inference_service_id、外部模型名和路径，不依赖 Kubernetes 服务名。

## 产品范围

### C42 P0 范围

- 新增推理访问策略资源。
- 提供租户侧后端 API，用于创建、启用、禁用、删除策略；Console 前端后续再接入。
- 支持把策略绑定到一个或多个推理服务。
- 支持策略作用到：
  - 指定推理服务；
  - 指定 API Key；
  - 指定推理服务 + 指定 API Key；
  - 租户默认推理策略。
- 支持限制：
  - QPS；
  - RPM；
  - 并发上限。
- 支持访问规则：
  - 允许租户内全部 AK；
  - 只允许指定 AK；
  - 拒绝指定 AK。
- 支持命中记录查询：
  - 访问拒绝；
  - QPS/RPM 超限；
  - 并发超限；
  - 策略后端不可用。
- 支持 chat 和 embeddings 两类 OpenAI 兼容路径。
- 真实门禁覆盖 Envoy AI Gateway：无 AK、无策略、策略允许、策略拒绝、QPS/RPM 超限、并发超限、跨租户隐藏、chat、embeddings。

### C42 不做

- token 用量统计、计费、余额扣减、账单出账。
- 按 prompt/completion 内容做审计或风控。
- 按 IP、地域、User-Agent 做复杂风控。
- 动态多后端权重、灰度发布、熔断或容灾。
- 把策略下发为 Kubernetes Secret 或让 Envoy 直接读取 ANI 数据库。
- 让 vLLM 感知 AK、租户身份或策略结果。

## 原型映射

| 原型能力 | C42 设计 |
|---|---|
| `/inference/policies` 列表 | `GET /inference-policies` |
| 创建策略 | `POST /inference-policies` |
| 策略名称、状态、作用范围 | `InferenceAccessPolicy` 基础字段 |
| 限流规则 | `rate_limits.qps`、`rate_limits.rpm` |
| 并发规则 | `concurrency.max_in_flight` |
| 访问范围 | `access.allow_api_key_ids`、`access.deny_api_key_ids`、`access.allow_all_tenant_keys` |
| 绑定推理服务 | `POST /inference-policies/{policy_id}/bindings` 或服务子路径绑定 |
| 推理详情“策略”Tab | 查询当前服务已绑定策略和生效摘要 |
| 命中记录 | `GET /inference-policy-events` |
| Playground 选择 API Key 调用 | 后端保持 API Key 管理和 Envoy AI Gateway 策略检查能力，前端 Playground 不在 C42 实施范围 |

## 组件边界

| 组件 | 职责 | 不负责 |
|---|---|---|
| ANI Gateway | 暴露 Services REST 控制面 API；鉴权用户身份；委托 inference-service | 代理 OpenAI 请求、执行数据面限流 |
| inference-service | 策略资源、绑定关系、策略检查 RPC、命中记录写入 | AK 校验、JWT 签发、直接写 Envoy CRD |
| auth-service | AK 校验、撤销、过期、AK 自身 rpm、last_used_at、key_id 权威来源 | 推理服务策略、策略命中记录 |
| envoy-authz-adapter | Envoy ext_authz 协议转换；提取 Bearer AK；调用 auth-service；调用策略检查；返回 allow/deny | 持久化策略、保存 AK、代理 vLLM 流量 |
| Envoy AI Gateway | OpenAI 路由、模型匹配、SSE 透传、调用 ext_authz、平台兜底限流 | ANI 产品策略存储、AK 生命周期 |
| vLLM | 执行 chat/embeddings | 鉴权、限流、租户策略 |

## 控制面 API 设计

所有 API 均属于 Services API，路径前缀仍为 `/api/v1/svc`。所有创建、更新、绑定和删除类请求必须带 `idempotency_key`。

### 策略资源

```yaml
InferenceAccessPolicy:
  id: uuid
  tenant_id: uuid
  name: string
  status: enabled | disabled
  description: string?
  priority: integer
  scope:
    type: tenant_default | inference_service | api_key | inference_service_api_key
    inference_service_ids: uuid[]
    api_key_ids: string[]
  access:
    allow_all_tenant_keys: boolean
    allow_api_key_ids: string[]
    deny_api_key_ids: string[]
  rate_limits:
    qps: integer?
    rpm: integer?
  concurrency:
    max_in_flight: integer?
    lease_ttl_seconds: integer
  created_at: date-time
  updated_at: date-time?
```

`api_key_ids` 使用 auth-service 返回的 AK ID，不接受 AK 原文。后端响应只返回 AK ID、name 和 prefix 等脱敏信息，供后续前端展示。

### API 列表

```text
GET    /inference-policies
POST   /inference-policies
GET    /inference-policies/{policy_id}
PATCH  /inference-policies/{policy_id}
DELETE /inference-policies/{policy_id}

GET    /inference-services/{service_id}/policies
PUT    /inference-services/{service_id}/policies

GET    /inference-policy-events
```

`/inference-services/{service_id}/policies` 当前固定返回 501。C42 将其升级为服务级策略绑定接口，但不删除既有路径，保持兼容。

C42 新增控制面接口按 v1 鉴权契约声明：`security` 同时允许 `BearerAuth` 与 `ApiKeyAuth`，并携带 `x-ani-authz` 元数据；`resource=inference_policy`，`boundary=tenant`，`principal_kinds=[user]`，action 按接口语义取 `read/create/update/delete`。这里约束的是 ANI Gateway 控制面授权格式；Envoy AI Gateway 数据面 AK 调用仍由 `envoy-authz-adapter` 与 auth-service/inference-service 策略检查链路执行。

### 内部策略检查 RPC

`envoy-authz-adapter` 不直接查询数据库，调用 inference-service 内部 gRPC：

```text
CheckInferenceAccess(request)
  tenant_id
  user_id
  api_key_id
  api_key_prefix
  inference_service_id
  external_model
  openai_path
  request_id
  stream
  now

CheckInferenceAccess(response)
  decision: allow | deny | rate_limited | concurrency_limited
  http_status
  reason_code
  policy_id
  lease_id
  retry_after_seconds
```

P0 中 `lease_id` 用于并发限制的保守 TTL 释放。非流式请求可以在请求结束时由适配器尽力释放；SSE 请求第一版允许依赖 TTL 自动释放。

## 数据模型

新增 Services 侧表：

```text
inference_access_policies
  id
  tenant_id
  name
  status
  description
  priority
  scope_type
  allow_all_tenant_keys
  rate_qps
  rate_rpm
  max_in_flight
  lease_ttl_seconds
  created_at
  updated_at
  deleted_at

inference_access_policy_services
  policy_id
  tenant_id
  inference_service_id

inference_access_policy_api_keys
  policy_id
  tenant_id
  api_key_id
  key_prefix
  effect: allow | deny

inference_access_policy_events
  id
  tenant_id
  policy_id
  inference_service_id
  api_key_id
  key_prefix
  request_id
  openai_path
  external_model
  decision
  reason_code
  http_status
  retry_after_seconds
  created_at
```

Redis 或现有 CacheStore key：

```text
inference:rl:qps:{tenant}:{service}:{api_key}:{epoch_second}
inference:rl:rpm:{tenant}:{service}:{api_key}:{epoch_minute}
inference:conc:{tenant}:{service}:{api_key}
inference:conc:lease:{lease_id}
```

key 中不放 AK 原文，只放 AK ID。若 auth-service 第一版无法把 `key_id` 传给 adapter，则 C42 第一项必须扩展 auth-service 的权威 Principal 返回，或新增只返回脱敏 AK 元数据的内部验证 RPC。不能用 AK hash 或 AK 原文作为跨服务限流 key。

## 策略匹配规则

匹配输入来自受信任上下文：

- `tenant_id`：auth-service 返回；
- `api_key_id`：auth-service 返回；
- `inference_service_id`：Envoy 路由上下文或 C41 发布控制器绑定；
- `external_model`：Envoy AI Gateway 匹配到的模型名；
- `openai_path`：Envoy 请求路径。

匹配顺序：

1. `inference_service + api_key` 精确策略；
2. `api_key` 策略；
3. `inference_service` 策略；
4. `tenant_default` 策略；
5. 无策略时使用系统默认策略。

系统默认策略：

- 允许本租户有效 AK 调用本租户已发布服务；
- 不额外施加服务级 QPS/RPM/并发；
- 仍受 auth-service AK 自身 `rate_limit_rpm` 限制；
- 记录允许类聚合指标，但 P0 不落每次允许事件。

同一层存在多个 enabled 策略时，按 `priority` 从小到大匹配；第一条匹配策略生效。删除策略采用软删除，已删除策略不参与匹配。

## 数据面请求链路

```text
Client
  -> Envoy AI Gateway /v1/chat/completions 或 /v1/embeddings
  -> AIGatewayRoute 根据 model 匹配受管推理服务
  -> SecurityPolicy 调用 envoy-authz-adapter
  -> adapter 调 auth-service ValidateToken/ValidatePrincipal
  -> adapter 校验 token tenant == route tenant
  -> adapter 调 inference-service CheckInferenceAccess
  -> allow: Envoy 删除敏感头并转发 vLLM
  -> deny: Envoy 返回 401/403/404/429/503
```

策略检查必须在 vLLM 前完成。vLLM 不接收 Authorization、`x-api-key`、`x-ani-tenant-id`、`x-ani-user-id`。

## 限流与并发语义

### AK 自身 RPM

auth-service 已有 AK 级 `rate_limit_rpm`。这是认证凭据保护，优先于 C42 策略执行。超限返回 429，原因归类为 `CREDENTIAL_RATE_LIMIT_EXCEEDED`。

### 策略 QPS/RPM

策略 QPS/RPM 是推理产品策略。维度为：

```text
tenant_id + inference_service_id + api_key_id + policy_id
```

P0 使用固定窗口计数：

- QPS：秒级窗口；
- RPM：分钟级窗口；
- 超限返回 429；
- 返回 `Retry-After`；
- 记录命中事件。

### 并发上限

并发限制保护 vLLM 长连接和排队压力。P0 使用 Redis lease：

- allow 前尝试递增 in-flight；
- 成功则返回 `lease_id`；
- 超限返回 429；
- 非流式请求结束后尽力释放；
- SSE 请求依赖 TTL 自动释放；
- 适配器进程崩溃时 TTL 保证最终释放。

P0 不声明严格实时并发精度。P1 可通过 Envoy access log、stream 结束回调或专用计量通道做精确释放。

## 错误语义

| 场景 | HTTP | code |
|---|---:|---|
| 缺少 Bearer AK、AK 格式错误、AK 无效 | 401 | `UNAUTHORIZED` |
| AK 已撤销、过期或不存在 | 401 | `UNAUTHORIZED` |
| AK 自身 rpm 超限 | 429 | `CREDENTIAL_RATE_LIMIT_EXCEEDED` |
| 有效 AK 但租户不匹配 | 404 | `NOT_FOUND` |
| 同租户但策略不允许该 AK | 403 | `INFERENCE_ACCESS_DENIED` |
| 策略 QPS/RPM 超限 | 429 | `INFERENCE_RATE_LIMIT_EXCEEDED` |
| 策略并发超限 | 429 | `INFERENCE_CONCURRENCY_LIMIT_EXCEEDED` |
| 策略检查依赖不可用 | 503 | `INFERENCE_POLICY_UNAVAILABLE` |
| vLLM 不可用 | 502/503/504 | 保持 Envoy/AI Gateway 上游错误语义 |

策略服务不可用时必须 fail closed，不能绕过限流直接访问 vLLM。

## Envoy 策略定位

Envoy Gateway/AI Gateway 的原生限流只做平台保护：

- Gateway 总入口兜底；
- 单 Route 粗粒度保护；
- adapter 或 vLLM 被异常流量打爆前的保险丝。

ANI 后端 API 返回的“限流与访问策略”不直接映射为 Envoy `BackendTrafficPolicy`。原因：

- 产品策略需要 AK ID、租户、推理服务、命中记录和后续计费归因；
- Envoy 路由级限流不能独立表达 ANI 的多层策略匹配；
- 策略更新应走 ANI Services 契约、审计和幂等，任何前端后续接入也只能调用 ANI 后端 API，不能直接操作 Envoy CRD。

## 观测与审计

C42 P0 至少记录：

- 策略命中事件；
- 429 次数；
- 403 次数；
- 策略检查延迟；
- 策略后端错误；
- 每服务、每 AK、每策略维度的聚合指标。

不记录：

- AK 原文；
- Authorization 头；
- prompt；
- completion；
- embedding 输入文本或向量内容；
- vLLM 完整响应。

事件保留期第一版固定为 7 天。BOSS 长期审计和计费归档另行设计。

## 安全边界

1. 只有受信任的 Envoy route context 可以传 `target_tenant_id`、`inference_service_id` 和 `external_model`。
2. 客户端提交的 `x-ani-tenant-id`、`x-ani-user-id`、`x-api-key`、query token 和 Cookie 不参与授权。
3. 策略检查输入中的 `api_key_id` 必须来自 auth-service 权威返回，不接受客户端字段。
4. 跨租户永远返回 404。
5. disabled、deleted 或租户不匹配的策略不参与匹配。
6. 命中记录必须脱敏。
7. 策略更新必须有 idempotency_key，并写操作审计。

## 与 C40/C41 的关系

C40 静态数据面：

- 继续验证 Envoy AI Gateway + AK + chat/embeddings 最小闭环；
- 只使用 auth-service AK 自身 rpm；
- 不暴露“策略已生效”的产品承诺。

C41 动态发布：

- 发布控制器维护推理服务到 Envoy 路由/后端的映射；
- 每条受管路由必须携带 `tenant_id`、`inference_service_id`、`external_model`；
- `invocation_url` 只在路由发布成功后填充。

C42 访问策略：

- 复用 C41 的受信任路由上下文；
- 在 ext_authz 中增加策略检查；
- 形成原型中“限流与访问策略”的产品闭环。

C42 可以在 C40 静态路由上先验证，再接入 C41 动态发布。

## 真实门禁

本地门禁：

1. Services OpenAPI 语义校验通过。
2. inference-service 策略匹配、QPS/RPM、并发 lease、事件写入单测通过。
3. envoy-authz-adapter 策略 allow/deny/429/503 转换单测通过。
4. ani-gateway 策略 CRUD/绑定 handler 单测通过。
5. `make validate-services`、`make validate-architecture`、`make test`、`git diff --check` 通过。

真实 Envoy AI Gateway 门禁：

1. 无 AK 请求 chat/embeddings 返回 401。
2. 有效 AK、无自定义策略时 chat/embeddings 成功。
3. 绑定 allowlist 策略后，允许 AK 成功。
4. 未在 allowlist 的同租户 AK 返回 403。
5. 跨租户 AK 返回 404。
6. QPS/RPM 超限返回 429，带 `Retry-After`。
7. 并发超限返回 429。
8. 策略服务不可用时返回 503，vLLM 不收到请求。
9. 命中记录能按租户、服务、策略、AK prefix 查询。
10. 日志、事件、evidence 中不出现 AK 原文、Authorization 头或请求内容。

## 实施切片建议

### C42-A：契约与静态验证

- Services OpenAPI 新增策略资源、绑定、事件查询。
- 新增契约 validator，禁止把策略能力标成已生效但 handler 仍 501。
- 不改运行时代码。

### C42-B：策略存储与控制面

- inference-service 增加策略领域模型、PG migration、repository、service。
- ani-gateway 增加 REST handler，经 gRPC 委托 inference-service。
- 不实现 Console 前端；只保证后续前端可调用的后端 API 形态稳定。

### C42-C：策略检查 RPC 与 adapter 接入

- inference-service 增加 `CheckInferenceAccess` 内部 gRPC。
- envoy-authz-adapter 调用策略检查。
- 支持 403/429/503 映射。

### C42-D：真实 Envoy AI Gateway 门禁

- 在 C40 静态 chat/embeddings 路由上跑策略 allow/deny/rate/concurrency。
- 写脱敏 evidence。
- 验证未授权请求不到达 vLLM。

### C42-E：后端 API 与原型字段对齐

- 后端 API 覆盖原型中 `/inference/policies` 所需字段。
- 推理服务策略绑定查询能返回“策略 Tab”需要的绑定和命中摘要。
- API Key 管理与 OpenAI 调用信息保持可被后续 Playground 消费，但本批不改前端。

## 后续演进

P1：

- 精确 SSE 并发释放；
- 请求成功/失败用量聚合；
- token usage 采集；
- BOSS 运营视角的全租户推理调用审计。

P2：

- 租户套餐和推理 token 配额；
- 欠费冻结；
- 成本归因和账单；
- 多区域/多集群策略同步。
