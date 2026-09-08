# Envoy AI Gateway 多租户动态发布设计

> 日期：2026-08-31
> 阶段：`INFERENCE-SERVICE-C41`
> 状态：本地/逻辑实现完成；真实集群 live 尚未执行
> 范围：ANI Services 推理服务、Envoy AI Gateway 数据面、系统 AK、动态路由发布

## 1. 文档定位

本文定义 C41 的权威目标方案：所有租户共用一个 Envoy AI Gateway 公网地址，客户端只提交 ANI API Key 和标准 OpenAI `model`，平台在认证后根据 `(tenant_id, served_model_name, OpenAI path)` 精确解析并路由到当前租户的推理服务。

本文不改写 C40 已完成的静态真实验证事实，但取代以下旧设计假设：

- C40 中“路由先静态绑定目标 tenant/service，再调用 ext_authz”的 C41 延伸方案；
- C40 中要求对外模型名全局唯一的方案；
- C42 中把 `inference_service_id` 和 `external_model` 作为鉴权前受信任路由输入的方案。

C42 已确认的访问策略 API、策略匹配顺序、Redis 限流、命中记录和错误语义继续有效；本设计只修正 C41 动态发布后的服务解析与路由上下文来源。

## 2. 目标

1. 所有租户共用一个 AI Gateway 公网基址，不为租户创建独立域名。
2. 客户端保持标准 OpenAI 请求，只传 AK、`model` 和业务请求体。
3. 不同租户可以创建同名 `served_model_name`，且只能调用本租户服务。
4. 推理服务就绪后自动发布 Envoy 资源，停止或删除前自动撤销。
5. chat 与 embeddings 根据服务任务类型分别发布和校验。
6. 复用 auth-service 的 ANI AK 校验和 C42 访问策略/限流，不复制认证与策略逻辑。
7. Envoy、adapter、策略服务或 Redis 失败时关闭访问，不绕过到 vLLM。
8. `invocation_url` 只在工作负载和网关路由都就绪后返回。

## 3. 非目标

- 不实现或开放租户感知的 `GET /v1/models`；该能力延期。
- 不增加 ANI Gateway 的 `/v1/chat/completions`、`/v1/embeddings` 代理。
- 不要求 AK 包含 `scope:inference:invoke` 或 `scope:inference:*`。
- 不实现 token 计费、余额扣减、账单或套餐配额。
- 不实现多集群路由、多后端权重、灰度、自动容灾或跨区域发布。
- 不让客户端传 `tenant_id`、`inference_service_id`、内部路由头或 Kubernetes 地址。
- 不把 AK 明文同步到 Kubernetes Secret。

`/v1/models` 延期不等于允许返回全局模型列表。若 Envoy AI Gateway 的原生端点在当前安装中自动暴露全局模型，首版必须用固定 `404` 阻断，不能泄露其他租户的模型名。

## 4. 核心决策

### 4.1 单域名、AK 决定租户

所有租户使用同一公网入口，例如：

```text
https://ai.example.com
```

租户身份只来自 auth-service 对 AK 的权威解析。Host、请求体、查询参数和客户端 `x-ani-*` 头都不能声明租户。

### 4.2 `served_model_name` 是唯一外部模型概念

客户端请求体中的 `model` 对应 `InferenceService.served_model_name`：

```json
{
  "model": "ani-qwen3"
}
```

唯一性范围是：

```text
(tenant_id, served_model_name) WHERE deleted_at IS NULL
```

同一租户的活动服务不能重名；不同租户可以重名。`served_model_name` 创建后不可变，停止时继续保留，删除完成后释放。

不新增第二个公开或持久化的 `external_model` 概念。旧代码、内部 RPC 或事件字段中的同义命名在实施时统一按 `served_model_name` 解释和收敛。

### 4.3 认证后解析服务并重新选路由

Envoy AI Gateway 从 OpenAI 请求体提取 `model`，并用提取结果强制覆盖内部 `x-ai-eg-model`。Gateway 级 SecurityPolicy 调用 `envoy-authz-adapter`；adapter 校验 AK 后调用 inference-service，以 `(tenant_id, served_model_name, OpenAI path)` 解析服务并执行访问策略。成功后 adapter 注入可信路由头，Envoy 使用 `recomputeRoute` 重新匹配最终服务路由。

客户端不传 `x-ai-eg-model`、`x-ani-tenant-id` 或服务 UUID。即使客户端伪造 `x-ai-eg-model`，该值也必须在模型提取前被删除或被提取结果强制覆盖；后续鉴权和路由只能使用由请求体 `model` 生成的值。

### 4.4 独立 Publisher 维护 Envoy 资源

新增独立、无公网 API、权限受限的 `inference-gateway-publisher` 进程/Deployment。它属于 inference-service 业务边界，负责把已就绪的推理服务投影为 Envoy AI Gateway 资源，但不创建、停止或代理 vLLM。

Publisher 使用持久化的期望/已观察发布状态和服务 generation 做幂等调谐；Kubernetes 资源使用确定性名称和 owner labels，进程重启后可以继续收敛，不依赖内存状态。

## 5. 组件边界

| 组件 | 职责 | 不负责 |
|---|---|---|
| ANI Gateway | Services REST 控制面、用户登录态鉴权、推理服务和策略 CRUD 委托 | 代理 OpenAI 推理流量 |
| inference-service | 推理服务身份与生命周期、按租户和模型解析服务、访问策略、发布期望状态 | 校验 AK 明文、直接承载公网 OpenAI 请求 |
| `inference-gateway-publisher` | 调谐 Backend、AIServiceBackend、AIGatewayRoute；等待 Envoy conditions；回写发布结果 | 创建/停止 vLLM、执行 AK 校验、承载推理请求 |
| `envoy-authz-adapter` | Envoy ext_authz 转换；调用 auth-service；调用 inference-service 策略检查；注入可信路由头 | 保存 AK、查询数据库、代理 vLLM |
| auth-service | ANI AK 的唯一认证来源；哈希、撤销、过期、AK RPM、`api_key_id`、`tenant_id` | 推理服务解析、Envoy 资源、产品策略 |
| Envoy AI Gateway | OpenAI 请求解析、模型头提取、重新选路由、SSE 透传、平台保护限流 | ANI 控制面、AK 生命周期、业务策略存储 |
| vLLM | chat 或 embeddings 执行 | 鉴权、租户隔离、限流、外部暴露 |

Publisher 直接操作 Kubernetes API 是限定在 controller/adapter 边界内的 `bounded_direct` 行为。inference-service 的领域服务和 ANI Gateway handler 不导入 Kubernetes SDK。

## 6. Envoy 资源模型

### 6.1 共享资源

以下资源不随单个推理服务创建或删除：

- Gateway 与推理 listener；
- Envoy AI Gateway extension/filter；
- Gateway/listener 级 SecurityPolicy；
- `envoy-authz-adapter` Deployment、Service、NetworkPolicy；
- Gateway 级 Redis Global RateLimit/BackendTrafficPolicy；
- Publisher ServiceAccount、Deployment 和最小 RBAC。

共享 SecurityPolicy 必须先于任何动态推理路由存在，配置 `failOpen: false` 和 `recomputeRoute: true`。它不携带静态 `target_tenant_id` 或 `inference_service_id`。

### 6.2 每个推理服务的资源

Publisher 为每个已发布服务维护：

- 一个 `gateway.envoyproxy.io/Backend`，指向该服务的内部 ClusterIP/FQDN 和端口；
- 一个 `aigateway.envoyproxy.io/AIServiceBackend`，声明 OpenAI schema；
- 一个 `aigateway.envoyproxy.io/AIGatewayRoute`，绑定模型和可信内部身份；OpenAI 路径由 AI Gateway 端点处理和 inference-service 的访问检查共同约束。

资源使用服务 UUID 派生的确定性名称，不使用用户输入直接拼接 Kubernetes 名称。标签至少包含受管标记、tenant ID、inference service ID 和 generation，便于恢复、审计和垃圾回收。

产品访问策略不逐条发布为 BackendTrafficPolicy。Envoy 原生限流只承担共享平台保护；租户、服务和 AK 策略继续由 inference-service + Redis 执行。

## 7. 最终路由匹配

当前集群安装的 `AIGatewayRoute v1beta1` 在 `spec.rules[].matches` 中只提供 Header match，不提供 Path match。因此不能在 CR 中虚构精确路径字段。每个服务路由至少同时匹配：

1. `x-ai-eg-model == served_model_name`；
2. adapter 注入的 `x-ani-tenant-id == tenant_id`；
3. adapter 注入的 `x-ani-inference-service-id == service_id`。

有效分发键仍是 `(tenant_id, served_model_name, OpenAI path)`：AI Gateway 只处理受支持的 OpenAI 端点；adapter 把 `AttributeContext.Http.Path` 传给 inference-service；inference-service 在路由重算前强制校验任务与路径，路径不匹配返回 404，不能到达上游。

任务与路径映射：

| 服务任务 | 外部路径 |
|---|---|
| `generate` | `/v1/chat/completions` |
| `embed` | `/v1/embeddings` |

未知或遗留任务在发布前规范化；无法确认任务类型时拒绝发布，不能同时注册两条路径。

## 8. 数据面请求链路

客户端请求：

```bash
curl https://ai.example.com/v1/chat/completions \
  -H 'Authorization: Bearer <ANI_API_KEY>' \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "ani-qwen3",
    "messages": [{"role": "user", "content": "你好"}]
  }'
```

处理顺序：

1. Envoy AI Gateway 接收请求，从请求体提取 `model` 并强制覆盖内部 `x-ai-eg-model`；请求体是模型名的唯一权威来源。
2. Gateway 级 SecurityPolicy 调用 adapter 的标准 `envoy.service.auth.v3.Authorization/Check`。
3. adapter 只接受 `Authorization: Bearer ani_*`，调用现有 `auth.v1.AuthService/ValidateToken`。
4. auth-service 执行 AK 哈希查询、撤销/过期检查、AK RPM 和 `last_used_at` 更新，返回权威 `tenant_id` 与 `api_key_id`。
5. adapter 调用 inference-service 内部 `CheckInferenceAccess`，传入 `tenant_id`、`api_key_id`、`served_model_name`、OpenAI path、request ID 和 stream 标记。
6. inference-service 只在该 tenant 内解析活动且已发布的服务，校验任务路径，并执行 C42 访问策略、QPS/RPM 和并发 lease。
7. 成功响应返回可信 `inference_service_id`。adapter 覆盖客户端同名头，注入 `x-ani-tenant-id` 和 `x-ani-inference-service-id`。
8. Envoy 清空路由缓存并 `recomputeRoute`，命中该服务的最终 AIGatewayRoute。
9. 转发前删除 `Authorization`、`x-api-key`、所有 `x-ani-*` 身份头和不需要进入上游的内部头。
10. vLLM 返回普通 JSON 或 SSE；一次流式请求只在建立时鉴权一次。

### 8.1 并发 lease 生命周期限制

P0 的策略并发 lease 在授权允许时创建；标准 ext_authz 的 `Check` 仅位于上游请求开始前，既看不到普通响应结束，也看不到 SSE 流关闭。因此所有 Envoy 数据面请求均依赖策略服务的 lease TTL 保守释放，adapter 不立即、定时或猜测性释放，避免实际仍在执行的请求超卖并发额度。`lease_id` 仅保留在 adapter 内部 `AccessDecision`，不写入上游头、响应、日志或事件。P1 需增加 Envoy access log、ext_proc 或等价结束回调后，才可实现精确完成释放。

`CheckInferenceAccess` 的内部请求不再要求调用方提前知道 `inference_service_id`。服务 ID 是按 AK 租户和模型解析后的结果，不是客户端输入。

## 9. 认证、访问策略与限流

### 9.1 AK 规则

- 推理数据面只接受 ANI API Key，不接受浏览器登录 JWT。
- 登录 JWT 继续用于控制面创建 AK、推理服务和访问策略。
- 第一版不检查推理专用 scope。
- 缺失、格式错误、随机、撤销或过期 AK 返回 401。
- adapter 不保存、缓存或记录 AK 原文。

### 9.2 策略匹配

服务解析成功后，按 C42 已确认顺序选择策略：

1. `inference_service + api_key`；
2. `api_key`；
3. `inference_service`；
4. `tenant_default`；
5. 无自定义策略时使用系统默认允许。

同一层多个 enabled 策略按 `priority` 从小到大取第一条。disabled、deleted 或 tenant 不匹配的策略不参与。所有策略 key 只使用 AK ID，不使用 AK 原文或哈希。

### 9.3 三层保护

1. Envoy Gateway 共享 Global RateLimit 保护总入口；
2. auth-service 的 AK 自身 RPM 保护凭据；
3. inference-service 产品策略执行服务/AK QPS、RPM 和保守并发 lease。

任一强依赖不可用时返回 503。vLLM 不执行 ANI 限流，也不能在策略失败时被直接访问。

## 10. Publisher 配置

公网基址通过 Publisher Deployment 的直接环境变量提供：

```yaml
env:
  - name: INFERENCE_AI_GATEWAY_PUBLIC_BASE_URL
    value: https://ai.example.com
```

首版不为该单值增加 ConfigMap，也不允许创建推理服务时传入。后续 Helm/Kustomize 可以把 `env.value` 参数化。

Publisher 启动时校验：

- 必须是绝对 `http` 或 `https` URL；
- 生产配置必须使用 HTTPS；本地/真实门禁可以使用 HTTP 和显式端口；
- 禁止 userinfo、query 和 fragment；
- 规范化末尾 `/` 后再拼接固定 OpenAI 路径。

配置缺失或非法时 Publisher readiness 失败，不发布新路由。

生成结果：

```text
generate -> ${BASE_URL}/v1/chat/completions
embed    -> ${BASE_URL}/v1/embeddings
```

`endpoint_url` 继续保持 `null`，不得返回 ClusterIP。

## 11. 发布状态与持久化协调

Publisher 与 inference lifecycle 通过内部持久化发布期望状态协调，至少保存：

- `tenant_id`、`inference_service_id`；
- service generation；
- desired publication：published/unpublished；
- observed publication generation；
- phase：pending/publishing/published/unpublishing/failed；
- 最后一次脱敏错误和更新时间。

该状态可以由现有 inference repository 扩展承载；它不是新的公开 API 资源。写入必须使用 generation/CAS 或等价 fencing，旧操作不能覆盖新代次。

Kubernetes 对象是实际投影，不是业务期望状态的唯一来源。Publisher 重启后读取持久化期望状态并对照带 owner labels 的实际资源重新收敛。

## 12. 生命周期

### 12.1 创建或启动

```text
预留 (tenant_id, served_model_name)
  -> 创建/恢复 vLLM workload
  -> Pod Ready
  -> task 对应 chat/embeddings smoke 成功
  -> 写入 desired publication=published
  -> Publisher apply Backend/AIServiceBackend/AIGatewayRoute
  -> 等待各对象适用的当前 generation 状态条件
  -> 写入 observed publication=published
  -> 设置 invocation_url
```

Publisher 只有在以下条件同时满足后才能把该 generation 标记为已发布：共享 Gateway/listener 已 `Programmed=True`；AIGatewayRoute 的目标 Gateway parent 由预期 Envoy Gateway controller 接管，且该 route 当前 generation 的 `Accepted=True`、`ResolvedRefs=True`；Backend 和 AIServiceBackend 已满足其 CRD 实际提供的当前 generation 就绪条件。不能假设每类对象都暴露同一组 condition。

只有上述状态确认并完成最后一步后，服务才对数据面可解析。工作负载已运行但发布未完成时不能返回调用地址。

### 12.2 发布失败

- `invocation_url` 保持 `null`；
- 数据面解析不到该服务；
- 保留已运行工作负载并有界重试发布，避免网关瞬时故障导致模型重新下载；
- 超过重试窗口后记录 `GATEWAY_PUBLISH_FAILED`，但不得伪装为已发布；
- 不回退到其他租户或同名模型。

### 12.3 停止

```text
先从数据面解析中摘除
  -> desired publication=unpublished
  -> 删除 AIGatewayRoute
  -> 等待 Envoy 撤销当前路由
  -> 删除 AIServiceBackend/Backend
  -> observed publication=unpublished
  -> invocation_url=null
  -> 停止 workload
```

如果路由撤销未确认，停止操作保持 `stopping` 并重试，不能先关闭 vLLM 留下指向死亡后端的路由。stopped 服务继续保留 `served_model_name`，数据面返回 404。

### 12.4 删除

删除复用停止顺序；只有路由和工作负载均确认删除、数据库软删除完成后，才释放 `(tenant_id, served_model_name)`。

### 12.5 伸缩、重启和代次更新

- 仅副本伸缩且 ClusterIP Service 身份不变时不重建路由；
- 重启按撤销后重新发布处理；
- 后端身份变化时先创建并验证新 generation，再切换路由、排空并删除旧资源；
- 旧 generation 的迟到调谐不得覆盖新 generation。

## 13. 错误语义

| 场景 | HTTP | 稳定语义 |
|---|---:|---|
| 缺少、无效、撤销或过期 AK | 401 | `UNAUTHORIZED` |
| 登录 JWT 或非 ANI AK 调用数据面 | 401 | `UNAUTHORIZED` |
| 同租户策略拒绝 | 403 | `INFERENCE_ACCESS_DENIED` |
| 当前租户没有该模型、服务未发布、已停止、正在删除或路径不匹配 | 404 | `NOT_FOUND` |
| AK、策略 QPS/RPM 或并发超限 | 429 | 对应 rate/concurrency reason |
| auth-service、策略服务、Redis 或 adapter 不可用 | 503 | fail closed |
| 路由存在但 vLLM 不可用 | 502/503/504 | 保持 Envoy 上游错误语义 |

跨租户场景始终折叠为 404，不泄露其他租户是否存在同名模型。

## 14. 安全边界

1. Gateway/listener 级 SecurityPolicy 必须先于动态路由存在。
2. `failOpen` 必须为 false；依赖失败不能绕过。
3. 客户端 `x-ani-tenant-id`、`x-ani-inference-service-id`、`x-ani-user-id` 和 `x-ai-eg-model` 不作为权威身份；内部身份头由 adapter 覆盖，`x-ai-eg-model` 由 AI Gateway 根据请求体提取并强制覆盖客户端值。
4. vLLM 不接收 Authorization、AK、用户身份或策略详情。
5. vLLM 保持 ClusterIP，NetworkPolicy 只允许受管 Envoy 数据面访问推理端口。
6. Publisher RBAC 只允许目标 namespace 内的 Backend、AIServiceBackend、AIGatewayRoute 及必要只读 Gateway/状态操作，不允许管理任意 workload、Secret 或集群级资源。
7. 日志、事件和 evidence 不得包含 AK 原文、Authorization、prompt、completion、embedding 输入或向量。
8. 任何部分 apply、删除超时或 condition 过期都不能标记发布成功。

## 15. 可观测性

至少记录和聚合：

- Envoy 请求量、状态码、路由服务、上游延迟、SSE 时长和上游错误；
- adapter 的认证/策略决策类别、依赖延迟和错误；
- Publisher 的调谐次数、期望/观察 generation、phase、condition 和失败原因；
- 策略 403/429/503、policy ID、服务 ID、AK prefix 和 request ID；
- 不以完整模型输入、AK 或高基数动态内容作为指标标签。

## 16. 验收矩阵

### 16.1 本地与静态门禁

1. Publisher URL 配置、确定性资源命名、任务路径映射和 generation fencing 单测通过。
2. Envoy manifest/CRD schema validator 覆盖共享 SecurityPolicy、`recomputeRoute`、Backend、AIServiceBackend 和 AIGatewayRoute。
3. adapter 覆盖 AK-only、头覆盖、服务解析、allow/401/403/404/429/503 转换。
4. inference-service 覆盖 `(tenant_id, served_model_name, path)` 解析和策略匹配。
5. 生命周期覆盖 publish、重复 reconcile、部分失败、unpublish、restart 和旧 generation 迟到。
6. `make validate-services`、`make test`、`make validate-architecture`、`git diff --check` 通过。

### 16.2 真实 Envoy AI Gateway 门禁

1. 新建 generate 服务在工作负载与 Envoy conditions 就绪后自动出现 `invocation_url`，AK chat 请求成功并验证输入输出。
2. 新建 embed 服务自动发布，AK embeddings 请求成功并返回非空向量。
3. A、B 租户均创建 `ani-qwen3` 时，各自 AK 精确进入各自 vLLM。
4. B 没有目标模型时，B AK 请求返回 404，不能到达 A 的 vLLM。
5. generate 模型请求 embeddings、embed 模型请求 chat 均返回 404。
6. 无 AK、随机 AK、登录 JWT、撤销 AK 和过期 AK 均返回 401，vLLM 请求计数不增加。
7. 伪造内部 tenant/service/model 头不能改变解析结果或跨租户；请求体 `model=A`、客户端头 `x-ai-eg-model=B` 时必须仍按 A 解析。
8. Gateway 全局、AK RPM、服务、AK、服务+AK 策略分别返回预期结果和 `Retry-After`。
9. adapter、auth-service、inference-service、Redis 任一强依赖故障均返回 503，不能到达 vLLM。
10. stop 先撤路由后停 workload，随后请求返回 404；start 完成后重新可用。
11. delete 不留下受管 Envoy 资源，完成后同租户可以重新使用模型名。
12. Publisher 重启、重复事件和部分 apply 不产生重复或悬挂资源。
13. 外部不能绕过 Gateway 访问 ClusterIP。
14. `/v1/models` 不返回跨租户或全局模型列表。
15. 日志、事件、Kubernetes Secret 和 evidence 的敏感信息扫描通过。

真实门禁必须同时覆盖普通 JSON 和 SSE；通过前只能声明 local/logic verified，不能声明 C41 runtime ready。

## 17. 兼容性与公开契约

本设计不新增 Services v1 字段或端点。现有字段语义为：

- `served_model_name`：OpenAI 请求体 `model`，租户活动服务内唯一，创建后不可变；
- `invocation_url`：仅在工作负载与 Envoy 路由发布成功后返回；
- `endpoint_url`：兼容保留并继续返回 `null`。

内部 gRPC 将服务解析输入收敛为 `tenant_id + served_model_name + openai_path`，并返回可信 service ID。该调整属于内部实现契约，不要求客户端新增请求字段。

## 18. 回滚

Publisher 只管理带明确 owner labels 的 C41 资源。回滚顺序：

1. 禁止新服务进入发布期望状态；
2. 对已发布服务逐个撤销 C41 路由并确认 Envoy 收敛；
3. 清理对应 AIServiceBackend/Backend；
4. 将 `invocation_url` 置空；
5. 保留推理工作负载和 ANI 控制面数据。

不得通过恢复 Secret 型静态 APIKeyAuth 将其重新定义为正式方案。C40 静态资源只可用于受控诊断，并必须与动态资源使用不同名称和明确隔离。

## 19. 实施边界

实施计划应拆成可独立验证的切片：

1. 内部发布状态、服务解析和 Publisher 配置契约；
2. Publisher Kubernetes adapter 与确定性资源渲染；
3. Gateway 级 SecurityPolicy、可信头和 `recomputeRoute`；
4. adapter 与 `CheckInferenceAccess` 的认证后解析；
5. 生命周期 publish/unpublish 协调和 `invocation_url`；
6. 本地门禁、服务端 dry-run 和真实多租户 chat/embeddings live gate。

设计文档批准并形成实施计划前，不开始以上实现。

## 20. 官方依据

- Envoy AI Gateway API：请求体模型提取和 `x-ai-eg-model` 路由：<https://aigateway.envoyproxy.io/docs/latest/api/>
- Envoy AI Gateway 内部 API：`x-ai-eg-model` 由请求体模型名自动填充并覆盖终端用户值：<https://pkg.go.dev/github.com/envoyproxy/ai-gateway@v1.0.0/internal/internalapi>
- Envoy AI Gateway 支持的 OpenAI 端点：<https://aigateway.envoyproxy.io/docs/capabilities/llm-integrations/supported-endpoints/>
- Envoy Gateway v1.8 ext_authz：<https://gateway.envoyproxy.io/v1.8/tasks/security/ext-auth/>
- Envoy Gateway 扩展 API `recomputeRoute`：<https://gateway.envoyproxy.io/v1.8/api/extension_types/>
