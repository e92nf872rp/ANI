# SPEC: 计量 Consumer 落地（事件驱动周期采集 V2）

> Technical specification derived from:
> - PRD: `repo/services/tasks/modules/prd/core/metering/prd-metering-consumer.md`
> - UX: N/A — backend-only
> - Plan: `repo/services/tasks/modules/plan/plan-metering-consumer-v2.md`

---

## 1. Summary

### 1.1 What This SPEC Covers

从零搭建 metering-service 独立进程和完整事件驱动计量采集链路：`metering_usage_records` 表 migration、`MeteringCollectionService` port 接口、三个 Collector 实现（GPU 占用/CPU Counter/内存 Gauge）、consumer（seenSeq 成功才推进 + MaxInflight=1 串行消费）、rebuilder（直接查 DB + WithPlatformTx 绕 RLS）、集成测试和 K8s 部署清单。覆盖 PR-M1 到 PR-M5 全部 5 个落地阶段。

### 1.2 PRD Reference

- Source: `repo/services/tasks/modules/prd/core/metering/prd-metering-consumer.md`
- UX source: N/A — backend-only
- User Stories covered: US-001 ~ US-010
- Functional Requirements covered: FR-1 ~ FR-38

### 1.3 Design Decisions Summary

| Decision | Choice | Rationale |
|----------|--------|-----------|
| metering-service 定位 | Core 内部服务（仿 auth-service） | 平台支撑层，可直连 Core DB 执行原生 SQL；不受 Services 层约束 |
| seenSeq 推进时机 | 处理成功后才推进 | V1 处理前推进导致 Nak 重投时被误判过期永久丢失 |
| 消费模式 | MaxInflight=1 串行 | JetStream 不保证 MaxInflight>1 的投递顺序，串行保证 seenSeq 严格递增判定 |
| 副本数 | replicas:1 单副本 | 进程内 ticker/seenSeq/everCollected 无法跨副本共享 |
| 重建数据源 | 直接查 workload_instances | 避免新增真相源（metering_collections 表），PG 为唯一 source of truth |
| 幂等机制 | 进程内 map + DB UNIQUE 双层 | 进程内 map 快速去重，DB UNIQUE 兜底重启/重放场景 |
| 周期相位 | 不对齐整分钟 | V2 简化实现，period 分钟级近似已足够 |
| seenSeq 持久化 | 不持久化，重启归零 | 持久化需新增表+恢复+同步写，复杂度收益比不成立 |

---

## 2. Architecture

### 2.1 System Context

metering-service 是 ANI Core 平台支撑层内部服务，物理位置在 `services/metering-service/`，逻辑归属 Core。通过 NATS JetStream 订阅实例生命周期事件，直连 Core DB 写计量记录，查询 Prometheus 采集资源用量。

```
                    NATS JetStream
                    (ani.events.instance.>)
                         │
                         ▼
              ┌──────────────────────┐
              │  metering-service    │
              │  (replicas:1)        │
              │                      │
              │  ┌────────────────┐  │         ┌──────────────────┐
              │  │ Consumer       │  │         │ Rebuilder        │
              │  │ (handleEvent)  │  │         │ (启动一次)       │
              │  │ seenSeq 推进   │  │         │ WithPlatformTx   │
              │  │ MaxInflight=1  │  │         │ 查 running 实例  │
              │  └───────┬────────┘  │         └────────┬─────────┘
              │          │           │                  │
              │  ┌───────▼────────┐  │                  │
              │  │ MeteringCollec  │◄─┼──────────────────┘
              │  │ tionService     │  │
              │  │ map[ref]ticker  │  │         ┌──────────────────┐
              │  │ + persistRecords│──┼────────►│ Core DB          │
              │  └───────┬────────┘  │         │ metering_usage_  │
              │          │           │         │ records          │
              │  ┌───────▼────────┐  │         └──────────────────┘
              │  │ CollectAll      │  │
              │  │ (3 Collectors)  │──┼────────►┌──────────────────┐
              │  └────────────────┘  │         │ Prometheus       │
              └──────────────────────┘         └──────────────────┘
```

### 2.2 Component Design

| 组件 | 位置 | 职责 | 依赖 |
|------|------|------|------|
| `MeteringCollectionService` port | `pkg/ports/metering.go`（扩展） | 采集生命周期控制契约（Start/Stop） | 无 |
| `InstanceLifecycleEvent` | `pkg/ports/instance_events.go`（新增） | 事件 payload schema 定义 | 无 |
| `meteringCollectionService` | `services/metering-service/internal/service/` | 进程内 ticker 管理 + DB 持久化 | `*pgxpool.Pool`、Collector |
| `Collector` interface + 3 实现 | `pkg/adapters/metering/collectors.go`（新增） | 三维度用量采集（GPU/CPU/Mem） | Prometheus HTTP API |
| `CollectAll` | `pkg/adapters/metering/collectors.go` | 路由入口，分钟对齐 Period | `Collector` map |
| `Consumer` | `services/metering-service/internal/consumer.go` | 事件处理 + seenSeq 乱序过滤 | `MeteringCollectionService` |
| `Rebuilder` | `services/metering-service/internal/rebuilder.go` | 启动重建 running 实例 ticker | `MetadataStore`、`MeteringCollectionService` |
| `buildSpec` | `services/metering-service/internal/spec.go` | workload_kind 维度映射 | `ports.CollectionSpec` |
| `main.go` | `services/metering-service/main.go` | bootstrap.MustConnect 启动 | 全部组件 |
| `config.go` | `services/metering-service/internal/config/` | 环境变量加载 | `bootstrap.Config` |

### 2.3 Module Interactions

**启动流程**：

```
main()
  ├── config.Load()                           // 加载环境变量
  ├── bootstrap.MustConnect(cfg.Config)        // 初始化 DB/NATS/Ports/Logger
  ├── NewMeteringCollectionService(deps.DB, deps.Logger)
  ├── NewConsumer(meteringSvc, deps.Ports.MessageBus, deps.Logger)
  ├── NewRebuilder(deps.Ports.Metadata, meteringSvc, deps.Logger)
  │
  ├── 1. rebuilder.Rebuild(ctx)               // 先重建（查 workload_instances WHERE state='running'）
  │     └── 失败不阻塞，日志告警后继续
  │
  ├── 2. deps.Ports.MessageBus.Subscribe(...) // 后订阅（DeliverAll, MaxInflight=1）
  │     └── 失败时 os.Exit(1)
  │
  └── 3. <-ctx.Done()                        // 常驻等待
        └── defer sub.Drain(context.Background())
```

**事件处理流程**：

```
NATS msg → adapter 包装为 ports.Message
  → Consumer.handleEvent(ctx, msg)
    ├── 1. 读 msg.Headers()["tenant-id"]，与 payload tenant_id 校验
    │     └── 不一致 → return error（Nak 重投）
    ├── 2. json.Unmarshal → 失败 → return nil（Ack 跳过，毒消息）
    ├── 3. seenSeq 乱序过滤：event.EventSeq <= seenSeq[instance_id] → return nil（丢弃）
    ├── 4. 路由：
    │     ├── "running" → StartCollection(buildSpec(...))
    │     └── "stopped"/"failed"/"deleted" → StopCollection(instance_id)
    ├── 5. 处理失败 → return error（Nak 重投，不推进 seenSeq）
    └── 6. 处理成功 → 推进 seenSeq → return nil（Ack）
```

**采集循环**：

```
StartCollection(spec)
  ├── 进程内 map 已有 ticker → return nil（幂等 no-op）
  ├── 建 ticker + stopCh + 存 spec
  └── go runCollectionLoop:
        select {
        case <-ticker.C:
          records = CollectAll(spec)     // 三维度采集
          persistRecords(records)        // ON CONFLICT DO NOTHING
          everCollected[ref] = true
        case <-stopCh:
          ticker.Stop()
          return
        }

StopCollection(ref)
  ├── 无 ticker → return nil（幂等 no-op）
  ├── ticker.Stop → close stopCh → delete map entries
  └── 锁外保底采集：!everCollected && spec != nil
        └── collectFullLifetime(spec) → persistRecords
```

### 2.4 File Structure

```
repo/
├── deploy/
│   ├── migrations/
│   │   └── 20260731000100_metering_usage.sql          [NEW]
│   └── real-k8s-lab/
│       └── metering-service-live-deps.yaml           [NEW]
├── pkg/
│   ├── ports/
│   │   ├── metering.go                               [MODIFY: 新增 CollectionSpec/CollectionDimension/MeteringCollectionService/ResourceRef 字段]
│   │   └── instance_events.go                        [NEW]
│   └── adapters/
│       └── metering/
│           └── collectors.go                          [NEW]
└── services/
    └── metering-service/                              [NEW: 整个目录]
        ├── main.go
        ├── go.mod
        ├── go.sum
        └── internal/
            ├── config/
            │   └── config.go
            ├── consumer.go
            ├── consumer_test.go
            ├── rebuilder.go
            ├── rebuilder_test.go
            ├── spec.go
            └── service/
                ├── metering_collection_service.go
                └── metering_collection_service_test.go
```

---

## 3. Data Model

### 3.1 Schema Changes

新增 `metering_usage_records` 表和 `ani_metering_writer` 角色。

```sql
-- repo/deploy/migrations/20260731000100_metering_usage.sql
-- Depends on: 20260501000100_init_schema.sql (tenants 表)
-- 执行顺序: ROLE → TABLE → GRANT → RLS

BEGIN;

-- 0) 采集写侧专用角色（类比 init_schema.sql:25 ani_outbox_publisher BYPASSRLS）
CREATE ROLE ani_metering_writer BYPASSRLS NOLOGIN;

-- 1) 建表
CREATE TABLE metering_usage_records (
    id            BIGSERIAL PRIMARY KEY,
    tenant_id     UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    resource_ref  TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    period        TEXT NOT NULL,
    quantity      DOUBLE PRECISION NOT NULL,
    unit          TEXT NOT NULL,
    recorded_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, resource_ref, resource_type, period)
);

CREATE INDEX idx_meter_tenant_type_time
    ON metering_usage_records(tenant_id, resource_type, recorded_at);

-- 2) 授权给写侧角色
GRANT SELECT, INSERT, UPDATE, DELETE ON metering_usage_records TO ani_metering_writer;

-- 3) 读侧 RLS（展示 QueryUsage 走 ani_app_user，RLS 生效）
--    采集写侧走 ani_metering_writer（BYPASSRLS），不 FORCE RLS
ALTER TABLE metering_usage_records ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON metering_usage_records
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

COMMIT;
```

**与既有 `metering_records` 的边界**：`init_schema.sql` 已存在 `metering_records` 表（代码零引用），废弃清理走独立 migration，本 PR 不触碰。

### 3.2 Entity Definitions

**Port 层（pkg/ports/）**：

```go
// pkg/ports/instance_events.go [NEW]
package ports

type InstanceLifecycleEvent struct {
    InstanceID   string        `json:"instance_id"`
    TenantID     string        `json:"tenant_id"`
    WorkloadKind string        `json:"workload_kind"`
    NewStatus    string        `json:"new_status"`
    EventSeq     uint64        `json:"event_seq"`
    GPUSpec      *GPUEventSpec `json:"gpu_spec,omitempty"`
    ErrorMsg     string        `json:"error_msg,omitempty"`
}

type GPUEventSpec struct {
    Count int `json:"count"`
}
```

```go
// pkg/ports/metering.go [MODIFY: 新增类型，不改动现有 MeteringService]

// 新增类型
type CollectionSpec struct {
    ResourceRef   string
    TenantID      string
    WorkloadKind  string
    Dimensions    []CollectionDimension
    IntervalSec   int
    StartedAt     time.Time
    GPUSpec       *GPUEventSpec
}

type CollectionDimension struct {
    ResourceType MeteringResourceType
    Source       string
}

type MeteringCollectionService interface {
    StartCollection(ctx context.Context, spec CollectionSpec) error
    StopCollection(ctx context.Context, resourceRef string) error
}

// 扩展 MeteringUsageRecord（新增 ResourceRef 字段，现有 5 字段不变）
type MeteringUsageRecord struct {
    TenantID      string
    ResourceRef   string                // 新增
    ResourceType  MeteringResourceType
    TotalQuantity float64
    Unit          string
    Period        string
}
```

**Adapter 层（pkg/adapters/metering/）**：

```go
// pkg/adapters/metering/collectors.go [NEW]
type Collector interface {
    Collect(ctx context.Context, spec ports.CollectionSpec, period string) ([]ports.MeteringUsageRecord, error)
}

type DCGMGPUCollector struct{}
type KubeletCPUCollector struct{}
type KubeletMemCollector struct{}

func Resolve(collectorID string) (Collector, bool)
func CollectAll(ctx context.Context, spec ports.CollectionSpec, logger *slog.Logger) ([]ports.MeteringUsageRecord, error)
```

**Service 层（services/metering-service/internal/）**：

```go
// internal/service/metering_collection_service.go
type meteringCollectionService struct {
    mu            sync.Mutex
    tickers       map[string]*time.Ticker
    stopChs       map[string]chan struct{}
    specs         map[string]*ports.CollectionSpec
    everCollected map[string]bool
    db            *pgxpool.Pool
    logger        *slog.Logger
}

// internal/consumer.go
type Consumer struct {
    metering ports.MeteringCollectionService
    logger   *slog.Logger
    mu       sync.Mutex
    seenSeq  map[string]uint64
}

// internal/rebuilder.go
type Rebuilder struct {
    metadataStore ports.MetadataStore
    metering      ports.MeteringCollectionService
    logger        *slog.Logger
}

// internal/spec.go
func buildSpec(tenantID, instanceID, kind string, gpuCount int) ports.CollectionSpec
func parseGPUCount(gpuStatusJSON []byte) int
```

### 3.3 Relationships

- `metering_usage_records.tenant_id` → `tenants.id`（FK, ON DELETE CASCADE）
- `metering_usage_records.resource_ref` = `workload_instances.instance_id`（逻辑关联，无 FK）
- `metering_usage_records.resource_type` 枚举值对齐 `ports.MeteringResourceType` 常量

### 3.4 Migration Plan

| 步骤 | 操作 | 回滚 |
|------|------|------|
| 1 | `CREATE ROLE ani_metering_writer BYPASSRLS NOLOGIN` | `DROP ROLE ani_metering_writer` |
| 2 | `CREATE TABLE metering_usage_records` | `DROP TABLE metering_usage_records` |
| 3 | `GRANT ... TO ani_metering_writer` | 随表删除自动撤销 |
| 4 | `ENABLE RLS + CREATE POLICY` | `DROP POLICY + DISABLE RLS` |

迁移使用 `BEGIN/COMMIT` 事务包裹，失败自动回滚。所有 DDL 幂等（`IF NOT EXISTS` 可选，因新表首次创建）。

---

## 4. API Design

本 PRD 不涉及 OpenAPI 契约变更，纯 Core 内部服务 + DB migration + 部署清单。无 HTTP/gRPC 端点新增。

### 4.1 内部接口契约

| 接口 | 方法 | 签名 | 调用方 |
|------|------|------|--------|
| `MeteringCollectionService` | `StartCollection` | `(ctx, CollectionSpec) error` | Consumer、Rebuilder |
| `MeteringCollectionService` | `StopCollection` | `(ctx, resourceRef string) error` | Consumer |
| `Collector` | `Collect` | `(ctx, CollectionSpec, period string) ([]MeteringUsageRecord, error)` | `CollectAll` |
| `MessageBus` | `Subscribe` | `(SubscribeOptions, MessageHandler) (Subscription, error)` | main.go |

### 4.2 事件 Payload Schema

```json
{
  "instance_id": "inst-X",
  "tenant_id": "00000000-0000-0000-0000-000000000001",
  "workload_kind": "gpu_container",
  "new_status": "running",
  "event_seq": 1693000000000000000,
  "gpu_spec": { "count": 2 }
}
```

### 4.3 NATS Subscribe 配置

| 参数 | 值 | 说明 |
|------|-----|------|
| Subject | `ani.events.instance.>` | 通配符匹配所有 instance 子事件 |
| Consumer | `metering-consumer` | Durable consumer name |
| Queue | `""`（空） | 单副本无竞争，不设 Queue Group |
| MaxInflight | `1` | 强制串行消费保证顺序 |
| AckWait | `30s` | 未 Ack 超时重投 |
| MaxDeliver | `5` | 最大重投次数 |

---

## 5. Business Logic

### 5.1 Core Algorithms

**5.1.1 StartCollection 幂等**

```
输入: spec CollectionSpec
1. Lock mu
2. if tickers[spec.ResourceRef] exists → Unlock, return nil（进程内幂等）
3. if spec.StartedAt.IsZero() → spec.StartedAt = time.Now()
4. ticker = NewTicker(IntervalSec * Second)
5. stopCh = make(chan struct{})
6. tickers[ref] = ticker, specs[ref] = &spec, everCollected[ref] = false, stopChs[ref] = stopCh
7. Unlock mu
8. go runCollectionLoop(ctx, spec, ticker, stopCh)
9. return nil
```

**5.1.2 runCollectionLoop**

```
for {
  select {
  case <-ticker.C:
    records, err = CollectAll(ctx, spec, logger)
    if err != nil → log Error, continue（不停 ticker）
    persistRecords(ctx, spec.TenantID, records)
    if err != nil → log Error
    else if len(records) > 0:
      Lock mu, everCollected[ref] = true, Unlock mu
  case <-stopCh:
    ticker.Stop()
    return
  }
}
```

**5.1.3 StopCollection 幂等 + 保底采集**

```
输入: resourceRef string
1. Lock mu
2. if tickers[resourceRef] not exists → Unlock, return nil（幂等 no-op）
3. ticker = tickers[ref], stopCh = stopChs[ref], ever = everCollected[ref], spec = specs[ref]
4. ticker.Stop()
5. close(stopCh)
6. delete(tickers, ref), delete(stopChs, ref), delete(everCollected, ref), delete(specs, ref)
7. Unlock mu
8. 锁外保底采集:
   if !ever && spec != nil:
     records, err = collectFullLifetime(ctx, *spec)
     if err == nil → persistRecords(ctx, spec.TenantID, records)
9. return nil
```

**5.1.4 Consumer handleEvent seenSeq 乱序过滤**

```
输入: ctx, msg
1. headerTenant = msg.Headers()["tenant-id"][0]
2. err = json.Unmarshal(msg.Data(), &event)
   if err != nil → log Error, return nil（毒消息 Ack 跳过）
3. if event.TenantID != headerTenant → return error（Nak 重投）
4. Lock mu, last = seenSeq[event.InstanceID], seen = exists, Unlock mu
5. if seen && event.EventSeq <= last → log Warn, return nil（丢弃过期事件）
6. switch event.NewStatus:
   "running" → processErr = StartCollection(buildSpec(...))
   "stopped"/"failed"/"deleted" → processErr = StopCollection(event.InstanceID)
   default → log Warn, return nil（未知状态 Ack 跳过）
7. if processErr != nil → return processErr（Nak 重投，不推进 seenSeq）
8. Lock mu
   if event.EventSeq > seenSeq[event.InstanceID] → seenSeq[event.InstanceID] = event.EventSeq
   Unlock mu
9. return nil（Ack）
```

**5.1.5 Rebuilder Rebuild**

```
输入: ctx
1. WithPlatformTx(ctx, func(tx):
   rows = tx.Query("SELECT tenant_id::text, instance_id, workload_kind, gpu_status FROM workload_instances WHERE state='running' ORDER BY updated_at ASC")
   for rows.Next():
     scan tenantID, instanceID, kind, gpuStatusJSON
     gpuCount = parseGPUCount(gpuStatusJSON)
     spec = buildSpec(tenantID, instanceID, kind, gpuCount)
     err = StartCollection(ctx, spec)
     if err != nil → log Error（不阻塞，继续）
     count++
   return rows.Err())
2. if err != nil → return err
3. log Info("rebuild done", running_instances=count)
```

**5.1.6 CollectAll 三维度路由**

```
period = time.Now().Format("2006-01-02T15:04")  // 分钟对齐
out = []
for dim in spec.Dimensions:
  col, ok = Resolve(dim.Source)
  if !ok → log Warn, continue
  rec, err = col.Collect(ctx, spec, period)
  if err != nil → log Error, continue
  out = append(out, rec...)
return out, nil
```

**三维度计算公式与 PromQL**：

| 维度 | 指标类型 | PromQL | 累加语义 | 计算公式 | Unit |
|------|---------|--------|---------|---------|------|
| `instance_gpu_seconds` | Gauge（瞬时利用率） | `DCGM_FI_DEV_GPU_UTIL`（**不查**） | 占用时长 | `持有卡数 × interval_sec` | `gpu_second` |
| `instance_cpu_seconds` | Counter（累计秒） | `container_cpu_usage_seconds_total` | Counter 增量 | `rate(...[60s]) × 60` | `cpu_second` |
| `instance_memory_gib_seconds` | Gauge（瞬时占用） | `container_memory_working_set_bytes` | 瞬时占用加权时长 | `bytes / 1024^3 × 60` | `gib_second` |

**枚举映射**：
- `instance_gpu_seconds` → `ports.MeteringResourceInstanceGPUSeconds`
- `instance_cpu_seconds` → `ports.MeteringResourceInstanceCPUSeconds`
- `instance_memory_gib_seconds` → `ports.MeteringResourceInstanceMemorySeconds`

**GPU 占用时长不查 DCGM 的理由**：计量语义是"持有时长"而非"利用率"。实例持有 2 张 GPU 运行 60s = 120 gpu_seconds，与 GPU 利用率高低无关。`spec.GPUSpec == nil` 时跳过 GPU 维度（不写 0 错值）。

**5.1.7 buildSpec 维度映射**

```
dims = dimensionsFor(kind)  // 硬编码:
  //   gpu_container → [GPU+CPU+Mem]
  //   vm            → [CPU+Mem]
  //   container     → [CPU+Mem]
  //   其他          → [CPU+Mem]
spec = CollectionSpec{
  ResourceRef: instanceID, TenantID: tenantID, WorkloadKind: kind,
  Dimensions: dims, IntervalSec: 60, StartedAt: time.Now()
}
if gpuCount > 0 → spec.GPUSpec = &GPUEventSpec{Count: gpuCount}
return spec
```

**5.1.8 collectFullLifetime（保底采集）**

仅在 StopCollection 时且 `!everCollected`（该实例从未产出周期记录）时触发，写一次即止。与 `CollectAll`（逐周期、以 interval 为窗口）不同，它按**从 Start 到 Stop 的完整存活时长**计算一次性量。

```
输入: spec CollectionSpec（含 StartedAt）
1. elapsed = time.Now().Sub(spec.StartedAt)  // 实际存活秒数
2. if elapsed <= 0 → return nil（异常边界，不写）
3. period = time.Now().Format("2006-01-02T15:04")  // Stop 时刻分钟对齐
4. out = []
   for dim in spec.Dimensions:
     switch dim.ResourceType:
       GPU:
         if spec.GPUSpec == nil → skip
         quantity = float64(spec.GPUSpec.Count) * elapsed.Seconds()
         unit = "gpu_second"
       CPU:
         secs = promQueryCPU(ctx, spec.ResourceRef, spec.StartedAt, time.Now())  // rate[存活窗口]
         quantity = secs * elapsed.Seconds()
         unit = "cpu_second"
       Mem:
         bytes = promQueryMem(ctx, spec.ResourceRef, spec.StartedAt, time.Now())
         quantity = bytes / (1024^3) * elapsed.Seconds()
         unit = "gib_second"
     out = append(out, MeteringUsageRecord{...})
5. persistRecords(ctx, spec.TenantID, out)
   └── ON CONFLICT DO NOTHING 兜底：
       若实例存活不满一个周期，无周期采集记录，保底记录不会碰撞
       若恰好已有周期记录，DB UNIQUE 约束丢弃保底记录（已有更精确数据，无需保底）
6. return nil
```

**单副本对保底采集的影响**：单副本下 created 和 stopped 都在同一进程，`everCollected`/`specs`/`ticker` 一定存在且一致，保底采集的状态管理安全可行。

### 5.2 Validation Rules

| 规则 | 实现位置 | 约束 |
|------|----------|------|
| 租户上下文一致性 | `handleEvent` step 3 | `msg.Headers()["tenant-id"]` == `event.TenantID`，不一致 → Nak |
| EventSeq 单调递增 | `handleEvent` step 5 | `event.EventSeq > seenSeq[instance_id]` 才处理 |
| GPU 卡数缺失 | `DCGMGPUCollector.Collect` | `spec.GPUSpec == nil` → return nil（跳过，不写 0） |
| 未知 collector source | `CollectAll` | `Resolve` 返回 false → Warn 跳过 |
| 未知 new_status | `handleEvent` step 6 | default → Warn，return nil（Ack 跳过） |

### 5.3 State Machine

**实例采集状态**（进程内隐式状态机）：

```
                    StartCollection
  [无 ticker] ──────────────────────► [有 ticker, everCollected=false]
                                               │
                                               │ 首次 ticker.C → persistRecords 成功
                                               ▼
                                          [有 ticker, everCollected=true]
                                               │
                                               │ StopCollection
                                               ▼
                                    [无 ticker]（保底采集 if !ever）
```

### 5.4 Edge Cases

| 场景 | 处理 |
|------|------|
| 短生命周期（<1 周期）实例 | StopCollection 时 `!everCollected` → `collectFullLifetime` 补采全周期量 |
| 重复 StartCollection 同一实例 | 进程内 map 已有 → no-op；DB UNIQUE 兜底 |
| 重复 StopCollection 同一实例 | 进程内 map 无 ticker → no-op |
| 进程崩溃后重启 | rebuilder 重建 running 实例 + DeliverAll 回放未 Ack 消息 |
| seenSeq 重启归零 | 接受此边界，durable consumer 从服务端 Ack 进度继续 |
| 重建失败 | 不阻塞，日志告警，靠事件增量 + DeliverAll 兜底 |
| Prometheus 不可用 | CollectAll 单维度失败 → Error 日志跳过，不停 ticker，下个周期重试 |
| 毒消息（json 畸形） | return nil → Ack 跳过 |
| 租户上下文不匹配 | return error → Nak 重投 |
| 崩溃窗口 instance.created 重投 | 重建已建 ticker → StartCollection 幂等 no-op |
| 崩溃窗口 instance.failed 重投 | 重建时该实例不在 running 列表 → StopCollection 无 ticker no-op |
| collectFullLifetime 与已有周期碰撞 | `ON CONFLICT DO NOTHING` 兜底丢弃保底记录 |

---

## 6. Error Handling

### 6.1 Error Taxonomy

| 场景 | handler 返回值 | adapter 行为 | seenSeq 推进 | 日志级别 |
|------|---------------|-------------|-------------|---------|
| 处理成功 | `nil` | Ack | 是 | Info/无 |
| StartCollection/StopCollection 失败 | `error` | Nak 重投 | 否 | Error |
| 租户上下文不匹配 | `error` | Nak 重投 | 否 | Error |
| json.Unmarshal 失败（毒消息） | `nil` | Ack 跳过 | 否 | Error |
| 过期事件（seq <= last） | `nil` | Ack 跳过 | 否 | Warn |
| 未知 new_status | `nil` | Ack 跳过 | 否 | Warn |
| handler panic | — | recover → Nak | 否 | Error |

### 6.2 Retry Strategy

| 操作 | 重试机制 | 最大次数 | 退避 |
|------|----------|---------|------|
| 事件处理（可恢复失败） | NATS Nak → JetStream 自动重投 | MaxDeliver=5 | AckWait=30s |
| 采集（CollectAll 失败） | 不停 ticker，下个周期自动重试 | 无限 | IntervalSec=60s |
| 持久化（persistRecords 失败） | 不停 ticker，下个周期自动重试 | 无限 | IntervalSec=60s |
| 重建（Rebuild 失败） | 不阻塞启动，下次重启自动补齐 | — | — |

### 6.3 Failure Modes

| 依赖故障 | 影响 | 降级策略 |
|---------|------|---------|
| Core DB 不可用 | persistRecords 失败、重建失败 | ticker 不停，下个周期重试；重建不阻塞启动 |
| NATS 不可用 | Subscribe 失败 → os.Exit(1) | K8s 自动重启 |
| Prometheus 不可用 | CollectAll 单维度失败 | 跳过该维度，其余维度正常采集 |
| Redis 不可用 | 无影响（metering-service 不依赖 Redis） | — |

---

## 7. Security

### 7.1 Authentication & Authorization

| 角色 | 权限 | 用途 |
|------|------|------|
| `ani_metering_writer` | BYPASSRLS, SELECT/INSERT/UPDATE/DELETE on `metering_usage_records` | 采集写侧，绕过 RLS 跨租户写入 |
| `ani_app_user` | RLS 生效, SELECT on `metering_usage_records` | 读侧（QueryUsage），租户隔离过滤 |

- `ani_metering_writer` 是 `NOLOGIN` 角色，仅供 metering-service 以成员身份使用
- `metering_usage_records` ENABLE RLS 但不 FORCE RLS：写侧 `ani_metering_writer` BYPASSRLS 绕过，读侧 `ani_app_user` RLS 生效

### 7.2 Input Validation

| 输入 | 校验规则 | 失败处理 |
|------|----------|---------|
| NATS message payload | `json.Unmarshal` | 失败 → Ack 跳过（毒消息） |
| tenant-id header | 与 payload `tenant_id` 一致 | 不一致 → Nak 重投 |
| event_seq | uint64, 按 instance_id 单调递增 | 过期 → 丢弃 |
| new_status | running/stopped/failed/deleted | 未知 → Ack 跳过 |

### 7.3 Data Protection

- 租户隔离：写入侧由 `spec.TenantID` 显式带租户（BYPASSRLS），读侧 RLS 自动过滤
- SQL 注入防护：所有 SQL 查询使用参数化（`pgx` 占位符），无字符串拼接
- `gpu_status` JSONB 解析使用标准 `encoding/json`，无动态 SQL 构造

---

## 8. Performance

### 8.1 Expected Load

| 指标 | 预估值 | 依据 |
|------|--------|------|
| 实例规模 | 数百级 | plan §7.3 |
| 事件频率 | 低频生命周期事件 | created/stopped/failed/deleted |
| 采集频率 | 每实例每 60s 一次 | IntervalSec=60 |
| 并发 ticker 数 | ≈ running 实例数 | 进程内 map |
| 单次采集 DB 写入 | 1-3 行（按维度数） | ON CONFLICT DO NOTHING |

### 8.2 Optimization Strategy

- **MaxInflight=1**：低频事件场景吞吐足够，串行消费保证顺序
- **ON CONFLICT DO NOTHING**：避免重复写入的 upsert 开销
- **批量写入**：`persistRecords` 单次 INSERT 多行（维度数=2-3，无需额外批处理）
- **单副本**：无跨副本协调开销

### 8.3 Database Considerations

- UNIQUE 约束 `(tenant_id, resource_ref, resource_type, period)` 作为写入幂等 DB 层
- 索引 `idx_meter_tenant_type_time (tenant_id, resource_type, recorded_at)` 支持读侧按租户+类型+时间查询
- `workload_instances` 表已有索引 `idx_workload_instances_kind (tenant_id, workload_kind, state)` 支持 state 过滤
- 重建查询 `WHERE state='running' ORDER BY updated_at ASC` 走已有索引

---

## 9. Testing Strategy

### 9.1 Unit Tests

| 测试文件 | 覆盖组件 | 关键场景 |
|---------|---------|---------|
| `metering_collection_service_test.go` | `meteringCollectionService` | Start 幂等、Stop 幂等、保底采集触发、collectFullLifetime 计算、persistRecords ON CONFLICT |
| `consumer_test.go` | `Consumer` | 成功推进 seenSeq、失败不推进、过期事件丢弃、毒消息 Ack 跳过、租户不匹配 Nak 重投、未知状态 Ack 跳过 |
| `rebuilder_test.go` | `Rebuilder` | WithPlatformTx 调用、running 实例建 ticker、gpu_status JSONB 解析、单实例失败不阻塞 |
| `collectors_test.go`（在 `pkg/adapters/metering/`） | 三个 Collector + CollectAll | DCGM GPU 卡数缺失跳过、Prometheus 查询 mock、CollectAll unknown source 跳过、单维度失败不中断 |

**Mock 策略**：
- `MeteringCollectionService` → mock 实现 port 接口
- `MetadataStore` → mock 实现 `WithPlatformTx`
- `*pgxpool.Pool` → `pgxmock` 或真实 test DB
- Prometheus → HTTP mock server

### 9.2 Integration Tests

| 场景 | 验证点 |
|------|--------|
| 1. 事件驱动采集 | 发布 instance.created(running) → StartCollection → ticker 产出记录写入 DB |
| 2. 停止采集 + 保底 | 发布 instance.stopped → StopCollection → ticker 停止 → 短生命周期保底采集触发 |
| 3. 幂等 no-op | 重复发布 instance.created 同一实例 → 进程内 map 幂等，DB 无重复行 |
| 4. 重建 + DeliverAll | consumer 重启 → rebuilder 重建 running 实例 ticker → DeliverAll 回放补齐 |
| 5. seenSeq 乱序 | 先发 seq=5 再发 seq=3 → seq=3 被丢弃 |
| 6. seenSeq 失败重投 | StartCollection 失败 → Nak 重投 → seenSeq 未推进 → 重投后重新处理 |
| 7. 租户校验 | tenant-id header 与 payload 不匹配 → Nak 重投 |
| 8. 毒消息 | json 畸形 → Ack 跳过 |
| 9. DB UNIQUE 兜底 | 同实例同维度同周期重复 INSERT → ON CONFLICT DO NOTHING |

**集成测试环境**：真实 NATS JetStream + 真实 PG（含 migration）+ 真实 Prometheus

### 9.3 Edge Case Tests

见 5.4 Edge Cases 表，每个边界场景对应至少一个测试用例。

### 9.4 Acceptance Criteria Mapping

| US/FR | 测试 | 类型 | 描述 |
|-------|------|------|------|
| US-001 / FR-1~3 | migration 执行 | integration | 表+角色+RLS 创建成功 |
| US-002 / FR-4~6 | port 接口编译 | unit | CollectionSpec/CollectionDimension/MeteringCollectionService 定义正确 |
| US-003 / FR-7~15 | meteringCollectionService | unit | Start/Stop 幂等、保底采集、persistRecords |
| US-004 / FR-16~22 | Collector + CollectAll | unit | 三维度采集、GPU 缺失跳过、unknown source 跳过 |
| US-005 / FR-23 | buildSpec | unit | 维度映射、GPUSpec 设置 |
| US-006 / FR-24~29 | main.go 启动 | integration | bootstrap.MustConnect、先重建后订阅、Subscribe 配置 |
| US-007 / FR-30~34 | consumer handleEvent | unit | seenSeq 推进/不推进、乱序、毒消息、租户校验 |
| US-008 / FR-35~37 | rebuilder Rebuild | unit | WithPlatformTx、gpu_status 解析、单实例失败不阻塞 |
| US-009 | 集成测试 9 场景 | integration | 端到端链路验证 |
| US-010 / FR-38 | 部署清单 | manual | replicas:1、ServiceAccount、env、securityContext |

---

## 10. Implementation Plan

### 10.1 Phases

| Phase | PR | 内容 | 依赖 |
|-------|-----|------|------|
| 1 | PR-M1 | migration + MeteringCollectionService port + meteringCollectionService 实现 + go.mod | 无 |
| 2 | PR-M2 | Collector 接口 + 3 实现 + CollectAll + 单测 | PR-M1 |
| 3 | PR-M3 | main.go + consumer + rebuilder + buildSpec + DeliverAll | PR-M1, PR-M2 |
| 4 | PR-M4 | 集成测试 9 场景 + 端到端验证 | PR-M3 |
| 5 | PR-M5 | 部署清单 metering-service-live-deps.yaml | PR-M4 |

### 10.2 Issue Mapping

| Issue | SPEC Sections | Priority | Depends On |
|-------|--------------|----------|------------|
| PR-M1 (US-001~003) | 3.1, 3.2, 3.4, 5.1.1~5.1.3 | high | — |
| PR-M2 (US-004~005) | 3.2, 5.1.6~5.1.7, 5.2 | high | PR-M1 |
| PR-M3 (US-006~008) | 2.3, 3.2, 5.1.4~5.1.5 | high | PR-M1, PR-M2 |
| PR-M4 (US-009) | 9.2 | medium | PR-M3 |
| PR-M5 (US-010) | 2.4, 部署清单 | medium | PR-M4 |

### 10.3 Incremental Delivery

- PR-M1 纯新增表 + port 扩展，零侵入，可独立 review
- PR-M2 Collector 实现可独立测试（mock Prometheus）
- PR-M3 主进程可本地启动验证（连接真实 NATS + DB）
- PR-M4 集成测试在 CI 环境验证端到端
- PR-M5 部署清单在 REAL-K8S-LAB live gate 验证

---

## 11. Open Questions & Risks

### 11.1 Unresolved Questions

- 上游 reconciler 写 outbox + instance.* 事件的落地时间表？本 SPEC 假设其已就绪，若延迟需调整 PR-M3/M4 的验证策略
- `METERING_PROMETHEUS_URL` 在 REAL-K8S-LAB 环境的具体地址？部署清单中为明文 env，live gate 时需确认
- `ani-metering-runtime` secret 的预置流程是否完全复用 `ani-auth-production-shaped-runtime`？需在 PR-M5 部署时确认

### 11.2 Technical Risks

| Risk | Impact | Mitigation |
|------|--------|-----------|
| 上游事件未落地 | consumer 无消息可消费 | PR-M3 可用 mock publisher 验证 |
| seenSeq 重启归零 | 跨生命周期极端场景误判 | 接受边界，durable consumer Ack 进度兜底 |
| 单副本故障 | 采集空白窗口 | K8s 自动重启（秒级），DeliverAll 回放 |
| Prometheus 不可用 | CPU/Mem 维度缺失 | GPU 维度不受影响（不查 Prometheus） |
| pgx 依赖版本不兼容 | 编译失败 | go.mod 锁定 `v5.9.2`（与 auth-service 一致） |

### 11.3 Assumptions

- `bootstrap.MustConnect` 返回的 `Deps.DB` 类型为 `*pgxpool.Pool`，可直接传入 `meteringCollectionService`
- `deps.Ports.MessageBus` 已注入 NATS adapter 实现
- `deps.Ports.Metadata` 已注入 PostgreSQL MetadataStore 实现，支持 `WithPlatformTx`
- 上游 `instance.*` 事件 payload 符合 `InstanceLifecycleEvent` schema
- `event_seq` 按 instance_id 严格单调递增（纳秒级时间戳）
- `stopped`/`failed`/`deleted` 终止事件必达
- NATS stream `ani-events` 已创建且 retention 足够覆盖崩溃窗口

---

## 12. Frozen Facts Table (Core)

| Item | Status | Reference |
|------|--------|-----------|
| OpenAPI 变更 | 无 | 本 PRD 不涉及 `v1.yaml` 变更 |
| 新增 HTTP/gRPC 端点 | 无 | 纯 Core 内部服务，无对外 API |
| idempotency_key | N/A | 无有副作用的 POST/PUT/PATCH 端点 |
| Frozen Paths | N/A | 不触碰 Core API 契约 |
| Frozen Schemas | N/A | 不触碰 Core API 契约 |
| Non-Frozen Capabilities | `MeteringCollectionService` port（新增） | `pkg/ports/metering.go` |
| Known Risky Assumptions | 上游事件生产侧未落地 | 见 11.1 |
