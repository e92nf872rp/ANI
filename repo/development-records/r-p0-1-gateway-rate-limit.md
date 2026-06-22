# R-P0-1 Gateway Rate Limit

> 记录类型：Sprint 14 execution batch / Core resilience P0
> 完成日期：2026-06-23
> 范围：ANI Core / ani-gateway middleware rate limiting

## 目标

替换 `services/ani-gateway/internal/middleware/ratelimit.go` 中的恒 true 限流桩，基于 R-P0-0 引入的 gateway shared store 对非公共路径执行 per-tenant + method + route-class 窗口计数限流。超过阈值时返回 `429 RATE_LIMIT_EXCEEDED`。

## 实现

- 修改 `RateLimit()` 为 `RateLimit(store GatewayStore)`：
  - 公共路径与缺失 tenant context 的请求继续放行。
  - 通过 `GatewayStore.Increment(ctx,key,ttl)` 维护窗口计数。
  - 默认限制为 `100` requests / `1s`。
  - 可通过 `GATEWAY_RATE_LIMIT_REQUESTS` 和 `GATEWAY_RATE_LIMIT_WINDOW` 覆盖。
- 修改 `middleware.Register(h, store)`：
  - 将 R-P0-0 注入的 shared store 继续传入 `RateLimit(store)`。
- 新增 `ratelimit_test.go`：
  - `TestRateLimitRejectsOverQuotaAndRecoversAfterWindow` 覆盖超限返回 429 与窗口恢复放行。
- 新增 `make validate-gateway-ratelimit`：
  - 固定 R-P0-1 本地逻辑 gate。

## 边界

- 本批不改 Core OpenAPI，不改 Services OpenAPI，不新增 Services 逻辑。
- 本批使用 shared store 窗口计数，不声明完整分布式 token bucket 或生产压测结果。
- 本批为 local/logic verified；未执行真实 Redis 压测、故障注入或 live gate，不声明 production ready。

## TDD 证据

红灯：

```bash
GOCACHE=/private/tmp/ani-go-build GOMODCACHE=/Users/zhangfan/ANI/repo/.cache/gomod go test ./services/ani-gateway/internal/middleware -run RateLimit -v
```

结果：失败，`RateLimit` 尚未接收 shared store。

绿灯：

```bash
GOCACHE=/private/tmp/ani-go-build GOMODCACHE=/Users/zhangfan/ANI/repo/.cache/gomod go test ./services/ani-gateway/internal/middleware -run RateLimit -v
make validate-gateway-ratelimit
```

结果：以上命令通过。

## 收口验证

```bash
GOCACHE=/private/tmp/ani-go-build GOMODCACHE=/Users/zhangfan/ANI/repo/.cache/gomod go test ./services/ani-gateway/... -run 'TestRateLimit|TestGatewayStore|TestAuthPublicPaths|TestAuthProtectedPaths|TestInferPermission' -v
make validate-gateway-ratelimit
make validate-architecture
make validate-doc-entrypoints
make test
git diff --check
```

结果：以上命令通过。

## 后续

- R-P0-2 可复用同一个 `GatewayStore` 注入模式实现幂等响应重放。
- R-P0-3/R-P1-5 后续再补每调用超时、重试与断路；本批不扩大这些范围。
