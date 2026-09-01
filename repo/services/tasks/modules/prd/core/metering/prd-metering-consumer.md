# PRD: 计量 Consumer 落地（事件驱动周期采集 V2）

> 来源计划: `repo/services/tasks/modules/plan/plan-metering-consumer-v2.md`
> 范围确认: 全部 5 个 PR（PR-M1 到 PR-M5），覆盖 migration、port、collector、consumer、rebuilder、集成测试和部署清单
> 前置依赖: reconciler 写 outbox + instance.* 事件（外部依赖，不在本 PRD 范围）

---

## 1. Introduction/Overview

ANI 平台需要一个事件驱动的计量采集系统，在实例生命周期事件（created/stopped/failed/deleted）触发下启动/停止周期性资源用量采集，将 GPU 占用时长、CPU 使用秒数、内存 GiB-秒三个维度的计量数据持久化到 `metering_usage_records` 表，为后续租户用量展示和计费提供数据基础。

当前消息总线骨架（MessageBus port、NATS adapter Publish/Subscribe）已就绪，但消费侧完全悬空——metering consumer 零生产调用方，`MeteringCollectionService` 接口不存在，`metering_usage_records` 表不存在，metering-service 进程不存在。本 PRD 从零搭建 consumer 进程和完整消费链路。

本 PRD 落地 V2 方案的全部 5 个 PR：

- **PR-M1**：`metering_usage_records` migration + `MeteringCollectionService` port + 实现类（持 `*pgxpool.Pool` 直连 Core DB）
- **PR-M2**：`Collector` 接口 + 三个 Collector 实现（DCGM GPU 占用/Kubelet CPU Counter/Kubelet Mem Gauge）+ 单测
- **PR-M3**：metering-service main.go（bootstrap.MustConnect 启动）+ consumer（seenSeq 成功才推进 + MaxInflight=1）+ 重建协调器（直接查 DB + WithPlatformTx 绕 RLS）+ DeliverAll 回放
- **PR-M4**：集成测试（真实 NATS + 真实 DB + 真实 Prometheus）+ 端到端验证
- **PR-M5**：部署清单 `metering-service-live-deps.yaml`（ServiceAccount + Deployment + Service）

**设计原则**：metering-service 定位为 Core 内部服务（仿 auth-service 范式），直连 Core DB 执行原生 SQL；seenSeq 处理成功后才推进（避免 V1 的 Nak 重投误判过期永久丢失）；单副本约束（replicas:1 + MaxInflight=1 串行消费）；双层幂等（进程内 map + DB UNIQUE 约束）；不建 metering_collections 表（PG workload_instances 为唯一 source of truth）。

---

## 2. Goals

- 新增 `metering_usage_records` 表 + `ani_metering_writer` 角色（BYPASSRLS），支持 UNIQUE 约束去重和 RLS 读侧隔离
- 新增 `MeteringCollectionService` port 接口（StartCollection/StopCollection），与现有 `MeteringService`（QueryUsage/ReportTokenUsage）分离
- 实现 `meteringCollectionService`：进程内 `map[resourceRef]*ticker` 管理采集生命周期 + `*pgxpool.Pool` 直连 DB 写记录 + `ON CONFLICT DO NOTHING` 写入幂等
- 新增 `Collector` 接口 + 三个 Collector 实现，覆盖 GPU 占用时长、CPU Counter 增量、内存 Gauge 瞬时占用加权三种语义
- 新建 metering-service 独立进程 main.go，用 `bootstrap.MustConnect` 启动
- 实现 consumer：事件驱动 Start/Stop Collection + seenSeq 成功才推进 + MaxInflight=1 串行消费 + 租户上下文校验 + 毒消息处理
- 实现 rebuilder：启动时直接查 `workload_instances` 表（WithPlatformTx 绕 RLS）重建 running 实例的 ticker
- 实现 DeliverAll 回放补齐机制，崩溃恢复后无丢采
- 实现 StopCollection 保底采集：短生命周期（<1 周期）实例从未产出记录时强制补采全周期量
- 新增部署清单 `metering-service-live-deps.yaml`，replicas:1 单副本
- 通过 `make test`、`make validate-architecture`、`git diff --check`
- 集成测试覆盖真实 NATS + 真实 DB + 真实 Prometheus 端到端链路
- 真实环境门禁在 REAL-K8S-LAB 验证

---

## 3. User Stories

### US-001: 新增 metering_usage_records migration
**Description:** 作为开发者，我需要新建 `metering_usage_records` 表和 `ani_metering_writer` 角色，为计量数据提供持久化存储。

**Acceptance Criteria:**
- [ ] 新增 `repo/deploy/migrations/20260731000100_metering_usage.sql`
- [ ] 创建 `ani_metering_writer` 角色（BYPASSRLS NOLOGIN），类比 `init_schema.sql` 中 `ani_outbox_publisher` 的创建范式
- [ ] 创建 `metering_usage_records` 表：`id` BIGSERIAL PK、`tenant_id` UUID FK→tenants(id) ON DELETE CASCADE、`resource_ref` TEXT、`resource_type` TEXT、`period` TEXT、`quantity` DOUBLE PRECISION、`unit` TEXT、`recorded_at` TIMESTAMPTZ DEFAULT NOW()
- [ ] UNIQUE 约束 `(tenant_id, resource_ref, resource_type, period)` 作为 StartCollection 幂等的 DB 层
- [ ] 索引 `idx_meter_tenant_type_time (tenant_id, resource_type, recorded_at)`
- [ ] GRANT SELECT/INSERT/UPDATE/DELETE 给 `ani_metering_writer`
- [ ] ENABLE ROW LEVEL SECURITY + 创建 `tenant_isolation` policy（读侧 RLS 过滤，写入侧 BYPASSRLS）
- [ ] 不 FORCE RLS（采集写侧用 `ani_metering_writer` BYPASSRLS，读侧用 `ani_app_user` RLS 生效）
- [ ] migration 执行顺序正确：ROLE → TABLE → GRANT → RLS
- [ ] Typecheck/lint 通过

### US-002: 新增 MeteringCollectionService port 接口
**Description:** 作为开发者，我需要新增 `MeteringCollectionService` 接口和扩展 `MeteringUsageRecord` 结构，为 consumer 和 rebuilder 提供采集生命周期控制契约。

**Acceptance Criteria:**
- [ ] 新增 `pkg/ports/instance_events.go`，定义 `InstanceLifecycleEvent` 结构（InstanceID/TenantID/WorkloadKind/NewStatus/EventSeq/GPUSpec/ErrorMsg）和 `GPUEventSpec` 结构
- [ ] 在 `pkg/ports/metering.go` 新增 `CollectionSpec` 结构（ResourceRef/TenantID/WorkloadKind/Dimensions/IntervalSec/StartedAt/GPUSpec）
- [ ] 在 `pkg/ports/metering.go` 新增 `CollectionDimension` 结构（ResourceType/Source）
- [ ] 在 `pkg/ports/metering.go` 新增 `MeteringCollectionService` interface，包含 `StartCollection(ctx, spec) error` 和 `StopCollection(ctx, resourceRef) error`
- [ ] `MeteringCollectionService` 与现有 `MeteringService` 分离（采集控制 vs 查询/上报）
- [ ] 扩展 `ports.MeteringUsageRecord` 新增 `ResourceRef string` 字段（现有 5 字段保持不变，新增字段无破坏性变更）
- [ ] `StartCollection`/`StopCollection` 文档注释说明幂等语义（进程内 map 去重 + DB UNIQUE 兜底 / 无 ticker 时 no-op）
- [ ] Typecheck/lint 通过

### US-003: 实现 meteringCollectionService（进程内 ticker 管理 + DB 持久化）
**Description:** 作为开发者，我需要实现 `meteringCollectionService`，持有 `*pgxpool.Pool` 直连 Core DB，管理 per-instance ticker 并写入 `metering_usage_records`。

**Acceptance Criteria:**
- [ ] 新增 `services/metering-service/go.mod`（独立 module），补 `github.com/jackc/pgx/v5` 及 `pgxpool` 依赖（PR-M1 范围，实现类需 pgx 才能编译）
- [ ] 新增 `services/metering-service/internal/service/metering_collection_service.go`
- [ ] `meteringCollectionService` 持有 `*pgxpool.Pool`、`map[string]*time.Ticker`（key: resourceRef）、`map[string]chan struct{}`（stopChs）、`map[string]*ports.CollectionSpec`（specs）、`map[string]bool`（everCollected）、`map[string]Collector`（cols）、`*slog.Logger`
- [ ] `StartCollection`：进程内 map 已有 ticker 时返回 nil（幂等 no-op）；否则建 ticker + stopCh + 存 spec，启动 `runCollectionLoop` goroutine
- [ ] `StartCollection`：`spec.StartedAt.IsZero()` 时设为 `time.Now()`（供 collectFullLifetime 计算）
- [ ] `runCollectionLoop`：`select <-ticker.C` 调用 `CollectAll` 采集 → `persistRecords` 写 DB；`<-stopCh` 时 `ticker.Stop()` 退出
- [ ] `persistRecords`：用 `ani_metering_writer` 角色连接（BYPASSRLS），INSERT 用 `ON CONFLICT DO NOTHING` 兜底写入幂等
- [ ] `persistRecords` 的 INSERT 语句列名用 `quantity`（对应 Go struct `TotalQuantity` 字段）
- [ ] `StopCollection`：无 ticker 时返回 nil（幂等 no-op）；否则 ticker.Stop → close stopCh → delete map entries
- [ ] `StopCollection`：锁外做保底采集——`everCollected[ref]==false && spec != nil` 时调 `collectFullLifetime` 补采一次全周期量
- [ ] `collectFullLifetime`：按 Start 到 Stop 的完整存活时长计算一次性量，Period 用 Stop 时刻分钟对齐
- [ ] `collectFullLifetime` 产出的记录若与已有周期记录碰撞，`ON CONFLICT DO NOTHING` 兜底丢弃
- [ ] 锁结构：Stop 时缩小锁范围，慢 I/O（collectFullLifetime + persistRecords）在锁外执行
- [ ] Typecheck/lint 通过

### US-004: 新增 Collector 接口 + 三个 Collector 实现
**Description:** 作为开发者，我需要实现三个 Collector，分别处理 GPU 占用时长、CPU Counter 增量、内存 Gauge 瞬时占用加权三种语义，并提供 CollectAll 路由入口。

**Acceptance Criteria:**
- [ ] 新增 `pkg/adapters/metering/collectors.go`
- [ ] 定义 `Collector` interface：`Collect(ctx, spec, period string) ([]ports.MeteringUsageRecord, error)`
- [ ] 实现 `DCGMGPUCollector`：`spec.GPUSpec == nil` 时返回 nil（跳过 GPU 维度，不写 0 错值）；否则产出 `TotalQuantity = float64(GPUSpec.Count) * float64(IntervalSec)`，unit=`gpu_second`
- [ ] 实现 `KubeletCPUCollector`：查询 Prometheus `container_cpu_usage_seconds_total` 的 `rate(...[60s])`，产出 `TotalQuantity = secs * float64(IntervalSec)`，unit=`cpu_second`
- [ ] 实现 `KubeletMemCollector`：查询 Prometheus `container_memory_working_set_bytes`，产出 `TotalQuantity = bytes / 1024^3 * float64(IntervalSec)`，unit=`gib_second`
- [ ] 实现 `Resolve(collectorID string) (Collector, bool)` 函数，路由 key：`dcgm_gpu`/`kubelet_cpu`/`kubelet_mem`
- [ ] 实现 `CollectAll(ctx, spec, logger)` 包级函数：生成分钟对齐 Period（`time.Now().Format("2006-01-02T15:04")`）→ 遍历 `spec.Dimensions` → 逐个 Resolve + Collect → 聚合返回
- [ ] `CollectAll` 在 unknown collector source 时 Warn 日志并跳过（不中断其余维度）
- [ ] `CollectAll` 在单维度 Collect 失败时 Error 日志并跳过（不中断其余维度）
- [ ] 三个 Collector 产出的记录均填充 `TenantID`/`ResourceRef`/`ResourceType`/`TotalQuantity`/`Unit`/`Period` 字段
- [ ] 单测覆盖三个 Collector 的 Collect 逻辑（含 GPU 卡数缺失跳过、Prometheus 查询 mock）
- [ ] Typecheck/lint 通过

### US-005: 新增 buildSpec 维度映射函数
**Description:** 作为开发者，我需要一个共享的 `buildSpec` 函数，根据 workload_kind 硬编码维度映射，供 consumer 和 rebuilder 共用。

**Acceptance Criteria:**
- [ ] 新增 `services/metering-service/internal/spec.go`
- [ ] `buildSpec(tenantID, instanceID, kind string, gpuCount int) ports.CollectionSpec` 作为 internal 包级函数
- [ ] 维度映射硬编码：`gpu_container` → 3 维（GPU+CPU+Mem），`vm` → CPU+Mem，其他 kind 按需映射
- [ ] `gpuCount > 0` 时设置 `spec.GPUSpec = &ports.GPUEventSpec{Count: gpuCount}`，否则 GPUSpec 为 nil
- [ ] `IntervalSec` 默认 60，`StartedAt` 默认 `time.Now()`
- [ ] consumer 调用：`buildSpec(event.TenantID, event.InstanceID, event.WorkloadKind, gpuCount)`，gpuCount 从 `event.GPUSpec.Count` 提取（nil 则 0）
- [ ] rebuilder 调用：`buildSpec(tenantID, instanceID, kind, gpuCount)`，gpuCount 从解析 `gpu_status` JSONB 的 `count` 字段提取（缺失则 0）
- [ ] Typecheck/lint 通过

### US-006: 新增 metering-service main.go（bootstrap.MustConnect 启动）
**Description:** 作为开发者，我需要新建 metering-service 进程入口，用 bootstrap.MustConnect 启动，按"先重建后订阅"协议执行。

**Acceptance Criteria:**
- [ ] 新增 `services/metering-service/main.go`
- [ ] 新增 `services/metering-service/internal/config/config.go`，加载环境变量（DATABASE_URL/NATS_URL/METERING_PROMETHEUS_URL/METERING_COLLECTION_INTERVAL_SECONDS/HEALTH_PORT）
- [ ] main.go 用 `bootstrap.MustConnect(cfg.Config)` 启动，获得 `*Deps`（DB/Ports.MessageBus/Ports.Metadata/Logger）
- [ ] 构造 `meteringCollectionService`（传入 deps.DB, deps.Ports.Metadata, deps.Logger）
- [ ] 构造 consumer（传入 meteringSvc, deps.Ports.MessageBus, deps.Logger）
- [ ] 构造 rebuilder（传入 deps.Ports.Metadata, meteringSvc, deps.Logger）
- [ ] 启动顺序：1) rebuilder.Rebuild(ctx) 重建 ticker → 2) Subscribe NATS（DeliverAllPolicy, MaxInflight=1, AckWait=30s, MaxDeliver=5）→ 3) `<-ctx.Done()` 常驻等待
- [ ] 重建失败不阻塞：日志告警后继续订阅（靠事件增量 + DeliverAll 兜底）
- [ ] Subscribe 失败时 `os.Exit(1)`
- [ ] 退出时 `defer sub.Drain(context.Background())`（Subscription 接口只有 Drain，无 Unsubscribe）
- [ ] Subscribe subject: `ani.events.instance.>`，Consumer name: `metering-consumer`
- [ ] 不设 Queue Group（单副本只有一个订阅，Queue 竞争语义无处发挥）
- [ ] Typecheck/lint 通过

### US-007: 实现 consumer（seenSeq 成功才推进 + MaxInflight=1 串行消费）
**Description:** 作为开发者，我需要实现 consumer 的 handleEvent，处理 InstanceLifecycleEvent，确保 seenSeq 处理成功后才推进，避免 Nak 重投误判过期。

**Acceptance Criteria:**
- [ ] 新增 `services/metering-service/internal/consumer.go`
- [ ] `Consumer` 结构持有 `metering ports.MeteringCollectionService`、`logger *slog.Logger`、`mu sync.Mutex`、`seenSeq map[string]uint64`
- [ ] `handleEvent` 从 `msg.Headers()["tenant-id"]` 读租户 ID，与 payload `tenant_id` 校验一致；不一致时返回 error（→ adapter Nak 重投）
- [ ] `handleEvent` 对 `json.Unmarshal` 失败的毒消息记 Error 日志后返回 nil（→ adapter Ack，不重投）
- [ ] `handleEvent` 乱序过滤：`event.EventSeq <= seenSeq[instance_id]` 时 Warn 日志并返回 nil（丢弃过期事件）
- [ ] `handleEvent` 路由：`new_status=="running"` → StartCollection；`stopped/failed/deleted` → StopCollection；未知状态 → Warn 日志返回 nil
- [ ] `handleEvent` 处理失败时返回 error（→ adapter Nak 重投），**不推进 seenSeq**
- [ ] `handleEvent` 处理成功后才推进 seenSeq：`event.EventSeq > seenSeq[instance_id]` 时更新
- [ ] seenSeq 是进程内存态，重启归零（接受此边界，不持久化）
- [ ] 单测覆盖：成功路径推进 seenSeq、失败路径不推进、过期事件丢弃、毒消息 Ack 跳过、租户不匹配 Nak 重投
- [ ] Typecheck/lint 通过

### US-008: 实现 rebuilder（直接查 DB + WithPlatformTx 绕 RLS）
**Description:** 作为开发者，我需要实现 rebuilder，启动时跨租户查所有 running 实例并建 ticker，不新增真相源。

**Acceptance Criteria:**
- [ ] 新增 `services/metering-service/internal/rebuilder.go`
- [ ] `Rebuilder` 结构持有 `metadataStore ports.MetadataStore`（用 WithPlatformTx 绕 RLS，不持裸 pool）、`metering ports.MeteringCollectionService`、`logger *slog.Logger`
- [ ] `Rebuild(ctx)` 用 `metadataStore.WithPlatformTx` 跨租户查 `workload_instances WHERE state='running'`
- [ ] SQL 查询 4 个字段：`tenant_id::text`、`instance_id`、`workload_kind`、`gpu_status`（JSONB）
- [ ] 解析 `gpu_status` JSONB 获取 GPU 卡数（`{"count": N}`，缺失返回 0）
- [ ] 对每个 running 实例调 `buildSpec` + `metering.StartCollection`
- [ ] 单个实例 StartCollection 失败不阻塞，记 Error 日志继续重建其余实例
- [ ] 查询用 `ORDER BY updated_at ASC`
- [ ] 重建完成后记 Info 日志（"rebuild done", running_instances count）
- [ ] 单测覆盖：WithPlatformTx 调用、running 实例建 ticker、gpu_status 解析、单实例失败不阻塞
- [ ] Typecheck/lint 通过

### US-009: 集成测试（真实 NATS + 真实 DB + 真实 Prometheus）
**Description:** 作为开发者，我需要集成测试覆盖端到端链路，验证事件驱动采集、幂等、重建和 DeliverAll 回放的完整行为。

**Acceptance Criteria:**
- [ ] 集成测试启动真实 NATS JetStream + 真实 PG（含 migration）+ 真实 Prometheus mock
- [ ] 测试场景 1：发布 `instance.created`(running) 事件 → consumer 调 StartCollection → ticker 产出记录写入 `metering_usage_records`
- [ ] 测试场景 2：发布 `instance.stopped` 事件 → consumer 调 StopCollection → ticker 停止 → 短生命周期保底采集触发
- [ ] 测试场景 3：重复发布 `instance.created` 同一 instance → 进程内 map 幂等 no-op，DB 无重复行
- [ ] 测试场景 4：consumer 进程重启 → rebuilder 查 running 实例重建 ticker → DeliverAll 回放补齐崩溃窗口消息
- [ ] 测试场景 5：seenSeq 乱序过滤——先发 seq=5 再发 seq=3，seq=3 被丢弃
- [ ] 测试场景 6：seenSeq 失败重投——StartCollection 失败后 Nak 重投，seenSeq 未推进，重投后重新处理
- [ ] 测试场景 7：租户上下文不匹配 → Nak 重投
- [ ] 测试场景 8：毒消息（json 畸形）→ Ack 跳过
- [ ] 测试场景 9：DB UNIQUE 约束兜底——同实例同维度同周期重复 INSERT 时 `ON CONFLICT DO NOTHING`
- [ ] Typecheck/lint 通过

### US-010: 部署清单 metering-service-live-deps.yaml
**Description:** 作为运维，我需要 K8s 部署清单来部署 metering-service，强制单副本运行。

**Acceptance Criteria:**
- [ ] 新增 `repo/deploy/real-k8s-lab/metering-service-live-deps.yaml`
- [ ] 包含 ServiceAccount（name: `ani-metering-service`，namespace: `ani-system`）
- [ ] 包含 Deployment（replicas: 1，强制单副本）
- [ ] 包含 Service（port: 9210 health）
- [ ] container.command: `/opt/ani/bin/metering-service`，hostPath 挂 `/opt/ani/bin`
- [ ] env: `DATABASE_URL`（secret `ani-metering-runtime` key `database_url`）、`NATS_URL`（secret key `nats_url`）、`METERING_PROMETHEUS_URL`（明文）、`METERING_COLLECTION_INTERVAL_SECONDS=60`、`HEALTH_PORT=9210`
- [ ] readinessProbe/livenessProbe: tcpSocket port health
- [ ] resources: requests cpu=100m memory=128Mi，limits cpu=1 memory=512Mi
- [ ] securityContext: runAsNonRoot, runAsUser=65532, seccompProfile RuntimeDefault, allowPrivilegeEscalation=false, capabilities drop ALL
- [ ] 参照 `sprint13-production-auth-dex.yaml` auth-service 部署段格式
- [ ] Typecheck/lint 通过

---

## 4. Functional Requirements

- FR-1: 系统必须新建 `metering_usage_records` 表，含 UNIQUE 约束 `(tenant_id, resource_ref, resource_type, period)` 作为写入幂等的 DB 层兜底
- FR-2: 系统必须创建 `ani_metering_writer` 角色（BYPASSRLS NOLOGIN），采集写侧用此角色绕过 RLS
- FR-3: 系统必须对 `metering_usage_records` 表 ENABLE ROW LEVEL SECURITY，读侧（QueryUsage）用 `ani_app_user` RLS 过滤
- FR-4: 系统必须新增 `MeteringCollectionService` interface（StartCollection/StopCollection），与 `MeteringService`（QueryUsage/ReportTokenUsage）分离
- FR-5: 系统必须扩展 `ports.MeteringUsageRecord` 新增 `ResourceRef string` 字段，不破坏现有 5 字段
- FR-6: 系统必须新增 `InstanceLifecycleEvent` 和 `GPUEventSpec` 结构在 `pkg/ports/instance_events.go`
- FR-7: `meteringCollectionService` 实现必须持有 `*pgxpool.Pool` 直连 Core DB（仿 auth-service 范式）
- FR-8: `StartCollection` 必须幂等：进程内 `map[resourceRef]` 已有 ticker 时返回 nil（no-op）
- FR-9: `StartCollection` 必须在 `spec.StartedAt.IsZero()` 时设为 `time.Now()`
- FR-10: `runCollectionLoop` 在 CollectAll 失败时记 Error 日志并 continue（不停 ticker，下个周期重试）
- FR-11: `persistRecords` 必须用 `ani_metering_writer` 连接（BYPASSRLS），INSERT 用 `ON CONFLICT DO NOTHING`
- FR-12: `persistRecords` 的 INSERT 列名用 `quantity`（对应 Go struct `TotalQuantity` 字段）
- FR-13: `StopCollection` 必须幂等：无 ticker 时返回 nil（no-op）
- FR-14: `StopCollection` 必须在锁外执行保底采集：`everCollected[ref]==false && spec != nil` 时调 `collectFullLifetime`
- FR-15: `collectFullLifetime` 必须按 Start 到 Stop 完整存活时长计算，Period 用 Stop 时刻分钟对齐
- FR-16: 系统必须实现 `Collector` interface 和三个实现：`DCGMGPUCollector`、`KubeletCPUCollector`、`KubeletMemCollector`
- FR-17: `DCGMGPUCollector` 在 `spec.GPUSpec == nil` 时返回 nil（跳过 GPU 维度，不写 0 错值）
- FR-18: `DCGMGPUCollector` 产出 `TotalQuantity = float64(GPUSpec.Count) * float64(IntervalSec)`，unit=`gpu_second`
- FR-19: `KubeletCPUCollector` 查询 Prometheus `container_cpu_usage_seconds_total` 的 rate，产出 Counter 增量
- FR-20: `KubeletMemCollector` 查询 Prometheus `container_memory_working_set_bytes`，产出 Gauge 瞬时占用加权时长
- FR-21: `CollectAll` 必须生成分钟对齐 Period（`time.Now().Format("2006-01-02T15:04")`）填入每条记录
- FR-22: `CollectAll` 在 unknown collector source 时 Warn 日志跳过，在单维度失败时 Error 日志跳过
- FR-23: `buildSpec` 必须作为 `internal` 包级函数，根据 `workload_kind` 硬编码维度映射
- FR-24: metering-service main.go 必须用 `bootstrap.MustConnect` 启动
- FR-25: 启动协议必须先重建（rebuilder.Rebuild）后订阅（Subscribe），消除竞态窗口
- FR-26: 重建失败不阻塞，日志告警后继续订阅
- FR-27: Subscribe 配置必须 `MaxInflight=1`（强制串行消费保证顺序）、`DeliverAllPolicy`、`AckWait=30s`、`MaxDeliver=5`
- FR-28: Subscribe 不设 Queue Group（单副本无竞争语义）
- FR-29: 退出时 `defer sub.Drain(context.Background())`
- FR-30: consumer `handleEvent` 必须从 `msg.Headers()["tenant-id"]` 读租户 ID 并与 payload 校验一致
- FR-31: consumer 对 `json.Unmarshal` 失败的毒消息返回 nil（Ack 跳过，不重投）
- FR-32: consumer 乱序过滤：`event.EventSeq <= seenSeq[instance_id]` 时丢弃（返回 nil）
- FR-33: consumer 处理失败时返回 error（Nak 重投），**不推进 seenSeq**
- FR-34: consumer 处理成功后才推进 seenSeq（`event.EventSeq > seenSeq[instance_id]` 时更新）
- FR-35: rebuilder 必须用 `metadataStore.WithPlatformTx` 跨租户查 `workload_instances WHERE state='running'`
- FR-36: rebuilder 解析 `gpu_status` JSONB 获取 GPU 卡数，缺失返回 0
- FR-37: rebuilder 单实例 StartCollection 失败不阻塞，继续重建其余实例
- FR-38: 部署清单必须 `replicas: 1`（单副本约束）

---

## 5. Non-Goals (Out of Scope)

- **reconciler 写 outbox + instance.* 事件生产侧**：上游前置依赖，本 PRD 假设其已就绪
- **`instance.created/failed/deleted/stopped` 事件的生产侧实现**：本 PRD 只定义 consumer 侧契约
- **metering 读取侧（QueryUsage 展示）**：属后续批次，本 PRD 只做采集写入
- **删除既有 `metering_records` 表**：该表代码零引用，废弃清理走独立 migration
- **多副本支持**：当前单副本已满足数百实例规模，多副本带来的分布式协调复杂度不值得
- **seenSeq 持久化**：接受重启归零边界，不新增表 + 启动恢复 + 运行时同步写
- **建 `metering_collections` 表**：避免新增真相源，重建直接查 `workload_instances`
- **周期相位对齐**：V2 去掉相位对齐简化实现，period 是分钟级近似
- **leader election**：单副本 + K8s 失败重启兜底，不引入 leader election 复杂度
- **NATS adapter DLQ**：靠 MaxDeliver 限制，超出由 JetStream 处理
- **GPU 利用率数据混入计量账本**：`DCGM_FI_DEV_GPU_UTIL` 是 Gauge 不可累加，利用率数据另作监控指标
- **动态维度组装**：workload_kind 硬编码映射，不查 `resource_quota_meta` 动态组装

---

## 6. Design Considerations

- **metering-service 定位为 Core 内部服务**（仿 auth-service 范式），物理位置在 `services/metering-service/`，逻辑归属 Core；可直连 Core DB 执行原生 SQL
- **单副本运行**：进程内 ticker map、seenSeq、everCollected 均无法跨副本共享，多副本会导致重复采集、seenSeq 失效、保底采集误判
- **双层幂等**：进程内 `map[resourceRef]` 去重 + DB UNIQUE 约束兜底
- **先重建后订阅**：消除"事件先停、重建又建"的竞态窗口
- **DeliverAll 回放**：崩溃恢复后重放历史消息，靠双层幂等兜底重复处理
- **Collector 语义分类**：三个维度语义不同质——GPU 用占用时长（Gauge 不累加）、CPU 用 Counter 增量、内存用 Gauge 瞬时占用加权

---

## 7. Technical Considerations

- **依赖**：`github.com/jackc/pgx/v5` + `pgxpool`（metering-service go.mod 补依赖）
- **bootstrap.MustConnect**：返回 `*Deps`（DB/Ports.MessageBus/Ports.Metadata/Logger），Core 内部服务启动范式
- **WorkloadInstanceStore 限制**：只有 `List(tenantID, kind)`，无按 state 查询方法，重建需直接原生 SQL
- **workload_instances 表**：FORCE RLS，跨租户查询需 `WithPlatformTx` 绕 RLS；state 字段有索引 `idx_workload_instances_kind (tenant_id, workload_kind, state)`
- **PrometheusInstanceObservability**：已有 DCGM/kubelet/kubevirt PromQL，Collector 复用其字段
- **MessageBus 契约**：`Subscribe(opts, handler)` 无 ctx 参数，handler 返回 nil→Ack、返回 error→Nak、panic→recover 后 Nak；handler 收到的 ctx 固定为 `context.Background()`，需自行 `context.WithTimeout`
- **NATS headers**：adapter Publish 自动写 5 个 headers（tenant-id/aggregate-id/aggregate-type/event-type/occurred-at）
- **Subject 命名**：consumer 订阅 `ani.events.instance.>`，上游发布 `ani.events.instance.<instance_id>`
- **上游前置依赖 1——EventSeq 按 instance_id 单调递增**：发送端必须保证同一 instance 的 `event_seq` 严格递增（建议用单调时间戳纳秒级，同毫秒碰撞补递增子序号，避免墙钟回拨）。seenSeq 乱序过滤逻辑依赖此前提，否则高水位判定失效
- **上游前置依赖 2——终止事件必达**：`stopped`/`failed`/`deleted` 事件必须可靠送达。重建只负责补齐 running 实例的 ticker，若终止事件丢失，ticker 不会被停止，将持续采到无效数据。此前提是 DeliverAll + 重建自愈机制的成立基础

---

## 8. Success Metrics

- `make test` 通过，单测覆盖 consumer（seenSeq 推进/乱序/毒消息/租户校验）、collector（三维度+GPU 缺失跳过）、rebuilder（WithPlatformTx+gpu_status 解析+单实例失败不阻塞）
- `make validate-architecture` 通过，无架构边界违规
- `git diff --check` 通过，无空白错误
- 集成测试覆盖 9 个端到端场景（事件驱动采集、幂等、重建、DeliverAll 回放、seenSeq 乱序/失败重投、租户校验、毒消息、DB UNIQUE 兜底）
- 真实环境门禁在 REAL-K8S-LAB 验证：metering-service 单副本部署 → 发布 instance 事件 → 验证 `metering_usage_records` 有记录
- 重建后无丢采：进程重启 → rebuilder 重建 running 实例 ticker → DeliverAll 回放补齐崩溃窗口

---

## 9. Open Questions

- 上游 reconciler 写 outbox + instance.* 事件的落地时间表？本 PRD 假设其已就绪，若延迟需调整 PR-M3/M4 的验证策略
- `METERING_PROMETHEUS_URL` 在 REAL-K8S-LAB 环境的具体地址？部署清单中为明文 env，live gate 时需确认
- `ani-metering-runtime` secret 的预置流程是否完全复用 `ani-auth-production-shaped-runtime`？需在 PR-M5 部署时确认

---

## 10. ANI Boundaries

| Item | Value |
|------|-------|
| Product line | core |
| Code scope | `repo/pkg/ports/metering.go`（扩展）、`repo/pkg/ports/instance_events.go`（新增）、`repo/pkg/adapters/metering/`（新增）、`repo/services/metering-service/`（新建 main.go + internal）、`repo/deploy/migrations/20260731000100_metering_usage.sql`（新增）、`repo/deploy/real-k8s-lab/metering-service-live-deps.yaml`（新增） |
| OpenAPI authority | consume only / N/A（本 PRD 不涉及 OpenAPI 契约变更，纯 Core 内部服务 + DB migration + 部署清单） |
| Frozen exclusions | Core OpenAPI v1.yaml、Services API services/v1.yaml、reconciler/outbox 生产侧、metering 读取侧（QueryUsage 展示）、多副本支持 |
| idempotency_key | N/A（本 PRD 为 Core 内部服务 + 基础设施改造，不新增有副作用的 POST/PUT/PATCH API 端点） |
| Module main doc | N/A（Core 平台支撑层，非 Console/BOSS UI 模块） |
| Non-Goals | reconciler 生产侧、事件生产侧、读取侧、多副本、seenSeq 持久化、metering_collections 表、周期相位对齐、leader election、DLQ、GPU 利用率入账、动态维度组装 |
