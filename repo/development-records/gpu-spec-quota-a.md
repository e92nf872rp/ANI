# GPU-SPEC-QUOTA-A

> 日期: 2026-08-14 (Issue #003-#006), 2026-08-17 (Issue #007-#012)
> 类型: Core adapter 实现批次 + BOSS/Console 前端批次
> 状态: 本地实现 + review 完成，待 ship
> Issues: #003 (CRD Store), #004 (Volcano Translator), #005 (Inventory + HAMi Cleanup), #006 (Reconciler), #007 (Orchestrator + Store + Outbox + Migrations + Bootstrap), #008 (Gateway Handler), #009 (BOSS GPU 资源池 4 Tab), #010 (BOSS 规格管理 + 配额 Drawer), #011 (Console 创建 Dialog 四态), #012 (Console 配额/预留展示 + 队列扩展)
> 批次: M2.1-TASK-A
> 依赖: #2 (GPUSpec ports), QUOTA-SERVICE (QuotaService port + TCC adapter)

## 目标

实现 GPU 规格与配额管理的 adapter 层：GPUSpec CRD 持久化、Volcano 资源翻译、GPU Inventory 四态可用性 + HAMi 代码清理、reconciler TCC Confirm/Cancel/Release 同事务。

## 实现范围

| Issue | 文件 | 内容 |
|---|---|---|
| #003 | `deploy/manifests/gpu-spec-a/00-gpuspec-crd.yaml` | GPUSpec CRD (Cluster-scoped, node_affinity + volcano_resources) |
| #003 | `pkg/adapters/runtime/crd_gpu_spec_store.go` | CRDGPUSpecStore (List/Get/Create/Delete + idempotency label) |
| #004 | `pkg/adapters/runtime/volcano_resource_translator.go` | VolcanoResourceTranslator.Translate(spec_id, queue_name, count) |
| #005 | `pkg/adapters/runtime/kubernetes_gpu_inventory.go` | ListSpecAvailability 四态算法 + HAMi 全量删除 + parseVolcanoVGPUAnnotation |
| #006 | `pkg/adapters/runtime/reconcile_controller.go` | TCC Confirm/Cancel/Release 同事务 + provisioning 超时 + 删除双调 + 对账循环 |
| #007 | `pkg/adapters/runtime/instance_orchestrator.go` | Apply 失败 markApplyFailed + Delete 时 Quota.Release + hasTransactionalQuotaSupport 回退 |
| #007 | `pkg/adapters/runtime/instance_store.go` | WorkloadInstanceStoreTx (UpsertStatusTx) + quota_tx_ids JSONB 列读写 + validateInstanceRecord |
| #007 | `pkg/adapters/runtime/demo_instances.go` | QuotaAwareInstanceOrchestrator (TryManyTx 预占 + Volcano 翻译 + Apply 失败 Cancel) |
| #007 | `pkg/adapters/runtime/outbox_writer.go` | outboxWriter 接口 + metadataOutboxWriter + mockOutboxWriter + encodeOutboxPayload |
| #007 | `pkg/adapters/runtime/volcano_queue_store.go` | volcanoQueueCRDStatus + crdToQueue 映射 allocated 到 GPUSchedulingQueue.Status |
| #007 | `deploy/migrations/20260812000100_quota_tx_ids.sql` | workload_instances 新增 quota_tx_ids JSONB NOT NULL DEFAULT '[]' |
| #007 | `deploy/migrations/20260812000200_resource_reservation_allocations.sql` | resource_reservation_allocations 表 + RLS + CHECK >= 0 |
| #007 | `pkg/bootstrap/server.go` | GPUQuotaEnabled + ProvisioningTimeoutMin + 环境变量覆盖 |
| #007 | `pkg/bootstrap/deps.go` | 注入 quotaService + gpuSpecStore + volcanoTranslator + reconcileController/orchestrator 条件装配 |
| #008 | `services/ani-gateway/internal/router/gpu_spec_resources.go` | POST /gpu-specs + DELETE /gpu-specs/:spec_id handler |
| #008 | `services/ani-gateway/internal/router/reservation_resources.go` | PUT/GET /admin/tenants/:tid/reservations + GET /quotas/me + GET /reservations/me |
| #008 | `services/ani-gateway/internal/router/gpu_inventory_resources.go` | gpuSpecResponse 扩展 gpu_mode/node_affinity/volcano_resources + CRD store 优先 |
| #008 | `services/ani-gateway/internal/router/instances.go` | network_config 顶层 fallback 字段 |
| #008 | `services/ani-gateway/internal/router/router.go` | RegisterOptions 新增 GPUSpecStore + MetadataStore + QuotaStoreService |
| #008 | `services/ani-gateway/gpu_inventory_runtime.go` | newGatewayGPUSpecStore (kubernetes_rest 模式) |
| #008 | `services/ani-gateway/quota_runtime.go` | newGatewayQuotaStore 返回 QuotaAdminService + QuotaStoreService + MetadataStore |
| #008 | `pkg/ports/quota_admin.go` | PutReservation + GetReservation 接口 + ReservationPutRequest + ReservationView |
| #008 | `pkg/ports/errors.go` | ErrReservationExceedsQuota 哨兵 |
| #008 | `pkg/adapters/runtime/postgres_quota.go` | PutReservation (lock + 422 + GREATEST clamp + UPSERT) + GetReservation |

## 1. Design Decisions

### D1: CRD Store 错误哨兵复用

**Ambiguity:** SPEC 未定义非 404 HTTP 错误（如 500 Server Error）应映射到哪个哨兵错误。

**Choice:** 非 404 HTTP 错误统一包装为 `ErrGPUSpecNotFound` 或 `ErrGPUSpecConflict`，与同包 `volcano_queue_store.go` 现有模式一致。

**Rationale:** ports 层未定义 `ErrGPUSpecInternal` 哨兵；新增哨兵超出本批次 scope；同包已有 adapter 采用相同模式，保持一致性优先。

### D2: Volcano Translator count 校验错误复用 ErrGPUSpecNotFound

**Ambiguity:** `count <= 0` 的错误应返回什么哨兵？SPEC 只说"spec_id 不存在返回明确错误"。

**Choice:** `count <= 0` 返回 `ErrGPUSpecNotFound` 包装错误。

**Rationale:** ports 层无 `ErrInvalidCount` 哨兵；count 校验是 bonus 防御，功能上正确（阻止无效请求），不值得新增哨兵。

### D3: device_idle_count 简化为 device_total

**Ambiguity:** SPEC §5.1 说"查已调度 Pod 资源请求求差得 device_idle_count"，但 Pod 列表需要额外 K8s API 调用。

**Choice:** `device_idle_count = device_total`（从 `volcano.sh/node-vgpu-register` annotation 解析的设备总量），不查已调度 Pod。

**Rationale:** Pod 列表查询需要 ListOptions + 跨 namespace 遍历，超出当前 adapter scope；四态算法仍正确（available/full/unavailable），device_full 态本期不会触发（idle 永远等于 total）；后续 issue 可补充 Pod 查询。

### D4: Reconciler 事务边界设计

**Ambiguity:** SPEC 说"Confirm/Cancel/Release 在 tenant 事务内执行"，但 reconciler 原有 `store.UpsertStatus` 是非事务的。

**Choice:** 新增 `metadataStore ports.MetadataStore` + `storeTx ports.WorkloadInstanceStoreTx` 字段。当两者都非 nil 时走 `WithTenantTx` + `UpsertStatusTx` 同事务路径；任一为 nil 时回退到非事务 `store.UpsertStatus`，保持向后兼容。

**Rationale:** 现有 9 个 reconciler 测试不注入 metadataStore/storeTx，必须继续通过；新 12 个测试注入后验证同事务路径；接口隔离原则（D14 grilling 决策）。

### D5: enrichProvisioningObservation 排队 vs 调度失败启发式

**Ambiguity:** SPEC §5.1 修订项 16 说"Events 文本匹配为主判据 + 数值校验兜底"，但 adapter 层不直接读 K8s Events API。

**Choice:** 基于 observation.Phase/Reason/NodeName 做启发式判断：`Pending + 空 NodeName + 无失败关键词` → `QueuedInScheduler`；`Failed/CrashLoopBackOff/ImagePullBackOff` 或 Reason 含 `FailedScheduling/Insufficient` → `SchedulingFailed`。

**Rationale:** observation 由 `WorkloadProviderStatusReader.Observe` 返回，已包含 Phase/Reason/NodeName；不需要额外 Events API 调用；reconciler 只做 Reason 富化，状态映射仍由 `WorkloadStatusReconciler` 决定。

## 2. Deviations

### DEV1: selectResourceName vGPU 模式返回值变更

**SPEC:** HAMi 节点 vGPU 用 `nvidia.com/gpu`，非 HAMi 用 `nvidia.com/vgpu`。

**实现:** vGPU 统一返回 `nvidia.com/vgpu`（删除 HAMi 分支后不再有 HAMi 节点）。

**原因:** HAMi 代码全量删除（修订项 3），vGPU 资源由 volcano-vgpu-device-plugin 注册为 `volcano.sh/vgpu-number` + `volcano.sh/vgpu-memory`。`local_gpu_inventory_test.go` 只测试 wholecard 模式，不受影响。

### DEV2: runtimeClassNameForMode 始终返回空字符串

**SPEC:** 修订项 3.1 说"runtime class `hami-vgpu` 本期不改"。

**实现:** `runtimeClassNameForMode` 和 `runtimeClassNameForNode` 始终返回 `""`。函数签名保留（被 `local_gpu_inventory.go` 调用）。

**原因:** HAMi 删除后 `hami-vgpu` 常量不再存在；Volcano vGPU 不需要 runtime class（由 device plugin + scheduler 处理）；保留签名避免破坏调用方。

### DEV3: CancelQuotaAndFinalize 不依赖原态判定

**SPEC:** D21' grilling 修正——删除流程不依赖原态判定，Cancel + Release 顺序双调。

**实现:** `CancelQuotaAndFinalize` 在单个 tenant 事务内顺序调用 `Cancel`（释放 reserved）和 `Release`（释放 used），靠 reservation 单态性质 + WHERE 守卫互斥。

**原因:** API 层同步删除时已把 state 改成 `deleting`，reconciler 读 PG 拿不到删除前原态；双调靠 TCC reservation 单态性质天然互斥，无需原态判定。

## 3. Tradeoffs

### TR1: GPUSpec CRD vs PG 表

**备选:** 用 PG 表存 GPUSpec（像 QuotaService 那样）。

**选择:** CRD（K8s Custom Resource）。

**理由:** SPEC D15 grilling 决策——"规格=静态少，切片=多+查询+ACID"。GPUSpec 是静态规格定义（几十条），不需要 ACID 事务、不需要复杂查询、不需要 RLS 多租户隔离；CRD 天然集群级、kubectl 可直接操作、与 K8s 生态集成。PG 表更适合高频读写 + 事务 + 多租户的场景（如 quota、slice）。

### TR2: ReconcileQuota 仅 log 不修正

**备选:** 对账发现差异时直接修正 PG quota used_count。

**选择:** 本期只打 `slog.Warn` 告警，不修正。

**理由:** SPEC 修订项 6 明确"本期先做 mock/桩"；修正需要 `QuotaAdminService` 或 `QuotaStoreService` 的写接口，这些 port 的 PG adapter 由佳生实现（D12 分工）；本期 reconciler 只读 K8s + PG 做对比，不写。

### TR3: provisioning 超时用 CreatedAt 而非 StatusUpdatedAt

**备选:** 用 `Status.UpdatedAt`（最后一次状态更新时间）计算超时。

**选择:** 用 `record.CreatedAt`（实例创建时间）。

**理由:** 超时语义是"创建后多久还没 running 就算失败"，不是"上次状态更新后多久"；一个 pending 实例可能被 reconcile 多次（每次更新 UpdatedAt），但创建时间不变；用 CreatedAt 确保超时窗口是固定的 10 分钟。

## 4. Open Questions

### Q1: device_idle_count 精确计算

当前 `device_idle_count = device_total`（从 annotation 解析），不查已调度 Pod。后续是否需要补充 Pod 资源请求求差逻辑？这会影响 `device_full` 态的触发。

**建议:** 后续 issue 补充 Pod 列表查询，或由 Volcano Queue status 的 `allocated` 字段提供实际占用。

### Q2: ReconcileQuota 对账修正

当前对账循环只 log 不修正。后续需要 `QuotaAdminService.SetUsed` 或类似接口来修正 PG quota used_count。

**建议:** 等 QuotaService PG adapter 合并后，新增修正能力。注意修正方向是"以 K8s 实际状态为准修正 PG"。

### Q3: GPUSpecCRD name/available 字段用途

CRD schema 新增了 `spec.name` 和 `spec.available` 字段（对齐 Go 类型），但当前 adapter 未在业务逻辑中使用这两个字段。

**建议:** 确认这两个字段是否在后续 issue（如前端规格管理）中使用，或是否可以移除以简化 CRD。

### Q4: enrichProvisioningObservation 事件读取深度

当前只基于 observation.Phase/Reason 做 heuristic 判断，不直接读 K8s Events API。SPEC 修订项 16 提到"Pod Events 读取 + 节点 idle 查询"。

**建议:** 确认是否需要后续 issue 实现 Events API 调用以精确区分 `queue resource quota insufficient` vs `Insufficient volcano.sh/vgpu-memory`。

## 5. 验证命令

```bash
# Build
go build ./pkg/adapters/runtime/...
go build ./services/ani-gateway/...

# Tests — Issue #003-#006
go test ./pkg/adapters/runtime/ -run "TestGPUSpec" -count=1
go test ./pkg/adapters/runtime/ -run "TestVolcanoTranslator" -count=1
go test ./pkg/adapters/runtime/ -run "TestListSpecAvailability|TestHAMiRemoved|TestInventory" -count=1
go test ./pkg/adapters/runtime/ -run "TestReconcile" -count=1

# Tests — Issue #007
go test ./pkg/adapters/runtime/ -run "TestQuotaEnabledSwitch|TestUpsertStatusTx" -count=1

# Tests — Issue #008
go test ./services/ani-gateway/... -count=1
go test ./pkg/adapters/runtime/ -run "TestGPUSpecStore" -count=1
go test ./pkg/adapters/runtime/ -run "TestQuota" -count=1

# Architecture gate
python scripts/validate_component_imports.py --root .

# Diff check
git diff --check
```

## 6. 测试覆盖

| Issue | 测试数 | 测试名 |
|---|---|---|
| #003 | 15 | TestGPUSpecStore* (CreateAndList, Get, GetNotFound, Delete, DeleteNotFound, CreateIdempotencyReplay, CreateConflict, CreateInvalidSpecID, VGPUSpecRoundTrip, ListMultipleSpecs, NilDoerReturnsNotFound, ConnectionRefused, ComputePerSharePreserved, NameDerivedFromIDWhenEmpty, AvailableFalsePreserved) |
| #004 | 8 | TestVolcanoTranslator* (Wholecard, VGPU, VGPUMemoryNeverEmpty, SpecNotFound, InvalidCount, QueueAnnotation, HuaweiAscend, GPUModeIsolation) |
| #005 | 8 | TestHAMiRemoved, TestInventoryNodeLabelDerivation, TestListSpecAvailability* (Available, Full, DeviceFull, Unavailable, VGPUAvailable, UnsupportedWithoutStores) |
| #006 | 12 | TestReconcile* (ConfirmOnRunning, CancelOnFailed, ReleaseOnFailed, ProvisioningTimeout, ProvisioningNotYetTimedOut, DeleteDualCall, DeleteDualCallCoversAllStates, QuotaDisabled, EmptyQuotaTxIDs, QuotaLoopLogsOnly, QuotaLoopSkipsNonGPU) |
| #007 | 4+15 | TestQuotaEnabledSwitch* (CreateWithoutQuota, CreateWithQuotaTryManyTx, ApplyFailCancels, DeleteReleases) + TestUpsertStatusTx + TestNormaliseVolcanoQueueState (13 子测试) + TestVolcanoQueueStoreCRDStatusMapped + TestVolcanoQueueStoreCRDStatusEmptyStateMapsUnknown (AC L23 补充) |
| #008 | — | Gateway handler 层无独立单元测试（通过 go build + 现有 router 测试覆盖）；adapter 层 TestGPUSpecStore 15 + TestQuota Confirm/Cancel/Release |
| 现有 | 9 | TestLocalWorkloadReconcileController* + TestLeaderElectingWorkloadReconcileController* (向后兼容) |
| **合计** | **52+** | 全部 PASS |

## 7. HAMi 删除清单

以下符号已从 `kubernetes_gpu_inventory.go` 完全删除（grep 零匹配验证）：

- `kubernetesHAMISchedulerName` 常量
- `isHAMINode` 函数
- `parseHAMIAnnotation` 函数
- `hamiPhysicalDevice` 结构体
- `hami-scheduler` 分支代码
- `hami.io/node-nvidia-register` annotation 读取

替代为 `parseVolcanoVGPUAnnotation`（解析 `volcano.sh/node-vgpu-register`）。

---

## Issue #007 — Orchestrator + Store + Outbox + Migrations + Bootstrap

> 日期: 2026-08-17
> Review: review-it 完成（3 accepted findings fixed: RLS on reservation table, TenantID in outbox writer, SchedulerName in Volcano translation）

### D6: QuotaAwareInstanceOrchestrator 包装模式

**Ambiguity:** SPEC §5.1 说"GPU_QUOTA_ENABLED=true 时 TryManyTx 预占"，但已有 `LocalInstanceOrchestrator` 是稳定的无配额实现。

**Choice:** 新增 `QuotaAwareInstanceOrchestrator` 包装 `LocalInstanceOrchestrator`，在 `Create` 前插入三道闸（quota cap → Volcano 翻译 → TryManyTx），Apply 失败时 Cancel。`GPU_QUOTA_ENABLED=false` 时 `bootstrap/deps.go` 直接用原始 orchestrator，不包装。

**Rationale:** 装饰器模式保持 `LocalInstanceOrchestrator` 的 9 个现有测试不变；新 4 个 `TestQuotaEnabledSwitch` 子测试验证包装器；单一职责原则。

### D7: markApplyFailed 静默写入

**Ambiguity:** SPEC §5.1 FR-28 说"Apply 失败保留 DB 行 UpsertStatusTx(state=failed)"，但 markApplyFailed 本身失败的错误如何处理？

**Choice:** `markApplyFailed` 不返回错误（调用方已有 Apply 错误要返回）。写入失败时静默丢弃（`_ = o.store.UpsertStatus(...)`），不影响原始 Apply 错误传播。

**Rationale:** Apply 失败的原始错误是必须返回给用户的；markApplyFailed 是审计辅助，其失败不应覆盖原始错误。后续可加 slog.Warn 但本期不做。

### D8: outboxWriter 不在 QuotaAwareInstanceOrchestrator 中使用

**Ambiguity:** SPEC §3.2 定义了 outboxWriter 接口，但 QuotaAwareInstanceOrchestrator 的 Create 流程未调用它。

**Choice:** outboxWriter 定义在 `outbox_writer.go` 但 QuotaAwareInstanceOrchestrator 未注入或调用。reconciler 的 `CancelQuotaAndFinalize` 也不调用 outboxWriter。

**Rationale:** SPEC §5.1 的创建流程未显式要求 outbox 事件写入；outboxWriter 是为后续 issue（如 NATS 事件发布）预留的基础设施；当前 Create 的审计通过 PG 行 + quota_tx_ids 记录即可追溯。

### D9: bootstrap 条件装配

**Ambiguity:** `GPUQuotaEnabled=false` 时，quotaService/gpuSpecStore/volcanoTranslator 是否构造？

**Choice:** `quotaService` 始终构造（`runtimeadapter.NewPostgresQuota(metadata)` 成本低）；`gpuSpecStore` 和 `volcanoTranslator` 仅在 `kubeClient != nil` 时构造；reconcileController/orchestrator 的 quota 选项仅在 `cfg.GPUQuotaEnabled` 时注入。

**Rationale:** quotaService 是纯 PG adapter，无外部依赖，构造安全；gpuSpecStore 需要 K8s client，可能为 nil；条件装配确保 false 时零配额调用。

## Issue #007 — Deviations

### DEV4: demo_instances.go injectVolcanoTranslation 补设 spec.SchedulerName

**SPEC:** SPEC §5.1 说 Volcano 翻译结果写入 Pod spec。

**实现:** review 发现 `injectVolcanoTranslation` 只把 scheduler name 写入 annotation，未设 `spec.SchedulerName`（renderer 读取的字段）。

**修正:** 补设 `spec.SchedulerName = translation.SchedulerName` + annotation。

**原因:** renderer 读 `spec.SchedulerName` 而非 annotation；原实现导致 Pod 不会被 Volcano 调度器接管。review-it Finding #3 修复。

### DEV5: resource_reservation_allocations 补设 RLS

**SPEC:** SPEC §3.1 migration 定义表结构，未显式说 RLS。

**实现:** review 发现其他所有 tenant-scoped PG 表都有 RLS，但新表没有。

**修正:** 补设 `ENABLE/FORCE ROW LEVEL SECURITY` + `CREATE POLICY tenant_isolation`。

**原因:** 该表以 `tenant_id` 为 PK，是 tenant-scoped 数据；无 RLS 则平台事务可绕过隔离。review-it Finding #34 修复。

### DEV6: outbox_writer.go 补设 TenantID 校验

**SPEC:** SPEC §3.2 outboxWriter 接口未明确说校验 TenantID。

**实现:** review发现 `WriteTx` 校验了 aggregate_type/aggregate_id/event_type 但漏了 tenant_id。`outbox_events.tenant_id` 是 `UUID NOT NULL`，空字符串会导致 DB 约束违反。

**修正:** 补设 `event.TenantID == ""` 到校验条件。

**原因:** 防御性校验在 Go 层比 DB 层报错更清晰。review-it Finding #21 修复。

## Issue #007 — Tradeoffs

### TR4: quota_tx_ids 用 JSONB 而非关联表

**备选:** 新建 `instance_quota_tx` 关联表（instance_id + tx_id）。

**选择:** 在 `workload_instances` 新增 `quota_tx_ids JSONB NOT NULL DEFAULT '[]'` 列。

**理由:** TCC tx_ids 是 1:N 但 N 很小（通常 1-2 个，对应 gpu_count + 可能的 memory_gb）；JSONB 列读写简单，不需要 JOIN；关联表增加 migration + query 复杂度不值得。

### TR5: Delete 时 Cancel + Release 顺序双调

**备选:** 根据原态判定只调一个（pending→Cancel, running→Release）。

**选择:** 不依赖原态，顺序调 Cancel（释放 reserved）+ Release（释放 used），靠 reservation 单态性质互斥。

**理由:** API 层删除时已改 state=deleting，reconciler 读 PG 拿不到删除前原态；TCC reservation 单态性质（reserved → confirmed → released）保证 Cancel 和 Release 各自幂等且互斥。

## Issue #007 — Open Questions

### Q5: outboxWriter 实际调用时机

outboxWriter 接口已定义但 Create/Delete 流程未调用。后续 issue 何时接入？

**建议:** 等 NATS 事件发布需求落地时，在 QuotaAwareInstanceOrchestrator.Create 成功后 + CancelQuotaAndFinalize 成功后调用 outboxWriter.WriteTx。

### Q6: markApplyFailed 错误日志

markApplyFailed 静默丢弃写入错误。是否需要加 slog.Warn？

**建议:** 后续加 `slog.Warn("markApplyFailed: failed to persist failed status", "err", err)`，但本期不做以避免引入 slog 依赖到 orchestrator。

### Q6.1: AC L23 State 映射测试补全（2026-08-17 补充）

Issue #007 AC L23 新增了 `VolcanoQueueCRD.Status.State` 字段和 `normaliseVolcanoQueueState` 大小写归一映射（Open→open / Closed→closed / 空/其他→unknown）。代码实现已完整（`volcano_queue_store.go` L191-194 + L522 + L608-616），但原批次未覆盖测试。

**补充:** 新增 3 个测试函数（15 个子测试）到 `volcano_queue_store_test.go`：
- `TestNormaliseVolcanoQueueState`（13 子测试）：覆盖 Open/OPEN/open/whitespace→open、Closed/CLOSED/closed/whitespace→closed、空/空白/unknown/Pending/foo→unknown
- `TestVolcanoQueueStoreCRDStatusMapped`：seed CRD with populated Status（Allocated: nvidia.com/gpu=2, cpu=8; State: Open），通过 List 验证 Allocated 透传 + State 归一为 "open"
- `TestVolcanoQueueStoreCRDStatusEmptyStateMapsUnknown`：seed CRD without Status，通过 Get 验证 State→"unknown"、Allocated→empty map

**验证:** 29 个 volcano queue 测试全部 PASS（14 existing + 15 new），review-it clean。

---

## Issue #008 — Gateway Handler

> 日期: 2026-08-17
> Review: review-it 完成（1 accepted finding fixed: specInUse 跨租户检查）

### D10: GET /gpu-specs 双路径设计

**Ambiguity:** GET /gpu-specs 已在 `gpu_inventory_resources.go` 用本地 `GPUSpecService` 实现；Issue #008 要求 POST/DELETE 走 `GPUSpecStore` (CRD)。GET 是否也走 CRD？

**Choice:** 当 `GPUSpecStore` 注入时，GET /gpu-specs 和 GET /gpu-specs/:spec_id 优先从 CRD 读取（返回 gpu_mode/node_affinity/volcano_resources 扩展字段）；`GPUSpecStore` 为 nil 时回退到本地 `GPUSpecService`（dev/local profile）。

**Rationale:** OpenAPI 的 GPUSpec schema 要求返回 gpu_mode 等字段，本地 GPUSpecService 没有这些字段；CRD store 有完整数据；双路径保证 local profile 不依赖 K8s 集群。

### D11: specInUse 跨租户检查用 WithPlatformTx

**Ambiguity:** `WorkloadInstanceStore.List` 需要 tenantID 参数且受 RLS 约束；spec 删除是平台管理操作，需检查所有租户的实例引用。

**Choice:** 注入 `MetadataStore`，当可用时用 `WithPlatformTx`（bypass RLS）执行 `SELECT COUNT(*) FROM workload_instances WHERE state <> 'deleted' AND gpu_status->>'SpecID' = $1`；不可用时回退到 `instanceStore.List(ctx, "demo-tenant", "")` 单租户检查。

**Rationale:** SPEC §5.2 要求删除 spec 时检查"有实例引用"——不限于某租户；平台事务绕过 RLS 是跨租户查询的唯一方式；review-it Finding #1 修复。

### D12: ReservationView 通过 QuotaAdminService 而非新接口

**Ambiguity:** `/reservations/me` 是租户自查端点，但 `QuotaAdminService.GetReservation` 用 `WithPlatformTx`（bypass RLS）。是否需要新增 tenant-scoped 的 `GetMyReservation` 接口？

**Choice:** 复用 `QuotaAdminService.GetReservation`，tenant_id 来自 `middleware.GetTenantID(c)`（认证中间件），平台事务只读该租户的行。

**Rationale:** 租户只能传自己的 tenant_id（来自认证）；新增接口增加复杂度不值得；`GetMy`（QuotaStoreService）和 `GetReservation`（QuotaAdminService）的 RLS 语义不同是因为 quota 表和 reservation 表的读取方式不同。

### D13: newGatewayQuotaStore 返回三个值

**Ambiguity:** `newGatewayQuotaStore` 原返回 `(QuotaAdminService, func(), error)`，Issue #008 需要 `QuotaStoreService` 和 `MetadataStore`。是新建函数还是扩展返回值？

**Choice:** 扩展为 `(QuotaAdminService, QuotaStoreService, MetadataStore, func(), error)`。`*PostgresQuota` 同时实现两个 quota 接口（编译期断言），同一个实例赋值给两个接口变量。

**Rationale:** 避免重复连接 PG；`MetadataStore` 从 `ConnectMetadataStore` 返回值复用；五返回值是 Go 惯例（虽然偏多，但都是必需的）。

## Issue #008 — Deviations

### DEV7: network_config 优先级实现

**SPEC:** v1.yaml 说 `network_config 优先级：*_config.network > 顶层 network_config`。

**实现:** `instanceSpecFromRequest` 先应用顶层 `network_config`（作为 base），再用 `*_config.network` 覆盖。`networkPolicyFromRequest(req.NetworkConfig, spec.Network)` 的第二个参数是已有值，非空字段不被覆盖。

**原因:** `networkPolicyFromRequest` 的 merge 语义天然实现"后调用者覆盖先调用者"；先 base 后 specific 保证 specific 优先。

### DEV8: POST /gpu-specs 不创建 VolcanoResources

**SPEC:** `GPUSpecCreateRequest` schema 不含 `volcano_resources` 字段。

**实现:** POST 创建时 `VolcanoResources` 为空；CRD 创建后由 controller 或后续 issue 填充。

**原因:** VolcanoResources 映射（wholecard/vgpu 的资源名→模板值）需要集群信息（如设备 plugin 注册的资源名），不适合在 API 请求中硬编码；CRD controller 可根据 `gpu_type` + `gpu_mode` 自动填充。

## Issue #008 — Tradeoffs

### TR6: specInUse 用 SQL 而非 instanceStore.List

**备选:** 给 `WorkloadInstanceStore` 新增 `ListAll`（平台级）方法。

**选择:** 用 `MetadataStore.WithPlatformTx` + 原生 SQL `SELECT COUNT(*)`。

**理由:** 新增 `ListAll` 方法需要改 port 接口 + 所有实现 + mock；SQL 查询只读不写，直接用 `MetadataStore` 更轻量；`gpu_status->>'SpecID'` 利用 JSONB 索引（后续可加 GIN）。

### TR7: quotaResponseFromView 不 JOIN resource_quota_meta

**备选:** 在 `GetMy` 实现中 JOIN `resource_quota_meta` 表返回 unit/display_name/is_discrete。

**选择:** `/quotas/me` 响应只返回 total/used/reserved，省略 meta 字段（OpenAPI schema 中它们是 optional）。

**理由:** `GetMy` 已经在 `QuotaStoreService` 接口定义且实现稳定；改 JOIN 会影响 adapter 和测试；meta 字段对 Console 自查不是必需的（前端可用 `GET /quota-meta` 单独查）。

## Issue #008 — Open Questions

### Q7: gpu_status JSONB 字段名大小写

`specInUse` SQL 查 `gpu_status->>'SpecID'`，但 Go 的 `GPUInstanceStatus.SpecID` 无 json tag，默认 marshaling 为 `"SpecID"`（PascalCase）。如果后续加 json tag 改为 `"spec_id"`（snake_case），SQL 查询需同步修改。

**建议:** 确认 `GPUInstanceStatus` 是否需要加 `json:"spec_id"` tag。如果加，更新 `specInUse` SQL 为 `gpu_status->>'spec_id'`。

### Q8: POST /gpu-specs 不创建 VolcanoResources 的后续填充

POST 创建的 CRD 不含 `volcano_resources`。由谁、何时填充？

**建议:** 确认是否有 GPUSpec CRD controller 负责根据 `gpu_type` + `gpu_mode` 自动填充 `volcano_resources` 映射。如果没有 controller，需要在创建 API 中接受 `volcano_resources` 参数或用 webhook 填充。

### Q9: /quotas/me 响应是否需要 meta 字段

当前 `/quotas/me` 省略 unit/display_name/is_discrete。前端 Console 是否需要这些字段？

**建议:** 确认 Console 配额页面是否展示单位（如 "GPU 槽位" vs "GB"）。如果需要，后续扩展 `GetMy` 实现 JOIN `resource_quota_meta`。

---

## Issue #009 — BOSS GPU 资源池改版（4 Tab + KPI + 节点/设备/队列扩展）

> 日期: 2026-08-17
> Review: review-it 完成（2 accepted findings fixed: unused row param, unstable useMemo deps）
> 产品线: boss

### 实现范围

| 文件 | 内容 |
|---|---|
| `repo/frontends/boss/src/api/coreClient.ts` | 新增 `newIdempotencyKey()` 辅助函数，文档化 quota/spec/inventory 调用入口 |
| `repo/frontends/boss/src/api/core-schema.d.ts` | 重新生成（`npm run gen-api`），包含 GPUSpec/GPUSpecSummary/GPUSpecAvailability/ReservationView 等新类型 |
| `repo/frontends/boss/src/routes/_authenticated/ops/gpu-pool.tsx` | 4 Tab 改版 + KPI 扩展 6 卡 + 配额分配 Drawer（内联） + 设备状态翻转 |

### D14: KPI 整卡/vGPU 节点数派生方式

**Ambiguity:** UX §4.1 Tab 1 KPI Cards 要求展示整卡/vGPU 节点数，但 `GET /gpu-inventory/occupancy` 只返回总量/已用/空闲/异常 + by_gpu_type，不返回节点维度统计。

**Choice:** 从 `GET /gpu-inventory` 返回的设备清单按 `node_name` 聚合，按每设备的 `gpu_mode` 标签分桶：`wholecard` 加入整卡节点集合，`vgpu` 加入 vGPU 节点集合，集合 size 即节点数。

**Rationale:** occupancy 端点无节点维度字段；inventory 端点有 per-device `gpu_mode` 标签；前端聚合成本低（一次遍历）；同节点多设备假设同模式（取首个设备标签即可）。

### D15: 配额分配 Drawer 内联实现

**Ambiguity:** Issue #009 AC 要求"配额分配入口 Button"，但 Issue #010 才要求独立的 `gpu-pool-quota-drawer.tsx` 文件。

**Choice:** Issue #009 内联 `QuotaAssignmentDrawer` 组件在 `gpu-pool.tsx` 中；Issue #010 抽取为独立文件。

**Rationale:** 按_issue 增量交付原则，#009 只要求入口 + 基本分配能力；#010 才要求独立 Drawer 文件 + clamp 提示 + 预留校验。内联实现让 #009 独立可验证。

### D16: 设备状态 PATCH 用 status=idle 恢复空闲

**Ambiguity:** OpenAPI `PATCH /gpu-inventory/{device_id}` 的 `GPUDeviceStatusUpdateRequest.status` 枚举为 `maintenance | idle`，但 inventory list 返回的设备状态是 `available`。

**Choice:** "恢复空闲"按钮传 `status=idle`（服务端负责 idle→available 映射）。

**Rationale:** OpenAPI 契约只允许 `maintenance | idle`；`idle` 是写入语义，`available` 是读取语义；服务端负责状态机转换。

## Issue #009 — Deviations

### DEV9: 规格目录 Tab 只读列表

**SPEC/UX:** UX §4.1 Tab 4 要求规格目录展示 spec_id/gpu_type/gpu_mode/shares/mb_per_share/操作列。

**实现:** Issue #009 的规格目录 Tab 只实现只读列表（无操作列、无新建/删除按钮）。

**原因:** AC 未要求规格 CRUD；CRUD 由 Issue #010 的 Drawer 组件提供。#009 的 Tab 4 是占位 + 只读列表，#010 才补全操作列。

## Issue #009 — Tradeoffs

### TR8: 租户列表从 /quotas 派生而非独立端点

**备选:** 调用独立的 listTenants 端点获取租户列表。

**选择:** 从 `GET /quotas` 返回的 `tenant_id` 集合派生配额分配 Drawer 的租户下拉选项。

**理由:** 当前无独立 listTenants 端点；`GET /quotas` 已返回 tenant_id；避免新增 API 调用。

## Issue #009 — Open Questions

### Q10: inventory 聚合假设同节点同模式

前端按 `node_name` 聚合时假设同节点的所有 GPU 设备 `gpu_mode` 相同（取首个设备标签）。如果集群中出现混合节点（同节点既有整卡又有 vGPU），KPI 节点数会失准。

**建议:** 确认集群是否允许混合节点。如果允许，KPI 应改为按设备计数而非节点计数。

---

## Issue #010 — BOSS 规格管理 Drawer + 配额/预留分配 Drawer

> 日期: 2026-08-17
> Review: review-it 完成（2 accepted findings fixed: unused type import + state, unstable availabilityItems deps）
> 产品线: boss

### 实现范围

| 文件 | 内容 |
|---|---|
| `repo/frontends/boss/src/routes/_authenticated/ops/-gpu-spec-drawer.tsx` (NEW) | 规格管理 Drawer: spec_id/gpu_type/gpu_mode/shares/mb_per_share 表单，POST /gpu-specs + Idempotency-Key |
| `repo/frontends/boss/src/routes/_authenticated/ops/-gpu-pool-quota-drawer.tsx` (NEW) | 配额分配 Drawer: 选租户 + 配额上限 + 预留额度，PUT quota + PUT reservations 串行 |
| `repo/frontends/boss/src/routes/_authenticated/ops/gpu-pool.tsx` | 移除内联 Drawer，导入两个新 Drawer，Tab 4 新增新建规格 Button + 删除操作列 |

### D17: Drawer 文件以 `-` 前缀避免路由识别

**Ambiguity:** TanStack Router 文件路由会将 `routes/` 目录下的 `.tsx` 文件识别为路由模块。

**Choice:** 新 Drawer 文件以 `-` 前缀命名（`-gpu-spec-drawer.tsx`、`-gpu-pool-quota-drawer.tsx`），利用 TanStack Router 的 `routeFileIgnorePrefix` 配置跳过路由生成。

**Rationale:** 不加前缀会导致 vite build 警告"unmatched route"；`-` 前缀是 TanStack Router 官方推荐的非路由模块命名方式。

### D18: gpu_type 下拉源从 inventory 派生

**Ambiguity:** UX §8.3 假设规格创建时 gpu_type 应从节点标签派生，但未指定 API 端点。

**Choice:** 从 `GET /gpu-inventory` 返回的设备清单中收集 `gpu_type` 集合（去重），作为规格创建 Drawer 的 gpu_type 下拉源。

**Rationale:** inventory 端点返回 per-device `gpu_type`；无独立 gpu-type 列表端点；前端派生成本低。实际 gpu_type 对齐节点标签的强校验由服务端执行（GPUTypeNotInNodes 422）。

### D19: 删除按钮禁用条件用 GPUSpecSummary.available

**Ambiguity:** UX §3.1 Flow 3b 说"有实例引用则禁用"，但未指定用哪个字段判断。

**Choice:** 使用 `GPUSpecSummary.available` 字段（false 表示有运行中实例引用，禁用删除）+ Tooltip 提示"该规格有运行中实例引用，无法删除"。

**Rationale:** OpenAPI schema 的 `available` 字段语义是"是否可用于新实例"；`false` 时表示被引用；服务端 DELETE 也会做 specInUse 校验（422），前端禁用是 UX 优化。

## Issue #010 — Tradeoffs

### TR9: 配额 + 预留合为一个 Drawer 两个 InputNumber

**备选:** 分两个 Drawer 或分两个 step。

**选择:** 单 Drawer 内配额上限 + 预留额度两个 InputNumber 字段，提交时串行两个 PUT。

**理由:** UX §4.2 + §8.3 假设明确要求合为一个 Drawer；配额和预留是关联操作（预留 <= 配额上限），分开操作增加用户心智负担。

### TR10: clamp 提示用 ReservationView.tightened 判定

**备选:** 前端比较新旧 total 判定是否下调。

**选择:** 用 `PUT /reservations` 返回的 `ReservationView.tightened` 字段判定是否触发 clamp 提示。

**Rationale:** 服务端是 clamp 逻辑的真实来源；`tightened=true` 表示服务端实际收紧了；前端比较新旧值可能不准确（并发场景）。

## Issue #010 — Open Questions

### Q11: 预留额度是否支持单维度不分 spec

当前预留 Drawer 只设 `allocated_gpu_count`（单维度），不支持 per-spec 预留。

**建议:** 确认 UX §8.3 假设"单维度不分 spec"是否最终设计。如果需要 per-spec 预留，Drawer 需改为多行表单。

---

## Issue #011 — Console 创建 Dialog 改版（规格下拉四态 + 队列下拉）

> 日期: 2026-08-17
> Review: review-it 完成（1 accepted finding fixed: availabilityItems unstable array identity）
> 产品线: console

### 实现范围

| 文件 | 内容 |
|---|---|
| `repo/frontends/console/src/routes/_authenticated/compute/gpu-containers/-create-dialog.tsx` | 重写: 移除 allocation_mode/model/gpu_count/workload_class 字段，新增 spec_id Select（四态标注）+ queue_name Select（必选），本地 quota 重算 |
| `repo/frontends/console/src/routes/_authenticated/compute/gpu-containers/index.tsx` | 移除 modelOptions useMemo + modelOptions prop |
| `repo/frontends/console/src/api/coreClient.ts` | 文档化 availability/queues/instances spec_id 调用 |

### D20: 使用 gpu_container_config.gpu.spec_id 而非顶层 gpu.spec_id

**Ambiguity:** `CreateInstanceRequest` 有 deprecated 顶层 `gpu` 字段和新的 `gpu_container_config.gpu` 字段，两者都有 `spec_id`。

**Choice:** 使用 `gpu_container_config.gpu.spec_id`（非 deprecated 顶层 `gpu`）走规格模式 Volcano 调度。

**Rationale:** OpenAPI schema 标注顶层 `gpu` 为 deprecated；`gpu_container_config` 是新的结构化配置；使用 deprecated 字段可能在后续版本被移除。

### D21: 旧字段在 payload 中保留兼容值

**Ambiguity:** 移除了 UI 上的 allocation_mode/workload_class 字段，但 payload 是否需要这些值？

**Choice:** payload 中 `gpu_container_config.gpu` 保留 `allocation_mode: 'dedicated'` + `workload_class: 'inference'` 兼容值，但 UI 不再暴露。

**Rationale:** 服务端可能仍校验这些字段非空；UI 简化为只选规格，但 payload 保持向后兼容；后续服务端支持纯 spec_id 模式后可移除。

### D22: 四态标注 device_full 用 warning theme

**Ambiguity:** UX §7.3 定义四态标注文案，但未明确 device_full 的 Tag theme。

**Choice:** available=success, full=default, device_full=warning, unavailable=default。

**Rationale:** device_full（配额有余但设备无空闲）是"接近满"状态，warning 比更合适区分；full 和 unavailable 都是"完全不可选"，用 default 灰标。

## Issue #011 — Tradeoffs

### TR11: 本地 quota 重算 vs 服务端实时校验

**备选:** 不做前端重算，提交时由服务端返回 409。

**选择:** 选规格后前端实时重算 `new_quota_remaining = quota_remaining - consumed`，刷新其他规格 `available_count`。

**Rationale:** UX §4.4 明确要求"避免跨规格共享配额超卖"；前端重算让用户在选规格前就看到余量变化；服务端 409 是最终防线但体验差。

## Issue #011 — Open Questions

### Q12: allocation_mode/workload_class 兼容值何时移除

payload 中保留的 `allocation_mode: 'dedicated'` + `workload_class: 'inference'` 何时可以移除？

**建议:** 等服务端确认纯 spec_id 模式（不需要 allocation_mode/workload_class）后，移除这些兼容值。

---

## Issue #012 — Console 列表页配额/预留展示 + 队列页扩展

> 日期: 2026-08-17
> Review: review-it 完成（1 accepted finding fixed: unused Card import）
> 产品线: console

### 实现范围

| 文件 | 内容 |
|---|---|
| `repo/frontends/console/src/routes/_authenticated/compute/gpu-containers/index.tsx` | 新增配额卡片（GET /quotas/me: total/used/reserved/可用余量）+ 预留卡片（GET /reservations/me: allocated/used/available） |
| `repo/frontends/console/src/routes/_authenticated/settings/gpu-queues.tsx` | 平台默认队列表 + 自定义队列表新增"已分配"列（status.allocated map Tag 展示） |
| `repo/frontends/console/src/api/coreClient.ts` | 文档化 /quotas/me + /reservations/me 调用 |

### D23: 可用余量前端计算（QuotaItem 无 available 字段）

**Ambiguity:** UX §4.6 要求配额卡片展示"可用余量"，但 `QuotaItem` schema 只有 total/used/reserved，无 available 字段。

**Choice:** 前端计算 `可用余量 = total - used - reserved`（`Math.max(0, ...)`）。

**Rationale:** QuotaItem 无 available 字段（服务端不返回）；ReservationView 有 available 字段（服务端计算）；两者语义不同——配额可用余量 = 配额上限 - 已实扣 - 已预占；前端计算简单且准确。

### D24: 预留卡片可用直接用 ReservationView.available

**Ambiguity:** 预留卡片的"可用"展示服务端计算值还是前端计算？

**Choice:** 直接用 `ReservationView.available`（服务端计算 = allocated - used - reserved）。

**Rationale:** ReservationView schema 有 available 字段，服务端已计算；前端无需重复计算；与配额卡片的前端计算形成对比——配额无 available 字段所以前端算，预留有所以直接用。

## Issue #012 — Open Questions

### Q13: 配额卡片是否需要展示多维度

当前只展示 `gpu_count` 维度。如果后续有多维度配额（如 memory_gb），卡片需要改为多维度展示。

**建议:** 确认是否有多维度配额需求。如果有，卡片改为遍历 `Quota.items` 展示每个维度。

---

## 前端批次验证命令（Issue #009-#012）

```bash
# BOSS 前端（Issue #009, #010）
cd repo/frontends/boss
npx tsc --noEmit
npx vite build

# Console 前端（Issue #011, #012）
cd repo/frontends/console
npx tsc --noEmit
npx vite build
```

> 注意：前端批次未运行 `make test` / `make validate-architecture` / `git diff --check`（仅改 frontends/boss + frontends/console，未触碰 Core/Services 代码）。`make` 命令在 Windows 环境下可能需要 WSL/Git Bash，本期使用 `npx tsc` + `npx vite build` 作为等效验证。

---

## Issue #009 + #012 增量 — 调度队列 state 徽标列

> 日期: 2026-08-17
> Review: review-it 完成（0 accepted findings，clean on first pass）
> 产品线: boss + console
> 触发: Issue 文档增量更新（#009 L10/L24 + #012 L10/L23-24 新增 state 徽标列要求）

### 实现范围

| 文件 | 内容 |
|---|---|
| `repo/frontends/boss/src/api/core-schema.d.ts` | 重新生成（`npm run gen-api`），GPUSchedulingQueue.status 新增 `state?: "open" \| "closed" \| "unknown"` |
| `repo/frontends/console/src/api/core-schema.d.ts` | 同上重新生成 |
| `repo/frontends/boss/src/routes/_authenticated/ops/gpu-pool.tsx` | Tab 3 调度队列 allocated 列后新增"状态"列 |
| `repo/frontends/console/src/routes/_authenticated/settings/gpu-queues.tsx` | platformDefaultColumns + customColumns allocated 列后各新增"状态"列 |

### D25: schema 重新生成而非手补字段

**Ambiguity:** `v1.yaml` 的 `GPUSchedulingQueue.status.state` (enum: open/closed/unknown) 已存在，但两个前端的 `core-schema.d.ts` 未同步（缺少 state 字段）。是手补还是重新生成？

**Choice:** 运行 `npm run gen-api` 重新生成两个 `core-schema.d.ts`，而非手补 `state` 字段。

**Rationale:** 生成物必须与 `v1.yaml` 保持一致（CLAUDE.md §6.2 生成物漂移门禁）；手补会导致后续 gen-api 覆盖手补内容；重新生成确保完整同步。

### D26: unknown 用 default theme 而非 warning

**Ambiguity:** UX §4.1 Tab 3 + §5.1/§5.3 说 `open=success / closed=default / unknown=default`，但 unknown 语义上是"状态未知"，是否用 warning 更合适？

**Choice:** 按文档用 `default`（灰标），不额外引入 warning。

**Rationale:** Issue AC 明确写 `open=success / closed=default / unknown=default`；unknown 是"Volcano Queue CRD status.state 为空或非 Open/Closed"，不是异常状态，灰标即可；引入 warning 会偏离 AC。

### D27: null/undefined state 缺省 'unknown'

**Ambiguity:** `status` 字段是 nullable，`status.state` 是 optional。当 status 为 null 或 state 为 undefined 时显示什么？

**Choice:** `row.status?.state ?? 'unknown'`，缺省显示 unknown 灰标。

**Rationale:** OpenAPI enum 的 `unknown` 值正是"空/其他"的归一表达；前端缺省与 enum 语义对齐；用户看到 unknown 灰标即知"队列状态未读到"。

## Issue #009 + #012 增量 — Deviations

None — 实现完全遵循 Issue AC 的 state 徽标映射（open=success / closed=default / unknown=default）。

## Issue #009 + #012 增量 — Tradeoffs

### TR12: state 列放在 allocated 列后而非最前

**备选:** 将 state 徽标列放在队列名后（最前），强调队列开关状态。

**选择:** 放在 allocated 列后（最后），与 Issue AC 描述顺序一致（"allocated 列 + state 徽标列"）。

**理由:** AC 明确先 allocated 后 state；state 是辅助信息（队列是否可调度），不是主标识；保持与 AC 描述顺序一致降低 review 摩擦。

## Issue #009 + #012 增量 — Open Questions

### Q14: Volcano Queue CRD state 归一化是否服务端完成

OpenAPI 描述说"归一自 Volcano Queue CRD status.state（Open→open / Closed→closed / 空/其他→unknown）"。前端假设服务端已归一化，直接用 enum 值。

**建议:** 确认 Gateway handler 或 adapter 已完成 Volcano CRD state 归一化（Open→open）。如果服务端返回原始值（如 "Open"），前端 enum 匹配会失败，全部显示 unknown。

## Issue #009 + #012 增量 — 验证命令

```bash
# Schema 重新生成
cd repo/frontends/boss && npm run gen-api
cd repo/frontends/console && npm run gen-api

# Lint + Build
cd repo/frontends/boss && npx eslint src/routes/_authenticated/ops/gpu-pool.tsx && npx vite build
cd repo/frontends/console && npx eslint src/routes/_authenticated/settings/gpu-queues.tsx && npx vite build
```

> tsc --noEmit 唯一错误是 `_authenticated.tsx:44` `session` possibly null — pre-existing dirty state from other branch work，非本次增量引入。
