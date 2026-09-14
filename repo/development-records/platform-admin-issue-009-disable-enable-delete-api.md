# PLATFORM-ADMIN-ISSUE-09：禁用 + 启用 + 软删除 API

> **批次类型：** Feature batch（BOSS 平台运营账号功能流 Issue #9）
> **完成日期：** 2026-09-02（本地未提交；含 review-it 补 SoftDelete Store 单测）
> **Scope：** Disable/Enable/Delete 全链路——Services 三 RPC + Core client `SetStatus`/`SoftDelete` + Core Store 状态机/最后 admin + 双网关转发；新增 `STATUS_UNCHANGED` 错误码
> **Product line：** services（主）+ core（Store 状态校验与 last-admin 保护；#4 已有 Core Gateway handler）
> **本地 issue：** `repo/services/tasks/modules/issue/boss/settings/platform-admin/issue-009-disable-enable-delete-api.md`
> **依赖：** #2（接口+SDK）、#4（Core handler + Services 网关骨架）

## 交付内容

1. **Services：** `DisablePlatformAdmin` / `EnablePlatformAdmin` / `DeletePlatformAdmin` 替换 501——`parsePlatformAdminUserID` 校验 → Core `SetStatus`/`SoftDelete` → best-effort 审计 `platform_admin.disable|enable|delete`（成功 details 仅 `target_id`）
2. **Core client：** `SetStatus` 按 `active`→`POST .../enable`、否则 `.../disable`；`SoftDelete`→`DELETE .../{userId}`；不传 `idempotency_key`
3. **Core Store：** `SetStatus` 同事务读 `status+role`、拒绝同状态（`ErrStatusUnchanged`）、禁用最后 admin 保护；`SoftDelete` 最后 admin 保护后 `is_deleted=TRUE, deleted_at=now(), status='disabled'`
4. **Core Gateway：** `mutatePlatformUserStatus` / `deletePlatformUser`（幂等由外层中间件；handler 不强制 `idempotency_key`；body 可空）
5. **Services Gateway：** `writeIdempotentUserAction` 转发三端点；`mapPlatformAdminError` 映射 `LAST_PLATFORM_ADMIN` / `STATUS_UNCHANGED` → 422
6. **错误码：** Core/Services ports 新增 `ErrStatusUnchanged`（`STATUS_UNCHANGED`）；OpenAPI Core/Services 补 `StatusUnchanged` response 与 disable/enable 422 描述
7. **测试：** Service Disable/Enable/Delete 成功 + LastAdmin；Core client SetStatus/SoftDelete；Store SetStatus（LastAdmin/AlreadyDisabled/AlreadyActive/Success）+ SoftDelete（LastAdmin/Success）；Gateway `DisableEnableFlow` / `DeleteFlow` / `LastAdminProtection`

## Design Decisions

1. **最后管理员保护只在 Core Store 事务内**
   - **Ambiguity：** Issue 写 Services 透传 Core 422；未要求 Services 本地计数。
   - **Choice：** `countActivePlatformAdminsTx` 仅在 `PostgresPlatformUserAdminStore.SetStatus(disabled)` 与 `SoftDelete` 内执行；Services 只调 Core SDK 并 `mapDomainError`。
   - **Rationale：** 与 SPEC §5.1.6–5.1.7 一致；原子性；避免 Services/Core 双实现漂移。

2. **重复启用/停用返回 `STATUS_UNCHANGED`（422），不写库**
   - **Ambiguity：** Issue/UX 未单独列此码；产品需区分「已是该状态」与成功。
   - **Choice：** `SetStatus` 在 UPDATE 前比较 `currentStatus == status` → `ErrStatusUnchanged`；Enable 仅 422 `STATUS_UNCHANGED`；Disable 可与 `LAST_PLATFORM_ADMIN` 并列 422。
   - **Rationale：** 防止无意义写与审计噪音；前端可提示「已是启用/禁用状态」。

3. **审计成功仅 `target_id`（与 Create/ChangeRole 成功路径对齐）**
   - **Choice：** disable/enable/delete 成功审计不写 `status`/`role`；失败写 `target_id` + `reason`（经 `writeAuditFailure`）。
   - **Rationale：** 与 #5/#8 隐私与字段最小化一致；详情可从 List/Get 事后查。

4. **幂等仅 BOSS 外层网关；Service 与 Services→Core 不消费键**
   - **Choice：** Services Gateway `writeIdempotentUserAction` 仍 `BindJSON` 取 `idempotency_key`（可回落 header）；gRPC 字段透传但 Service 不校验；Core client 不传 key；Core handler 不强制（#8 已确立）。
   - **Rationale：** 用户明确跨层不写幂等；避免双层去重。

5. **共享 `parsePlatformAdminUserID`**
   - **Choice：** Disable/Enable/Delete 三 RPC 共用 UUID 校验 + 失败审计。
   - **Rationale：** 减少重复；错误语义一致。

6. **Enable 无最后管理员保护**
   - **Choice：** 仅 `status=disabled` 且 `role=platform-admin` 时计数；`active` 路径不拦截。
   - **Rationale：** 对齐 Issue AC 与 UX；恢复账号不应被 last-admin 规则阻挡。

## Deviations

| Spec / Issue | 实现 | 原因 |
|---|---|---|
| Issue AC：Service 消费/校验 `idempotency_key` | 网关转发 proto 字段；Service 不校验不消费 | #5/#8 产品决策：幂等仅外层 |
| Issue AC：`make test` 全绿 | 本会话仅跑聚焦包测试 | 合入前仍需全量门禁 |
| SPEC §9.3 集成测「真实 Core」 | Gateway 层 fake gRPC + 有状态 fake client 合成流 | 与 #5/#6 CreateFlow 同模式；未连真实 PG/Core |
| Issue scope 未列 Core Store | 本批强化 `SetStatus`（同状态拒绝）+ `SoftDelete` 单测 | 行为属 #4 Core handler 契约，review 补覆盖 |
| Core OpenAPI body 仍 `required: idempotency_key` | Handler 不强制；Services→Core 不传 | 与 Create/ChangeRole 同「契约提示、实现宽松」模式 |

## Tradeoffs

- **同状态 422 vs 幂等 200：** 选 422 `STATUS_UNCHANGED`——语义明确；代价是前端必须用新 key 重试且需处理 422（不能依赖幂等回放掩盖重复点击）。
- **SoftDelete 对已禁用 last-admin 仍拦截：** 与「活跃 admin 计数」一致（`status=active` 才计入他人）；已禁用名义最后 admin 仍不能删——偏保守，与 SPEC 计数公式一致。
- **DELETE 带 JSON body（idempotency_key）：** HTTP 语义略别扭，但 OpenAPI/中间件已约定；Gateway 必须 `BindJSON`，空 body → 400。
- **Gateway 合成流测 vs 端到端：** fake client 维护内存 `status`/`deleted`——快且稳定；不覆盖 Core SQL/RLS 真环境。

## Open Questions

1. 前端重复点击「禁用」：应展示 `STATUS_UNCHANGED` 文案还是当作成功？当前为 422。
2. 已软删账号再次 DELETE：404 `PLATFORM_USER_NOT_FOUND`——是否需要在 UX 上区分「从未存在」与「已删除」？（当前不区分。）
3. 合入前是否把 Core OpenAPI 写接口 `idempotency_key` 改为 optional，与 handler 行为一致？
4. Feature batch 四文件：`CURRENT-SPRINT.md` / `ANI-06` Section 零 / README 分组是否随本批一并更新？

## Verification

```bash
# 本会话已执行（全部通过）：
go test ./pkg/adapters/runtime/ -run "SetStatus|SoftDelete" -count=1
go test ./services/platform-settings-service/internal/service/ -run "Disable|Enable|Delete" -count=1
go test ./services/platform-settings-service/internal/repo/adapters/core/ -run "SetStatus|SoftDelete" -count=1
go test ./services/ani-gateway/internal/router/ -run "DisableEnable|DeleteFlow|LastAdmin" -count=1

# 待合入前：
cd repo
make test
make validate-architecture
git diff --check
```

## 边界声明

- **本批聚焦 disable / enable / soft-delete**；ResetPassword / AuditLogs 仍 501（#10/#11）。
- **不声称** live / production ready。
- 全部改动本地未提交；commit/PR 归用户指令。
