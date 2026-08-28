# 推理服务限流最小闭环设计

> 日期：2026-08-28  
> 范围：ANI Services 推理服务控制面与 ANI Gateway 数据面  
> 阶段：第一版最小闭环

## 1. 目标与范围

第一版把现有推理服务访问策略契约从 `501 FEATURE_NOT_AVAILABLE` 提升为可用的后端闭环：租户管理员能够为指定推理服务配置限流与 AK 访问范围，推理请求进入 ANI Gateway 后按策略执行，超限请求稳定返回 429。

本轮包含：

- 实现已有 `PUT /api/v1/svc/inference-services/{service_id}/policies`；
- 按租户隔离保存和读取策略；
- 支持 `rate_limits.qps` 与 `rate_limits.rpm`，同时配置时两者均必须通过；
- 支持 `allow_api_key_ids` 与 `deny_api_key_ids`；
- 在 `/v1/chat/completions` 与 `/v1/embeddings` 请求路径执行策略；
- 复用现有 AK 鉴权、租户注入、审计和 Redis 共享计数能力；
- 保证 POST/PUT 幂等和重试语义。

本轮不包含：计费、用量结算、AK 签发规则调整、独立限流资源、`max_concurrency` 执行、队列排队或复杂并发租约。

## 2. 现状与约束

- Services 契约位于 `repo/api/openapi/services/v1.yaml`，策略资源已存在，不能回流 Core API。
- Gateway 现有 `RateLimit(store)` 已提供共享窗口计数和 429/503 错误语义，但当前按通用路由和主体限流，不能读取推理服务策略。
- Gateway 必须通过 ANI Services API/SDK 或稳定的服务端口调用推理服务能力，不直接访问数据库表。
- 所有新行为必须保持 `tenant_id` first 的查询与 RLS 边界，不能通过请求路径中的 ID 绕过租户校验。
- 现有 auth middleware 负责解析 Bearer/API Key、注入主体和 `api_key_id`；限流只消费其结果，不重复校验凭证。

## 3. 方案与组件

### 3.1 控制面

在 inference-service 中增加策略存储与服务逻辑：

1. 校验 `service_id` 属于当前租户且未删除；不存在或跨租户统一映射为 404。
2. 校验策略字段：`qps/rpm` 至少一个存在时必须为正数；允许列表和拒绝列表不能包含重复 ID；同一 AK 同时命中 deny 和 allow 时 deny 优先。
3. 以 `idempotency_key` 对 PUT 做请求指纹约束：相同 key + 相同请求重放原结果；相同 key + 不同请求返回幂等冲突。
4. 使用事务完成策略替换，更新版本/时间戳，并写入可供 Gateway 读取的公开策略视图；不返回 secret 或原始 AK。

策略按 `(tenant_id, inference_service_id)` 唯一。第一版采用“单个服务一份当前策略”的替换语义，避免并行规则合并造成优先级歧义。

### 3.2 数据面

ANI Gateway 在已有认证和路由解析之后增加推理策略 enforcement：

1. 从已认证请求上下文取得 `tenant_id`、主体标识、`api_key_id`（若有）和外部模型名；通过推理服务策略读取端口获得当前启用策略。
2. 仅对 chat/completions 和 embeddings 数据面请求执行；管理 API、健康检查和公共路径不进入该策略。
3. AK 访问范围检查先于计数：命中 deny 或不在非空 allow 列表时返回 403，不消耗限流计数。
4. `qps` 使用 1 秒 Redis 原子窗口；`rpm` 使用 1 分钟 Redis 原子窗口。计数键必须包含租户、AK/主体、推理服务、请求类型和窗口，避免不同租户或模型互相污染。
5. 任一配置的窗口超过阈值即返回 429，并设置不超过对应窗口的 `Retry-After`；未配置的维度不参与判断。
6. Redis 或策略读取失败时默认 fail-closed，返回 `503 RATE_LIMIT_UNAVAILABLE`，不得静默放行。

建议键格式：

```text
inference_rl:v1:{tenant_id}:{principal_key}:{inference_service_id}:chat:qps:{epoch_second}
inference_rl:v1:{tenant_id}:{principal_key}:{inference_service_id}:chat:rpm:{epoch_minute}
```

其中 `principal_key` 优先使用 `api_key_id`，无 AK 的受信主体使用稳定主体 ID；不得使用原始 AK 值。

## 4. 请求与错误语义

策略更新继续使用现有契约的 `PUT /inference-services/{service_id}/policies`，成功返回 `200 + InferenceService`，并要求 `idempotency_key`。

数据面结果：

| 场景 | HTTP | code |
|---|---:|---|
| 策略允许且计数未超限 | 继续转发 | — |
| AK 被 deny 或不满足 allow | 403 | `FORBIDDEN` |
| QPS/RPM 超限 | 429 | `RATE_LIMIT_EXCEEDED` |
| 策略或 Redis 不可用 | 503 | `RATE_LIMIT_UNAVAILABLE` |
| 无效/过期/撤销 AK | 401 | 现有 auth 错误语义 |

审计记录至少保留：租户、推理服务、请求类型、AK ID/前缀（不保存原始 key）、allow/deny/rate_limited/policy_unavailable 决策、命中维度和 `retry_after_seconds`。原始凭证永不写日志、数据库或响应。

## 5. 测试与验收

### 控制面

- 租户内 PUT 创建/替换策略成功；跨租户 service 返回 404；禁用策略不执行；字段边界校验稳定。
- 相同幂等键重放返回相同结果；同键不同请求返回幂等冲突。
- allow/deny 冲突时 deny 优先，重复 ID 和非法 UUID 被拒绝。

### 数据面

- chat 和 embeddings 分别验证 qps、rpm；未配置维度不限制。
- 同一 AK 超限返回 429，换 AK 或换租户不共享计数；窗口过期后恢复。
- deny/allow 失败返回 403 且不增加 Redis 计数。
- 策略读取失败、Redis 失败均返回 503；无策略的兼容行为按明确默认策略测试。
- 带有效 AK 的请求响应体和流式/非流式响应不被限流中间件改写。

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

1. `/policies` 不再返回 501，策略能按租户持久化并幂等更新；
2. Gateway 对 chat/embeddings 按服务策略执行 qps/rpm 和 AK allow/deny；
3. 429、403、503、401 错误语义与审计字段稳定；
4. 多租户、跨 AK、窗口恢复和故障 fail-closed 测试通过；
5. Services、架构、OpenAPI、SDK 生成物和 CI 门禁全部通过。
