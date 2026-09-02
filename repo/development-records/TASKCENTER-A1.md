# TASKCENTER-A1 — 任务中心异步任务 Core 集成 · list + 实例真进度批次

完成日期：2026-08-27
对应 Sprint：Sprint 13（并行功能流：任务中心异步任务 Core 集成）
分支：`feat/async-task-core-integration`
方案依据：仓库根目录 `async-task-core-integration-migration.md`（§6 阶段 B / §8.2 / §8.3）
验证结果：`make test` 等价拆解全通（go test 全包 + go vet；store + router 共 30 个新测试全绿：store 9 + router 21）；`make validate-architecture` 通过（component import guard + inference legacy control plane）；`make validate-services` 等价拆解通过（services boundary / services yaml / inference control-plane migration / services contract / services route contract / spec-split 含 Go 测试 / sdk-beta / gen_sdk_alpha + generate_api_docs 生成物零漂移 / doc-api / doc-entrypoints / rag compileall / Console schema 零漂移）；`validate-async-task-store` 通过；`git diff --check` 通过。真实 PG 验证：连接成功，`20260827_001` 索引迁移 SQL 成功应用并确认存量索引确缺 `(tenant_id, created_at DESC, id DESC)`；RLS 跨租户拦截无法用给定凭据验证（dev 库 `ani` 账号为 SUPERUSER + BYPASSRLS + 表 owner，RLS 被绕过），隔离正确性由 `LocalAsyncTaskStore` 租户键隔离测试与 SQL `WHERE tenant_id` 双层保证。本地 Windows 存量失败：`pkg/adapters/runtime` 两个 sandbox file script 测试（symlink 特权 + `os.O_DIRECTORY` 缺失）与 `validate_sdk_alpha` 的 javac 编译（本机仅 JDK 1.8 编译器，`java.net.http` 需 11+），均已在 C1 期 pristine HEAD 复现确认与基线一致，非本批引入。

## 实现了什么

Core 任务中心异步任务集成的实现批次（TASKCENTER-C1 契约的 handler 落地）：`ports.AsyncTaskStore` 追加 `AsyncTaskListFilter` 与 `List`（cursor 分页）并固化 Update 终态写保护接口语义；`LocalAsyncTaskStore` / `MetadataAsyncTaskStore` 双实现 List（keyset 排序 `created_at DESC, id DESC`、limit+1 探测、base64 不透明 cursor、非法 cursor 报 `ErrInvalid`）与 Update 终态写保护（SQL 守卫 `status NOT IN ('completed','failed','cancelled','dead_letter') OR status=$3` + 0 行受影响重读返回当前记录；Local 实现 mutex 内同语义比较）；新增 `20260827_001_async_tasks_list_index.sql` 复合索引迁移；Gateway 注册 `GET /api/v1/tasks` list handler（limit 1-100 默认 20、status/task_type/resource_type 筛选、非法入参 400）；实例 create（含 409 completed 重放补写）/lifecycle 两个写入点（`instanceProgressTask` 构造 running/10 真进度记录 + `writeAuditTask` 旁路失败降级仅日志）；`observeInstance`（store 读 + 单实例 K8s 刷新）提取为共享观察入口并注入任务路由；`GET /tasks/{task_id}` 非终态 `instance.*` 任务读时懒同步按实例 state 映射表推进（含写放大抑制、失败降级、终态守卫并发乱序保护）；任务响应结构补齐 `resource_id`/`error_message`/`dead_letter_at`（§2.6 双向映射修复，单查/list/模式 B 202 三处共用）；任务中心页面文档同步（list 上线、kb 域噪声声明、TODO-YAML 解除、取消归延后方案 V2-3）。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `pkg/ports/async_task.go` | 修改 | 追加 `AsyncTaskListFilter` 与 `List` 接口（keyset cursor 语义注释）；`Update` 接口注释固化 §5.2 规则六终态写保护语义（终态行不可被不同 status 覆写，守卫命中返回当前记录而非报错） |
| `pkg/adapters/runtime/async_task_store.go` | 修改 | 双 store `List` 实现（排序/筛选/limit+1 探测/cursor 编解码/租户隔离）；`Update` 双实现终态写保护（SQL 守卫 + ErrNoRows 重读；Local mutex 内比较）；非法 cursor/limit 校验报 `ErrInvalid` |
| `deploy/migrations/20260827_001_async_tasks_list_index.sql` | 新增 | `idx_async_tasks_tenant_created (tenant_id, created_at DESC, id DESC)`，list keyset 比较全索引覆盖；已在真实 PG 应用验证（IF NOT EXISTS 幂等） |
| `services/ani-gateway/internal/router/task_resources.go` | 修改 | list handler（参数解析/校验/400）；GET 单查懒同步（`syncInstanceTask` + `instanceTaskAdvance` 映射表 + `instanceIDForTask`）；响应结构补字段；`registerTasksWithStore` 接受 `instanceObserver` |
| `services/ani-gateway/internal/router/instances.go` | 修改 | create（含 409 重放补写，pending: in-progress 重放跳过）与 lifecycle（start/stop/restart/delete）写入点；`instanceProgressTask`/`writeAuditTask`/`instanceResourceID`；`refreshOneStoreStatus` 提取 `observeInstance` 并随 `registerInstancesWithRuntime` 返回 |
| `services/ani-gateway/internal/router/router.go` | 修改 | `registerTasksWithStore` 移至 `registerInstancesWithRuntime` 之后并注入 `observeInstance` 观察闭包（方案 §5.2 注册顺序约束） |
| `services/ani-gateway/internal/router/storage_resources.go` | 修改 | `storageSnapshotTaskResponse` 补 `ResourceID`/`ErrorMessage`/`DeadLetterAt`，`CompletedAt` 改 omitempty（§2.6，三处响应共用） |
| `services/ani-gateway/internal/router/{instances,storage_resources,vector_store_resources}_test.go` | 修改 | `registerInstancesWithRuntime`/`registerTasksWithStore` 签名适配（观察闭包返回值/nil observer） |
| `services/docs/console-modules/alerts/async-task-center.md` | 修改 | 页面文档同步：list 已冻结实现、懒同步边界、kb 域噪声声明、`scope:tasks:read` 权限行、接口冻结规则补 400、延后方案引用 |
| `pkg/adapters/runtime/async_task_list_test.go` | 新增 | store 层 9 测试：排序/翻页/筛选/非法入参/租户隔离/Update 终态守卫（Local）；keyset SQL 断言/limit+1 探测/守卫 SQL + 重读（Metadata fake tx） |
| `services/ani-gateway/internal/router/task_resources_test.go` | 新增 | Gateway 层 21 测试：HTTP list 边界/跨租户/写入点/幂等重放/409 补写/懒同步映射表逐行/自带刷新/懒同步降级/任务库不可用降级（§8.3）/写放大抑制/并发乱序守卫/orphan 重试写入/字段完整性/模式 B + kb 域不变 |

## 完工标准达成（对照方案 §8.2 / §8.3）

- [x] list：默认排序、limit 边界（1/100/超限 400）、cursor 翻页、三类筛选、非法 cursor/status 400（`TestTaskListHTTPEndpoint` + store 层 9 测试）
- [x] 跨租户隔离：A 租户任务在 B 租户 list/get 不可见；懒同步用请求租户上下文（`TestTaskListAndGetCrossTenantIsolation`、`TestLocalAsyncTaskStoreListTenantIsolation`）
- [x] 实例任务写入：create 201 响应零变化（forbidden 字段断言）+ `instance.create` running/10；lifecycle 四 action 同理；幂等重放单条；409 重放补写唯一一条；正常重试无重复（5 个写入点测试）
- [x] 懒同步映射表逐行：create provisioning→running/20、running→completed/100（completed_at 非空、终态幂等）、stop stopping→running/60 / stopped→completed、delete deleting→running/80 / deleted→completed / 记录消失→completed（防御分支）、failed→failed+error_message、deleted/gone→failed（非 delete action）、restart 中间态不误判终态、卡 provisioning 诚实停留 running/20（`TestInstanceTaskAdvanceMappingTable` + 6 个流程测试）
- [x] 懒同步自带刷新：实例 list 从未被调用、store 停留 provisioning 时 GET 任务触发单实例刷新返回 completed/100（`TestTaskGetLazySyncRefreshesInstanceWithoutList`）
- [x] list 返回库内快照不做懒同步（`TestTaskListReturnsStoreSnapshotWithoutLazySync`）
- [x] 懒同步失败降级：observe/Update 出错时 GET 仍 200 返回旧快照（`TestTaskGetLazySyncDegradesGracefully`）
- [x] 写放大抑制：state 稳定后重复 GET 无 Update（排除首跳变；store 计数验证，`TestTaskGetLazySyncSuppressesWriteAmplification`）
- [x] 并发乱序不回退终态：乱序写 X(running/20) 晚于 Y(completed/100) 提交，终态不被覆写；Local 与 Metadata 双实现一致（`TestTaskGetLazySyncConcurrentOutOfOrderWrites`、`TestLocalAsyncTaskStoreUpdateTerminalWriteGuard`、`TestMetadataAsyncTaskStoreUpdateTerminalGuardReReadsCurrentRow`）
- [x] orphan 重试路径写入：实例不在内存 store 但存在于 K8s 时 lifecycle 重试成功同样产生任务记录（`TestInstanceLifecycleOrphanRetryWritesTask`）
- [x] 响应字段完整性：GET/list items 含 `error_message`/`resource_id`/`dead_letter_at`，failed 任务 error_message 非空（`TestTaskResponseFieldsComplete`）
- [x] 既有模式 B 任务可见性：storage/vector/platform/sandbox 任务 list 正常返回字段完整且不受懒同步影响（`TestTaskListReturnsModeBAndKbDomainTasksUnchanged`）
- [x] kb 域预期任务：`kb.parse` pending 记录 list 返回且状态不变、CreateKB completed 正常返回（同上测试）
- [x] 幂等与隔离（§8.3）：同 idempotency_key 不产生重复（ON CONFLICT/内存幂等）；实例库与任务库键空间互不干扰（create 重放走实例库幂等）；任务库不可用时实例主响应不受影响、降级旁路（`TestInstanceTaskStoreUnavailableKeepsInstanceResponse`）
- [x] 实例 API 响应契约不变（不 202 化，forbidden 字段断言）；不做取消契约；不建 worker/outbox/后台协程；不扩 22 种 lifecycle task_type；list 不做懒同步；无 Services 层改动（方案 §7.2 全部遵守）

## 备注（与方案的偏差，详见差异文档 `implementation-diff-async-task.md`）

1. **`instanceResourceID`/`instanceIDForTask` 双约定**（方案 §4.1 未细化）：`async_tasks.resource_id` 列为 UUID 类型，写入点存 `inst_<uuid>` 的裸 UUID 部分，完整实例 ID 保留在 `result.instance_id` 供懒同步定位——沿用 reconcile controller outbox `aggregate_id` 同款约定。
2. **写入点对 in-progress 重放的跳过**（方案 §4.1 只要求 completed 409 重放补写）：`pending:<op-id>` in-progress 重放时实例 ID 不可解析，跳过写入并留 `[TASK-AUDIT]` 日志；completed 重放走正常写入点由幂等键兜底补写。
3. **生命周期校验失败不写任务**（方案未明说）：写入点位于 `transition()` 状态校验之后，被拒绝的 lifecycle 请求（409 CONFLICT）不产生任务记录——属"任务只记受理成功的操作"语义的自然延伸，测试断言固化。
4. **测试修复过程中的 3 处自愈**：`failingCreateStore` 增加可恢复开关（模拟 store 故障→恢复）、重放测试的 fingerprint 一致性（body name 必须与首次一致否则重放被拒）、failed 映射测试的实例状态重置（stop 后 restart 要求 running）。均属测试造数问题，非产品代码缺陷。
5. **真实验证发现（非缺陷，如实记录）**：dev PG 的 `ani` 账号为 SUPERUSER + BYPASSRLS + 表 owner，RLS（FORCE ROW LEVEL SECURITY + tenant 策略）对该会话不生效；跨租户 RLS 拦截无法用给定凭据验证。应用层隔离由 `WHERE tenant_id` 显式过滤 + Local store 键隔离测试保证。
6. **本机环境限制**（与 C1 一致）：sandbox symlink 两个存量测试、javac 1.8 编译 SDK（`validate_sdk_alpha`）均失败于 Windows 本机限制，pristine HEAD 已复现确认。
7. **计数勘误（2026-08-28 复核）**：初版"router 包含 29 个新测试"/"Gateway 层 20 测试"均为少计，实际新增 30 个（store 9 + Gateway 21）；提交消息 `5d27599` 中的"29 个新测试"同样少计 1 个（漏数 `TestInstanceTaskStoreUnavailableKeepsInstanceResponse`）。本文与差异文档已修正，提交消息不改写。

## 后续

- Services 层集成（推理桥接 A2 / kb 闭环 V2-1 / model.import V2-2 / 取消 V2-3 / 通知 V2-4）全部延后，存档于仓库根目录 `async-task-services-integration-deferred.md`。
- 差异文档：仓库根目录 `implementation-diff-async-task.md`（含 §8.2 验收矩阵逐条对照与测试报告）。
