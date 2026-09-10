# KB-API-B4 — issue-019 + issue-020：KB 权限读写对（GET/PUT permissions）

> Issue 编号：plan 内编号 #19（GET）/ #20（PUT）——kb-p1-api-completion-plan §2（无独立 issue 文件，plan §2 即实施契约）
> Batch: KB-API-B4 · 产品线: core（Services / kb-service + ani-gateway + 契约层）
> Plan: `repo/services/tasks/modules/plan/knowledge_base/kb-p1-api-completion-plan.md` §2（B4）
> 前置: KB-API-B1/B2/B3（issue-040~048）已合入，#1~#18 三层全通

完成日期：2026-09-08（实现日）/ 同日收口（E2E + review-it）
分支：`feat/kb-api-completion`（B1/B2/B3 改动同分支叠加，未提交）
验证结果：kb-service pytest **321 passed**；`go test ./services/ani-gateway/internal/router/` ok（含 5 个权限专项单测）；**E2E 24/24 全绿**（本地 gateway + kb-service + 服务器 PG/Redis 全链路）；`make validate-architecture` 通过（component import guard + inference legacy control plane）；review-it 终审 **clean，0 actionable findings**。

## 实现了什么

1. **契约层（proto + OpenAPI）**：
   - `kb_service.proto`：新增 `GetKBPermissions(GetKBPermissionsRequest) returns (KBPermissions)` RPC（L56-58）+ `GetKBPermissionsRequest`（L324-328）+ `KBPermissions` message（kb_id/public_read/repeated allowed_user_ids/Timestamp updated_at，L330-336）；`UpdateKBPermissions` 契约既有（P1 声明），本批落地实现。
   - `v1.yaml`：`/knowledge-bases/{kb_id}/permissions` 路径补 `get`（置于 put 前，L2757-2775，operationId `getKnowledgeBasePermissions`，200 返回 KBPermissions）；新增 `KBPermissions` schema（required: [kb_id, public_read, allowed_user_ids, updated_at]，L956-964）；`UpdateKBPermissionsRequest` 补 `idempotency_key format: uuid`（L952）。**零发明路由**：GET/PUT 与 plan §2.2 逐字对齐。
   - 生成物同步：两侧 pb（Go `pkg/generated/pb/kb/v1/`、Python `app/generated/kb/v1/`）、SDK 四语言、docs/api、Console schema.d.ts。
2. **数据层**（`services/kb-service/migrations/005_kb_permissions.sql`，新建）：
   - `kb_permissions` 表：`kb_id UUID PK REFERENCES knowledge_bases ON DELETE CASCADE`（一对一，无需额外唯一索引）+ 冗余 `tenant_id`（RLS 谓词必需，kb_chunks 同模式）+ `public_read BOOL D:FALSE` + `allowed_user_ids UUID[] D:'{}'` + `updated_at TIMESTAMPTZ D:now()`；
   - RLS 双 policy（**对 plan §2.3 原稿的关键修正**，见偏差 1）：`kbp_self` + `kbp_platform_bypass` 均 PERMISSIVE，与 knowledge_bases/kb_documents/kb_chunks/async_tasks 全库形态一致；
   - `idx_kb_permissions_tenant` 索引；GRANT SELECT/INSERT/UPDATE/DELETE TO ani_app。
3. **repository 层**（`app/repositories/permission.py`，新建）：
   - `get_permissions`：RLS 事务内读行；**无行返回默认值 dict**（public_read=False/allowed_user_ids=[]/updated_at=None）——契约"默认值非 404"的数据层落点；
   - `upsert_permissions_in_tx`：`INSERT ... ON CONFLICT (kb_id) DO UPDATE`（整体替换语义，kb_id 即 PK）。
4. **kb-service servicer**（`app/api/grpc_server.py` L2038-2199，替换 p1_rpcs stub 委托）：
   - `GetKBPermissions`（读路径，无幂等）：tenant/kb 非空校验 → `get_kb` RLS 查询（KB 不存在 → NOT_FOUND，404 语义与 GetKB 一致）→ `get_permissions` → `updated_at = perm.updated_at or kb.created_at`（无行回退 KB 创建时间，防 proto Timestamp 零值漏 1970）；
   - `UpdateKBPermissions`（写路径，幂等）：idempotency_key 非空+uuid 校验 / kb_id / tenant_id 校验 → allowed_user_ids 逐项 uuid 校验 + **set 辅助保序去重** → 单事务原子提交（find_by_idempotency_key 回放（task_type=`kb.perm.update` 收窄）→ get_kb → upsert → create_task_in_tx（payload 记录 public_read + allowed_user_ids）+ UniqueViolationError 毒 key 自愈（回读复用 pending 行）→ complete_task_in_tx）→ 返回 `_kb_row_to_pb(kb_row)`（契约 returns KnowledgeBase）；**省去 plan §2.5 步骤 d 的二次 get_kb**——upsert 只触 kb_permissions 表，事务内 KB 行不变，直接复用步骤 2b 快照（见设计决策 D1）。
5. **Gateway**（`internal/router/kb_resources.go` + `kb_grpc_client.go`）：
   - 路由 `GET /knowledge-bases/:kb_id/permissions`（L68，B2 路由前注册）；request struct `updateKBPermissionsRequest`（L135-139）；
   - PUT handler（L499-526）：nil-client 503 → BindJSON 400 → idempotency_key 必填 + uuid.Parse 校验 400 → client 调用 → `kbToJSON`；
   - GET handler（L534-545）：nil-client 503 → client → `kbPermissionsToJSON`；
   - `kbPermissionsJSON`（updated_at `omitempty` 防 1970 泄漏）+ `kbPermissionsToJSON` 归一：**proto 空 repeated → Go nil → JSON `null` 陷阱修复**（nil 归一为 `[]string{}`，契约 required array）；
   - `KBGRPCClient` 接口 + 实现补 `UpdateKBPermissions`/`GetKBPermissions`（tenant 从 Auth 中间件 `instanceTenantID(c)` 注入，body 内 tenant_id 一律忽略）。
6. **测试**：
   - kb-service pytest 321 passed（新增权限 repo/servicer 用例）；
   - `kb_resources_test.go` 5 个权限单测：GetPermissions_Passthrough（含 tenant 注入断言）、ZeroUpdatedAtOmitted、NotFoundMappedTo404、GetPermissions_NilClientReturns503、UpdatePermissions_NilClientReturns503；`IdempotencyKey_MustBeUUID` 六入口全量断言（B3 已建，UpdateKBPermissions 在列）；
   - `kb_sse_test.go` fake 补空实现（接口新增方法的编译义务）；
   - **B4 E2E**（`tests/e2e/test_kb_permissions_e2e.py`，未跟踪交付物）：本地 gateway + kb-service 对服务器 PG/Redis 全链路 **24/24**——CreateKB 前置、GET 默认值契约（无行 → false/[]/kb.created_at）、PUT 写入 + DB 直查、幂等回放（不新增 task 行）、整体替换语义、4 负例（非法 uuid 400/缺幂等键 400/缺 KB 404/跨租户 GET+PUT 404 RLS 隔离）、DeleteKB 级联清理。
7. **review-it 收口审查**：clean，0 actionable findings（七项候选发现全部拒绝有据：404 语义必需 get_kb、返回 KnowledgeBase 为既有契约、幂等回放语义对齐 UpdateKB、毒 key 自愈同模式、深度校验分层归属 kb-service、两项 E2E bug 已修复）。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `api/proto/kb/v1/kb_service.proto` | 修改 | GetKBPermissions RPC（L56-58）+ 3 message（L316-336） |
| `api/openapi/services/v1.yaml` | 修改 | GET 路由（L2757-2775）+ KBPermissions schema（L956-964）+ format: uuid 补齐 |
| `services/kb-service/migrations/005_kb_permissions.sql` | 新增（未跟踪） | kb_permissions 表 + 双 PERMISSIVE RLS + GRANT；**必须随批次提交** |
| `services/kb-service/app/repositories/permission.py` | 新增（未跟踪） | get_permissions（默认值）+ upsert_permissions_in_tx；**必须随批次提交** |
| `services/kb-service/app/api/grpc_server.py` | 修改 | GetKBPermissions/UpdateKBPermissions servicer（L2038-2199），移除 p1_rpcs 委托 |
| `services/kb-service/app/api/p1_rpcs.py` | 删除 | stub 清理（对齐 B2 ListKBCitations/ListKBSessions 先例） |
| `services/ani-gateway/internal/router/kb_resources.go` | 修改 | 路由（L68）+ struct（L135-139）+ PUT handler（L499-526）+ GET handler（L534-545）+ kbPermissionsJSON/ToJSON |
| `services/ani-gateway/internal/router/kb_grpc_client.go` | 修改 | 接口 + 实现（UpdateKBPermissions L343-353 / GetKBPermissions L358-362） |
| `services/ani-gateway/internal/router/kb_resources_test.go` | 修改 | fake 字段/方法 + 5 个权限单测 |
| `services/ani-gateway/internal/router/kb_sse_test.go` | 修改 | fake 空实现（编译义务，SSE 测试本体零改动） |
| `services/kb-service/tests/e2e/test_kb_permissions_e2e.py` | 新增（未跟踪） | B4 E2E 24/24 全链路；**必须随批次提交** |
| 生成物（pb ×2 侧 / SDK 四语言 / docs/api / schema.d.ts / services-contract-baseline.yaml） | 修改 | 契约再生成产物 |

## 设计决策（Design Decisions）

### D1：UpdateKBPermissions 省去二次 get_kb（plan §2.5 步骤 d → 实现删除）
- **模糊点：** plan §2.5 步骤 d 要求 upsert 后再 SELECT kb 行拿"最新快照"作为幂等 result。
- **选择：** 复用步骤 2b（KB 存在性检查）的行快照，删除二次查询。
- **理由：** `upsert_permissions_in_tx` 只写 kb_permissions 表，事务内 knowledge_bases 行不可能变化（RLS 锁定同租户、无并发可见性问题——同事务内读自己未提交的写也一致）；第二次 SELECT 是纯冗余往返。B4 收口审查时用户选定执行此优化（同批优化：v1.yaml 补 format:uuid、PUT nil-client 501→503、去重加 set 辅助）。
- **代价：** 若未来 upsert 扩展为同时更新 KB 主表（如 updated_at 联动），需恢复二次查询；当前契约无此需求。

### D2：GET 权限行的 updated_at 无行回退 kb.created_at
- **模糊点：** 无权限行时 updated_at 返回什么——proto Timestamp 零值（1970）、null、还是回退值。
- **选择：** `perm.updated_at or kb_row.created_at`。
- **理由：** v1.yaml `KBPermissions.updated_at` 为 required date-time，null/省略违反契约；1970 是 proto 零值泄漏（JSON 序列化陷阱）；KB 创建时间是最接近"权限生效起点"的真实语义（默认 private 继承自创建时刻）。Gateway 侧 `omitempty` 作第二道防线（防上游极端情况下零值漏出）。

### D3：allowed_user_ids 保序去重用 set 辅助（而非列表 in 或 dict）
- **模糊点：** 白名单数组含重复 uuid 时——拒绝 400、静默存储重复、还是去重。
- **选择：** `seen_user_ids: set` + 保序 append 去重，静默归一。
- **理由：** 重复 uuid 是无害冗余而非语义错误（客户端勾选列表重复提交），去重保序既不破坏用户输入顺序（幂等回放 byte-stable）又保证 DB 数组无重复；O(1) 查重优于列表 O(n)。B4 收口审查时用户选定执行（四项优化之一）。

### D4：Gateway 只做 idempotency_key 校验，allowed_user_ids 的 uuid 深度校验留在 kb-service
- **模糊点：** Gateway 是否镜像校验白名单逐项 uuid（幂等键已有六入口镜像校验先例）。
- **选择：** 幂等键在 Gateway + servicer 双层校验；白名单只在 servicer 校验。
- **理由：** 幂等键是**所有写入口的横切安全属性**（Gateway 校验可省一次 gRPC 往返，B3 D1 已论证）；allowed_user_ids 是本接口的业务字段，servicer INVALID_ARGUMENT → 400 映射已覆盖错误响应，Gateway 前置校验收益仅一个往返但需在 struct 侧加逐项遍历，且未来若白名单扩为"用户对象引用"（Q3 展示名联查）Gateway 校验反而要跟着改。分层职责：Gateway 守契约形状，servicer 守业务语义。

## 偏差（Deviations vs PRD/UX/SPEC）

### 偏差 1（重大）：RLS policy 形态——plan §2.3 单 RESTRICTIVE → 实现双 PERMISSIVE
- **plan 原稿：** `CREATE POLICY tenant_isolation ... AS RESTRICTIVE USING (tenant_id = current_setting('app.tenant_id')::uuid)`。
- **实际实现：** `kbp_self` + `kbp_platform_bypass` 双 PERMISSIVE（`app.current_tenant_id` + NULLIF 形态），并 DROP 掉初版已建的 tenant_isolation。
- **原因：** E2E 揭示 plan 原稿照抄了 002_kb_chunks.sql 的 RESTRICTIVE 形态，但 PostgreSQL RLS 规则是"至少一条 PERMISSIVE 通过 AND 所有 RESTRICTIVE 通过"——**单 RESTRICTIVE 且无 PERMISSIVE 时全部拒绝**，表完全不可访问（PUT 500 `new row violates row-level security policy`）。GET 未报错只是空结果集走默认值路径掩盖了问题。全库真实形态（knowledge_bases/kb_documents/kb_chunks/async_tasks）实为双 PERMISSIVE（TASKCENTER-A2 批次已做过同型修复：RESTRICTIVE-only fail-closed 改双 PERMISSIVE），plan §2.3 的"对齐 002_kb_chunks.sql"引述的是初版 002 的历史形态。修正后与全库一致，服务器 DB 已同步修复。
- **性质：** plan 原稿缺陷，实现修正——**kb-p1-plan §2.3 的 SQL 示例建议后续勘误**（不影响本批已落地代码）。

### 偏差 2：proto 空 repeated → JSON null（E2E Bug 2，gateway 层修复）
- **契约：** v1.yaml `allowed_user_ids` required array，空集应为 `[]`。
- **初版实现：** `kbPermissionsToJSON` 直取 `p.GetAllowedUserIds()`——proto 未设置 repeated 字段时返回 Go nil，`json.Marshal` 输出 `null` 而非 `[]`。
- **修复：** nil 归一 `[]string{}`；`p == nil` 时也归一（第二道防线）。
- **性质：** 实现缺陷，E2E 抓获后修复（GET 默认值契约用例直接命中——无行场景 servicer 返回空 repeated，正是触发面）。

### 偏差 3：PUT nil-client 返回 503（非 plan 隐含的 501）
- **plan 原稿：** §2.6 GET handler 示例 nil-client 返回 503，PUT 未明示（P1 时期 route 注释曾标"UNIMPLEMENTED → 501"）。
- **实现：** GET/PUT 统一 503 UNAVAILABLE（与其他 19 条 KB 路由的 nil-client 语义一致——kb-service 未接线属部署瞬时不可用，非功能未实现）。
- **理由：** B4 落地后 stub 消失，"未实现"语义不再成立；503 是 Gateway 对下游缺失的既有惯例。B4 收口审查时用户选定（四项优化之一）。

## 权衡（Tradeoffs）

### T1：整体替换语义 vs 增量合并语义（PUT permissions）
- **备选 A（采纳）：** 整体替换——PUT body 的 allowed_user_ids 全量覆盖 DB 数组。
- **备选 B（弃用）：** 增量合并（只加/删 delta）。proto 无 add/remove 语义字段，契约 PUT + body 携带全量列表即替换语义（REST PUT 幂等性要求：同 body 重复 PUT 结果一致）；增量语义需 PATCH 或专用字段，属后续需求。
- **验证：** E2E 明确断言整体替换（第二次 PUT 短名单后 GET 确认长名单消失）。

### T2：GET 无权限行返回默认值 vs 404
- **备选 A（采纳）：** 默认值（false/[]）——权限的"未配置"即"默认 private"，是合法状态而非缺失资源。
- **备选 B（弃用）：** 404——会让前端对全新 KB 的权限面板报错，需特判兜底；把默认语义推给每个调用方。
- **注意：** KB 本身不存在仍 404（get_kb 先行）——"资源存在但属性未配置"与"资源不存在"严格区分。

### T3：E2E 复用服务器基础设施（PG NodePort/Redis）vs 全本地栈
- **备选 A（采纳）：** 本地起 gateway/kb-service 进程，DB/Redis 走服务器（10.10.1.66 NodePort）。
- **备选 B（弃用）：** 全本地 docker 栈——Windows 环境搭建成本高，且 RLS/真实数据形态核验本就需要与部署态一致的 PG。
- **代价：** 测试数据落服务器（已清理：kb_permissions/async_tasks 测试行清零，KB 软删）；幂等键用例需随机 uuid 防共享库冲突（CreateKB 前置补显式 idempotency_key 由此发现——gateway 层强制要求，非 B4 缺陷）。

## 开放问题（Open Questions）

### OQ1：查询侧权限执行闭环（继承 plan §0.3/§10.2 Q2）
B4 只交付管理面读写。list/get/query 按 `public_read`/`allowed_user_ids` 过滤可见性的执行闭环留待后续批次（需 proto 请求补 user_id 链路，影响面大）。维持 plan 处置：不在本批。

### OQ2：allowed_user_ids 返回展示名（继承 plan §2.2 注/Q3）
白名单当前只回 uuid 数组；「返回用户展示名便于前端渲染」需 Core 用户中心联查，依赖 B5 的 Core API client 模式，列 Q3 不阻塞。

### OQ3：kb-p1-plan §2.3 的 RLS 示例 SQL 勘误（本批偏差 1 的文档债）
plan 原稿 §2.3 的单 RESTRICTIVE SQL 与全库真实形态不符（本批实现已修正落地，但 plan 文档本身未改——B5~B8 批次的 `kb_audit_log` 迁移若照抄 §2.3 会复现同坑）。建议：随 B8（audit 表新建）或下次 plan 修订时勘误 §2.3，或立即小修。

### OQ4：服务器 DB 的 migration 005 与仓库版同步性
E2E 期间发现并直接在服务器 DB 修复了 RLS policy（scp → kubectl cp → psql 路径），随后仓库 migration 005 同步改为一致形态。**仓库版与服务器版现已一致**，但该修复未经 migration 重放验证（服务器库是手工 ALTER，非重跑 005）——首次部署到新环境时 migration 005 的幂等性（DROP POLICY IF EXISTS ×3 + CREATE）已覆盖，无风险，仅记录操作路径差异。

### OQ5：CURRENT-SPRINT.md / ANI-06-开发计划.md 四件套（继承 #048 OQ2）
KB-API 系列 B1~B3 只更新 development-records/{批次}.md + README.md 两件。B4 沿用该惯例（本记录 + README 索引）。待系列收口 PR 时统一决定是否补齐。

## 验证命令（已运行）

```
cd repo/services/kb-service
python -m pytest tests -q --ignore=tests/e2e                 # 321 passed
python -m pytest tests/e2e/test_kb_permissions_e2e.py -q     # B4 E2E 24/24（本地 gateway+kb-service / 服务器 PG+Redis）
cd repo/services/ani-gateway
go test ./internal/router/ -count=1                           # ok（含 5 个权限单测 + 六入口 MustBeUUID）
cd repo
make validate-architecture                                    # 通过（component import guard + inference legacy control plane）
git diff --check                                              # exit 0
```

> E2E 期间发现并修复 2 个真实 bug（RLS 单 RESTRICTIVE 锁死整表 → 双 PERMISSIVE 修正；GET 空 repeated → JSON null → 归一 `[]`），修复后全量回归（321 pytest + router go test + E2E 重跑）通过。测试期服务器数据已清理；本地服务进程已停；一次性辅助脚本（_check/_apply/_inspect/_fix 系列）已删除。
>
> 注：`make` 目标在 Windows PowerShell 下的 POSIX 语法限制同 #048 偏差 1；本批 validate-architecture 直接可跑（无 env 前缀依赖）。
