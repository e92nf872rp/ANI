# Issue 004: 实现 meteringCollectionService（进程内 ticker 管理 + DB 持久化）

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/metering/prd-metering-consumer.md`
- UX: N/A
- SPEC: `repo/services/tasks/modules/spec/core/metering/spec-metering-consumer.md`

## Description

实现 `meteringCollectionService`，持有 `*pgxpool.Pool` 直连 Core DB，管理 per-instance ticker 并写入 `metering_usage_records`。包含 Start/Stop 幂等、runCollectionLoop 采集循环、persistRecords 持久化、collectFullLifetime 保底采集。

## Scope
- Product line: core
- Code paths allowed: `repo/services/metering-service/internal/service/`

## Acceptance Criteria
- [ ] 新增 `services/metering-service/internal/service/metering_collection_service.go`
- [ ] `meteringCollectionService` 持有 `*pgxpool.Pool`、`map[string]*time.Ticker`（key: resourceRef）、`map[string]chan struct{}`（stopChs）、`map[string]*ports.CollectionSpec`（specs）、`map[string]bool`（everCollected）、`*slog.Logger`
- [ ] `StartCollection`：进程内 map 已有 ticker 时返回 nil（幂等 no-op）；否则建 ticker + stopCh + 存 spec，启动 `runCollectionLoop` goroutine
- [ ] `StartCollection`：`spec.StartedAt.IsZero()` 时设为 `time.Now()`（供 collectFullLifetime 计算）
- [ ] `runCollectionLoop`：`select <-ticker.C` 调用 `CollectAll` 采集 → `persistRecords` 写 DB；`<-stopCh` 时 `ticker.Stop()` 退出
- [ ] `runCollectionLoop`：CollectAll 失败时记 Error 日志并 continue（不停 ticker，下个周期重试）
- [ ] `persistRecords`：用 `ani_metering_writer` 角色连接（BYPASSRLS），INSERT 用 `ON CONFLICT DO NOTHING` 兜底写入幂等
- [ ] `persistRecords` 的 INSERT 语句列名用 `quantity`（对应 Go struct `TotalQuantity` 字段）
- [ ] `StopCollection`：无 ticker 时返回 nil（幂等 no-op）；否则 `ticker.Stop → close stopCh → delete map entries`
- [ ] `StopCollection`：锁外做保底采集——`everCollected[ref]==false && spec != nil` 时调 `collectFullLifetime` 补采一次全周期量
- [ ] `collectFullLifetime`：按 Start 到 Stop 的完整存活时长计算一次性量，Period 用 Stop 时刻分钟对齐
- [ ] `collectFullLifetime` 产出的记录若与已有周期记录碰撞，`ON CONFLICT DO NOTHING` 兜底丢弃
- [ ] 锁结构：Stop 时缩小锁范围，慢 I/O（collectFullLifetime + persistRecords）在锁外执行
- [ ] 单测覆盖：Start 幂等、Stop 幂等、保底采集触发、collectFullLifetime 计算、persistRecords ON CONFLICT
- [ ] Typecheck/lint 通过

## Dependencies
- Issue #001（migration 提供表）
- Issue #002（port 接口）
- Issue #003（go.mod 提供 pgx 依赖）

## Type
core

## Priority
high

## Labels
core

## Batch
PR-M1

## SPEC Reference
- SPEC §3.2 Entity Definitions（Service 层）
- SPEC §5.1.1 StartCollection 幂等
- SPEC §5.1.2 runCollectionLoop
- SPEC §5.1.3 StopCollection 幂等 + 保底采集
- SPEC §5.1.8 collectFullLifetime
- PRD FR-7 ~ FR-15
