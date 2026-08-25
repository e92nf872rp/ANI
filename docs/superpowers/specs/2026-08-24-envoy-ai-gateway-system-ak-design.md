# Envoy AI Gateway 系统 AK 接入设计

> 日期：2026-08-24
> 阶段：`INFERENCE-SERVICE-C40/C41`
> 状态：已评审

## 背景

ANI 推理控制面已经贯通 Console、ANI Gateway、inference-service、Core `platform-workloads`、Kubernetes 和 vLLM，并通过 CPU、整卡 GPU 与 vGPU 的真实集群门禁。当前缺口不是工作负载创建，而是租户可使用的 OpenAI 兼容推理数据面：现有 ANI Gateway `/v1/chat/completions` 仍是 `501` 占位，`invocation_url` 仍为空。

集群中已经手工安装 Envoy AI Gateway v1.0.0 和 Envoy Gateway v1.8.x，并存在一个直接指向 vLLM ClusterIP 的静态演示路由。该路由、后端和 Secret 型 APIKeyAuth 尚未进入仓库，也不受 ANI 推理服务生命周期管理。

本设计把 Envoy AI Gateway 定为唯一推理调用数据面，复用 ANI Auth 已有 `ani_*` 系统 AK 校验，不在 Kubernetes Secret 中复制 AK 明文。

## 已确认决策

1. 外部调用入口是 Envoy AI Gateway 下由受管推理 Route 注册的 OpenAI 兼容路径；ANI Gateway 继续只承担控制面职责，不实现第二套推理反向代理。
2. vLLM Service 保持 `ClusterIP`，不直接暴露给租户。
3. Envoy 使用标准 `envoy.service.auth.v3.Authorization/Check` 做外部授权。
4. 新建独立、无状态的 `envoy-authz-adapter`，只转换 Envoy ext_authz 与 ANI Auth gRPC 协议。
5. adapter 直接调用现有 `auth.v1.AuthService/ValidateToken`；不新增 `ValidateAPIKey` RPC，也不在 adapter 内复制 AK 业务逻辑。
6. `ValidateToken` 收到 `ani_*` 后沿用现有 AK 模块，执行哈希查询、撤销/过期检查、每分钟限流和 `last_used_at` 更新；不进入登录、OIDC、Refresh Token 流程。
7. C40 只要求系统 AK 有效，不要求 `scope:inference:invoke` 或 `scope:inference:*`。细粒度推理 scope 延后。
8. AK 有效不等于可跨租户访问。目标推理服务的 owner tenant 必须由受信任的路由配置传给 adapter，并与 `ValidateToken` 返回的 `tenant_id` 相等。
9. 不把现有 AK 明文同步到 Kubernetes Secret；手工的 Secret 型 APIKeyAuth 只在 ext_authz 真实门禁通过后移除。
10. C40 先产品化一个静态推理服务；C41 再实现与 InferenceService 生命周期联动的动态发布。
11. C40 的受管推理 Route 下所有已注册推理路径统一要求系统 AK；Envoy 和 adapter 使用工作负载自身的 Kubernetes readiness/liveness probe，公网 Gateway 不开放 `/healthz`、`/readyz`，未注册路径直接返回 `404`。
12. 客户端第一版只能通过 `Authorization: Bearer ani_*` 传递 AK；不接受 `x-api-key`、Cookie 或查询参数。

## 范围

### C40：静态单服务闭环

- 仓库管理 Envoy AI Gateway 基础资源、一个静态 AI 后端、模型路由、ext_authz SecurityPolicy、adapter 部署与配置。
- 使用一个已运行并 Ready 的 ANI InferenceService/vLLM Service 完成普通响应和 SSE 流式响应。
- 认证使用系统中已创建的真实 `ani_*` AK。
- 路由配置显式绑定该推理服务的 `inference_service_id`、owner `tenant_id` 和对外模型标识。
- 受管推理 Route 下的所有已注册推理路径复用同一 ext_authz 鉴权闭环；工作负载探针不经过公网 Route 或 SecurityPolicy。
- 复用 auth-service 现有单 AK RPM；不新增租户总 RPM、服务级并发或 token 硬配额。
- C40 不改变 InferenceService API 的 `invocation_url`，也不实现自动发布/撤销。

### C41：动态服务发布

- 由独立、权限受限的发布 controller/adapter 观察 ANI 推理服务期望状态并维护 Envoy 资源。
- Inference-service 不直接操作 Kubernetes 或 Envoy CRD，继续遵守 ANI Services/Core 边界。
- 服务 Ready 后发布路由；stop/delete 前先撤销路由；新 generation 健康后再切流。
- 动态闭环稳定后再填充正式 `invocation_url`。

### 不在本批范围

- 推理专用 scope 强制校验。
- 计费、token 用量账单和配额系统。
- 租户聚合限流、服务级并发限制和 Envoy 到 Metering 的 token 用量上报。
- 多集群、跨节点 LWS、复杂流量分割或多后端容灾。
- 用 ANI Gateway 代理 OpenAI 请求。
- Envoy AI Gateway 版本升级；C40 以集群现有稳定版本为基线。

## 组件边界

| 组件 | 职责 | 明确不负责 |
|---|---|---|
| Envoy AI Gateway | 公网入口、OpenAI 请求解析、模型匹配、转发、SSE 透传 | AK 数据存储、ANI 登录、推理服务控制面 |
| `envoy-authz-adapter` | 实现标准 ext_authz；提取 Bearer AK；调用 `ValidateToken`；校验目标 tenant；转换响应 | 数据库访问、AK 生命周期、JWT 签发、模型推理 |
| auth-service | ANI AK 的唯一认证来源；撤销、过期、RPM、最近使用时间 | Envoy 协议和路由资源 |
| 发布 controller/adapter（C41） | 把已 Ready 的推理服务投影为受管 Envoy 路由与后端 | 创建/停止 vLLM、承载请求流量 |
| inference-service | 推理服务期望状态、生命周期与业务身份 | 直接写 Envoy/Kubernetes CRD、代理推理请求 |
| vLLM | OpenAI 兼容推理执行 | ANI AK 校验、租户授权、外部暴露 |

`envoy-authz-adapter` 与 auth-service 是不同 Deployment、Service、端口和 gRPC service，不存在端口或协议注册冲突。adapter 是防腐层；Envoy 类型不会进入 auth-service。

## 完整请求链路

客户端请求：

```http
POST /v1/chat/completions
Authorization: Bearer ani_<tenant>_<secret>
Content-Type: application/json

{
  "model": "<ANI 对外模型标识>",
  "messages": [{"role": "user", "content": "你好"}],
  "stream": true
}
```

处理顺序：

1. Envoy AI Gateway 接收受管 Route 下的已注册推理路径，例如 `/v1/chat/completions`。
2. Envoy AI Gateway 根据 OpenAI 请求和 `model` 完成静态模型路由匹配；未注册路径或模型直接返回 `404`，不转发上游。
3. 绑定到该推理路由的 SecurityPolicy 调用 adapter 的标准 `Authorization/Check`。
4. SecurityPolicy 通过受信任的 ext_authz context extension 传入静态目标 `tenant_id` 与 `inference_service_id`；这两个值来自仓库/发布 controller，不接受客户端同名头。
5. adapter 只接受 `Authorization: Bearer <token>`，不接受 `x-api-key`、Cookie 或查询参数；提取 token 后调用现有 `AuthService.ValidateToken`。
6. auth-service 识别 `ani_*` 并进入现有 AK 校验；成功返回 `TenantContext`。
7. adapter 比较 `TenantContext.tenant_id` 与目标 `tenant_id`。相同则允许；不同则拒绝且不泄露目标是否存在。
8. 第一版不向 vLLM 注入租户或用户身份头。Envoy 在转发前删除 `Authorization`、`x-api-key`、`x-ani-tenant-id` 和 `x-ani-user-id`，保证 AK 原文和客户伪造身份不进入 vLLM。
9. 请求转发到对应 AIServiceBackend 和 vLLM ClusterIP Service。
10. vLLM 返回普通 JSON，或保持连接返回 SSE；Envoy 将响应透传给客户端。一次流式请求只在开始时授权一次，不逐 chunk 调用 Auth。
11. Envoy 和 adapter 的 Kubernetes readiness/liveness probe 直接检查各自工作负载，不经过公网 Gateway、SecurityPolicy 或推理后端；公网 `/healthz`、`/readyz` 返回 `404`。

## 租户与模型标识

仅使用模型展示名无法保证租户隔离，因为不同租户可以部署同名模型。C40 的对外模型标识必须唯一绑定一个 InferenceService，不能把用户可覆盖的 `x-ani-tenant-id` 用作路由依据。

C40 采用静态绑定：

```text
public model id
  -> inference_service_id
  -> owner tenant_id
  -> vLLM Kubernetes Service
```

adapter 从受信任的 route context 获得 owner tenant，并在放行前比较系统 AK 的 tenant。C41 由发布 controller 原子维护同一组绑定。具体对外模型标识格式在实施计划中根据现有 InferenceService ID/模型字段确定，但必须全局无歧义且不暴露 Kubernetes Service DNS。

## 认证与授权契约

adapter 不修改 Auth proto，调用：

```text
auth.v1.AuthService/ValidateToken(ValidateTokenRequest)
  -> common.v1.TenantContext
```

adapter 只将 `ani_*` 用于此数据面。即使 `ValidateToken` 也支持 JWT，C40 不把浏览器登录 token 作为推理调用凭据；非 `ani_*` Bearer token 在 adapter 边界拒绝。

第一版允许条件：

```text
token 以 ani_ 开头
AND ValidateToken 成功
AND returned tenant_id == trusted target tenant_id
```

不检查 `TenantContext.roles` 中的 inference scope。scope 字段仍原样保留，供后续版本启用细粒度授权。

当前 `TenantContext` 没有 AK `key_id`，C40 不为日志需要而扩展 Auth proto；日志使用可用的 tenant/user/request/route 身份，且不得记录 AK 原文或哈希。

## 错误语义

| 场景 | 对外状态 | 说明 |
|---|---:|---|
| 缺少 Bearer AK、格式错误、非 `ani_*` | 401 | adapter 本地拒绝；不尝试其他凭据位置 |
| AK 不存在、已过期、已撤销 | 401 | `ValidateToken` 返回 `Unauthenticated` |
| AK RPM 超限 | 429 | `ResourceExhausted` 映射为 Too Many Requests |
| 有效 AK 但 tenant 不匹配 | 404 | 隐藏其他租户服务是否存在 |
| adapter 或 auth-service gRPC 不可达 | 503 | `failOpen: false`，不绕过认证 |
| 模型/路由不存在 | 404 | Envoy 路由拒绝 |
| 路由存在但 vLLM 未就绪 | 503 | 后端不可用 |
| 上游响应协议错误 | 502 | Bad Gateway |
| 上游推理超时 | 504 | Gateway Timeout |

现有 `ValidateToken` 会把 AK store 内部错误统一折叠为 `Unauthenticated`，只有 adapter 到 auth-service 的传输失败能稳定区分为 503。C40 不为此新增 Auth RPC；如果真实故障门禁要求区分 PostgreSQL/Redis 内部故障，则另行对现有 `ValidateToken` 做通用错误分类修正，不能在 adapter 猜测错误文本。

## 生命周期与发布顺序

### 创建或启动

```text
InferenceService desired=running
  -> Core 创建/恢复 workload
  -> vLLM readiness 通过
  -> 发布 Backend/AIServiceBackend/AIGatewayRoute 绑定
  -> Envoy Accepted/ResolvedRefs/Programmed
  -> invocation ready
```

### 停止或删除

```text
先撤销新请求路由
  -> 等待 Envoy 接受新配置
  -> 再停止或删除 vLLM workload
```

已建立的 SSE 请求采用有界 drain；不得无限阻塞 stop/delete。具体 drain timeout 属于 C41 实施参数。

### generation 更新

```text
创建新 generation
  -> readiness 与最小推理探针通过
  -> 原子切换 Envoy 后端
  -> drain 旧 generation
  -> 回收旧 workload
```

发布失败不能伪装为推理服务可调用；控制面仍能查询服务状态和失败原因。

## 安全要求

- adapter 无数据库凭据，不保存或缓存 AK。
- AK 仅在客户端、Envoy ext_authz 调用和 auth-service 校验内短暂存在。
- Envoy、adapter、auth-service、vLLM 的日志与门禁证据不得输出完整 AK、AK 哈希或 Authorization 头。
- 客户端提供的 `x-ani-*` 身份头在进入受信任链路前必须删除或覆盖。
- adapter 到 auth-service 使用集群内 gRPC；生产加密与服务身份沿用平台内部服务通信规范。
- ext_authz 必须 fail closed；不得因 auth-service 超时而直接转发到 vLLM。
- adapter 使用最小网络权限，只允许 Envoy 入站和 auth-service 出站。
- 发布 controller 使用限定 namespace、kind、name/label 范围的最小 RBAC，不获得任意 workload 管理权限。

## C40 验收门禁

真实集群门禁必须至少证明：

1. Gateway、AIGatewayRoute、AIServiceBackend、Backend、SecurityPolicy 和 adapter 均处于可用/Accepted 状态。
2. 有效 owner-tenant 系统 AK 可以完成非流式 `/v1/chat/completions`。
3. 有效 owner-tenant 系统 AK 可以完成 SSE，并收到结束标记。
4. 缺失、格式错误、随机、过期和已撤销 AK 均被拒绝。
5. AK 撤销后无需同步 Secret，下一次请求立即失败。
6. AK RPM 超限返回 429。
7. 另一个租户的有效 AK 不能调用目标服务，并返回 404。
8. adapter 或 auth-service 不可达时返回 503，且 vLLM 请求计数不增加。
9. vLLM、Envoy/adapter 日志、测试输出、证据文件和 Kubernetes Secret 中不存在该 ANI AK 明文。
10. vLLM 只通过 ClusterIP 接收 Envoy 转发，外部不能绕过 Gateway 访问。
11. 现有 Console/ANI Gateway 推理控制面查询和生命周期门禁不回归。
12. 旧 Secret 型 APIKeyAuth 仅在新链路全部通过后移除，并再次证明有效 AK 调用成功。
13. 受管推理 Route 下每个已注册推理路径都要求 AK；未注册路径返回 `404`且不到达 vLLM。
14. Envoy 和 adapter 的 Kubernetes readiness/liveness probe 正常工作且不占用 AK RPM；公网 `/healthz`、`/readyz` 均返回 `404`且不到达 vLLM。
15. `x-api-key`、Cookie 和查询参数中的 AK 均不能授权；只有 `Authorization: Bearer ani_*` 可用。

## C41 验收门禁

除重复执行 C40 门禁外，还必须证明：

- 新推理服务 Ready 前不可调用，Ready 后自动发布。
- stop/delete 先撤销路由，再停止/删除 workload。
- generation 更新只在新后端健康后切流。
- controller 重启、重复事件和部分失败不会产生重复或悬挂绑定。
- 两个租户部署同名模型时互不越权。
- `invocation_url` 只在发布成功后出现，撤销后不再宣称可用。

## 可观测性

至少记录并聚合：

- Envoy 请求数、状态码、上游延迟、SSE 时长和后端错误。
- adapter 授权结果类别、Auth gRPC 延迟和依赖错误；标签不得包含 AK。
- 以 request ID 关联 Envoy、adapter 和 vLLM；tenant/service 标签必须控制基数。
- C41 发布 controller 的 reconcile 次数、失败原因、Envoy condition 和最后成功 generation。

## 回滚

C40 部署采用并行验证地址，不立即替换任何已有公开入口。回滚时撤销新 SecurityPolicy/route/backend 和 adapter；vLLM 与 ANI 控制面保持不变。旧的手工 Secret 路由只有在新门禁通过后才删除，删除后若必须恢复，仅用于受控应急，不把它重新定义为正式系统 AK 方案。

## 实施顺序

1. 定义 adapter 端口、配置和错误映射，先写协议与安全单元测试。
2. 实现标准 ext_authz 到现有 `ValidateToken` 的薄适配。
3. 增加容器、Deployment、Service、NetworkPolicy/最小权限配置。
4. 仓库化静态 Envoy AI Gateway route/backend/SecurityPolicy，并绑定单个 owner tenant。
5. 增加本地契约门禁和真实集群 C40 门禁。
6. 新链路通过后移除手工 Secret 型 APIKeyAuth，再执行回归门禁。
7. C40 关闭后单独设计并实现 C41 动态发布 controller。

## 官方依据

- Envoy Gateway v1.8 External Authorization：<https://gateway.envoyproxy.io/v1.8/tasks/security/ext-auth/>
- Envoy Gateway v1.8 API Key Authentication：<https://gateway.envoyproxy.io/v1.8/tasks/security/apikey-auth/>
- Envoy AI Gateway API（请求体模型提取与 `x-ai-eg-model` 路由）：<https://aigateway.envoyproxy.io/docs/latest/api/>
