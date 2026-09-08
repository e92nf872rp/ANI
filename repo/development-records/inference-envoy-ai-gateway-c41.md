# INFERENCE-SERVICE-C41 — Envoy AI Gateway 多租户动态发布

完成日期：2026-09-01
对应 Sprint：Sprint 13
批次：`INFERENCE-SERVICE-C41`
Readiness：`local/logic verified`；不是 runtime ready 或 production ready。

## 契约与范围

Services v1 **没有新增 endpoint 或 field**。`served_model_name`、`invocation_url` 和 create operation 的文字说明已澄清：前者是 OpenAI 请求体 `model`、在活动租户服务内唯一；后者只在工作负载和 Gateway publication 都成功后返回。Gateway 的既有 policy flat DTO 实现已修正以对齐该既有契约；这不是公开契约新增。

本批次为同一公网 Envoy AI Gateway 上的租户感知动态发布提供本地/逻辑闭环：AK-only ext_authz、认证后按 `(tenant_id, served_model_name, OpenAI path)` 解析、可信头覆盖和 `recomputeRoute`、按 generation fencing 且持续续租的 Publisher、发布/撤销先于 lifecycle runtime 操作，以及最小 RBAC/NetworkPolicy。标准 ext_authz 在 P0 没有响应结束回调，普通 JSON 和 SSE 的并发 lease 由 Redis 有序集合按单个租约 TTL 保守释放；精确 release 延后至 P1。Publisher 依赖异常时 stop/restart/delete 持续 fail-closed 重试，恢复后自动继续，不把操作终结为不可恢复状态。

访问策略控制面也完成了契约闭环。Create/PATCH 以租户、操作范围和 idempotency key 持久化 canonical request hash 与结果快照，并在任何业务 mutation 前完成 replay/conflict 判定；PATCH 的 hash 来自 Gateway 收到的原始 partial intent，而不是合并后的当前策略，因此 `K1 → K2 → K1` 会重放 K1 的原结果，不会被 K2 改写后的状态污染。公开 JSON 严格区分 omitted、显式 `null` 和具体值：可空的 qps/rpm/max-in-flight 用 `null` 清除、omitted 保留；description 可用 `null` 清空；其他 non-nullable 字段以及 lease TTL 的显式 `null` 均在读取或修改策略前返回 400。Create 的 omitted 默认仍为 enabled、priority 1000、空限额和 lease TTL 60 秒。Policy/event 响应补齐契约要求的 RFC3339Nano 时间戳，service-policy projection 使用公开字段 `service_id`，events limit 与 OpenAPI 的 200 上限一致。

## 验证证据

| 命令或证据 | 结果 |
|---|---|
| `PATH=/tmp/ani-pybin:$PATH make validate-inference-gateway-publication-migration` | EXIT:0（9 tests） |
| `PATH=/tmp/ani-pybin:$PATH make validate-inference-envoy-ai-gateway-c41-manifest` | EXIT:0（25 tests） |
| `PATH=/tmp/ani-pybin:$PATH make validate-inference-envoy-ai-gateway-c41-live-gate` | EXIT:0（50 tests；仅 live-gate contract） |
| `PATH=/tmp/ani-pybin:$PATH make validate-inference-access-policy-contract` | EXIT:0（5 tests） |
| `PATH=/tmp/ani-pybin:$PATH make validate-inference-access-policy-migration` | EXIT:0（5 tests；含 tenant RLS、request hash/result snapshot 与 TTL 1..3600） |
| `PATH=/tmp/ani-pybin:$PATH make validate-inference-control-plane` | EXIT:0（migration/legacy-control-plane validators） |
| `PATH=/tmp/ani-pybin:$PATH make validate-openapi-spec` | EXIT:0（15 tests） |
| `PATH=/tmp/ani-pybin:$PATH make validate-services-contract` | EXIT:0（accepted baseline warnings only） |
| `PATH=/tmp/ani-pybin:$PATH make validate-services-route-contract` | EXIT:0（7 条已实现 inference-policy route 的 stale baseline 已移除） |
| 外部：`GOWORK=off go test ./... -count=1`（inference-service） | EXIT:0 |
| 外部：`GOWORK=off go test -race ./... -count=1`（inference-service） | EXIT:0 |
| 外部：`PATH=/tmp/ani-pybin:$PATH make test` | EXIT:0 |
| 外部：`PATH=/tmp/ani-pybin:$PATH make validate-services` | 使用 `/tmp/ani-c41-validate-services.index` 与临时 object directory 模拟 shipping index，纳入唯一需要的 Console generated schema artifact 后 EXIT:0；真实 index 保持为空 |
| `PATH=/tmp/ani-pybin:$PATH make validate-architecture` | EXIT:0 |
| `git diff --check` | EXIT:0 |

受限沙箱中的 IPv6 loopback `httptest` 与本机 Go 1.26.3 race toolchain 错误均已由外部匹配环境复跑覆盖：inference-service normal/race 均 EXIT:0，repository `make test` EXIT:0。`GOWORK=off go vet ./...` 为 EXIT:0；envoy-authz-adapter 的 normal test 与 vet 也已通过。

Task10 首次 `make validate-services` 曾暴露两类真实 RED：C41 Services/Core import 违规，以及 7 条已被实际 Gateway handler 注册的 inference-policy `spec_not_in_code` stale baseline。前者已通过 inference-service 自有 port/Redis adapter 修复；后者在确认 7 条 OpenAPI route 已注册且 handler 非 stub 后，只移除了对应存量例外。外部 aggregate 首次发现的唯一残余是 Console generated schema 的三处 description drift；保留 `frontends/console/src/api/schema.d.ts` 的对应生成更新后，以 `/tmp/ani-c41-validate-services.index` 和临时 object directory 模拟 shipping index 复跑 `make validate-services` EXIT:0。真实 Git index 未 stage、保持为空；未增加 OpenAPI、路由或新的 baseline。

## Kubernetes 与 live 状态

Task 8 的已执行 server schema dry-run 命令为：

```bash
kubectl apply --server-side --dry-run=server -f deploy/real-k8s-lab/inference-envoy-ai-gateway-c41.yaml
```

结果为 11 个对象中 10 个 accepted；唯一未接受的是已安装 `BackendTrafficPolicy` CRD 的自相矛盾 `int32`/`maximum` schema，不是 runtime evidence。未在本任务中执行 kubectl、apply 或 live runner。

Live status：`not-run`。C41 live runner 仍需要新的明确 live 授权和全部九项临时环境输入；因此不得从 local gate、契约 gate 或 server dry-run 推导 runtime ready 或 production ready。

## 安全与已知边界

- 没有持久化 AK、`Authorization`、prompt、completion、embedding 输入或 vector；没有读取 Kubernetes Secret data。
- Publisher 不管理 workload 或 Secret；live runner 的 Secret 检查仅允许 metadata-only 名称查询。
- `/v1/models` 保持固定 404，不暴露全局或跨租户模型列表。
- billing、multi-cluster routing 与 weighted backends 不在本批次范围。
- PostgreSQL live integration 未执行：测试 DSN 未设置，故准确记为 skip，不能当作 PostgreSQL live evidence。
- 访问策略 PostgreSQL integration fixture 已覆盖 mutation replay/conflict、`K1 → K2 → K1` 与 RLS/事务边界并通过 integration-tag 编译；因为同一测试 DSN 未设置，运行时仍准确记为 skip。
