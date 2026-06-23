# R-P2-7 · Multi-Endpoint Failover Config

> 记录类型：Execution / Sprint 14 Core resilience
> 完成日期：2026-06-23
> 分支：`feature/sprint14-core-resilience-semantics`
> 状态：local/logic verified；未执行真实 primary kill / HA topology failover；不声明 failover production ready
> 后续补齐：`SPRINT14-CORE-RESILIENCE-LIVE-GATE` 已覆盖 controller primary pod delete / follower lease failover；PG 读副本、Redis/Postgres/MinIO/Milvus 后端自身 HA 拓扑仍未完成。

## 范围

本批把 Sprint 14 F7 的“全线单 endpoint”推进到可配置多端点形态：

- Redis bootstrap 改为 `redis.UniversalClient`，新增 `RedisConfig`，支持：
  - 向后兼容单 `REDIS_URL`。
  - `REDIS_MODE=sentinel` + `REDIS_ADDRS` + `REDIS_MASTER_NAME`。
  - `REDIS_MODE=cluster` + 多个 `REDIS_ADDRS`。
  - gateway 侧支持 `GATEWAY_REDIS_*` 覆盖，未设置时继续使用既有默认 `REDIS_URL`。
- MinIO object-store adapter 新增 `Endpoints []string`，接受单 endpoint、endpoint list 或 LB/VIP；对网络错误、`429`、`5xx` 会在 endpoint list 内尝试下一个 endpoint。
- Milvus vector-store adapter 新增 `Endpoints []string`，接受单 endpoint、endpoint list 或 LB/VIP；对网络错误、`429`、`5xx` 会在 endpoint list 内尝试下一个 endpoint。
- `bootstrap.Config`、Gateway storage/vector runtime env 增加 `OBJECT_STORE_ENDPOINTS`、`VECTOR_STORE_ENDPOINTS` 接线。
- 新增 `make validate-ha-failover-live-gate`，当前包装本地配置解析、装配与 endpoint fallback 测试；命名沿用计划里的 gate 名称，单批次输出仍明确标注这是 local gate、不是 live 拓扑演练；后续 Sprint14 aggregate live gate 已补齐 controller primary kill / follower failover。

## 非范围 / 未完成

- MinIO/Milvus endpoint fallback 仅为本地逻辑验证：覆盖网络错误、`429`、`5xx` 后尝试下一个 endpoint；未证明真实集群主备切换、数据一致性或 DNS/VIP 行为。
- 未实现 PostgreSQL 读副本路由；现阶段 `DatabaseURL` 仍是单 URL，只能由外部 VIP / proxy 提供 HA。
- 本批未执行 Redis Sentinel/Cluster、MinIO、Milvus 或 PG 的真实主节点 kill / 网络分区 / failover 演练；后续 aggregate live gate 补齐的是 controller primary pod delete / follower lease failover。
- 已由 Sprint14 aggregate live gate 产出 controller failover evidence JSON；不得把它外推为 Redis/Postgres/MinIO/Milvus 后端自身 HA production ready。

## 验证

已通过本地/逻辑门禁：

```bash
go test ./pkg/bootstrap ./pkg/adapters/redis ./pkg/adapters/objectstore ./pkg/adapters/vectorstore ./services/ani-gateway
make validate-ha-failover-live-gate
```

关键新增测试：

- `TestMinIOHealthFailsOverEndpointList`
- `TestMilvusHealthFailsOverEndpointList`

批次收口还需与本分支常规门禁一起执行：

```bash
make test
make validate-architecture
make validate-doc-entrypoints
git diff --check
```
