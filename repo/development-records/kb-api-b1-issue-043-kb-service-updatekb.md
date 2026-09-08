# KB-API-B1 — issue-043 实现层：update_kb repository + UpdateKB servicer + 修复批次

> Issue: `repo/services/tasks/modules/issue/core/knowledge/issue-043-b1-kb-service-updatekb.md`
> Batch: KB-API-B1 (implementation phase) · 产品线: core（Services kb-service）
> Plan: `repo/services/tasks/modules/plan/knowledge_base/kb-api-completion-plan.md`
> SPEC: `repo/services/tasks/modules/spec/core/knowledge/spec-services-kb-api-completion.md`（§5.1 算法、§5.4 空字段、§6.1 错误分类、§6.4 幂等）

完成日期：2026-09-03
分支：`feat/kb-api-completion`
验证结果：kb-service pytest 48 passed（test_grpc_wiring + test_update_kb + test_grpc_server）；前序批次 25 passed + `compileall`；`atlas migrate hash` 重生成 + `atlas migrate validate` exit 0；`validate_inference_legacy_control_plane.py` exit 0；`validate_component_imports.py` 过滤 `.run/` 后零真实违规；`git diff --check` 干净。

## 实现了什么

1. **UpdateKB servicer**（`grpc_server.py`）：idempotency_key/kb_id/tenant_id 三重前置校验（tenant_id 缺失此前依赖 RLS 兜底，现前置 INVALID_ARGUMENT，与 `_create_kb` 对齐）→ 单外层事务内：幂等重放检查 → `update_kb`（23505 → ALREADY_EXISTS）→ async_tasks 幂等记录插入 + 完成（毒 key 自愈）→ 原子提交。
2. **update_kb repository**（`knowledge_base.py`）：`COALESCE(NULLIF($n,''), col)` 部分更新语义 + `updated_at=now()`，WHERE `id AND status <> 'deleted'`（软删 KB 不可复活），RETURNING 全列，RLS 事务内执行。
3. **毒 key 自愈**：外层事务内 INSERT 撞 `UNIQUE(tenant_id, idempotency_key)`（前次尝试在 create_task 与 complete_task 之间崩溃留下的 pending 行）→ SAVEPOINT 回滚内层 → 复用该 pending 行并以新 result 完成之——重试转为成功而非永久 UNKNOWN。
4. **修复批次 A-1/A-2/A-3**（用户确认"全部三项"）：
   - A-1：`_create_kb` 补 23505 → ALREADY_EXISTS 映射（此前 Core 失败清理残留软删行时，同名重试会泄漏裸 23505）。
   - A-2：迁移 `20260903000100`——`UNIQUE(tenant_id, name)` 改 partial unique index `WHERE status <> 'deleted'`，软删行不再占名（DeleteKB 后可同名重建；CreateKB 失败清理后重试不再撞名）。
   - A-3：迁移 `20260903000200`——删 `async_tasks` 上与表级 UNIQUE 重复的显式唯一索引 `idx_async_tasks_tenant_idempotency`（同列双唯一索引写放大）。
5. **atlas.sum 收尾**：重哈希全目录，顺带修复 HEAD 缺失的 7 个条目（历史 PR #126/#128 提交迁移未跑 hash）；重命名撞版本的 `20260827_001_*` 两文件为 `20260827000300` / `20260827000400`（Atlas 版本解析规则：文件名首个 `_` 前数字串为版本，`_001` 后缀不区分版本导致 "multiple files with the same version 20260827"）。
6. **review-it 审查**：接受 1 项 finding（`_update_kb` 注释声称四个 repo 函数均不自开事务，实际 `find_by_idempotency_key`/`update_kb` 各自开 `conn.transaction()`——嵌套降级 SAVEPOINT，功能正确注释失实，已修正），拒绝 2 项越界建议（`.run/` 误扫、CI atlas 门禁，转 follow-up）。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `services/kb-service/app/api/grpc_server.py` | 修改 | `UpdateKB` wrapper + `_update_kb` 实现；`_create_kb` 补 23505 映射；`json` 顶部导入、`_ts` 接受 ISO 字符串；CreateKB 毒 key 自愈；GetKB/ListKBs tenant 前置校验 |
| `services/kb-service/app/repositories/knowledge_base.py` | 修改 | 新增 `update_kb`（COALESCE+NULLIF + 软删过滤） |
| `services/kb-service/app/repositories/async_task.py` | 修改 | 新增 `complete_task_in_tx`（不自开事务变体）+ `complete_task` 薄包装 |
| `services/kb-service/tests/test_update_kb.py` | 新增（未跟踪） | 13 用例：成功/NOT_FOUND/RLS 隔离/软删 23505/ALREADY_EXISTS/毒 key 自愈×2/幂等 replay×2/空字段×2/校验×3/skeleton FAILED_PRECONDITION |
| `services/kb-service/tests/test_grpc_wiring.py` | 修改 | A-1 冲突测试、CreateKB 毒 key 测试、`_StubContext` |
| `services/kb-service/tests/test_grpc_server.py` | 修改 | missing-tenant 校验测试 |
| `deploy/migrations/20260903000100_kb_name_unique_active.sql` | 新增 | A-2 partial unique index |
| `deploy/migrations/20260903000200_async_tasks_drop_duplicate_idempotency_index.sql` | 新增 | A-3 冗余索引清理 |
| `deploy/migrations/20260827000300_async_tasks_list_index.sql` | 重命名 | 由 `20260827_001_` 改名消版本撞车，内容不变（幂等 CREATE INDEX IF NOT EXISTS） |
| `deploy/migrations/20260827000400_user_roles_single_role.sql` | 重命名 | 同上 |
| `deploy/migrations/atlas.sum` | 重生成 | 全目录重哈希 + 补 HEAD 缺失 7 条目 |

## 设计决策（Design Decisions）

### D1：毒 key 自愈采用"单外层事务 + SAVEPOINT 复用 pending 行"，而非独立清理任务或状态机
- **模糊点：** SPEC §6.4 只规定幂等记录语义，未规定崩溃窗口（create_task 已提交、complete_task 未跑）留下的 pending 行如何处理。
- **选择：** 重放检查跳过 result=NULL 的 pending 行；UPDATE 重跑后 INSERT 撞 UNIQUE 时，靠 asyncpg 嵌套事务（自动降级 SAVEPOINT）在内层捕获 23505、外层事务保持可用，随后复用该 pending 行并以新 result 完成之。
- **理由：** 零额外基础设施（无需 TTL 清理任务/状态机）；重试语义自然变为"自愈成功"；与 NotifyDocumentUploaded（SPEC §6.1 US-010）单事务模式同构。若复查找不到行（RLS 竞态）则原样抛出，不掩盖。

### D2：tenant_id 缺失前置 INVALID_ARGUMENT，不再依赖 RLS 兜底
- **模糊点：** SPEC §5.1 算法只列 idempotency_key/kb_id 校验；既有 UpdateKB 路径 tenant 缺失时靠 RLS 吞掉返回 NOT_FOUND。
- **选择：** 与 `_create_kb` 相同的三重前置校验（idempotency_key/kb_id/tenant_id 均缺失即 INVALID_ARGUMENT），校验顺序一致。
- **理由：** RLS 兜底把客户端 bug（漏传 tenant header）伪装成 404，排障困难；前置校验给出明确错误且不触 DB。

### D3：名称冲突检测继续依赖 DB 唯一约束（不做 SELECT-then-UPDATE 预查询）
- **沿承 issue-040 D2 的决策在实现层落地**：`update_kb` 直接 UPDATE，靠 `UNIQUE(tenant_id, name)`（现 A-2 后为 partial unique index）触发 23505 → `context.abort(ALREADY_EXISTS)`。避免 TOCTOU 竞态与额外往返。

### D4：两个既有迁移撞版本用改名而非改 atlas.sum 手工条目
- **模糊点：** Atlas 版本解析取文件名首个 `_` 前数字串，`20260827_001_async_tasks_list_index.sql` 与 `20260827_001_user_roles_single_role.sql` 解析均为 `20260827` → hash 拒绝。
- **选择：** 重命名为同日主流 `20260827NNNNNN` 风格（`20260827000300` / `20260827000400`），保持 list_index → user_roles 字典序与内容不变，再跑 `atlas migrate hash`。
- **理由：** 两文件均幂等（`CREATE ... IF NOT EXISTS`）、来自近期 PR 且从未在已部署环境区分执行（版本号撞车意味着 Atlas 本就无法区分）；手工编辑 atlas.sum 破坏哈希链、为下一玩家埋雷。

## 偏差（Deviations vs PRD/UX/SPEC）

### DEV1：A-1/A-2/A-3 修复批次超出 issue-043 Scope（allowed paths 仅 `repo/services/kb-service/`）
- **Spec 说：** Scope 限定 kb-service 代码路径；迁移目录不在 allowed paths。
- **实现：** 追加 2 个新迁移 + atlas.sum 重哈希 + 2 个既有迁移改名（`deploy/migrations/`）。
- **理由：** 全部经用户在架构复查后明确确认（"全部三项（推荐）"）；A-2 是 UpdateKB/CreateKB 23505 路径正确性的前置（软删行占名使部分场景永败）；迁移改名与 atlas.sum 修复是 `atlas migrate validate` 门禁恢复绿色的必要条件。改动已在本批次 review-it 中单独审查。

### DEV2：软删 KB 的 UpdateKB 返回 NOT_FOUND（WHERE 追加 `status <> 'deleted'`）
- **Spec 说：** §5.1 AC 写 "WHERE id=$kb_id"。
- **实现：** WHERE 同时过滤软删行，与 get_kb/list_kbs 读路径一致。
- **理由：** SPEC §6.1 DeleteKB 语义规定软删后对所有读路径隐藏；若 UPDATE 可复活软删行将与 DeleteKB 语义冲突。测试 `test_update_kb_soft_deleted_kb_returns_not_found` 固化。

## 权衡（Tradeoffs）

### T1：毒 key 自愈靠嵌套事务（SAVEPOINT）而非预检跳过
- **备选 A（采纳）：** INSERT 后捕获 23505，SAVEPOINT 保证外层事务可用，复用 pending 行。
  - 优点：无需预查询窗口的锁/竞态推理；与 DB 唯一约束单一事实源。
  - 缺点：依赖 asyncpg 嵌套事务降级语义（已在测试注释与本批次 review 中核验）。
- **备选 B（弃用）：** 插入前 SELECT 判断 pending 行并跳过插入。
  - 弃用理由：SELECT 与 INSERT 之间仍有竞态窗口，仍需处理撞 UNIQUE；两套逻辑并存徒增复杂度。

### T2：`update_kb`/`find_by_idempotency_key` 保持自开事务（外层内降级 SAVEPOINT），而非拆 `_in_tx` 变体
- **备选 A（采纳）：** repository 保持既有自开事务风格，servicer 外层事务内调用时自动降级 SAVEPOINT。
  - 优点：repository 单独使用时（无外层事务）行为不变；改动面最小。
  - 缺点：注释须写清嵌套语义（review finding F-1 已修正），初读者易误解。
- **备选 B（弃用）：** 仿 `create_task_in_tx` 为 `update_kb`/`find_by_idempotency_key` 再拆事务外变体。
  - 弃用理由：函数数翻倍、调用点需判断用哪个变体；SAVEPOINT 语义已足够，YAGNI。

### T3：atlas.sum 修复采"无条件全目录重哈希"（Atlas 官方 `migrate hash` 语义），而非手工补 7 条目
- **备选 A（采纳）：** `atlas migrate hash` 重生成整文件。
  - 优点：哈希链由工具保证；顺带修复 HEAD 缺失条目（历史 PR 漏跑 hash）与 4 个哈希漂移。
  - 缺点：diff 噪音大（整文件重写）；对从未跑过该命令的历史无副作用。
- **备选 B（弃用）：** 手工编辑 atlas.sum 补条目。
  - 弃用理由：Atlas 目录哈希算法非公开稳定接口，手工构造易错且不可审计。

## 开放问题（Open Questions）

### OQ1：`make validate-architecture` 在本机因 `.run/gomodcache/` 误报失败
校验脚本（`validate_component_imports.py` L149-151）只排除 `/vendor/` 与 `/.cache/`，不排除 `/.run/`；本机 `.run/`（未跟踪运行时工件目录，含本地 Go 模块缓存）被扫描产生数百条 forbidden import 误报。过滤后真实代码零违规（本次改动全为 Python/SQL）。**建议 follow-up issue**：脚本加 `/.run/` 排除 + `.gitignore` 补 `.run/`。

### OQ2：CI 缺 `atlas migrate validate` 门禁
历史 PR 已两次漏跑 hash 导致 atlas.sum 缺条目（本次顺带修复 7 条）。建议 CI 加门禁防再犯，属 CI 配置改进，未在本批次处理。

### OQ3：route-contract 门禁仍待 issue-044 Gateway 路由注册解除
沿承 issue-040 OQ1：2 条 `spec_not_in_code`（PUT updateKB / GET getKBDocument）需 #044 同 PR 落地后转绿。

### OQ4：`name` 字段无 maxLength（沿承 issue-040 OQ4）
CreateKB/UpdateKB 均无长度约束，KB 域既有惯例。若统一收紧应整域同步做。

### OQ5：提交时须排除本批次临时工件
`review-it.diff`、`.bin-atlas/`（128MB Atlas CLI 二进制）为会话临时产物，**勿入库**。`tests/test_update_kb.py` 为未跟踪新文件，**必须随批次提交**。

## 验证命令（已运行）

```
cd services/kb-service && python -m pytest tests/test_grpc_wiring.py tests/test_update_kb.py tests/test_grpc_server.py -q   # 48 passed
python -m compileall -q services/kb-service/app                                    # 语法通过
../.bin-atlas/atlas.exe migrate hash --dir file://deploy/migrations                # 重生成 atlas.sum
../.bin-atlas/atlas.exe migrate validate --dir file://deploy/migrations            # exit 0
python scripts/validate_inference_legacy_control_plane.py                          # exit 0
python scripts/validate_component_imports.py --root . 2>&1 | Where-Object { $_ -notmatch '\.run[/\\]' }   # 过滤 .run/ 后零发现
git diff --check                                                                   # exit 0
```

> 注：`make validate-architecture` 整体 exit 2 全部由 `.run/gomodcache/` 误报构成（OQ1），仓库真实代码零违规。Atlas CLI v1.3.0 下载至 `.bin-atlas/atlas.exe`（go install 因 Go 1.25 与 x/tools 不兼容不可用）；atlas 命令需 `requires_approval` 运行（遥测目录 `C:\Users\PC\.atlas` 沙箱限制）。
