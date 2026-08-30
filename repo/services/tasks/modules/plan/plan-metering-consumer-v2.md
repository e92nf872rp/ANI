# 计量 Consumer 落地方案 · 事件驱动周期采集(V2)

> 状态: 设计定稿(grill-me 三轮头脑风暴收敛,待实现评审)
> 创建日期: 2026-08-11
> 适用范围: ANI Core 内部服务 metering-service(平台支撑层)
> 关联:
> - [plan-metering-consumer.md](./plan-metering-consumer.md)(V1 历史版本,保留归档,不再作为实施依据)
> - [handoff-metering-consumer.md](./handoff-metering-consumer.md)(V1 交接文档,记录 V1 待修改项,V2 已覆盖)
> 上游前置: `reconciler 写 outbox + instance.* 事件`(本方案假设其已就绪,不在本方案范围)

---

## 0. 本方案与 V1 的关系

**本方案(V2)是唯一实施依据,完整覆盖并替代 V1。** V1 `plan-metering-consumer.md` 降为历史版本保留归档,不再作为实施依据。

V2 相对 V1 的三项关键变更:
1. **metering-service 定位为 Core 内部服务**:仿 auth-service 范式,main.go 用 `bootstrap.MustConnect` 启动,重建走直接查 DB,不走 Core SDK HTTP
2. **seenSeq 推进时机修正**:V1 在处理前推进 seq 导致处理失败 Nak 重投时被误判过期永久丢失;V2 改为处理成功后才推进
3. **单副本约束**:声明 consumer 只能单副本运行,`MaxInflight=1` 强制串行消费

其余设计(三个 Collector 分类累加语义、metering_usage_records 表、事件契约、PR 切分等)沿用 V1 已收敛结论,V2 完整重写以保持独立可读。

---

## 1. 现状基线

| 项 | 状态 | 影响 |
|---|---|---|
| MessageBus port(Ack/Nak/Headers) | 已落地 | consumer 可用 |
| NATS adapter Publish(写 headers) | 已落地 | envelope 元数据进 headers |
| NATS adapter Subscribe(业务层 Ack) | 已落地,但 **metering consumer 零生产调用方** | consumer 需从零接线 |
| NATS adapter 死信 DLQ | **未实现** | 靠 MaxDeliver 限制,超出由 JetStream 处理 |
| outbox publisher(task-service) | 已落地 | 但仅 task-service 启动,且只发 task 流事件 |
| reconciler 写 outbox(instance 事件) | **未落地** | 本方案前置缺失,假设上游 PR 已就绪 |
| `instance.created/failed/deleted/stopped` 事件 | **未落地** | 本方案定义其 consumer 侧契约 |
| `MeteringCollectionService.StartCollection/StopCollection` | **未落地** | 本方案落地 |
| `metering_usage_records` 表 | **migration 不存在** | 本方案新增 |
| `LocalMeteringService` | 纯内存,无持久化 | 本方案为采集侧替换为持久化写入实现;读取侧接表属后续批次(见 §4.1) |
| `metering-service` main.go | **不存在** | 本方案新建独立进程 |
| `bootstrap.MustConnect` | 已落地 | 返回 `*Deps`(DB/Ports.MessageBus/Ports.WorkloadStore),Core 内部服务启动范式 |
| auth-service 范式 | 已落地 | main 用 bootstrap,DB 直接下发 service 子 store 执行原生 SQL |
| `WorkloadInstanceStore` | 已落地,只有 List(tenantID, kind) | 无按 state 查询方法,重建需直接原生 SQL |
| `workload_instances` 表 | 已落地,FORCE RLS | state 字段有索引,跨租户查询需 WithPlatformTx 绕 RLS |
| `InstanceMetricsRecord` | 已落地(在 instance_observability.go) | Collector 复用其字段 |
| `PrometheusInstanceObservability` | 已落地 | 已有 DCGM/kubelet/kubevirt PromQL |

**核心结论**:消息总线骨架就绪,但消费侧完全悬空。本方案从零搭 consumer 进程 + 消费链路。

---

## 2. 架构设计

### 2.1 定位:Core 内部服务(平台支撑层)

**metering-service 是 ANI Core 内部服务(平台支撑层),不是 Services 层业务服务。**

依据:
- ANI-02 §2.5 把"用量计量"列为 Core 平台支撑层,与可观测性、审计日志同级
- 仿照 auth-service、reconcile-worker 范式:物理位置在 `services/metering-service/`,逻辑归属 Core
- Core 内部服务可直连 Core DB(已验证 auth-service 的子 store 直接持有 `*pgxpool.Pool` 执行原生 SQL)
- 不受 CLAUDE.md §3.2 "Services 禁止 import Core 代码包"约束——该约束针对 Services 层业务服务

### 2.2 分层结构

```
物理位置(services/ 下,独立 module)        逻辑归属(Core 内部服务)
services/metering-service/                  pkg/
├── main.go                                 ├── ports/metering.go(扩展)
├── internal/                               ├── ports/instance_events.go(新增)
│   ├── config/config.go                    ├── adapters/metering/collectors.go(新增)
│   ├── service/                            └── adapters/runtime/
│   │   metering_collection_service.go ←─┐      (已有 prometheus_instance_observability.go)
│   │   (Start/StopCollection)          │
│   ├── consumer.go                     │
│   └── rebuilder.go                    │
└── go.mod (独立 module)               │
                                       └── 持有 *pgxpool.Pool 直连 Core DB(仿 auth-service)
```

- `meteringCollectionService` 实现在 `internal/service/metering_collection_service.go`,自己持有 `*pgxpool.Pool`,执行原生 SQL 写 `metering_usage_records`
- `pkg/adapters/metering/collectors.go` 放 Collector 接口 + 三个 Collector 实现 + `CollectAll` 路由函数(查询 Prometheus 的纯计算逻辑,无 DB 交互)

### 2.3 运行形态:单副本独立进程

```
┌─────────────────────────────────────────────────────┐
│ metering-service 进程(新建 main.go,replicas:1)     │
│                                                     │
│  ┌──────────────┐   ┌────────────────────────────┐  │
│  │ NATS         │   │ 重建协调器(启动一次)        │  │
│  │ Consumer     │   │  直接查 workload_instances  │  │
│  │ (常驻订阅)   │   │  WHERE state='running'      │  │
│  │              │   │  → 建 ticker                │  │
│  │ 事件 →       │   └────────────────────────────┘  │
│  │  Start/Stop  │                                  │
│  │  Collection  │──→│ MeteringCollectionService  │  │
│  │              │   │  (内存 map[ref]ticker)     │  │
│  │ MaxInflight=1│   │  + Collector 策略路由       │  │
│  │ (串行消费)   │   │  + 写 metering_usage_records│ │
│  └──────────────┘   └────────────────────────────┘  │
└─────────────────────────────────────────────────────┘
        ↑ NATS Subscribe                ↑ 直接查 DB(WithPlatformTx 绕 RLS)
        │                               │
   JetStream stream               Core DB(workload_instances)
```

- consumer 进程常驻,订阅 NATS subject,事件驱动 Start/Stop Collection
- 启动时先重建 ticker(直接查 DB,见 §6.2),再订阅 NATS(见 §6.2 启动协议)
- 进程内 `map[resourceRef]*ticker` 去重(StartCollection 幂等的进程内层)
- DB `metering_usage_records` 的 UNIQUE 约束做写入兜底(StartCollection 幂等的 DB 层)
- **单副本约束**(见 §7):replicas:1,MaxInflight=1 串行消费

**前置依赖**:
1. **`event_seq` 必带且按 instance_id 单调递增**(乱序判定依据,见 §3.1 契约)。生成建议:发送端用单调时间戳(纳秒级)保证对同一 instance 严格递增,同毫秒碰撞时补递增子序号;避免墙钟回拨导致的 seq 回退。
2. **`stopped`/`failed`/`deleted` 事件必达**:重建只负责补齐 running 实例,实例生命周期终结事件必须可靠送达。

### 2.4 事件契约

consumer 依赖的最小 `InstanceLifecycleEvent` payload schema:

```go
// pkg/ports/instance_events.go(新增)
package ports

type InstanceLifecycleEvent struct {
    InstanceID   string `json:"instance_id"`              // 必需,StartCollection 的 resource_ref
    TenantID     string `json:"tenant_id"`                // 必需,租户上下文
    WorkloadKind string `json:"workload_kind"`            // 必需,决定采哪些维度
    NewStatus    string `json:"new_status"`               // 必需,running/stopped/failed/deleted
    EventSeq     uint64 `json:"event_seq"`                // 必需:按 instance_id 单调递增
    GPUSpec      *GPUEventSpec `json:"gpu_spec,omitempty"` // 可选,仅 gpu_container 携带
    ErrorMsg     string `json:"error_msg,omitempty"`      // 仅 failed 携带
}

type GPUEventSpec struct {
    Count int `json:"count"`
}
```

**Subject 命名**(对齐代码库实际行为):
- `PublishOptions.Subject` 决定投递路径,`event.EventType` 只写入 envelope 元数据
- consumer 订阅 subject:`ani.events.instance.>`(`>` 通配符匹配所有 instance 子事件)
- 上游发布 subject:`ani.events.instance.<instance_id>`(如 `ani.events.instance.inst-X`)
- `event.EventType` 仍用 `instance.created`/`instance.stopped`/`instance.failed`/`instance.deleted` 作为 envelope 元数据,payload 中的 `new_status` 字段是 consumer 路由依据

| `event.EventType`(envelope 元数据) | `new_status`(payload) | consumer 动作 |
|---|---|---|
| `instance.created` | `running` | `StartCollection` |
| `instance.stopped` | `stopped` | `StopCollection` |
| `instance.failed` | `failed` | `StopCollection` |
| `instance.deleted` | `deleted` | `StopCollection` |

**Headers 用法**(NATS adapter 已写 envelope 元数据到 headers):
- adapter Publish 自动写 5 个 headers:`tenant-id`、`aggregate-id`、`aggregate-type`、`event-type`、`occurred-at`
- consumer 从 `Message.Headers()["tenant-id"]` 读租户 ID,与 payload `tenant_id` 校验一致
- 不一致时记 Error 日志并返回 error → adapter Nak 重投

---

## 3. consumer 主循环

### 3.1 对齐当前 MessageBus 契约

现有 `ports.MessageBus.Subscribe(opts, handler)` 无 ctx 参数、`ports.Message` 不暴露 Ack/Nak 方法。Ack/Nak 由 adapter 根据 handler 返回值统一执行:
- 返回 `nil` → adapter 调 Ack(消息已处理)
- 返回 `error` → adapter 调 Nak(消息重投)
- panic → adapter recover 后调 Nak(兜底)

handler 收到的 ctx 固定为 `context.Background()`,需超时自行 `context.WithTimeout`。

### 3.2 consumer 主循环(seenSeq 修正:成功才推进)

```go
// services/metering-service/internal/consumer.go(新增)
type Consumer struct {
    metering ports.MeteringCollectionService
    logger   *slog.Logger
    mu       sync.Mutex
    seenSeq  map[string]uint64 // per-instance 已见最大 event_seq(乱序判定)
}

func (c *Consumer) handleEvent(ctx context.Context, msg ports.Message) error {
    // 1. 租户上下文校验
    headerTenant := ""
    if hs, ok := msg.Headers()["tenant-id"]; ok && len(hs) > 0 {
        headerTenant = hs[0]
    }
    var event ports.InstanceLifecycleEvent
    if err := json.Unmarshal(msg.Data(), &event); err != nil {
        c.logger.Error("unmarshal failed", "err", err)
        return nil // 毒消息:记日志跳过,返回 nil(不重投)
    }
    if event.TenantID != headerTenant {
        c.logger.Error("tenant mismatch", "header", headerTenant, "payload", event.TenantID)
        return fmt.Errorf("tenant mismatch") // 数据完整性问题:返回 error(adapter Nak 重投)
    }

    // 1.5 乱序过滤(V2 修正):先判定是否过期,过期直接丢弃;
    //     未过期则处理,处理成功后才推进 seenSeq。
    //     V1 的 BUG 是处理前就推进,StartCollection 失败 Nak 重投时
    //     seq<=seen 被误判过期 → 永久丢失事件。
    c.mu.Lock()
    last, seen := c.seenSeq[event.InstanceID]
    c.mu.Unlock()
    if seen && event.EventSeq <= last {
        c.logger.Warn("stale event dropped", "ref", event.InstanceID, "seq", event.EventSeq, "last", last)
        return nil
    }

    // 2. 路由到 Start/Stop(处理逻辑)
    var processErr error
    switch event.NewStatus {
    case "running":
        gpuCount := 0
        if event.GPUSpec != nil {
            gpuCount = event.GPUSpec.Count
        }
        processErr = c.metering.StartCollection(ctx, buildSpec(event.TenantID, event.InstanceID, event.WorkloadKind, gpuCount))
    case "stopped", "failed", "deleted":
        processErr = c.metering.StopCollection(ctx, event.InstanceID)
    default:
        c.logger.Warn("unknown status, skip", "status", event.NewStatus)
        return nil // 未知状态:Ack 跳过,不推进 seenSeq(无害,幂等兜底)
    }

    if processErr != nil {
        // 处理失败:不推进 seenSeq,返回 error 触发 Nak 重投
        // 重投时 seenSeq[inst] 仍为旧值,event.EventSeq > last → 不会被误判过期
        return processErr
    }

    // 3. 处理成功:推进 seenSeq(V2 修正核心)
    c.mu.Lock()
    if event.EventSeq > c.seenSeq[event.InstanceID] {
        c.seenSeq[event.InstanceID] = event.EventSeq
    }
    c.mu.Unlock()

    return nil
}
```

### 3.3 seenSeq 修正的故障路径对比

| 场景 | V1(处理前推进) | V2(成功才推进) |
|---|---|---|
| StartCollection 成功 | seenSeq 推进,Ack | seenSeq 推进,Ack |
| StartCollection 失败 | seenSeq 已推进 → Nak 重投 → `seq<=seen` 误判过期 → **永久丢失** | seenSeq 不推进 → Nak 重投 → `seq>last` 判定非过期 → 重新处理 |

### 3.4 MaxInflight=1 强制串行消费(V2 新增)

`MaxInflight > 1` 允许多条消息并发在途,JetStream 不保证投递顺序——seq=5 可能先于 seq=3 到达。seenSeq 高水位会把后到的 seq=3 误判过期丢弃。

**MaxInflight=1 强制串行消费**:JetStream 一次只投一条,前一条 Ack 后才投下一条。配合 seenSeq 严格递增判定,实现**完全有序的事件处理**。

```go
// main.go 中的 Subscribe 配置(见 §6.2)
SubscribeOptions{
    MaxInflight: 1, // ← V2:强制串行消费保证顺序
    ...
}
```

MaxInflight=1 的吞吐降低对低频生命周期事件(数百实例规模)完全够用。

### 3.5 seenSeq 重启归零(已知边界,接受)

`seenSeq` 是进程内存态,重启归零。常规场景无风险:JetStream durable consumer 从服务端 Ack 进度继续,已 Ack 的不重投,未 Ack 的重投当新事件处理(幂等兜底)。

真正会出错的仅一种运维极端:实例跨多段生命周期,且一条超久未 Ack 的陈旧旧事件在 retention 内被回放,而此时实例正处于新生命周期——归零会让旧 seq 误判为新事件。

**接受此边界,不持久化 seenSeq**。理由:引入持久化需新增表 + 启动恢复 + 运行时同步写,复杂度收益比不成立(Karpathy 原则五)。

---

## 4. 数据模型

### 4.1 metering_usage_records(新增 migration)

通用计量落地表。一张表覆盖所有维度。

```sql
-- repo/deploy/migrations/20260731000100_metering_usage.sql
-- 执行顺序:ROLE → TABLE → GRANT
-- 0) 采集写侧专用角色(类比 init_schema.sql:26 创建 ani_outbox_publisher BYPASSRLS)
CREATE ROLE ani_metering_writer BYPASSRLS NOLOGIN;

-- 1) 建表
CREATE TABLE metering_usage_records (
    id            BIGSERIAL PRIMARY KEY,
    tenant_id     UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    resource_ref  TEXT NOT NULL,          -- instance_id
    resource_type TEXT NOT NULL,          -- 枚举值对齐 ports.MeteringResourceType:
                                          --   'instance_gpu_seconds'
                                          --   'instance_cpu_seconds'
                                          --   'instance_memory_gib_seconds'
    period        TEXT NOT NULL,           -- '2026-07-31T10:00'(聚合周期标识,分钟对齐)
    quantity      DOUBLE PRECISION NOT NULL,
    unit          TEXT NOT NULL,          -- 'gpu_second' | 'cpu_second' | 'gib_second'
    recorded_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, resource_ref, resource_type, period)  -- StartCollection 幂等的 DB 层
);
CREATE INDEX idx_meter_tenant_type_time
    ON metering_usage_records(tenant_id, resource_type, recorded_at);

-- 2) 授权给写侧角色(必须在 CREATE TABLE 之后)
GRANT SELECT, INSERT, UPDATE, DELETE ON metering_usage_records TO ani_metering_writer;

-- 3) 读侧(展示 QueryUsage)走普通 ani_app_user,RLS 生效防越权
--    采集写侧走 ani_metering_writer(BYPASSRLS),不 FORCE:
--    RLS 只做读侧过滤,写入侧由 spec.TenantID 显式带租户
ALTER TABLE metering_usage_records ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON metering_usage_records
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);
```

**RLS 职责拆分**:
- 采集 `persistRecords` 用 `ani_metering_writer`(`BYPASSRLS`),绕过 RLS
- 展示 `QueryUsage` 用普通 `ani_app_user`,RLS 生效
- 不 FORCE RLS

**UNIQUE 约束作用**:同一实例同一维度同一周期只能有一行。ticker 重启重建后重复写入靠 `ON CONFLICT DO NOTHING` 兜底,实现写入幂等。

**字段映射**:表列 `quantity` 对应 Go struct `MeteringUsageRecord.TotalQuantity`(`persistRecords` 的 INSERT 语句列名用 `quantity`,Go 赋值用 `record.TotalQuantity`)。命名差异是历史遗留——port struct 早已叫 `TotalQuantity`,本方案沿用不改名。

> **与既有 `metering_records` 的边界**:init_schema 已存在 `metering_records` 表(代码零引用)。该表"不要了",但删除不在本 PR——本 PR 只新增 `metering_usage_records`,废弃清理走独立 migration。

### 4.2 不建 metering_collections 表

不新建活跃采集任务表用于重启重建:
- 该表是实例状态的本地副本,会与 Core `workload_instances.state` 漂移
- 漂移后仍需校对,不如直接查 DB
- 新增表 = 新增真相源,违反"PG workload_instances 唯一 source of truth"

重建改为直接查 `workload_instances` 表(见 §6.2)。

---

## 5. Port 契约扩展

### 5.1 新增 MeteringCollectionService 接口

现有 `MeteringService` 保留 `QueryUsage`/`ReportTokenUsage`(查询/上报语义)。本方案新增 `MeteringCollectionService` 专管 consumer 的周期采集生命周期,与查询语义分离。

```go
// pkg/ports/metering.go(扩展)
type CollectionSpec struct {
    ResourceRef   string                 // instance_id
    TenantID      string
    WorkloadKind  string                 // 决定采哪些维度(见 §9 取舍 1)
    Dimensions    []CollectionDimension  // 该资源要采哪些维度(一个实例可多维度)
    IntervalSec   int                    // 默认 60
    StartedAt     time.Time              // V2 新增:Start 时间,供 collectFullLifetime 计算
    GPUSpec       *GPUEventSpec          // V2 新增:GPU 卡数(事件/重建带下),nil 则跳过 GPU 维度
}

type CollectionDimension struct {
    ResourceType MeteringResourceType     // ports.MeteringResourceInstanceGPUSeconds/CPUSeconds/MemorySeconds
    Source       string                   // Collector 路由 key:'dcgm_gpu' | 'kubelet_cpu' | 'kubelet_mem'
}

// MeteringCollectionService 专管事件驱动的周期采集生命周期(Start/Stop ticker)。
// 与 MeteringService(查询/上报)分离,因为消费方不同:consumer 只需采集控制,不需查询。
type MeteringCollectionService interface {
    // 事件驱动周期采集
    // 幂等:进程内 map[resourceRef] 去重 + DB UNIQUE 兜底
    StartCollection(ctx context.Context, spec CollectionSpec) error
    // 幂等:无 ticker 时 no-op
    StopCollection(ctx context.Context, resourceRef string) error
}
```

**`MeteringUsageRecord` 扩展**(对齐当前 port 结构):

现有 `ports.MeteringUsageRecord` 有 5 个字段:`TenantID`/`ResourceType`/`TotalQuantity`/`Unit`/`Period`,无 `ResourceRef`。本方案新增 `ResourceRef string` 字段。扩展后:

```go
type MeteringUsageRecord struct {
    TenantID      string
    ResourceRef   string                 // 新增:标识来源实例(instance_id)
    ResourceType  MeteringResourceType
    TotalQuantity float64                 // 现有字段名(非 Quantity)
    Unit          string
    Period        string
}
```

兼容性:新增字段,现有消费方忽略新字段即可,无破坏性变更。

### 5.2 Collector 策略(adapter 层)

```go
// pkg/adapters/metering/collectors.go(新增)
package metering

type Collector interface {
    // 从源拉一次指标,转成本周期增量。
    // period 是采集时刻分钟对齐标识,写入记录的 Period 字段(UNIQUE 约束去重依据)
    Collect(ctx context.Context, spec ports.CollectionSpec, period string) ([]ports.MeteringUsageRecord, error)
}

var collectors = map[string]Collector{
    "dcgm_gpu":     DCGMGPUCollector{},      // GPU 占用时长
    "kubelet_cpu":  KubeletCPUCollector{},   // CPU Counter 增量
    "kubelet_mem":  KubeletMemCollector{},   // 内存 Gauge 瞬时占用加权
}

func Resolve(collectorID string) (Collector, bool) {
    c, ok := collectors[collectorID]
    return c, ok
}
```

---

## 6. 幂等与重启重建

### 6.1 StartCollection 幂等(进程内 + DB 双层)

```go
// services/metering-service/internal/service/metering_collection_service.go(新增)
type meteringCollectionService struct {
    mu            sync.Mutex
    tickers       map[string]*time.Ticker       // key: resourceRef
    stopChs       map[string]chan struct{}
    specs         map[string]*ports.CollectionSpec // 供 Stop 保底采集读取 spec
    everCollected map[string]bool               // 该实例是否至少产出一条记录(短生命周期保底)
    db            *pgxpool.Pool                 // 直连 Core DB(仿 auth-service)
    logger        *slog.Logger
}

func (m *meteringCollectionService) StartCollection(ctx context.Context, spec ports.CollectionSpec) error {
    m.mu.Lock()
    if _, exists := m.tickers[spec.ResourceRef]; exists {
        m.mu.Unlock()
        return nil  // 进程内幂等:已有 ticker,no-op
    }
    // V2:去掉相位对齐,直接起 ticker(简化实现)
    //   原因:period 是分钟级近似,相位对齐收益有限,实现负担不值得
    if spec.StartedAt.IsZero() {
        spec.StartedAt = time.Now() // V2:记录 Start 时间,供 collectFullLifetime 计算
    }
    ticker := time.NewTicker(time.Duration(spec.IntervalSec) * time.Second)
    stopCh := make(chan struct{})
    m.tickers[spec.ResourceRef] = ticker
    m.specs[spec.ResourceRef] = &spec
    m.everCollected[spec.ResourceRef] = false
    m.stopChs[spec.ResourceRef] = stopCh
    m.mu.Unlock()

    go m.runCollectionLoop(ctx, spec, ticker, stopCh)
    return nil
}

func (m *meteringCollectionService) runCollectionLoop(ctx context.Context, spec ports.CollectionSpec, ticker *time.Ticker, stopCh chan struct{}) {
    for {
        select {
        case <-ticker.C:
            records, err := metering.CollectAll(ctx, spec, m.logger)
            if err != nil {
                m.logger.Error("collect failed", "ref", spec.ResourceRef, "err", err)
                continue  // 不停 ticker,下个周期重试
            }
            // DB 幂等层:ON CONFLICT DO NOTHING
            // 写侧连接使用 ani_metering_writer(BYPASSRLS),绕过 RLS
            if err := m.persistRecords(ctx, spec.TenantID, records); err != nil {
                m.logger.Error("persist failed", "ref", spec.ResourceRef, "err", err)
            } else if len(records) > 0 {
                m.mu.Lock()
                m.everCollected[spec.ResourceRef] = true
                m.mu.Unlock()
            }
        case <-stopCh:
            ticker.Stop()
            return
        }
    }
}

func (m *meteringCollectionService) StopCollection(ctx context.Context, resourceRef string) error {
    // V2:保留 V1 的锁结构(缩小锁范围,慢 I/O 在锁外)
    //   单副本 + MaxInflight=1 下同一实例的 Stop 不会并发
    //   V1 的顺序(close stopCh → ticker.Stop → delete map → 锁外 collectFullLifetime)
    //   在单副本下无正确性问题(DB UNIQUE 兜底并发风险)
    m.mu.Lock()
    if _, ok := m.tickers[resourceRef]; !ok {
        m.mu.Unlock()
        return nil  // 幂等:无 ticker 时 no-op
    }
    ticker := m.tickers[resourceRef]
    stopCh := m.stopChs[resourceRef]
    ever := m.everCollected[resourceRef]
    spec := m.specs[resourceRef]
    ticker.Stop()
    close(stopCh)
    delete(m.tickers, resourceRef)
    delete(m.stopChs, resourceRef)
    delete(m.everCollected, resourceRef)
    delete(m.specs, resourceRef)
    m.mu.Unlock()

    // 锁外做保底采集(短生命周期 <1 周期):
    //   存活不满一个周期、从未产出过记录 → 强制补采一次全周期量
    if !ever && spec != nil {
        if records, err := m.collectFullLifetime(ctx, *spec); err == nil {
            _ = m.persistRecords(ctx, spec.TenantID, records)
        }
    }
    return nil
}
```

**collectFullLifetime(保底采集)**:与 collectAll(逐周期、以 interval 为窗口)不同,它按**从 Start 到 Stop 的完整存活时长**计算一次性量——如 GPU 用 `卡数 × 实际存活秒数`、CPU 用 `rate[存活窗口] × 存活时长`。仅在 Stop 时且该实例从未产出记录时触发,写一次即止。产出的记录同样需填 `Period`(用 Stop 时刻分钟对齐),与 collectAll 的 Period 格式一致——若实例存活不满一个周期,无周期采集记录,保底记录不会碰撞;若恰好已有周期记录,`ON CONFLICT DO NOTHING` 兜底丢弃保底记录(已有更精确数据,无需保底)。

**单副本对保底采集的影响**:单副本下 created 和 stopped 都在同一进程,everCollected/specs/ticker 一定存在且一致,保底采集的状态管理安全可行。多副本下 everCollected 各副本独立,Stop 可能落在不同副本导致误判(见 §7.2)。

### 6.2 重启重建(直接查 DB + WithPlatformTx 绕 RLS)

**V2 变更**:V1 重建走 Core SDK HTTP `GET /instances?state=running`,V2 改为直接查 `workload_instances` 表。

#### 为什么可以直连 DB

- metering-service 是 Core 内部服务,auth-service 范式已验证 Core 内部服务可直接持 `*pgxpool.Pool` 执行原生 SQL
- `workload_instances` 表的 `state` 字段有索引 `idx_workload_instances_kind (tenant_id, workload_kind, state)` 支持 state 过滤
- 直连 DB 消除 HTTP 往返,重建更快

#### RLS 处理

`workload_instances` 表启用了 FORCE ROW LEVEL SECURITY,普通查询必须设置 `app.current_tenant_id`。重建需要跨所有租户批量查 running 实例,必须用平台级事务绕过 RLS(仿 `MetadataInstanceStore.ListReconcileTargets` 先例)。

```go
// services/metering-service/internal/rebuilder.go(新增)
type Rebuilder struct {
    metadataStore ports.MetadataStore          // 用 WithPlatformTx 绕 RLS,不持裸 pool
    metering      ports.MeteringCollectionService
    logger        *slog.Logger
}

func NewRebuilder(metadataStore ports.MetadataStore, metering ports.MeteringCollectionService, logger *slog.Logger) *Rebuilder {
    return &Rebuilder{metadataStore: metadataStore, metering: metering, logger: logger}
}

func (r *Rebuilder) Rebuild(ctx context.Context) error {
    // 用 WithPlatformTx 绕过 RLS,跨租户查所有 running 实例
    // 仿 MetadataInstanceStore.ListReconcileTargets 的 WithPlatformTx 范式
    var count int
    err := r.metadataStore.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
        // V2:拉 4 个字段 + 解析 gpu_status JSONB(获取 GPU 卡数)
        rows, err := tx.Query(ctx, `
            SELECT tenant_id::text, instance_id, workload_kind, gpu_status
            FROM workload_instances
            WHERE state = 'running'
            ORDER BY updated_at ASC
        `)
        if err != nil {
            return fmt.Errorf("query running instances: %w", err)
        }
        defer rows.Close()

        for rows.Next() {
            var tenantID, instanceID, kind string
            var gpuStatusJSON []byte
            if err := rows.Scan(&tenantID, &instanceID, &kind, &gpuStatusJSON); err != nil {
                return fmt.Errorf("scan instance row: %w", err)
            }
            // 解析 gpu_status JSONB 获取 GPU 卡数(缺失则 GPU 维度跳过)
            gpuCount := parseGPUCount(gpuStatusJSON) // 解析 {"count": N},缺失返回 0
            spec := buildSpec(tenantID, instanceID, kind, gpuCount)
            if err := r.metering.StartCollection(ctx, spec); err != nil {
                r.logger.Error("rebuild start collection failed", "ref", instanceID, "err", err)
                // 单个失败不阻塞,继续重建其余实例
            }
            count++
        }
        return rows.Err()
    })
    if err != nil {
        return err
    }
    r.logger.Info("rebuild done", "running_instances", count)
    return nil
}
```

#### 启动协议(先重建后订阅)

```go
// services/metering-service/main.go(新增)
func main() {
    cfg := config.Load()
    deps := bootstrap.MustConnect(cfg.Config)
    defer deps.Close()

    // metering collection service 实现持有 *pgxpool.Pool(仿 auth-service 子 store 范式)
    meteringSvc := service.NewMeteringCollectionService(deps.DB, deps.Logger)
    consumer := internal.NewConsumer(meteringSvc, deps.Ports.MessageBus, deps.Logger)
    rebuilder := internal.NewRebuilder(deps.Ports.Metadata, meteringSvc, deps.Logger)

    ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer cancel()

    // 1. 先重建 ticker(直接查 DB)
    //    理由:重建拉的是 T0 时刻 DB 快照,T0 之后所有变化由事件增量补齐
    //    先重建后订阅消除"事件先停、重建又建"的竞态窗口
    if err := rebuilder.Rebuild(ctx); err != nil {
        deps.Logger.Error("rebuild failed, continue with event-driven only", "err", err)
        // 重建失败不阻塞,靠事件增量 + DeliverAll 兜底
    }

    // 2. 订阅 NATS(DeliverAllPolicy,单副本约束)
    //    不设 Queue:单副本只有一个订阅,Queue Group 竞争语义无处发挥,
    //    保留反而暗示多副本可工作。多副本会导致计量正确性错误(见 §7.2)。
    sub, err := deps.Ports.MessageBus.Subscribe(ports.SubscribeOptions{
        Subject:     "ani.events.instance.>",
        Consumer:    "metering-consumer",
        MaxInflight: 1,              // V2:强制串行消费保证顺序
        AckWait:     30 * time.Second,
        MaxDeliver:  5,
    }, consumer.handleEvent)
    if err != nil {
        deps.Logger.Error("subscribe failed", "err", err)
        os.Exit(1)
    }
    defer sub.Drain(context.Background()) // Subscription 接口只有 Drain,无 Unsubscribe

    // 3. 常驻等待
    <-ctx.Done()
}
```

**先重建后订阅的理由**:
- 重建拉 T0 快照建 ticker,T0 之后的所有变化由事件增量补齐
- 消除"事件先停、重建又建"的竞态窗口(事件先于重建处理把 ticker 停了,但重建拉到 DB 旧快照又建起来)
- 重建期间(查 DB,几百毫秒)不消费事件,对低频生命周期事件完全可接受

**DeliverAll 回放补齐机制**:
- consumer 重启后,JetStream `DeliverAllPolicy` 从最早未 Ack 消息开始重放
- 崩溃期间 `instance.created` 被重投 → `StartCollection` → 进程内 map 已有(重建时建的)→ 幂等 no-op
- 崩溃期间 `instance.failed` 被重投 → `StopCollection` → 无 ticker(重建时该实例已 failed 不在 running 列表)→ 幂等 no-op
- 结果:无丢采、无遗漏,靠双层幂等兜底

---

## 7. 单副本约束(V2 新增)

### 7.1 约束声明

**metering-service consumer 只能单副本运行,不支持多副本。**

部署配置强制 `replicas: 1`。

### 7.2 三个根源

#### 根源 1:进程内 ticker map 无法跨副本共享

`meteringCollectionService` 持有 `map[string]*time.Ticker`(key: resourceRef)。多副本下:
- 副本 A 收到 `instance.created` → 建 ticker → 开始采集
- 副本 B 收到同一事件 → 也建 ticker → **重复采集**

`ON CONFLICT DO NOTHING` 兜底写入去重,但 ticker 仍在两个副本都跑,浪费采集资源。

#### 根源 2:seenSeq 高水位多副本无法维护

`seenSeq` 是进程内 `map[string]uint64`。多副本下各副本只看到部分 seq,无法维护连续的高水位:
- 副本 A 处理 seq=5 → `seenSeq[A][inst]=5`
- 副本 B 处理 seq=3 → `seenSeq[B][inst]` 还是 0 → `3 > 0` 判定非过期 → 处理过期事件

各副本的 seenSeq 相互独立,作为高水位去重机制失效。

#### 根源 3:everCollected 保底标记多副本下不可靠

多副本下:
- 副本 A 收到 `created` → 设 `everCollected[A][inst]=false`
- 副本 A 的 ticker 产出记录 → `everCollected[A][inst]=true`
- 副本 B 收到 `stopped` → 查 `everCollected[B][inst]` → **不存在**(created 在副本 A)→ 误判为"从未产出"→ **重复触发保底采集**

### 7.3 多副本方案评估(均否决)

| 方案 | 否决理由 |
|---|---|
| 多副本(各副本独立消费) | 见 §7.2 三个根源:seenSeq 失效、everCollected 不可靠、Stop 可能落在不同副本 |
| 多副本 + 外部协调 | seenSeq/everCollected/ticker 状态存 Redis/DB,引入分布式协调复杂度;ticker 无法跨副本共享 |
| 多副本 + leader election | 引入 leader election 框架,failover 期间仍有窗口;单副本已满足数百实例规模 |

**结论**:当前实例规模(数百级)单副本完全够用,多副本带来的复杂度不值得。

### 7.4 故障期间的可用性(单副本 + K8s 失败重启兜底)

单副本故障期间:
- JetStream 持久化未 Ack 的消息
- K8s 自动重启副本(秒级)
- 副本恢复后 durable consumer 从服务端 Ack 进度继续,DeliverAll 回放补齐
- 重建拉取当前 running 实例建 ticker(自愈)
- 短暂故障窗口的事件靠 DeliverAll + 幂等兜底

接受故障期间的计量采集空白(K8s 自动重启秒级,空白可忽略)。不引入 leader election 复杂度。

---

## 8. Collector 分类累加语义

**核心:三个维度语义不同质,不能用一刀切累加模型。**

| 维度 | 指标类型 | PromQL | 累加语义 | 计算公式 |
|---|---|---|---|---|
| `instance_gpu_seconds` | Gauge(瞬时利用率) | `DCGM_FI_DEV_GPU_UTIL` | **占用时长**(不查 DCGM) | `持有卡数 × interval_sec` |
| `instance_cpu_seconds` | Counter(累计秒) | `container_cpu_usage_seconds_total` | **Counter 增量** | `rate(...[60s]) × 60` |
| `instance_memory_gib_seconds` | Gauge(瞬时占用) | `container_memory_working_set_bytes` | **瞬时占用加权时长** | `bytes / 1024^3 × 60` |

**枚举映射**:
`instance_gpu_seconds` → `ports.MeteringResourceInstanceGPUSeconds`
`instance_cpu_seconds` → `ports.MeteringResourceInstanceCPUSeconds`
`instance_memory_gib_seconds` → `ports.MeteringResourceInstanceMemorySeconds`

### 8.1 CollectAll 主入口

```go
// pkg/adapters/metering/collectors.go

// CollectAll 按 spec.Dimensions 逐个路由到对应 Collector,产出各维度本期增量记录。
// GPU 卡数缺失时跳过 GPU 维度(宁可缺一条,不写 0 错值)
// Period:采集时刻分钟对齐,用于 UNIQUE 约束去重(同一实例同一维度同一周期只保留一行)
func CollectAll(ctx context.Context, spec ports.CollectionSpec, logger *slog.Logger) ([]ports.MeteringUsageRecord, error) {
    period := time.Now().Format("2006-01-02T15:04") // 分钟对齐
    var out []ports.MeteringUsageRecord
    for _, dim := range spec.Dimensions {
        col, ok := Resolve(dim.Source)
        if !ok {
            logger.Warn("unknown collector source, skip", "source", dim.Source)
            continue
        }
        rec, err := col.Collect(ctx, spec, period)
        if err != nil {
            logger.Error("dim collect failed", "ref", spec.ResourceRef, "dim", dim.Source, "err", err)
            continue
        }
        out = append(out, rec...)
    }
    return out, nil
}

// ——— 1. GPU 占用时长 Collector ———
type DCGMGPUCollector struct{}

func (DCGMGPUCollector) Collect(ctx context.Context, spec ports.CollectionSpec, period string) ([]ports.MeteringUsageRecord, error) {
    if spec.GPUSpec == nil {
        return nil, nil // 卡数未知 → 跳过 GPU 维度,不写 0 错值
    }
    return []ports.MeteringUsageRecord{{
        TenantID:      spec.TenantID,
        ResourceRef:   spec.ResourceRef,
        ResourceType:  ports.MeteringResourceInstanceGPUSeconds,
        TotalQuantity: float64(spec.GPUSpec.Count) * float64(spec.IntervalSec),
        Unit:          "gpu_second",
        Period:        period,
    }}, nil
}

// ——— 2. CPU Counter 增量 Collector ———
type KubeletCPUCollector struct{}

func (KubeletCPUCollector) Collect(ctx context.Context, spec ports.CollectionSpec, period string) ([]ports.MeteringUsageRecord, error) {
    secs, err := promQueryCPU(ctx, spec.ResourceRef) // rate(...[60s])
    if err != nil {
        return nil, err
    }
    return []ports.MeteringUsageRecord{{
        TenantID:      spec.TenantID,
        ResourceRef:   spec.ResourceRef,
        ResourceType:  ports.MeteringResourceInstanceCPUSeconds,
        TotalQuantity: secs * float64(spec.IntervalSec),
        Unit:          "cpu_second",
        Period:        period,
    }}, nil
}

// ——— 3. 内存 Gauge 瞬时占用加权 Collector ———
type KubeletMemCollector struct{}

func (KubeletMemCollector) Collect(ctx context.Context, spec ports.CollectionSpec, period string) ([]ports.MeteringUsageRecord, error) {
    bytes, err := promQueryMem(ctx, spec.ResourceRef)
    if err != nil {
        return nil, err
    }
    return []ports.MeteringUsageRecord{{
        TenantID:      spec.TenantID,
        ResourceRef:   spec.ResourceRef,
        ResourceType:  ports.MeteringResourceInstanceMemorySeconds,
        TotalQuantity: bytes / (1024 * 1024 * 1024) * float64(spec.IntervalSec),
        Unit:          "gib_second",
        Period:        period,
    }}, nil
}
```

### 8.2 为什么 GPU 用占用时长不查 DCGM

- 配额维度 `gpu_card` 是"卡的持有权",计量维度 `instance_gpu_seconds` 是"持有时长"——语义一致
- `DCGM_FI_DEV_GPU_UTIL` 是 Gauge 瞬时利用率,不能累加
- 利用率数据另作监控指标,不混入计量账本

### 8.3 GPU 卡数缺失

重建或事件未携带 `gpu_spec`(卡数未知)时,跳过不写 GPU 维度,宁可缺一条、不写 0 错值。CPU/内存维度不受影响。

### 8.4 短生命周期保底采集

存活期不满一个采样周期(60s)的实例可能一个 tick 都不触发。`StopCollection` 时若该实例从未产生过任何记录,则强制补采一次"从 Start 到 Stop 的全周期量"(见 §6.1)。

---

## 9. 已定取舍

### 取舍 1:collector 维度如何从 workload_kind 推导?

建议 **A**(简单):consumer 根据 `workload_kind` 硬编码维度映射表(`gpu_container` → 三个维度)。workload_kind 种类有限、映射直观。选项 B(查询 `resource_quota_meta` 动态组装)作为未来演进方案保留。

**`buildSpec` 归属**:维度映射是计量域逻辑,`buildSpec` 作为 `internal` 包的包级函数,consumer 和 rebuilder 各自从自己的数据源提取参数后调用:

```go
// services/metering-service/internal/spec.go(新增)
// buildSpec 根据 workload_kind 硬编码维度映射,构造 CollectionSpec。
// consumer 和 rebuilder 共用此函数,避免维度映射逻辑重复。
func buildSpec(tenantID, instanceID, kind string, gpuCount int) ports.CollectionSpec {
    dims := dimensionsFor(kind) // 硬编码映射:gpu_container→3 维,vm→CPU+Mem,等
    spec := ports.CollectionSpec{
        ResourceRef:  instanceID,
        TenantID:     tenantID,
        WorkloadKind: kind,
        Dimensions:   dims,
        IntervalSec:  60,
        StartedAt:    time.Now(),
    }
    if gpuCount > 0 {
        spec.GPUSpec = &ports.GPUEventSpec{Count: gpuCount}
    }
    return spec
}
```

- consumer 调用:`buildSpec(event.TenantID, event.InstanceID, event.WorkloadKind, gpuCount)`,`gpuCount` 从 `event.GPUSpec.Count` 提取(nil 则传 0)
- rebuilder 调用:`buildSpec(tenantID, instanceID, kind, gpuCount)`,`gpuCount` 从解析 `gpu_status` JSONB 的 `count` 字段提取(缺失则传 0)

### 取舍 2:重建时 DB 查询失败怎么办?

重建失败不阻塞,日志告警,靠事件增量 + DeliverAll 兜底。DB 恢复后下次重启自动补齐。

### 取舍 3:毒消息处理

`json.Unmarshal` 失败的毒消息,handler 记 Error 日志后返回 nil(→ adapter Ack,不重投)。能重投的重试型错误(如采集/持久化连续失败)超过 `MaxDeliver=5` 后会被 JetStream 停止投递。

### 取舍 4:重建与事件增量的竞态

先重建后订阅消除竞态窗口(见 §6.2)。重建拉 T0 快照建 ticker,T0 之后所有变化由事件增量补齐。重建未完成时事件不会到达(订阅还没开始)。

### 取舍 5:周期相位(V2 变更)

V2 去掉相位对齐(简化实现)。ticker 启动后 60s 后第一个 tick,不对齐整分钟。period 标识用采集时刻分钟对齐,按小时/天聚合影响可忽略。

### 取舍 6:单副本 HA(V2 新增)

单副本 + K8s 失败重启兜底。接受故障期间计量采集空白(K8s 自动重启秒级),不引入 leader election 复杂度。

---

## 10. 落地顺序(分 PR)

| PR | 内容 | 依赖 |
|---|---|---|
| **PR-M1** | `metering_usage_records` migration + 新增 `MeteringCollectionService` port + `meteringCollectionService` 实现(进程内 map + DB UNIQUE 幂等,持 `*pgxpool.Pool` 直连 DB)+ metering-service `go.mod` 补 `github.com/jackc/pgx/v5` 及 `pgxpool` 依赖 | 无 |
| **PR-M2** | `Collector` 接口 + 三个 Collector 实现(DCGM占用/Kubelet CPU Counter/Kubelet Mem Gauge) + 单测 | PR-M1 |
| **PR-M3** | `metering-service` main.go(bootstrap.MustConnect 启动)+ consumer(seenSeq 成功才推进 + MaxInflight=1)+ 重建协调器(直接查 DB + WithPlatformTx)+ DeliverAll 回放 | PR-M1, PR-M2 |
| **PR-M4** | 集成测试(真实 NATS + 真实 DB + 真实 Prometheus)+ 端到端验证 | PR-M3 |
| **PR-M5** | 部署清单 `metering-service-live-deps.yaml`(ServiceAccount + Deployment + Service) | PR-M4 |

**建议起点**:PR-M1(纯新增表 + port 扩展,零侵入,可独立 review)。

---

## 11. 部署清单

### 文件

`repo/deploy/real-k8s-lab/metering-service-live-deps.yaml`

### 包含资源

ServiceAccount + Deployment + Service,参考 [sprint13-production-auth-dex.yaml](../../repo/deploy/real-k8s-lab/sprint13-production-auth-dex.yaml) 的 auth-service 部署段(L124-261)。

### 关键配置

| 项 | 值 | 说明 |
|---|---|---|
| `ServiceAccount.name` | `ani-metering-service` | namespace `ani-system` |
| `Deployment.replicas` | `1` | **必须单副本**(见 §7.2 单副本三个根源),靠 K8s 失败重启兜底 |
| `container.command` | `/opt/ani/bin/metering-service` | 仿 auth-service,hostPath 挂 `/opt/ani/bin` |
| `container.ports` | health `9210` | 建议端口,避免与 auth-service(9201)冲突 |
| `DATABASE_URL` | secret `ani-metering-runtime` key `database_url` | Core DB 连接串 |
| `NATS_URL` | secret `ani-metering-runtime` key `nats_url` | JetStream 连接串 |
| `METERING_PROMETHEUS_URL` | 明文 env | Prometheus 查询地址,如 `http://prometheus.ani-system.svc.cluster.local:9090` |
| `METERING_COLLECTION_INTERVAL_SECONDS` | `60` | 采集周期 |
| `readinessProbe` | tcpSocket port `health` | 仿 auth-service |
| `livenessProbe` | tcpSocket port `health` | 仿 auth-service |
| `resources.requests` | cpu `100m`,memory `128Mi` | 仿 auth-service |
| `resources.limits` | cpu `1`,memory `512Mi` | 仿 auth-service |
| `securityContext` | runAsNonRoot,runAsUser `65532` | 仿 auth-service |

### 部署清单骨架

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: ani-metering-service
  namespace: ani-system
  labels:
    app.kubernetes.io/name: ani-metering-service
    ani.dev/profile: metering-live-deps
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ani-metering-service
  namespace: ani-system
  labels:
    app.kubernetes.io/name: ani-metering-service
    ani.dev/profile: metering-live-deps
spec:
  replicas: 1                          # 单副本,见 §7.2
  selector:
    matchLabels:
      app.kubernetes.io/name: ani-metering-service
  template:
    metadata:
      labels:
        app.kubernetes.io/name: ani-metering-service
        ani.dev/profile: metering-live-deps
    spec:
      serviceAccountName: ani-metering-service
      securityContext:
        runAsNonRoot: true
        runAsUser: 65532
        runAsGroup: 65532
        seccompProfile:
          type: RuntimeDefault
      containers:
        - name: metering-service
          image: registry.k8s.io/pause:3.10   # live gate 用 pause 占位,真实镜像在 live gate 时替换
          imagePullPolicy: IfNotPresent
          command:
            - /opt/ani/bin/metering-service
          ports:
            - name: health
              containerPort: 9210
          env:
            - name: HEALTH_PORT
              value: "9210"
            - name: DATABASE_URL
              valueFrom:
                secretKeyRef:
                  name: ani-metering-runtime
                  key: database_url
            - name: NATS_URL
              valueFrom:
                secretKeyRef:
                  name: ani-metering-runtime
                  key: nats_url
            - name: METERING_PROMETHEUS_URL
              value: http://prometheus.ani-system.svc.cluster.local:9090
            - name: METERING_COLLECTION_INTERVAL_SECONDS
              value: "60"
          readinessProbe:
            tcpSocket:
              port: health
            initialDelaySeconds: 5
            periodSeconds: 5
          livenessProbe:
            tcpSocket:
              port: health
            initialDelaySeconds: 10
            periodSeconds: 10
          resources:
            requests:
              cpu: 100m
              memory: 128Mi
            limits:
              cpu: "1"
              memory: 512Mi
          securityContext:
            allowPrivilegeEscalation: false
            capabilities:
              drop:
                - ALL
          volumeMounts:
            - name: ani-bin
              mountPath: /opt/ani/bin
              readOnly: true
      volumes:
        - name: ani-bin
          hostPath:
            path: /opt/ani/bin
            type: Directory
---
apiVersion: v1
kind: Service
metadata:
  name: ani-metering-service
  namespace: ani-system
  labels:
    app.kubernetes.io/name: ani-metering-service
    ani.dev/profile: metering-live-deps
spec:
  selector:
    app.kubernetes.io/name: ani-metering-service
  ports:
    - name: health
      port: 9210
      targetPort: health
```

### Secret 预置

部署前需创建 `ani-metering-runtime` secret(含 `database_url`、`nats_url`),可从 Core DB / NATS 现有凭据复用。具体 secret 创建方式参考 `ani-auth-production-shaped-runtime` 的预置流程。

---

## 12. 合规核对

| 约束 | 本设计 | 核对 |
|---|---|---|
| §3.2 Services 不直连 Core 代码包/内部表 | metering-service 是 Core 内部服务,不受 §3.2 约束;仿 auth-service 直连 DB | ✅(Core 内部服务定位) |
| §5.2 能力经 ports | `MeteringCollectionService`/`Collector` 经 ports | ✅ |
| §5.3 业务服务不直连组件 | Collector 封装 Prometheus 查询在 adapter | ✅ |
| §5 奥卡姆 | 三个 Collector 当前已知维度;不建本地副本表;重建直接查 DB 不新增 port 方法 | ✅ |
| §6.6 real-provider 不误标 | local profile 下 RealProvider:false | ✅ |
| 两个真相源 | 不建 `metering_collections`,实例状态以 Core `workload_instances` 为准 | ✅ |
| 幂等 | StartCollection 进程内 map + DB UNIQUE 双层;StopCollection 幂等 no-op | ✅ |
| Karpathy 原则二 | 不为未来资源建插件框架;单副本避免分布式协调复杂度 | ✅ |
| Karpathy 原则五 | 不建 metering_collections 表;不持久化 seenSeq;不加 WaitGroup;重建直接原生 SQL 不新增 port 方法 | ✅ |

---

## 13. FAQ

### Q1:为什么不建 metering_collections 表用于重建?

该表是实例状态的本地副本,会与 Core `workload_instances.state` 漂移。新建表 = 新增真相源,违反"PG workload_instances 唯一 source of truth"。重建直接查 DB 读当前状态,不产生新真相源。

### Q2:为什么用 DeliverAll 而非 DeliverNew?

consumer 进程崩溃期间,JetStream 持久化的消息若用 DeliverNew 会被跳过,导致该实例永久丢采。DeliverAll 重放历史消息,靠双层幂等兜底重复处理,保证不丢采。

### Q3:重建直接查 DB 是否违反"workload_instances 是唯一 source of truth"?

不违反。重建是只读查询,不写 `workload_instances`,不产生新真相源。重建只是读 source of truth 的当前状态建 ticker,与走 Core API 读同一张表是等价的只读访问,只是访问路径从 HTTP 变为直连 DB。

### Q4:seenSeq 成功才推进后,处理成功但 Ack 前崩溃会怎样?

handler 返回 nil → adapter 调 `msg.Ack()` → 若 Ack 到达 JetStream 服务端前进程崩溃,该消息重启后被重投。此时 seenSeq 已推进(崩溃前 handler 成功就推进了),但重启归零(内存变量),`event.EventSeq > 0` 判定非过期 → 重新处理。重新处理是幂等的,重复无害。

### Q5:MaxInflight=1 会不会成为吞吐瓶颈?

不会。metering consumer 处理的是实例生命周期事件(created/stopped/failed/deleted),不是高频流。实例规模数百级,事件吞吐远低于 NATS 单连接上限。

### Q6:单副本挂了怎么办?

K8s 自动重启副本(秒级)。恢复后 durable consumer 从服务端 Ack 进度继续,DeliverAll 回放补齐崩溃窗口消息,重建拉 running 实例建 ticker 自愈。接受短暂计量采集空白。

### Q7:为什么不把 seenSeq 持久化到 DB?

引入持久化需新增表 + 启动恢复 + 运行时同步写,复杂度收益比不成立。当前只有"超久未 Ack 陈旧事件跨生命周期回放"运维极端场景会出错,接受此边界(Karpathy 原则五)。

### Q8:为什么先重建后订阅?

消除"事件先停、重建又建"的竞态窗口。重建拉 T0 快照建 ticker,T0 之后所有变化由事件增量补齐。若先订阅后重建,事件先于重建处理(把 ticker 停了),但重建拉到 DB 旧快照(实例还是 running)又建起来——停了又建,白停了。

---

## 14. 变更记录

| 日期 | 变更 |
|---|---|
| 2026-08-11 | V2 初版:grill-me 三轮头脑风暴收敛,完整覆盖 V1。三项关键变更:① metering-service 定位为 Core 内部服务,main.go 改用 bootstrap.MustConnect,重建改直接查 DB(WithPlatformTx 绕 RLS);② seenSeq 推进时机修正为处理成功后才推进,避免 StartCollection 失败 Nak 重投被误判过期永久丢失;③ 单副本约束声明(replicas:1 + MaxInflight=1),分析三个根源,多副本方案均否决。另:去掉周期相位对齐(简化),保留 collectFullLifetime 保底采集(单副本下状态安全),先重建后订阅(消除竞态窗口) |
| 2026-08-11 | 删除 §7.4 误配保护:Queue 在单副本下是死代码(竞争语义无处发挥),保留会误导部署者以为多副本可部分工作。SubscribeOptions 去掉 Queue 字段,§7.2/§7.3 措辞同步修正,原 §7.5 顺位为 §7.4 |
| 2026-08-11 | 代码审查修正:① `sub.Unsubscribe()` → `sub.Drain(context.Background())`(Subscription 接口只有 Drain);② Rebuilder 改持 `ports.MetadataStore` 调 `WithPlatformTx`(回调范式,非独立函数);③ Collector 接口及三个实现补 `period string` 参数,`collectAll` 统一生成分钟对齐 Period 填入记录(修复 UNIQUE 约束碰撞丢数据);④ §4.1 补充 `quantity` 列与 `TotalQuantity` 字段映射说明;⑤ §10 PR-M1 补充 go.mod 加 pgx 依赖;⑥ §9 取舍 1 补充 `buildSpec` 归属(internal 包级函数)及 consumer/rebuilder 调用方式 |
| 2026-08-11 | `collectAll` 从 `meteringService` 方法改为 `pkg/adapters/metering` 包级函数 `CollectAll`(logger 传入),与三个 Collector 同文件;`collectFullLifetime` 补充 Period 填充说明(Stop 时刻分钟对齐);§2.2 分层说明同步更新 |
| 2026-08-11 | 新增部署清单章节(原 §10.1 提升为独立 §11),含 ServiceAccount + Deployment + Service YAML 骨架及关键配置表;§10 落地顺序新增 PR-M5;原 §11/§12/§13 顺延为 §12/§13/§14 |
| 2026-08-11 | 拆分 `MeteringService` 接口:新增 `MeteringCollectionService` 专管 `StartCollection`/`StopCollection`(consumer 专用),`MeteringService` 保留 `QueryUsage`/`ReportTokenUsage`;实现类 `meteringService` → `meteringCollectionService`,文件 `metering_service.go` → `metering_collection_service.go`;consumer/rebuilder 字段类型、main.go 构造函数、§1 基线表、§2.2 分层图、§2.3 架构图、§7.2 根源 1、§10 PR-M1、§12 合规核对同步更新 |
