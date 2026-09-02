# TASKCENTER-A2 — async\_tasks RLS 真实验证与仓库对齐修复

* 完成日期：2026-08-31

* 对应 Sprint：当前 Sprint（见 `repo/CURRENT-SPRINT.md`）

* 分支：`feat/async-task-core-integration`（延续 C1/A1 分支，未 push、未合并 main）

* 方案依据：`async-task-core-integration-migration.md` §8.3（幂等与隔离验收）+ A1 遗留项（RLS 无法真实验证）

* 批次性质：Feature batch（真实缺陷修复 + 迁移新增 + 集成测试新增，非 guard micro-batch）

## 验证结果

验证结果：`go test ./pkg/adapters/runtime/ -run TestRLSAsyncTasks -tags integration -count=1` 连续两轮全绿（幂等重跑无累积，收口复核再跑一轮仍全绿）；默认 build tag 下 runtime 包仅存量 2 个 Windows sandbox 失败（与 pristine 基线一致，非本批引入）；router 包 ok；`make validate-architecture` 核心守卫通过（component import guard passed、inference legacy control plane retired、"architecture guardrails valid"，make 包装层 exit code 1 仅为 TRAE 沙箱拦截 `>/dev/null` 的已知噪音，与 A1 记录一致）；`git diff --check` 通过。dev PG 直连 `ani_app_user`（非 SUPERUSER/非 BYPASSRLS）完成 A1 遗留项真实验证：本租户 SELECT 可见、**跨租户 SELECT/Get 拦截 = 0 行**、Create 同款 INSERT 成功、Update 同款 SQL（懒同步路径 + 终态写保护守卫）成功、平台上下文全可见。

## 实现内容

1. **A1 遗留项收口（核心目标）**：dev 库切换 `ani_app_user`（受 RLS 约束的应用角色）真实验证 async\_tasks 跨租户拦截——A1 交付时仅有 `ani`（SUPERUSER + BYPASSRLS）凭据，RLS 全被绕过，隔离只能靠 Local store 键隔离测试 + SQL WHERE 双层保证；本批次用受限角色实测确认 RLS 行级拦截真实生效。
2. **修复迁移** **`20260831_001_async_tasks_rls_fix.sql`**（补齐三处 dev 库与仓库的漂移，全部 live-verified 后回写仓库）：

   * RLS 策略：仓库 `init_schema` 为 async\_tasks 建 RESTRICTIVE-only `tenant_isolation`（无任何 PERMISSIVE 策略），PostgreSQL 对非 BYPASSRLS 角色将 fail-closed（查询恒 0 行、写入被拒）——任务中心在 fresh 部署的应用角色下会看到空表。dev 库已被带外修复为双 PERMISSIVE 策略但未入库。迁移 DROP RESTRICTIVE-only 策略并 CREATE `async_tasks_platform_bypass` + `async_tasks_self`（PERMISSIVE，对齐 20260825\_001 workload\_instances 模式）。

   * 表级 GRANT：`init_schema` 的 `GRANT ... ON ALL TABLES` 在建表之前执行（fresh 空库零效果），async\_tasks 的 SELECT/INSERT/UPDATE 授权在仓库中缺失（dev 手工授予）。迁移补 `GRANT SELECT, INSERT, UPDATE ON async_tasks TO ani_app`（成员 `ani_app_user` 继承）；**不授予 DELETE**——产品代码无 async\_tasks 删除路径（任务走状态机不走删除），对齐 dev 实测最小权限形态。

   * platform\_bypass 用 `NULLIF(current_setting(...), '') IS NULL` 形态而非裸 `IS NULL`：实测发现 `WithTenantTx` 的 `set_config(..., is_local=true)` 在事务结束后于池化连接残留空串 `app.current_tenant_id`，裸 `IS NULL` 会把空串误判为租户上下文导致 `WithPlatformTx` 0 行；NULLIF 形态把 NULL 与空串同视为平台上下文，与 self 策略的 NULLIF 空串处理对称。

   * 迁移幂等（DROP IF EXISTS × 3 + CREATE），dev 库重放安全。
3. **集成测试** **`pkg/adapters/runtime/rls_async_tasks_test.go`**（build tag `integration` 隔离，不进默认 make test）：

   * `TestRLSAsyncTasksPolicyShape`：断言双 PERMISSIVE 策略形态、无 RESTRICTIVE-only 残留、RLS ENABLE+FORCE——fresh 部署漏跑 20260831\_001 时在此失败给出明确信号（fail-closed 回归防护）。

   * `TestRLSAsyncTasksTenantIsolation`：用 `MetadataAsyncTaskStore` 同款 SQL 断言本租户可见 / 跨租户拦截 = 0 / Get 同款跨租户 = 0 / 对称租户 B 视角 / Create 同款 INSERT + 幂等重放回读 / Update 同款懒同步推进（completed/100）/ 终态写保护守卫（completed 改回 running 恒 0 行）/ 平台上下文全可见。固定租户/任务 UUID + 固定幂等键 + 终态同值写使全部断言可重复执行（已验证两轮幂等重跑）。

## 关键文件改动

| 文件                                                       | 状态 | 说明                                                                |
| -------------------------------------------------------- | -- | ----------------------------------------------------------------- |
| `deploy/migrations/20260831_001_async_tasks_rls_fix.sql` | 新增 | async\_tasks RLS dual-policy + GRANT 修复迁移（幂等，live-verified 后回写仓库） |
| `pkg/adapters/runtime/rls_async_tasks_test.go`           | 新增 | 2 个 integration 测试（策略形态防回归 + 跨租户隔离行为断言，幂等可重跑）                     |
| `development-records/README.md`                          | 修改 | 批次索引新增 TASKCENTER-A2 条目（A1 条目补"（A2 收口）"标注）                        |
| `repo/CURRENT-SPRINT.md`                                 | 修改 | 当前 Sprint 状态追加 TASKCENTER-A2 条目                                   |
| `ANI-06-开发计划.md`（仓库根）                                    | 修改 | Section 零批次列表追加 TASKCENTER-A2 条目                                  |
| `implementation-diff-async-task.md`（仓库根）                 | 修改 | 摘要遗留风险行与文末"遗留风险"第 1 条标注 A2 收口；§七提交状态追记（3 commits + A2 产物）         |

## 完工标准

* [x] A1 遗留项收口：ani\_app\_user（非 BYPASSRLS）真实验证 async\_tasks RLS 跨租户拦截（SELECT/Get = 0 行）

* [x] 真实缺陷修复：RESTRICTIVE-only fail-closed、表级 GRANT 缺失、空串误判三处仓库漂移由 `20260831_001` 补齐（对齐 dev 库 live-verified 形态）

* [x] 修复可持续验证：2 个 integration 测试固化结论（策略形态 + 行为断言），幂等重跑两轮全绿

* [x] 默认测试链不受影响：build tag 隔离，runtime/router 包无新增失败（仅存量 2 sandbox Windows 失败与基线一致）

* [x] `git diff --check` 通过

## 备注

1. **dev 库迁移应用遗留**：admin（`ani`）凭据当前密码认证失败（`2026-08-31`），`20260831_001` 需 DBA 在 dev 库执行以把 platform\_bypass 升级为 NULLIF 形态；迁移幂等安全，dev 库当前形态（裸 IS NULL）下测试同样全绿（平台上下文断言在租户事务污染连接之前执行，两种形态均确定通过）。
2. **跨表遗留（不在本批范围，如实记录）**：`WithTenantTx` 的 `set_config(..., is_local)` 事务后空串残留是 `MetadataStore` 连接池的全局行为；workload\_instances / resource\_quota 等表的 `platform_bypass` 策略仍是裸 `IS NULL` 形态（20260825\_001 模式），"同连接先租户事务后平台事务"的 `WithPlatformTx` 查询（如 specInUse COUNT）会 0 行假阴性。建议后续批次统一把存量表 platform\_bypass 升级为 NULLIF 形态或在 `WithPlatformTx` 入口显式清理会话变量。
3. **探察残留行**：验证期动态 UUID 探察在 dev 库留下数行 `rls-at-probe-*` 前缀测试行（应用角色无 DELETE 权限无法清理，可识别、无业务影响）；正式测试改用固定 UUID + 固定幂等键后不再累积。
4. **本机环境限制**（与 C1/A1 一致）：sandbox symlink 两个存量测试失败于 Windows 限制，pristine HEAD 已复现确认与基线一致。
