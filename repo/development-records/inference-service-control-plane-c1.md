# INFERENCE-SERVICE-CONTROL-PLANE-C1

> 日期：2026-08-14
> 状态：local/logic verified
> 方案依据：`services/docs/console-modules/inference/inference-service-design.md` 阶段 C
> 前置契约：Core PR #99、Services PR #101 已合入上游 `main`

## 完成范围

- 新建独立 Go workspace module `services/inference-service`，冻结 `pending/deploying/running/stopping/stopped/failed` 状态、desired state、generation、operation 和 stop/delete 抢占规则。
- accelerator 是否存在是 CPU/GPU 执行选择的唯一新规格依据；遗留 `gpu_type/gpu_count_per_pod` 仅保留兼容数据，不触发 GPU 推断。
- 新增 additive PostgreSQL migration：在遗留 `inference_services` 上补 desired/applied spec、generation、内部 runtime reference、tombstone 等字段，并新增 `inference_operations` 幂等、lease、retry、result/error 状态。遗留行按原 replicas/GPU/status 回填并标记 `legacy_quarantined`，显式迁移前不得被新 reconciler 接管。
- tenant 请求事务使用 transaction-local `app.current_tenant_id`；跨租户 operation claim 使用独立 platform pool，并要求部署角色具备显式 RLS bypass 能力。worker 更新仍同时携带 tenant、service、operation 和 target generation。
- 创建与 operation 在一个事务中落库；使用 transaction advisory lock 串行化 `(tenant, operation_scope, idempotency_key)`，相同 hash 从 operation 的 `result_snapshot` 返回原始结果，不依赖 ModelCatalog 或未删除 service 行；不同 hash 返回冲突。
- operation claim 使用 `FOR UPDATE SKIP LOCKED`、到期 lease 和每次 claim 唯一的 fencing token；runtime observation 同时校验 tenant/service/operation/generation/token/lease，过期 worker 不得写回或完成任务。
- service 读取会恢复当前 pending/running operation 的 action，保证重启后 stop/delete 抢占仍按原状态机执行；领域 transition 对重复 delete/stop 返回“复用 operation ID” disposition，对已 stopped/running 的同向操作返回 already-desired disposition，不增加 generation，也不合成字段不完整的任务；后续事务用例必须据此加载真实 operation 或映射稳定 no-op 成功。
- 定义 inference-service 自有 `ModelCatalog` 与 `InferenceRuntime` dependency ports，不 import ANI Core 内部包、不操作 Kubernetes/Volcano/LWS/provider SDK。
- 创建用例在写库前校验 ready 模型版本，冻结模型展示快照、artifact/secret reference 与 engine profile；数据库不保存明文凭据。
- lease worker 使用 service ID + generation 派生稳定 runtime idempotency key；先持久化 `runtime_ref/runtime_endpoint`，仅在 ready replicas、health 和 smoke 全部通过后完成 operation 并进入 running。
- fake ModelCatalog、fake Runtime 和测试内 memory store 已证明 `Create → pending operation → worker claim → deploying → running` 的逻辑闭环。
- Services boundary guard 已把 `inference-service` 登记为 Services-owned source 并纳入 Go import 扫描；pgx 仅对 bounded repository 文件作精确 `bounded_direct` 登记。

## Design Decisions

- PostgreSQL 同时承载资源权威状态与 operation queue。当前闭环只需要持久 lease、重试和重启恢复语义，引入 NATS/Kafka 会增加双写与消费一致性问题。
- request path 与 worker path 使用两个显式 pool。前者必须设置 transaction-local tenant RLS context；后者由独立平台数据库角色跨租户 claim，但每次 mutation 仍用 tenant/service/generation 限定。
- create 使用 advisory transaction lock 串行化幂等身份。原因是 operation 外键依赖新 service，单靠 `ON CONFLICT` 无法在并发 retry 下避免先插入重复 service。
- `runtime_ref/runtime_endpoint` 在 ready 前先写入 PG。这样进程重启或 Core 响应丢失后的重试仍可用同一 runtime idempotency key 恢复，而不会把内部地址暴露给租户 DTO。

## Deviations

- C1 计划中的 PostgreSQL migration 目前已提供带 `integration` build tag 的真实 pgx 测试，使用随机 schema/role 并清理隔离资源，覆盖 apply/reapply、遗留隔离、tenant fail-closed/跨租户 denial、platform bypass、事务 rollback、并发同 key/claim、lease 接管/fencing、generation CAS 与 tombstone replay；但按当前执行顺序尚未连接 PostgreSQL 运行，因此记录状态仍限定为 local/logic verified。集群 PG 验收放在完整服务链路形成后执行。
- C1 没有创建可启动的 service binary。此偏离是有意的：外部 ingress transport、Gateway delegation 和真实 adapter 尚未冻结为同一批次，提前装配只会产生不可用入口。
- fake create-to-running 使用测试内 memory store，而不是生产 memory adapter；这避免建立第二个控制面权威实现，生产只保留 PostgreSQL store。

## Tradeoffs

- 考虑过把 PostgreSQL 抽到 Core `pkg/ports`，但 inference resource/operation 是 Services 业务状态，跨层 port 会把业务语义回流 Core。最终在 Services repository 边界使用精确 `bounded_direct` pgx allowlist。
- 考虑过 service insert 后再 update `current_operation_id`，最终使用 deferred foreign key，使 service 与 operation 可在一个事务中互相引用并保持最终约束完整。
- 考虑过删除旧 inference gRPC/CRD/operator 资产；当前没有构建或状态双写冲突，按用户确认与最小改动原则保留，后续只有在出现可证明冲突时处理。

## Open Questions

- C2 按设计实现 inference-service HTTP/OpenAPI handler；仍需冻结 Gateway 到该 cluster-internal endpoint 的服务发现、服务身份与超时配置，Gateway 不得直接访问数据库。
- 部署前需确定 platform PostgreSQL worker role 的最小 `BYPASSRLS`/授权方案；现有 integration test 已冻结 tenant role 与 platform role 行为，但尚未在集群 PostgreSQL 执行。
- 真实 ModelCatalog adapter 的稳定接口与生成 Core SDK adapter 的错误分类仍需在各自批次冻结；不得从依赖方数据库读取模型状态。

## 验证证据

```text
go test -race ./services/inference-service/... -count=1
INFERENCE_TEST_DATABASE_URL=... make test-inference-control-plane-postgres # 尚未实跑
python3 scripts/validate_inference_control_plane_migration_test.py
python3 scripts/validate_inference_control_plane_migration.py
make validate-services
make test
make validate-architecture
git diff --check
```

其中 migration 当前已通过静态契约与负向门禁，真实 pgx integration gate 已落代码但尚未连接 PostgreSQL 执行；因此本批次不声明数据库 live ready。

## 明确未完成

- 没有 inference-service HTTP/gRPC ingress、进程入口、Gateway delegation 或 Services DTO mapping。
- 没有真实 ModelCatalog adapter，也没有通过生成 Core SDK 调用 `platform-workloads` 的 adapter。
- 没有 Core platform-workloads handler/port/adapter、Deployment、LeaderWorkerSet、Volcano、vLLM 镜像或集群内真实推理。
- 没有集群 migration apply/restart recovery、多进程 worker、真实 RLS、CPU/GPU/LWS live evidence；带 build tag 的 PG integration test 不等同于已执行证据。
- `runtime_endpoint` 只存在于内部 domain/repository/runtime port，不进入 Services OpenAPI 响应；`invocation_url` 仍为 null。
- 不清理未影响新模块构建、测试或运行边界的旧 inference gRPC、CRD、operator 代码。

## 下一批次边界

阶段 C2 应先补真实 PostgreSQL integration gate、服务进程装配和现有 Services API handler delegation；真实 Core SDK adapter 与 platform-workloads runtime 属后续独立批次。在相应 live gate 通过前，不得标记 control-plane ready、runtime ready 或 production ready。
