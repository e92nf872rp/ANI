# MODEL-IMPORT-REDRIVE-A

> **日期**：2026-09-17　**分支**：`hotfix/network-store-read`（fork: djm-afk）　**PR**：e92nf872rp/ANI#174　**状态**：live verified（隔离测试环境 ani-test2，镜像 tag `test2-20260917-redrive`）
> **性质**：Feature batch（hotfix 系列，Services 侧模型导入卡住补偿能力落地）
> **来源**：测试异常结果记录 模型仓库系列与 `特有Bug修复问题清单.md`（模型仓库-1）

## 背景

从 HuggingFace/ModelScope 导入模型后，导入任务长期停留在 `pending` 且永不收敛：`async_tasks.status=pending`、**`attempt_count=0`**、`lease_owner` 为空（worker 从未认领），`outbox_events.published=true`（outbox 认为已发布）。根因是三个叠加缺陷（定位证据见 `特有Bug修复问题清单.md` §五 模型仓库-1）：

1. **"已发布但未投递"不可感知**：outbox 只记录 `published`，不校验端到端消费；`ANI_TASKS` 为 WorkQueue retention + `MaxAge=24h`，无人消费/未 ack 的消息 24 小时后被静默清除，DB 与业务侧均无痕迹。
2. **导入状态机没有收敛兜底**：model-service 内不存在卡住任务的 reconciler/超时机制，`pending` 可以永久存在，UI 拿不到任何错误（`worker.go` 注释里假设的 reconciler 实际未实现）。
3. 环境约束：业务侧无外网，HuggingFace 源不可达，失败本身合理，但用户看到的是"一直在排队"而非"失败"。

本批次只解决第 2 条（业务侧兜底），第 1 条的平台级可见性单列为遗留。

## 修复内容

1. **进程内重投 sweeper**：`model-service` 启动时拉起 `ImportRedriveSweeper`（goroutine + ticker），启动即扫一次，此后按间隔扫描；随 `SIGINT/SIGTERM` 退出。
2. **候选筛选（两段式，RLS 决定）**：
   - 平台事务（不设租户）扫 `async_tasks` 取候选租户——该表只有宽松 RLS（`platform_bypass` + `self`），无租户上下文即可看到"从未认领"信号：`task_type='model.import' AND status='pending' AND attempt_count=0 AND lease_owner IS NULL AND created_at < NOW() - make_interval(secs => $after)`；
   - 每个候选租户在**租户事务**（`types.WithTenant` + `types.SetDBTenant`，事务级 `set_config`）内选择并插入——`model_import_tasks` 有严格租户隔离，必须在租户上下文内读；选行用 `FOR UPDATE OF it SKIP LOCKED`，这是多副本并发安全的依据。
3. **重投动作**：取该任务**原始 outbox 事件 payload**（`payload->>'task_id'` 匹配中 `created_at`/`id` 最小的一条），新插一条 outbox 事件（`event_type` 与创建期一致 = `natsmsg.SubjectModelImport`、`aggregate_type='model_import'`），由既有 task-service outbox relay 发布——复用事务性 outbox 纪律，payload 与原消息一致，worker 侧 `terminalTaskStatus` 提前 ack + `AcquireLease` 保证重复投递幂等。
4. **自我节流**：同一任务已存在 `published=false` 的重投事件时跳过，不堆叠事件。
5. **不做强制失败**：任务终态仍由 worker 决定（投递成功后由 lease + 3 次重试收敛为 completed/failed），不把"合法排队"判为失败；整个投递链长期不可用时保持 `pending`，此时 pending 是真实状态。
6. **配置**：`MODEL_IMPORT_REDRIVE_INTERVAL`（默认 60s，扫描周期）、`MODEL_IMPORT_REDRIVE_AFTER`（默认 2m，判定"过久未认领"的阈值）；未设置或不可解析时沿用默认值。

## 变更文件

- `repo/services/model-service/internal/repo/import_redrive.go`：新增 `StalledImport`、`CandidateImportTenants`（跨租户候选，平台事务）、`SelectStalledImports`（租户内 `FOR UPDATE SKIP LOCKED` 选择 + 未发布重投事件跳过）、`InsertImportRedriveOutbox`（重投事件插入，列清单与 `createModelImportOutboxSQL` 一致）；三方法均带 `tx == nil` 守卫与 limit 归一化（`defaultImportRedriveLimit=50`）
- `repo/services/model-service/internal/service/import_redrive.go`：新增 `ImportRedriveSweeper`（`Run`/`Sweep`/`candidateTenants`/`sweepTenant`）；默认 `interval=1m`、`after=2m`、`limit=50`；仅在确有重投时打 `model import redrive queued`，无候选静默
- `repo/services/model-service/internal/config/config.go`：新增 `ImportRedrive` 类型、`LoadImportRedrive()`、`envDuration`（解析失败或非正数返回 0 交回默认值）
- `repo/services/model-service/main.go`：import `os/signal`/`syscall`，构造 sweeper 并在 `signal.NotifyContext` 上下文中启动
- 单测：`repo/services/model-service/internal/repo/import_redrive_test.go` 新增 5 个静态契约用例（只选"从未认领"、复用原始已发布 payload、候选租户保持平台范围、重投事件列与原始事件一致、缺事务守卫）
- `repo/architecture/component-import-allowlist.yaml`：为两个新文件补 `bounded_direct` 条目（`internal/repo/import_redrive.go` 用 `pgx`、`internal/service/import_redrive.go` 用 `pgxpool`），与既有 model-service repo/worker 条目同口径——直接 pgx 是租户 RLS 作用域与多副本行锁（`FOR UPDATE SKIP LOCKED`）能够显式表达的前提
- 未改：OpenAPI 契约与生成物、SDK、Gateway handler、worker 状态机、DB schema（无迁移）、`ANI_TASKS` 的 MaxAge

## 验证汇总

- 单测与门禁：`gofmt -l`（本次改动 Go 文件）干净；model-service 模块 `go build` / `go test`（含 5 个新增契约用例）通过；`make validate-architecture` 通过（新增 allowlist 条目后）；`make validate-services` 在 Windows 本机有两处环境性中断——`python3` 不存在、Makefile 的 bash 风格 env 前缀（`GOCACHE=… go test`）无法在 cmd 解析——对应步骤已单独重跑通过（`validate_model_repository_remote_import(_test).py`；`go test ./services/ani-gateway/internal/middleware ./services/ani-gateway/internal/router -run 'TestInferPermission|TestAuthPublicPaths|TestAuthProtectedPaths'`），其余步骤（Services boundary、OpenAPI semantic contract、route contract、spec split）均通过；`git diff --check` 干净；完整门禁以本 PR 的 CI（Linux）为准
- 隔离测试环境准备（ani-test2）：此前该命名空间无任何 model 组件，为验证本批次部署 model-service、model-import-worker、task-service（outbox relay）三组件，并应用 `deploy/migrations/20260904000100_model_import_resolved_revision.sql`；网关 ConfigMap 的 `model_service_grpc_addr` 指向本命名空间 model-service。sweeper 参数取 `INTERVAL=30s`、`AFTER=60s` 以缩短验证观察窗
- **验证①（正常导入）**：`POST /api/v1/svc/models/import`（ModelScope `BAAI/bge-small-zh-v1.5`）→ 轮询至 `completed`，`models=1`、`model_versions=1`，原始 outbox 事件 `published=t`
- **验证②（丢失重投）**：先把 relay 与 worker 缩容为 0（确保无人消费）→ 建导入（`async_tasks.pending`、`attempt_count=0`、`lease_owner` 空）→ SQL 将该任务原始 outbox 事件置 `published=true` 模拟"已发布但从未投递" → 恢复观察：sweeper 日志出现 `model import redrive queued count:1` 且新增一条 `published=false` 的 outbox 事件 → 恢复 relay + worker 后导入收敛为 `completed`，全部 outbox 事件 `published=t`，`models=2`/`model_versions=2`；通用任务查询 `GET /api/v1/tasks/{id}` 同步返回 `completed`
- **验证③（重复投递幂等）**：把最新重投 outbox 事件置回 `published=false`，由 relay 以同一 payload 重新发布；对比前后 `models`/`model_versions`/`async_tasks`(status,attempt_count,lease_owner,error)/`model_import_tasks`(status,completed_at) 完全一致，worker 日志中该 task 的 `model import started` 计数仍为 1（worker 因 `terminalTaskStatus` 提前 ack），`published_at` 前移证明重复投递确实发生
- CI：本 PR GitHub Actions 为准

## 遗留

- 平台级：`ANI_TASKS` 的 `MaxAge=24h` 仍会静默清除无人消费/未 ack 的消息（无积压监控、无告警）；`ani.tasks.kb.rebuild.v1` 在该 stream 上没有任何消费者，属同源缺陷，均未在本批次处理
- 导入入口未做源可达性预检，HuggingFace 在当前业务侧仍不可达，用户看到的仍是排队/失败而非"源不可用"
- 本批次只在 worker 侧兜底投递丢失；`async_tasks` 与 `model_import_tasks` 的租户严格隔离使候选扫描必须两段式，若后续为 `async_tasks` 增加严格租户策略，需同步调整此实现
- 通用任务查询接口 `GET /api/v1/tasks/{task_id}` 可用，Services 侧 `GET /api/v1/svc/model-import-tasks/{task_id}` 未注册（返回 404），前端如需专有导入任务详情需另行补契约