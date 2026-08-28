# 推理服务限流最小闭环设计

> 日期：2026-08-28  
> 范围：ANI Services 推理服务控制面与 ANI Gateway 数据面  
> 阶段：第一版最小闭环

## 1. 目标与范围

第一版围绕 C40 真实数据面完成限流闭环：客户端请求进入 Envoy AI Gateway，由现有 ext_authz 链路调用 auth-service 的 AK RPM 限流，并在 Envoy 路由层增加共享的全局保护限流。ANI Gateway 的 `/v1` 不参与推理流量。

本轮包含：

- 保持 C40 现有 `/v1/chat/completions`、`/v1/embeddings` Envoy AI Gateway 路由；
- 复用 auth-service 对 `ani_*` AK 的每分钟请求限制；
- 为受管 AIGatewayRoute 增加 Envoy `BackendTrafficPolicy` 全局路由保护限流；
- 校验限流策略只作用于 Envoy 数据面，不注册 ANI Gateway `/v1` handler；
- 验证 401/404/429/503 语义、SSE 透传和 vLLM 不可绕过。

本轮不包含：推理访问策略 CRUD、动态按租户/模型配置、计费、用量结算、AK 签发规则调整、`max_concurrency` 执行、队列排队或复杂并发租约。动态业务策略需要后续独立 Rate Limit Service 或 Envoy 动态配置方案。

## 2. 现状与约束

- Services 契约位于 `repo/api/openapi/services/v1.yaml`，访问策略契约已存在但本轮不启用其动态执行。
- Envoy AI Gateway 是唯一推理数据面；ANI Gateway 的 `inferenceProxy` 仍是旧控制面仓库中的占位，不得接入此限流链路。
- auth-service `ValidateToken` 已对 API Key 执行哈希校验、撤销/过期检查、AK RPM 限流和 `last_used_at` 更新；adapter 只做协议转换和租户比对。
- Envoy Gateway 的 Global RateLimit 通过 `BackendTrafficPolicy` 和 Redis 共享，策略按路由生效；Local RateLimit 不作为跨副本业务限流。
- 所有新行为必须保持 `tenant_id` first 的查询与 RLS 边界，不能通过请求路径中的 ID 绕过租户校验。
- envoy-authz-adapter 负责提取 Bearer/API Key 并调用 auth-service；限流不复制 AK 校验或登录流程。

## 3. 方案与组件

### 3.1 Envoy 数据面限流

第一版不在 ANI Gateway 或 inference-service 增加策略 enforcement，而是在 C40 的受管 AIGatewayRoute 上挂载 `gateway.envoyproxy.io/v1alpha1` `BackendTrafficPolicy`：

1. `global.rules` 提供按路由的共享保护上限，覆盖 chat 和 embeddings 两条已注册路径；
2. Envoy Gateway 使用 Redis 作为 Global RateLimit 后端，多个 Envoy 副本共享窗口；
3. C40 的业务身份限流仍由 auth-service AK RPM 执行，Envoy 路由保护限流只负责整体防护；
4. 任何 429 都在 Envoy/adapter 层产生，vLLM 不实现限流；
5. 动态的每租户/每模型额度不在本轮伪装成静态 CRD，后续应单独建设 Rate Limit Service 或动态发布器。

## 4. 请求与错误语义

本轮不改变 Services OpenAPI 契约，不实现 `/inference-policies` 控制面；限流配置通过受管 Envoy `BackendTrafficPolicy` 清单和现有 auth-service AK 配置生效。

数据面结果：

| 场景 | HTTP | code |
|---|---:|---|
| ext_authz 允许且 Envoy 计数未超限 | 继续转发 | — |
| AK 被 auth-service 拒绝 | 401/404 | 现有 C40 ext_authz 语义 |
| AK RPM 或 Envoy Global RateLimit 超限 | 429 | `RATE_LIMIT_EXCEEDED` |
| adapter/auth-service/Envoy RateLimit backend 不可用 | 503 | `RATE_LIMIT_UNAVAILABLE` |
| 无效/过期/撤销 AK | 401 | 现有 auth 错误语义 |

审计记录至少保留：租户、推理服务、请求类型、AK ID/前缀（不保存原始 key）、authorized/rate_limited/dependency_failed 决策、命中维度和 `retry_after_seconds`。原始凭证永不写日志、数据库或响应。

## 5. 测试与验收

### 配置与 adapter

- C40 路由的 `BackendTrafficPolicy` 和 EnvoyGateway Redis 配置可通过服务端 dry-run。
- adapter 对 auth-service `ResourceExhausted` 映射为 429、依赖不可达映射为 503，并保持租户不匹配 404。
- `Authorization: Bearer ani_*` 才能进入限流链路；`x-api-key`、Cookie、查询参数不能授权。

### Envoy 数据面

- chat 和 embeddings 均命中受管 AIGatewayRoute 的 Global RateLimit；超限返回 429，不到达 vLLM。
- 同一 AK 的 RPM 超限经 ext_authz 返回 429；跨 AK/跨租户不泄漏目标服务。
- Envoy RateLimit backend 或 adapter/auth-service 不可用时返回 503，fail closed。
- 非流式响应和 SSE 流式响应均能透传；一次 SSE 请求只授权一次。

### 必跑门禁

```bash
cd repo
make validate-services
make test
make validate-architecture
git diff --check
```

并补充 Gateway 限流专项测试和 inference-service 策略专项测试；提交前确保生成 SDK/API 文档无漂移、工作树干净。

## 6. 成功标准

当以下条件全部满足时，本轮完成：

1. C40 受管 Envoy 路由配置 Global RateLimit 并能通过真实 Gateway 生效；
2. auth-service 对 AK 的 RPM 限流和 Envoy 路由保护限流均能对 chat/embeddings 返回 429；
3. 429、403、503、401 错误语义与审计字段稳定；
4. 多租户、跨 AK、SSE、窗口恢复和故障 fail-closed 测试通过；
5. C40 manifest validator、Envoy/adapter live gate 和现有 Services/架构门禁全部通过。
