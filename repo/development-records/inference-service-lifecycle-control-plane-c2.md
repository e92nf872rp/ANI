# INFERENCE-SERVICE-LIFECYCLE-CONTROL-PLANE-C2

> 日期：2026-08-14
> 状态：local/logic verified
> 方案依据：`services/docs/console-modules/inference/inference-service-design.md`
> 前置：Core PR #99、Services PR #101、`INFERENCE-SERVICE-CONTROL-PLANE-C1`

## 完成范围

- PostgreSQL repository 新增 tenant-scoped list/get 与 scale/start/stop/restart/delete 原子 mutation：事务内锁定 service、复用幂等 operation、取消被抢占 operation，并一起写入 desired spec/state、generation、current operation 和新 operation。
- repeated active stop/delete 返回真实已持久化 operation；already-desired start/stop 和同副本 scale 写入可查询的 completed no-op operation，不增加 service generation。
- application controller 提供 get/list/get-operation/scale/lifecycle/delete 用例；mutation request hash 不包含幂等键，delete 使用 tenant/service/desired-deleted 派生的稳定键。
- 公共查询 projection 不包含 `runtime_ref`、`runtime_endpoint` 或 Core task ID；P0 `invocation_url`/`endpoint_url` 保持 null；operation projection 固定 `resource_type=inference_service`、明确 task type 并返回契约要求的 `idempotency_key`。
- `InferenceRuntime` anti-corruption port 与 fake runtime 覆盖 ensure/observe/start/stop/restart/delete；fake 按 tenant/service 冻结最高 generation、幂等键和规范化 intent，同 generation 只有同 key/同 intent 可重放。
- stop/delete 即使 provider runtime 尚不存在也写入 generation fence；被抢占的旧 create 后到时在 runtime 边界被拒绝，repository generation/current-operation/lease CAS 继续作为状态写回防线。
- worker 对 create/scale/start/stop/restart/delete 均先提交 mutation，再通过独立 `Observe` 确认收敛；delete 只有观测到 runtime NotFound 后才 tombstone，running 只有 observed ready replicas、health 和 smoke 全通过后完成。
- fake 全流程覆盖 create → scale → stop → start → restart → delete，以及 create 被 stop 抢占的正反时序。
- PostgreSQL integration test 代码扩展了 lifecycle rollback、RLS/list isolation、并发 mutation/claim、lease fencing、generation CAS 与 tombstone replay；按当前执行顺序未连接集群 PostgreSQL运行。

## Design Decisions

- mutation 接受与 runtime 收敛分为两个阶段。Core/provider 返回无错误只表示请求已接受，worker 必须再 Observe，避免异步 delete 尚未完成就写 tombstone，或未 ready 就写 running。
- fake runtime 也实现 provider-side generation fence，而不只依赖数据库 CAS。DB CAS 能防止旧 worker覆盖服务状态，但不能删除旧请求在 provider 侧晚到创建的孤儿资源。
- already-desired 动作仍持久化 completed operation。这样 Services 的异步响应始终有稳定、可查询的 task，同时不制造无意义 generation。
- public projection 单独定义，不直接序列化 domain entity，确保 cluster-internal endpoint 和 Core task identity 不会随字段扩展意外泄露。

## Deviations

- 本批次没有启动 HTTP 进程，也没有替换 Gateway 现有 inference stub。契约已审批，但 ingress/delegation 需要独立 C3 批次装配和 HTTP 语义门禁。
- 带 build tag 的真实 pgx integration suite 本批次只完成编译/静态路径验证；遵循当前决定，集群 PostgreSQL migration/RLS/concurrency 执行推迟到完整服务链形成后统一验证。
- 当前 worker 只闭合成功收敛、暂态 retry、抢占和 fencing；设计中的不可重试错误分类、最大重试/dead-letter、scale rollback generation 与失败 runtime 清理尚未实现，不能据此声明完整故障恢复或 runtime ready。

## Tradeoffs

- 考虑过让 runtime mutation 直接返回终态，但这只适用于同步 fake，无法表达 Core AsyncTask/provider eventual consistency。最终保留 mutation 返回作为接受结果，并以 Observe 作为完成权威。
- 考虑过只在 service 状态写回处使用 generation CAS；该方案无法阻止 provider orphan。最终把 identity fence同时放在 runtime adapter 契约实现中，形成 provider-side 与 DB-side 双重防线。
- 考虑过 already-desired 直接返回空结果；这会破坏 `202 + AsyncTask` 查询闭环。最终持久化 completed no-op，代价是一条审计 operation，但不改变 generation。

## Open Questions

- C3 先实现 inference-service cluster-internal HTTP application adapter；按当前范围暂不修改 Gateway。后续 Gateway delegation 必须单独冻结服务发现、服务身份与超时，并且只能调用 inference-service HTTP/OpenAPI handler，不能直连数据库或内部 gRPC。
- 真实 ModelCatalog 与 Core `platform-workloads` adapter 的错误分类必须区分可重试和不可重试；在这之前不能实现可信的 dead-letter、失败清理和 scale rollback。
- 完整服务链形成后需在集群 PostgreSQL 执行 tagged integration gate，验证 migration apply/reapply、双角色 RLS、并发 mutation/claim、lease takeover 和 tombstone，再开始 Deployment/LWS/vLLM live gate。

## 验证证据

```text
cd repo/services/inference-service
GOWORK=off go test -race ./... -count=1
GOWORK=off go test -tags=integration ./internal/repository -run TestPostgresControlPlaneIntegration -count=1 -v

cd repo
python3 scripts/validate_inference_control_plane_migration_test.py
python3 scripts/validate_inference_control_plane_migration.py
make validate-services
make test
make validate-architecture
git diff --check
```

真实 PostgreSQL DSN 未配置时，tagged integration test 必须明确 SKIP；该结果只证明测试代码可编译，不构成 PG live evidence。

## 明确未完成

- 无 inference-service HTTP server、Services OpenAPI handler、Gateway delegation、auth middleware 或进程部署。
- 无真实 ModelCatalog/Core SDK adapter、Core `platform-workloads` handler/port/adapter。
- 无不可重试分类、dead-letter、scale rollback/失败清理完整闭环。
- 无 migration live apply、真实 PostgreSQL/RLS/concurrency 执行证据。
- 无 Deployment、LeaderWorkerSet、Volcano、vLLM、CPU/GPU/跨节点推理或清理 live evidence。

## 下一批次边界

C3 只实现 standalone inference-service 的 cluster-internal HTTP/OpenAPI application adapter，保持本批次 controller/repository/runtime 边界且暂不修改 ANI Gateway；Gateway delegation、真实 Core SDK/runtime adapter 与集群推理继续后置。任何后续批次在对应 live gate 前都不得标记 control-plane ready、runtime ready 或 production ready。
