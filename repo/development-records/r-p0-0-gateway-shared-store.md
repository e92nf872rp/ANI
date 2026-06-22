# R-P0-0 Gateway Shared Store

> 记录类型：Sprint 14 execution batch / Core resilience prerequisite
> 完成日期：2026-06-23
> 范围：ANI Core / ani-gateway middleware shared store

## 目标

为 Sprint 14 R-P0-1 限流和 R-P0-2 幂等重放提供 gateway 共享存储前置能力。gateway middleware 运行时接收 `ports.CacheStore`（别名为 `GatewayStore`），支持 `Set/Get/Delete/Increment/Exists/SetNX`，其中 `SetNX` 用于后续幂等 in-progress 哨兵。

## 实现

- 新增 `services/ani-gateway/internal/middleware/store.go`：
  - `GatewayStore` 作为 `ports.CacheStore` 的 gateway 侧别名，middleware 不直接 import Redis SDK。
- 修改 `pkg/ports/cache_store.go` 与 `pkg/adapters/redis/cache_store.go`：
  - `ports.CacheStore` 增加 `SetNX`。
  - Redis adapter 实现 `SetNX`。
- 修改 `pkg/bootstrap/redis.go`：
  - 新增 `ConnectRedisCacheStore(redisURL)`，在允许的 bootstrap/adapter 边界内构造 Redis-backed cache store。
- 修改 `services/ani-gateway/main.go`：
  - 通过 `GATEWAY_REDIS_URL` / `REDIS_URL` / 本地默认 Redis URL 构造 gateway shared store。
  - 将 store 注入 `middleware.Register`。
- 修改 `services/ani-gateway/internal/middleware/chain.go`：
  - `Register(h, store)` 显式要求 shared store。

## 边界

- 本批只建立共享 store 前置能力，不实现限流令牌桶，也不实现幂等响应重放。
- 不改 Core OpenAPI，不改 Services OpenAPI，不新增 Services 逻辑。
- 本批为 local/logic verified；不声明 production ready。

## 验证

```bash
GOCACHE=/private/tmp/ani-go-build GOMODCACHE=/Users/zhangfan/ANI/repo/.cache/gomod go test ./services/ani-gateway/internal/middleware -run Store -v
GOCACHE=/private/tmp/ani-go-build GOMODCACHE=/Users/zhangfan/ANI/repo/.cache/gomod go test ./services/ani-gateway/... -run 'TestGatewayStore|TestAuthPublicPaths|TestAuthProtectedPaths|TestInferPermission' -v
GOCACHE=/private/tmp/ani-go-build GOMODCACHE=/Users/zhangfan/ANI/repo/.cache/gomod go build -o /private/tmp/ani-gateway-rp0-0 ./services/ani-gateway
make validate-architecture
```

结果：以上命令通过。
