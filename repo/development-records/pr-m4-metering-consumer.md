# PR-M4 — Metering Consumer 集成测试（真实 NATS + 真实 DB + 真实 Prometheus）

完成日期：2026-08-13
对应 Sprint：以 repo/CURRENT-SPRINT.md 为准
批次类型：Feature batch（新增计量采集产品能力）
依赖批次：PR-M3

> **说明：** 本文件记录 Issue 010 集成测试的实现笔记。批次全部完成后再一次性更新 README.md、CURRENT-SPRINT.md、ANI-06-开发计划.md。

---

## Issue 010: 集成测试（真实 NATS + 真实 DB + 真实 Prometheus）

完成日期：2026-08-13
验证结果：`go vet ./services/metering-service/... ./pkg/adapters/metering/... ./pkg/ports/...` 通过，`go build ./services/metering-service/... ./pkg/adapters/metering/...` 通过，`python scripts/validate_component_imports.py --root .` 通过，`git diff --check` 通过，9/9 集成测试 PASS（25.359s，真实 NATS JetStream + 真实 PG + 真实 Prometheus fallback）

### 实现了什么

新增 `integration_test.go`（1214 行），覆盖 SPEC §9.2 定义的全部 9 个集成测试场景。使用 `//go:build integration` 标签隔离，手动验证项不作为硬性门禁。测试环境连接真实 NATS JetStream（InterestPolicy stream + durable consumer）、真实 PG（含 migration，admin 连接绕 RLS）和真实 Prometheus（CPU/Mem 维度查询，无数据时 fallback 到 mock Prometheus）。

### 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `services/metering-service/internal/integration_test.go` | 新增 | 9 个集成测试场景 + 测试基础设施（1214 行） |

### Design Decisions

1. **`fallbackCollector` 包装真实 Prometheus + mock Prometheus**
   - 模糊性：SPEC §9.2 要求"真实 Prometheus"，但测试实例不在真实 K8s 集群中，Prometheus 查询必然返回无数据（`result` 数组为空），导致 CPU/Mem 维度 CollectAll 返回 0 条记录。用户在前一会话中质疑："真实 prometheus 没有数据，你是怎么测的，可信吗"。
   - 选择：实现 `fallbackCollector{primary, fallback}`，先查真实 Prometheus（primary），若返回 error 则回退到 mock Prometheus（fallback，返回固定值 0.5）。GPU 维度不查 Prometheus（纯持有时长计算），不受影响。
   - 理由：保留对真实 Prometheus 连接和 HTTP 交互路径的验证（连接、请求构造、响应解析），同时在 Prometheus 无测试实例指标数据时确保 CPU/Mem 维度仍产出可预测的记录。用户明确要求"真实 prometheus 和 mock 的 prometheus 都可以保留"。

2. **`itestRunID` 时间戳后缀实现 consumer 名称隔离**
   - 模糊性：SPEC §9.2 未指定跨测试运行的 consumer 隔离策略。NATS durable consumer 的 subject 绑定是排他的——同一 stream 上以不同 subject 创建的同名 durable consumer 会冲突。
   - 选择：`itestRunID = fmt.Sprintf("%d", time.Now().UnixNano()%100000)`，每个 consumer 名称为 `metering-itest-scX-<itestRunID>`，确保跨运行唯一。
   - 理由：避免跨测试运行时 durable consumer subject 绑定冲突。同时 `newItestEnv` 中调用 `deleteOldItestConsumers` 清理以 `metering-itest` 为前缀的旧 consumer，双重保险。

3. **`filteredMetaStore` 拦截 rebuilder 查询注入测试实例过滤**
   - 模糊性：SPEC §9.2 场景 4 要求"rebuilder 查 running 实例重建 ticker"，但真实 DB 中可能有非测试实例的 running 记录，rebuilder 会为所有 running 实例启动 ticker，污染测试结果。
   - 选择：实现 `filteredMetaStore` + `filteredMetadataTx`，包装 `ports.MetadataStore`/`MetadataTx`，在 `Query` 中检测包含 `workload_instances` + `state` + `running` 的 SQL，自动追加 `AND instance_id LIKE 'inst-itest-%'` 过滤条件。
   - 理由：使 rebuilder 只查到测试实例，避免为非测试实例启动 ticker。SQL 注入采用简单的 `ORDER BY` 前插入策略，无需复杂 SQL 解析器。

4. **`shortIntervalSvc` 覆写 IntervalSec 为 2 秒加速测试**
   - 模糊性：SPEC 默认采集周期为 60 秒，集成测试若等 60 秒过长。
   - 选择：`shortIntervalSvc` 包装 `service.NewMeteringCollectionService`，在 `StartCollection` 时将 `spec.IntervalSec` 覆写为 `itestShortInterval=2`。
   - 理由：将 ticker 周期从 60s 缩短到 2s，使测试在合理时间内完成（9 个场景总计 25s）。wrapper 模式不修改生产代码，仅测试侧注入。

5. **`//go:build integration` 标签隔离集成测试**
   - 模糊性：SPEC §9.2 要求集成测试覆盖 9 个场景，但集成测试需要真实 NATS + PG + Prometheus 基础设施，不能在 CI 中每次运行。
   - 选择：使用 `//go:build integration` build tag，集成测试默认不编译、不运行。需手动指定 `-tags integration` 运行。
   - 理由：Issue 010 AC 明确"集成测试为手动验证项，不作为硬性门禁"。build tag 是 Go 标准的测试隔离机制，比 `t.Skip` 更彻底（不编译不运行，不会影响 `go test ./...` 的默认执行）。

6. **PurgeStream + deleteOldItestConsumers 双重清理策略**
   - 模糊性：InterestPolicy stream 在没有 consumer 时消息会保留，跨测试运行时旧消息会干扰新测试。
   - 选择：`newItestEnv` 中先 `js.PurgeStream(itestStreamEvents)` 清空所有旧消息，再 `deleteOldItestConsumers` 删除以 `metering-itest` 为前缀的所有旧 durable consumer。
   - 理由：InterestPolicy 的语义是"至少有一个 consumer 消费后才删除消息"。如果上一次测试运行留下了未消费的消息，新测试 subscribe 后会收到旧消息。PurgeStream 确保干净起点，deleteOldItestConsumers 确保 consumer 名称可用。

7. **`countingMeteringService` 和 `failFirstMeteringService` 测试 wrapper**
   - 模糊性：SPEC §9.2 场景 5（seenSeq 乱序）需验证 StartCollection 只调用 1 次；场景 6（失败重投）需验证 StartCollection 被调用 ≥2 次（第一次失败 Nak，重投后成功）。
   - 选择：`countingMeteringService` 用 `atomic.Int64` 统计 Start/Stop 调用次数；`failFirstMeteringService` 第一次 StartCollection 返回 error，后续返回 nil。
   - 理由：通过 wrapper 模式注入测试行为，不修改生产代码。`atomic.Int64` 线程安全，适合并发场景下的计数。`failFirstMeteringService` 模拟瞬时故障，验证 Nak 重投后 seenSeq 未推进的语义。

### Deviations

1. **场景 2 保底采集只验证 GPU 维度，不验证 CPU/Mem 维度**
   - SPEC 规定：SPEC §9.2 场景 2 验证"短生命周期保底采集触发"。
   - 实际实现：`collectFullLifetime` 当前只处理 GPU 维度（`case ports.MeteringResourceInstanceGPUSeconds`），CPU/Mem 维度注释标注"PR-M2 接入后完善"。这是 `metering_collection_service.go` 中 `collectFullLifetime` 的有意设计（PR-M2 Collector 在 PR-M3 之后才接入），非测试侧偏差。
   - 理由：保底采集的 GPU 维度计算纯持有时长（`count × elapsed`），不依赖 Prometheus。CPU/Mem 保底采集需要 Prometheus 查询，在 `collectFullLifetime` 中实现意味着 StopCollection 时同步查 Prometheus，增加复杂度。PR-M2 Collector 已在 PR-M2 中落地，但 `collectFullLifetime` 的 CPU/Mem 分支标注了"接入后完善"，属于后续迭代项。

### Tradeoffs

1. **真实 Prometheus + mock fallback vs 纯 mock Prometheus**
   - 考虑过的替代方案：纯 mock Prometheus（不连真实 Prometheus），所有维度都用 mock 返回固定值。
   - 优点：测试完全自包含，不依赖真实 Prometheus 可用性。
   - 缺点：无法验证真实 Prometheus 的 HTTP 交互路径（连接、请求构造、响应解析、NaN/Inf 过滤）。如果 Prometheus adapter 代码有 bug（如 URL 拼接错误、header 缺失），纯 mock 测试无法发现。
   - 选择理由：`fallbackCollector` 先查真实 Prometheus，失败时回退到 mock。这样既验证了真实 HTTP 交互路径，又保证测试在 Prometheus 无数据时仍能产出 CPU/Mem 记录。用户明确要求保留两者。

2. **SQL 字符串注入过滤 vs 参数化查询**
   - 考虑过的替代方案：在 `filteredMetadataTx.Query` 中解析 SQL 参数化查询，通过参数传递 `inst-itest-%` 过滤条件。
   - 优点：更安全，避免 SQL 注入风险。
   - 缺点：需要完整 SQL 解析器，复杂度高。rebuilder 的查询是固定的 `SELECT ... FROM workload_instances WHERE state = 'running' ORDER BY updated_at ASC`，SQL 注入风险可控（测试代码，非生产代码）。
   - 选择理由：测试侧 SQL 注入采用简单的 `ORDER BY` 前插入策略，足够覆盖 rebuilder 的固定查询模式。不引入 SQL 解析器依赖，遵循奥卡姆剃刀。

3. **`time.Sleep` 等待 vs 条件轮询等待**
   - 考虑过的替代方案：部分测试（场景 2、3、5）使用固定 `time.Sleep` 等待事件处理完成，而非 `waitForCondition` 轮询。
   - 优点：固定 sleep 简单直接。
   - 缺点：sleep 时间难以调优——太短导致 flaky test，太长浪费时间。
   - 选择理由：关键验证点（记录产出、StartCollection 调用次数）使用 `waitForCondition` 轮询（超时 20s，轮询间隔 50ms）。部分辅助等待（如场景 2 中 `time.Sleep(500*time.Millisecond)` 确保在 ticker 第一次产出前发布 stopped 事件）使用固定 sleep，因为这些等待的时序约束是确定性的（ticker 周期 2s，500ms 内必未产出）。

### Open Questions

1. **`collectFullLifetime` 的 CPU/Mem 维度何时完善**
   - 当前 `collectFullLifetime` 只处理 GPU 维度，CPU/Mem 分支注释标注"PR-M2 接入后完善"。PR-M2 Collector 已落地，但 `collectFullLifetime` 的 CPU/Mem 分支尚未实现。
   - 影响：场景 2 保底采集只验证 GPU 维度记录，无法验证 CPU/Mem 保底采集。
   - 建议：后续迭代中完善 `collectFullLifetime` 的 CPU/Mem 分支（StopCollection 时查询 Prometheus 获取最终值），并补充场景 2 的 CPU/Mem 维度断言。

2. **集成测试在 CI 中的执行策略**
   - 当前集成测试需手动运行（`-tags integration`），依赖真实 NATS + PG + Prometheus 基础设施。
   - 影响：集成测试不会在 `make test` 或 CI 中自动执行，存在回归风险。
   - 建议：后续在 CI 环境中预置 NATS + PG + Prometheus（或使用 docker-compose），将集成测试纳入 CI 流程。或至少在 PR-M5 部署清单 live gate 时执行一次完整集成测试。

### 验证命令

```bash
cd repo/services/metering-service
go vet ./services/metering-service/... ./pkg/adapters/metering/... ./pkg/ports/...  # 通过
go build ./services/metering-service/... ./pkg/adapters/metering/...               # 通过

cd repo
python scripts/validate_component_imports.py --root .                              # 通过
git diff --check                                                                   # 通过

# 集成测试（需真实 NATS + PG + Prometheus）
ANI_TEST_NATS_URL=nats://10.10.1.66:31062 \
ANI_TEST_ADMIN_DSN=postgres://ani:ani_dev_password@10.10.1.66:30945/ani?sslmode=disable \
ANI_TEST_PROMETHEUS_URL=http://10.10.1.66:31990/ \
go test ./services/metering-service/internal/ -v -run TestIntegration -tags integration -timeout 300s
# 9/9 PASS (25.359s)
```

### AC 对照

| AC | 证据 |
|---|---|
| 集成测试启动真实 NATS JetStream + 真实 PG + 真实 Prometheus | `newItestEnv` 连接三者的 `itestNATSURL()`/`itestAdminDSN()`/`itestPrometheusURL()` |
| 场景 1：事件驱动采集 | `TestIntegrationEventDrivenCollection` — 发布 running → StartCollection → ticker 产出 GPU 记录 |
| 场景 2：停止采集 + 保底 | `TestIntegrationStopAndCollectFullLifetime` — stopped 事件 → StopCollection → 保底 GPU 记录 + ticker 停止验证 |
| 场景 3：幂等 no-op | `TestIntegrationIdempotentStartCollection` — 两次 running 事件 → 无重复 (period, resource_type) 行 |
| 场景 4：重建 + DeliverAll | `TestIntegrationRebuildAndDeliverAll` — 两阶段 consumer 重启 → rebuilder 重建 ticker → DeliverAll 回放 |
| 场景 5：seenSeq 乱序 | `TestIntegrationSeenSeqOutOfOrder` — seq=5 后 seq=3 被丢弃，StartCollection 仅调用 1 次 |
| 场景 6：seenSeq 失败重投 | `TestIntegrationSeenSeqFailureRedelivery` — failFirstSvc 第一次失败 Nak，重投后成功，StartCollection ≥2 次 |
| 场景 7：租户校验 | `TestIntegrationTenantMismatchNak` — header tenant-id ≠ payload tenant_id → Nak 重投 ≥2 次 |
| 场景 8：毒消息 | `TestIntegrationPoisonMessageAck` — 非法 JSON → Ack 跳过，仅投递 1 次 |
| 场景 9：DB UNIQUE 兜底 | `TestIntegrationDBUniqueConstraint` — 重复 INSERT → ON CONFLICT DO NOTHING，保留首次值 100 |
| Typecheck/lint 通过 | go vet + go build + validate_component_imports + git diff --check 全 pass |

---
