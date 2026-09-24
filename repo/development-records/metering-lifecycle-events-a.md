# METERING-LIFECYCLE-EVENTS-A

> 日期：2026-09-24 ｜ 分支：`hotfix/metering-gpu-lifecycle-events` ｜ 状态：live verified（隔离测试环境 ani-system 30080，镜像 `dev-20260924-metering-events` / `dev-20260924-metering-events2`）

## 一、背景与现象

用户报障：新租户创建 GPU workload 并进入 running 后，BOSS 平台计量接口该租户 `instance_gpu_seconds = 0`，`metering_usage_records` 无该实例任何记录；metering-service 日志无该实例 `collection started`。日志中的 `active_instances=37` 只是 DB running 实例数，不代表已有采集 ticker。

## 二、根因（六项，全部实测核实）

1. **outbox 注入与 `GPU_QUOTA_ENABLED` 耦合**（`pkg/bootstrap/deps.go`）：outboxWriter/metadataStore/storeTx 仅在配额开启时注入 reconcile controller，配额关闭时实例生命周期迁移完全不写 `outbox_events`。
2. **outbox payload 不符合 metering 契约**（`pkg/adapters/runtime/reconcile_controller.go` writeOutbox）：只写 `{instance_id, kind, state, reason}`，metering consumer 需要的 `ports.InstanceLifecycleEvent` 是 `{tenant_id, name, workload_kind, new_status, event_seq, gpu_spec}`。
3. **publisher subject 不匹配**（`services/task-service/internal/worker/outbox_publisher.go`，用户根因分析未列出，实测发现）：outbox 行按 `event.EventType`（如 `instance.confirmed`）作 NATS subject 发布，而 metering 订阅 `ani.events.instance.>`，事件永远投递不到。契约（plan-metering-consumer-v2）要求上游发布 `ani.events.instance.<instance_id>`。
4. **event_seq 无来源**（同上，实测发现）：payload 无合法 `event_seq` 时 consumer 的 seenSeq 判定（`seq <= last` 丢弃）会把同实例所有后续事件当过期丢弃，事件驱动停止链路死亡。
5. **gpu_status 大写 `Count` 解析失败**（`services/metering-service/internal/spec.go`）：`gpu_status` 落库来自 `json.Marshal(GPUInstanceStatus)`（无 json tag，键为 Go 字段名 `"Count"`），而 parseGPUCount 只认小写 `"count"` → GPU 数解析为 0 → GPU 计量维度被跳过。**这是"计量恒为 0"的直接死因之一**。
6. **Reconciler 只停不启**（`services/metering-service/internal/reconciler.go`）：周期校准只做 StopStale，事件缺失时新增 running 实例永远无人补启动 ticker。**这是另一死因**。

## 三、修复（跨三个服务）

### gateway（ani-gateway 镜像）
- deps.go：reconcile controller 的 metadataStore/storeTx/outboxWriter 改为无条件注入；orchestrator 与 InstanceService 同样解耦（metadataStore/storeTx/outboxWriter 无条件、quota service 仍受 `GPU_QUOTA_ENABLED` 门控）。
- reconcile_controller.go writeOutbox：payload 改为完整 `InstanceLifecycleEvent`。
- outbox_writer.go：抽取共享 helper `writeInstanceOutboxTx` + `encodeInstanceLifecyclePayload`（controller/orchestrator/service 三处复用，UUID 校验与 best-effort 语义统一）。
- instance_orchestrator.go：创建同步迁移（persistWithQuotaTransition）发 `instance.confirmed`/`instance.cancelled`/`instance.retried`；Apply 失败发 `instance.create_failed`；Delete 发 `instance.deleted`。
- instance_service.go：API 生命周期 persistLifecycleWithQuota——delete→`instance.deleted`、stop→`instance.stopped`、start/restart/rollback→`instance.started`；其余动作（无生命周期语义）不写事件；配额 Try/Cancel/Release 仍由 quotaService 判空门控。

### task-service（task-service 镜像）
- outbox_publisher.go：`workload_instance` 聚合的 subject 映射为 `ani.events.instance.<aggregate_id uuid>`（event_type 保留为 envelope header 元数据）；发布时注入 `event_seq = outbox_events.id`（全局单调，满足 per-instance seenSeq 序判定）。

### metering-service（metering-service 镜像）
- spec.go parseGPUCount：同时识别大写 `"Count"`（GPUInstanceStatus 直序列化）与小写 `"count"`（契约格式）。
- reconciler.go：双向校准——对每个 DB running 实例先调幂等 `StartCollection` 补启动缺失 ticker（已有则 no-op），再 StopStale 停泄漏 ticker；`NewReconciler` 增加 intervalSec 参数（main.go 同步）。

## 四、验证

- 单测：metering reconciler/spec 全套 + 新增 `instance_lifecycle_outbox_test.go` 5 用例（确认事件/非迁移跳过/删除事件-配额关闭/停止事件/无关动作跳过）+ 既有 reconcile controller outbox 用例回归，全绿。
- 门禁：`go build` 受影响包、`gofmt -l` 改动文件无输出、`make validate-architecture` ✅、`git diff --check` ✅。仅 `pkg/adapters/runtime` 两个 sandbox symlink 用例失败（Windows 本机缺 python3 的既有环境限制，与本批次无关）。
- live E2E（隔离测试环境 ani-system，三镜像部署后）：
  - 第一轮（metering-events）：创建 1×RTX 4090 GPU 容器 → 5 分钟 Reconciler tick 补启动（`collection started`，active 37→38）→ `metering_usage_records` 持续写入 `instance_gpu_seconds=60/period`（大写 Count 解析正确）；删除后下一 tick `collection stopped`（stale_stopped=1）。**用户报障的"计量恒为 0"在此轮即已修复并实测。**
  - 第二轮（metering-events2，补齐创建/删除路径 outbox 后）：删除经 lifecycle API → outbox `instance.deleted`（完整契约 payload：new_status/gpu_spec{count:1}/tenant_id/workload_kind/name）→ publisher 发布到 `ani.events.instance.<uuid>`（500ms 内 published=t）→ metering **事件驱动 `collection stopped`（0.48 秒）**，不再依赖 5 分钟 tick。期间生产流量另两笔真实删除（非本测试实例）同样正常写入并发布。

## 五、边界与遗留

1. **创建 `instance.confirmed` 事件在"pod 慢启动 + 用户轮询详情"场景不发**：provisioning→running 的 DB 落库被实例详情接口的 live 状态合成抢先（每次 GET 详情即触发 UpsertStatus），绕过 reconcile controller 与 outbox。这是第三个落库路径，属架构性缺口（改动风险大于收益），metering 已有 Reconciler 兜底（≤5 分钟）不受影响，建议单独评估。pod 在同步 create 窗口内即 running 的场景下 confirmed 事件正常发出（代码路径已就绪）。
2. metering 长期存在 `kubelet_cpu/kubelet_mem "no samples"` ERROR（既有采集器/Prometheus 指标缺失问题，running 实例普遍报），与本批次无关，另批次处理。
3. 多镜像部署：本批次涉及 gateway/task-service/metering-service 三个镜像需同时或按序部署（metering/task-service 因 NATS durable consumer 单订阅者限制需 scale 0→1 切换）。仅部署部分镜像时：metering 单独部署即可修复计量（Reconciler 双向兜底独立生效）；gateway 单独部署只补事件链。
4. 兼容性：outbox payload 为 additive 变更（新增字段），event_type 新增 `instance.started`/`instance.stopped`；metering consumer 按 payload `new_status` 路由，不受 event_type 影响。无 OpenAPI 契约变更、无 DB 迁移、无生成物变更。

## 六、部署记录

- 镜像：`docker.changqingyun.cn/ani/{ani-gateway,task-service,metering-service}:dev-20260924-metering-events`（第一轮）+ gateway `dev-20260924-metering-events2`（第二轮补齐创建/删除路径 outbox）。
- 回滚锚点：gateway=`dev-20260924-metering-all-tenants`，task-service=`dev-20260904-obsruntime`，metering=`dev-20260909-metering-log`。
- E2E 测试实例已删除清理，无资源残留。
