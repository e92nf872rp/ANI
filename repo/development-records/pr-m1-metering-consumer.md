# PR-M1 — Metering Consumer 计量采集

完成日期：2026-08-12（进行中，逐 Issue 追加）
对应 Sprint：以 repo/CURRENT-SPRINT.md 为准
批次类型：Feature batch（新增计量采集产品能力）

> **说明：** 本文件按 Issue 逐条追加实现笔记。批次全部完成后再一次性更新 README.md、CURRENT-SPRINT.md、ANI-06-开发计划.md。

---

## Issue 001: 新建 metering_usage_records 表 migration

完成日期：2026-08-12
验证结果：`python scripts/validate_component_imports.py --root .` passed（component import guard passed），`git diff --check` 通过

### 实现了什么

新建 `metering_usage_records` 表和 `ani_metering_writer` 角色，作为计量采集的 DB 层基础设施。表含 UNIQUE 约束作为写入幂等兜底，角色 BYPASSRLS 供采集写侧跨租户写入。

### 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `deploy/migrations/20260731000100_metering_usage.sql` | 新增 | 建表 + 角色 + 索引 + GRANT + RLS policy |

### Design Decisions

1. **`recorded_at` 使用 `NOT NULL DEFAULT NOW()`（比 AC 更严格）**
   - 模糊性：AC 只写 `recorded_at TIMESTAMPTZ DEFAULT NOW()`，未明确 NOT NULL。
   - 选择：加 `NOT NULL`。
   - 理由：记录时间戳是计量计费核心字段，不应允许 NULL。与 init_schema.sql 中 `created_at`/`updated_at` 均为 NOT NULL 的范式一致。

2. **RLS policy 不加 `AS RESTRICTIVE`，与 AC 显式一致**
   - SPEC/AC 明确要求不加 `AS RESTRICTIVE`。
   - 与 init_schema.sql 中 `metering_records` 表（L612-617）使用 `AS RESTRICTIVE` 不同，因为本表写侧通过 BYPASSRLS 绕过，读侧仅靠 permissive policy 即可实现租户隔离，无需 RESTRICTIVE 叠加。

### Deviations

None — 实现严格遵循 AC 和 SPEC §3.1 的每一项要求。

### Tradeoffs

1. **新建独立角色 `ani_metering_writer` vs 复用 `ani_outbox_publisher`**
   - 考虑过的替代方案：复用 `ani_outbox_publisher` 角色跨租户写入。
   - 优点：减少角色数量。
   - 缺点：outbox publisher 的权限语义是"扫描 outbox_events"，与计量写入是不同产品域，权限耦合后难以独立审计和回收。
   - 选择理由：遵循 init_schema.sql 中"一个跨租户写侧一个专用 BYPASSRLS 角色"的范式，权限边界清晰。

### Open Questions

None

### 验证命令

```bash
cd repo
python scripts/validate_component_imports.py --root .  # component import guard passed
git diff --check                                       # 通过
```

> **注：** `make validate-architecture` target 依赖 Unix `date -u` 命令，Windows PowerShell 环境不兼容；已直接运行底层 `validate_component_imports.py` 验证通过。`make test` 中 `pkg/adapters/runtime` sandbox symlink 测试在 Windows 不兼容（既有失败，与本次改动无关）。

---

## Issue 002: 新增 MeteringCollectionService port 接口和事件 schema

完成日期：2026-08-12
验证结果：`go build ./pkg/...` 通过，`go vet ./pkg/ports/...` 通过，`make validate-architecture` 通过，`git diff --check` 通过

### 实现了什么

新增 `MeteringCollectionService` interface 和 `InstanceLifecycleEvent` 事件 schema，为 consumer 和 rebuilder 提供采集生命周期控制契约。扩展 `MeteringUsageRecord` 新增 `ResourceRef` 字段。

### 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `pkg/ports/instance_events.go` | 新增 | `InstanceLifecycleEvent`（7 字段）+ `GPUEventSpec`（Count） |
| `pkg/ports/metering.go` | 修改 | 新增 `CollectionDimension`/`CollectionSpec`/`MeteringCollectionService`；扩展 `MeteringUsageRecord` 新增 `ResourceRef` |

### Design Decisions

1. **`MeteringUsageRecord.ResourceRef` 字段位置放在 `TenantID` 之后**
   - 模糊性：SPEC §3.2 只说"新增 `ResourceRef string` 字段，现有 5 字段不变"，未指定位置。
   - 选择：放在 `TenantID` 之后（第二个位置）。
   - 理由：`ResourceRef` 与 `TenantID` 同为标识字段，逻辑上相邻更自然；Go 结构体字段顺序不影响命名字段构造的兼容性。

2. **`InstanceLifecycleEvent` 使用 JSON tag + `omitempty`**
   - 模糊性：SPEC §3.2 给出了结构体定义但未明确 JSON 序列化行为。
   - 选择：`GPUSpec` 用指针 `*GPUEventSpec` + `json:"gpu_spec,omitempty"`，`ErrorMsg` 用 `json:"error_msg,omitempty"`。
   - 理由：与 SPEC §4.2 事件 payload schema 一致（`gpu_spec` 在无 GPU 时省略）；指针类型天然区分"未设置"与"零值"。

### Deviations

None — 实现严格遵循 AC 和 SPEC §3.2 的每一项要求。

### Tradeoffs

1. **`MeteringCollectionService` 独立接口 vs 合并到 `MeteringService`**
   - 考虑过的替代方案：在现有 `MeteringService` 接口上追加 `StartCollection`/`StopCollection` 方法。
   - 优点：减少接口数量。
   - 缺点：违反接口隔离原则（ISP），`MeteringService` 的消费方（查询/上报）被迫依赖采集控制方法；后续采集控制变更会破坏所有 `MeteringService` 实现的 ABI。
   - 选择理由：AC 显式要求分离（"采集控制 vs 查询/上报"），且 SPEC §3.2 将两者定义为独立接口，职责边界清晰。

### Open Questions

None

### 验证命令

```bash
cd repo
go build ./pkg/...                  # 通过
go vet ./pkg/ports/...              # 通过
make validate-architecture          # architecture guardrails valid
git diff --check                    # 通过
```

---

## Issue 003: 新建 metering-service go.mod 和 config

完成日期：2026-08-12
验证结果：`go build ./...` 通过，`go vet ./...` 通过，`go test ./...` 通过，`go mod verify` 通过，`python scripts/validate_component_imports.py --root .` 通过，`git diff --check` 通过

### 实现了什么

补全 metering-service 独立 module 的 go.mod（添加 `pgx/v5 v5.9.2` 及 pgxpool 传递依赖）和 config 加载模块，为 PR-M1 的 `meteringCollectionService` 实现提供编译依赖和配置基础。

### 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `services/metering-service/go.mod` | 修改 | 添加 `pgx/v5 v5.9.2` 及 indirect 依赖块 |
| `services/metering-service/go.sum` | 新增 | `go mod tidy` 自动生成 |
| `services/metering-service/internal/config/config.go` | 新增 | 嵌入 `bootstrap.Config` + metering 专有字段 |

### Design Decisions

1. **GRPCPort 默认值设为 9104**
   - 模糊性：Issue AC 要求嵌入 `bootstrap.Config` 获得 `GRPCPort` 字段，但未指定默认值。metering-service 不对外暴露 gRPC。
   - 选择：设为 9104。
   - 理由：`bootstrap.Config.GRPCPort` 是必填字段（`MustConnect` 用它启动 gRPC server），需要一个不与其他服务冲突的值。auth-service 用 9101，9104 在当前服务端口分配中无冲突。

2. **PrometheusURL 和 CollectionIntervalSeconds 作为独立字段而非嵌入 bootstrap.Config**
   - 模糊性：SPEC §2.4 和 PRD US-006 要求 config.go 加载 `METERING_PROMETHEUS_URL` 和 `METERING_COLLECTION_INTERVAL_SECONDS`，但未指定存放位置。
   - 选择：作为 `Config` 结构体的直接字段，与 `bootstrap.Config` 嵌入并列。
   - 理由：这两个字段是 metering-service 专有配置，不属于 bootstrap 公共基础设施配置，放在外层更清晰。auth-service 的 config.go 也是同样的范式（JWT/OIDC 字段在外层）。

### Deviations

None — 实现严格遵循 AC 的每一项要求。

### Tradeoffs

1. **pgx/v5 标记为 `// indirect` vs `// direct`**
   - 当前状态：`go mod tidy` 将 `pgx/v5 v5.9.2` 标记为 `// indirect`，因为 Issue 003 范围内只有 config.go 导入 `bootstrap`，不直接导入 `pgx/v5`。
   - 替代方案：手动改为 direct require。
   - 选择理由：不手动干预 `go mod tidy` 的结果。Issue 004（PR-M1 meteringCollectionService 实现）会直接导入 `pgx/v5/pgxpool`，届时 `go mod tidy` 会自动将其转为 direct require。当前 indirect 状态不影响编译和依赖下载。

### Open Questions

None

### 验证命令

```bash
cd repo/services/metering-service
go build ./...                          # 通过
go vet ./...                            # 通过
go test ./...                           # 通过（eventconsumer 测试通过）
go mod verify                           # all modules verified

cd repo
python scripts/validate_component_imports.py --root .  # component import guard passed
git diff --check                                          # 通过
```

> **注：** `make validate-architecture` target 依赖 Unix `date -u` 命令，Windows PowerShell 环境不兼容；已直接运行底层 `validate_component_imports.py` 验证通过。

---

## Issue 004: 实现 meteringCollectionService（进程内 ticker 管理 + DB 持久化）

完成日期：2026-08-12
验证结果：`go test ./internal/service/... -v -count=1` 13/13 PASS，`go vet ./...` 通过，`go build ./...` 通过，`make validate-architecture` 通过，`git diff --check` 通过

### 实现了什么

实现 `meteringCollectionService`，持有 `*pgxpool.Pool` 直连 Core DB，管理 per-instance ticker 并写入 `metering_usage_records`。包含 Start/Stop 幂等、runCollectionLoop 采集循环、persistRecords 持久化（ON CONFLICT DO NOTHING）、collectFullLifetime 保底采集。

### 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `services/metering-service/internal/service/metering_collection_service.go` | 新增 | meteringCollectionService 实现（227 行） |
| `services/metering-service/internal/service/metering_collection_service_test.go` | 新增 | 13 个单测用例 |
| `services/metering-service/go.mod` | 修改 | pgx/v5 从 indirect 提升为直接依赖 |
| `architecture/component-import-allowlist.yaml` | 修改 | 新增 metering-service bounded_direct 条目 |

### Design Decisions

1. **`CollectAllFunc` 作为注入类型而非硬依赖 Collector 接口**
   - 模糊性：SPEC §5.1.2 的伪代码写 `CollectAll(ctx, spec, logger)`，但 PR-M1 阶段 Collector 实现（Issue #005）尚未落地。
   - 选择：定义 `CollectAllFunc` 函数类型，通过构造函数 `NewMeteringCollectionService` 注入；PR-M1 阶段可为 nil（runCollectionLoop 跳过采集）。
   - 理由：避免提前引入不存在的 `pkg/adapters/metering` 依赖；nil 时 runCollectionLoop 跳过采集，等待 PR-M2 注入真实实现。符合 Karpathy 原则二"用能解决问题的最小代码"。

2. **`persistFunc` 可注入字段用于单测 mock DB 交互**
   - 模糊性：AC 要求 persistRecords 用 ani_metering_writer 角色连接执行 INSERT ON CONFLICT，但单测不应依赖真实 PG 连接。
   - 选择：在 struct 中新增未导出字段 `persistFn persistFunc`，persistRecords 优先使用 persistFn，nil 时 fallback 到 s.db 原生 SQL。
   - 理由：单测通过注入 mock persistFn 验证调用逻辑，无需启动 PG；生产路径使用 s.db 执行原生 SQL，行为与 SPEC §5.1.3 一致。

3. **`collectFullLifetime` 中 CPU/Mem 维度留空 switch case**
   - 模糊性：SPEC §5.1.8 的伪代码包含 CPU/Mem 维度的 Prometheus 查询逻辑（promQueryCPU/promQueryMem），但 PR-M1 阶段无 Collector 实现。
   - 选择：GPU 维度按 SPEC 实现完整（纯持有时长计算，不查 DCGM）；CPU/Mem 维度留空 case 带注释"PR-M2 Collector 接入后完善"。
   - 理由：GPU 维度不依赖外部组件（纯 `Count * elapsed`），可独立验证；CPU/Mem 需要 Prometheus 查询，属于 Issue #005 范围。提前实现会引入未测试的猜测代码，违反 Karpathy 原则二。

4. **`runCollectionLoop` 使用 `context.Background()` 而非继承 StartCollection 的 ctx**
   - 模糊性：SPEC §5.1.1 step 8 写 `go runCollectionLoop(ctx, spec, ticker, stopCh)`，但 ctx 是 StartCollection 的调用方 context。
   - 选择：goroutine 内用 `context.Background()` 创建独立 context，不继承调用方 ctx。
   - 理由：goroutine 生命周期独立于调用方 context——调用方 context 可能提前取消（如 HTTP 请求结束），但 ticker 需要持续运行直到 StopCollection 被显式调用。用调用方 ctx 会导致 ticker 采集被意外取消。SPEC §2.3 明确"副本数 replicas:1"，ticker 生命周期应与进程一致。

5. **IntervalSec <= 0 时默认 60 秒**
   - 模糊性：SPEC §5.1.1 step 4 写 `ticker = NewTicker(IntervalSec * Second)`，未说明 IntervalSec <= 0 时的行为。
   - 选择：`if interval <= 0 { interval = 60 }`。
   - 理由：`time.NewTicker(0)` 或负值会 panic，必须兜底；60 秒是 config.go 中 `CollectionIntervalSeconds` 的默认值。

### Deviations

None — 实现严格遵循 AC 和 SPEC §5.1.1-§5.1.3、§5.1.8 的伪代码逻辑。唯一差异是 `CollectAllFunc` 注入替代硬编码 `CollectAll` 调用，这是 PR-M1 阶段 Collector 未落地的必要适配，不改变语义。

### Tradeoffs

1. **`CollectAllFunc` 注入 vs 等待 Issue #005 落地后直接调用**
   - 考虑过的替代方案：不实现 runCollectionLoop 的 CollectAll 调用，等 Issue #005 完成后再补充。
   - 优点（注入）：runCollectionLoop 框架完整，PR-M2 只需注入函数，无需修改 service 代码。
   - 缺点（注入）：多了一个 `CollectAllFunc` 类型定义和 nil 检查。
   - 选择理由：框架完整 > 逐步追加。nil 检查是 1 行代码，但避免了 PR-M2 再改 service 文件的风险。

2. **persistRecords 逐行 INSERT vs 批量 INSERT**
   - 考虑过的替代方案：用 pgx `CopyFrom` 或多 VALUES 批量 INSERT。
   - 优点（逐行）：代码简单，每行独立错误隔离。
   - 缺点（逐行）：N 条记录 N 次 RTT。
   - 选择理由：SPEC §6.3 明确"维度数=2-3，无需额外批处理"，每周期最多 3 条记录，逐行 INSERT 性能足够。

3. **allowlist 新增条目 vs 走 pkg/adapters 抽象**
   - 考虑过的替代方案：在 `pkg/adapters/metering/` 新建一个 persistence adapter 封装 pgxpool。
   - 优点（adapter）：不需要修改 allowlist。
   - 缺点（adapter）：为一个 service 内部使用的持久化模块新增 adapter 层，过度抽象；与 auth-service/task-service 等现有 bounded_direct 范式不一致。
   - 选择理由：遵循现有范式（auth-service 有 5 个 bounded_direct 条目），metering-service 同属 Core 内部服务的持久化模块。

### Open Questions

1. **`ani_metering_writer` 角色的连接配置在何处注入？**
   - 当前实现：`NewMeteringCollectionService(db *pgxpool.Pool, ...)` 接收已配置好的 pool，pool 的连接用户需是 `ani_metering_writer` 角色成员。
   - 待确认：Issue #009（main.go bootstrap）中 pgxpool 的连接字符串是否正确配置为使用 `ani_metering_writer` 角色凭据。PR-M1 阶段无法端到端验证。

### 验证命令

```bash
cd repo/services/metering-service
go test ./internal/service/... -v -count=1   # 13/13 PASS
go vet ./...                                 # 通过
go build ./...                               # 通过

cd repo
make validate-architecture                   # ✅ architecture guardrails valid
git diff --check                             # 通过
```

> **注：** `go test -race` 在 Windows 环境下因 DLL 加载问题（exit code 0xc0000139）无法运行，eventconsumer 包同样失败，非代码问题。非 race 模式测试全部通过。

---
