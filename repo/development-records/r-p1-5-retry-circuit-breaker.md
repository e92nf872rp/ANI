# R-P1-5 · Retry / Circuit Breaker Foundation

> 记录类型：Execution / Sprint 14 Core resilience
> 完成日期：2026-06-23
> 分支：`feature/sprint14-core-resilience-semantics`
> 状态：local/logic verified；未执行真实后端 kill、network partition 或 soak；不声明 production ready
> 后续补齐：`SPRINT14-CORE-RESILIENCE-LIVE-GATE` 已覆盖真实 backend kill/degradation/controller failover；命名 circuit breaker 的持续故障注入与 soak 仍未完成。

## 范围

本批在 R-P0-3 的 `pkg/adapters/resilience` 超时骨架上补齐操作级 retry、retryable error classification 与命名 circuit breaker，并优先接到最高杠杆的 Kubernetes REST client 幂等调用路径：

- `resilience.Retryable(err)` 可识别 `context.DeadlineExceeded`、网络连接类错误、HTTP `429` 与 `5xx` 状态错误。
- `resilience.Do(ctx, Policy, fn)` 支持 `MaxAttempts`、指数退避、`BreakerName`、`FailureRatio`、`MinRequests` 与 `CooldownPeriod`。
- Kubernetes REST client 修正非 2xx 错误分类：`400` 等调用方错误仍包 `ports.ErrInvalid`；`429/5xx` 返回可重试状态错误，不再伪装成 invalid request。
- Kubernetes REST client 新增 `RetryPolicy`，仅 `Health`、观察类 GET 与 server-side dry-run 这类幂等/无副作用路径使用；真实 `Apply` 写路径即使配置 retry policy 也不重试。
- 新增 `make validate-resilience-faultinjection-live-gate`，当前只包装本地逻辑测试；名称沿 Sprint14 计划，输出明确说明未执行真实故障注入。

## 非范围 / 未完成

- MinIO、Milvus 尚未接入命名 circuit breaker policy；R-P2-7 已补充 endpoint list fallback，本地覆盖网络错误、`429`、`5xx` 后尝试下一个 endpoint。
- 本批未执行真实 MinIO/Milvus/Kubernetes API 后端 kill、network partition、持续 5xx 注入或 half-open live probe；后续 aggregate live gate 补齐的是 backend kill/degradation/controller failover，不等同于命名 circuit breaker soak。
- 未声明 full platform production ready；本批只证明共享逻辑和 Kubernetes REST 幂等路径本地可复跑。

## 验证

已通过：

```bash
go test ./pkg/adapters/resilience ./pkg/adapters/runtime
make validate-resilience-faultinjection-live-gate
```

批次收口还需与本分支常规门禁一起执行：

```bash
make test
make validate-architecture
make validate-doc-entrypoints
git diff --check
```

## 关键边界

- Retry 只由 adapter policy 决定，不进入 `ports` 产品语义。
- Kubernetes 写路径不自动 retry；HTTP 层幂等重放由 R-P0-2 gateway middleware 兜底。
- `validate-resilience-faultinjection-live-gate` 目前是 local/logic gate，不是 production-shaped fault-injection evidence。
