# INFERENCE-SERVICE-JWT-C7

> 日期：2026-08-15
> 状态：local/logic verified
> 方案依据：Core v1 `BearerAuth` / `x-ani-principal-kind=service`，`inference-service-design.md` 跨层 service-only
> 前置：Core PR #99、`INFERENCE-PLATFORM-WORKLOAD-LOCAL-C5`、`INFERENCE-SERVICE-MODEL-CATALOG-C6`

## 完成范围

- auth-service 签发短期 service JWT：`aud=ani-core`、`principal_kind=service`、`tid`、`scope:platform-workloads:read|write`。默认 TTL 5 分钟，上限 1 小时。
- 内部 gRPC `IssueServiceToken` 只允许 `caller_service=inference-service`，密钥与 `AUTH_SERVICE_MINT_CREDENTIALS` 做常量时间比较。未改 OpenAPI，Gateway 不发布该 RPC。
- 既有租户/平台 JWT 无 `aud` 仍可校验；伪造 `principal_kind=service` 但缺 `aud` 或 scope 会被拒绝。
- Gateway `ANI_AUTH_MODE!=dev` 时：service JWT 才能进 `/platform-workloads*`；租户 JWT / API key 在中间件即 403。RBAC 对 service principal 不再走用户角色 CheckPermission。
- inference-service 在 `AUTH_SERVICE_GRPC_ADDR` + `AUTH_SERVICE_MINT_SECRET` 时按租户 mint 并只带 `Authorization: Bearer`；静态非 JWT `CORE_SERVICE_TOKEN` 仍走 C5 的 `X-Dev-*` 本地回退。

## Design Decisions

- 令牌必须带租户，因此不能用一个全局静态 JWT 服务所有租户。
- 签发留在 Auth 控制面内部 gRPC，不新增租户 HTTP。
- 真实 K8s/LWS/vLLM live gate 仍单独做，本批次只补齐契约要求的服务身份。

## Deviations

- 无 JWKS 轮换、无 mint 审计日志、无 KMS。
- `ANI_AUTH_MODE=dev` 仍可用 `X-Dev-*`。
- 无 CPU single-node 真实 provider / live evidence。

## 验证证据

```text
cd repo/services/auth-service && GOWORK=off go test -count=1 ./internal/service/ -run 'ServiceToken|JWTValidator'
cd repo && go test ./services/ani-gateway/internal/middleware/ -count=1 -run 'ServiceJWT|PlatformLogin_TenantIsolation|AuthPublic'
cd repo/services/inference-service && GOWORK=off go test -race ./internal/runtime/coresdk/ -count=1
PATH=/tmp/ani-pybin:$PATH make validate-architecture
PATH=/tmp/ani-pybin:$PATH make validate-doc-entrypoints
git diff --check
```

## 明确未完成

- 无真实 K8s/LWS/Volcano/vLLM、无 PG PlatformWorkload store、无推理 live evidence。
- 不得标记 control-plane ready、runtime ready 或 production ready。

## 下一批次边界

CPU single-node PlatformWorkload 真实 provider / live gate。
