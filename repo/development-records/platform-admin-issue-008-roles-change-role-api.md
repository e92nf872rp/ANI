# PLATFORM-ADMIN-ISSUE-08：Services 角色列表 + 账号权限查询 + 改角色 API

> **批次类型：** Feature batch（BOSS 平台运营账号功能流 Issue #8）
> **完成日期：** 2026-09-01（本地未提交；含 review-it：审计字段、最后 admin、幂等边界）
> **Scope：** Services `ListPlatformAdminRoles` / `GetPlatformAdminPermissions` / `UpdatePlatformAdminRole` + OpenAPI/proto/网关 JSON；Core `ChangeRole` 最后 admin 保护与写接口幂等边界对齐
> **Product line：** services（主）+ core（ChangeRole last-admin / Gateway 写 handler 不强制 idempotency_key）
> **本地 issue：** `repo/services/tasks/modules/issue/boss/settings/platform-admin/issue-008-roles-change-role-api.md`
> **依赖：** #4（网关骨架）、#7（Core roles/permissions）；Create/List 已用 `role_id`

## 交付内容

1. **契约：** Services `GET .../permissions`；`PlatformRole` / permissions 响应对齐 Core（含 `id`/`role_id`）；proto `GetPlatformAdminPermissions` + `repeated Struct permissions`；改角色 422 文档化 `LAST_PLATFORM_ADMIN`
2. **Services：** 三角色 RPC 实现——ListRoles/GetPermissions 只读不写审计；UpdateRole 校验 UUID → Get 取 old_role → `ListPlatformRoles` 解析 new_role 名 → ChangeRole → 审计 `platform_admin.change_role`
3. **Core client：** ChangeRole body 仅 `role_id`（不传幂等键）
4. **Services Gateway：** `/roles`、`/:userId/permissions`、`PUT /:userId/role`；permissions JSON 数组；外层仍可带 `idempotency_key`（中间件去重，Service 不消费）
5. **Core ChangeRole：** 降级唯一活跃 `platform-admin` → `LAST_PLATFORM_ADMIN`；有绑定 UPDATE / 无绑定 INSERT
6. **Core Gateway 写接口：** Create/ChangeRole/Reset/Disable/Enable/Delete **handler 不强制** `idempotency_key`（与「幂等只在外层网关」一致；Services→Core 不传 key）
7. **测试：** Service ListRoles/GetPermissions/UpdateRole（含 LastAdmin 审计）；Store ChangeRole Update/Insert/LastAdmin/Demote；Gateway roles/permissions/role 转发

## Design Decisions

1. **改角色入参用 `role_id`（UUID），不用角色名字符串**
   - **Ambiguity：** 旧 SPEC/Issue 草稿写 `role` 名；Create 已切 `role_id`。
   - **Choice：** OpenAPI/proto/Service/Core 全链路 `role_id`；列表返回 `id` 供下拉。
   - **Rationale：** 稳定主键；改名不影响绑定；与 #5/#6/#7 一致。

2. **幂等只在外层（BOSS）网关；Service 与 Services→Core 均不处理幂等键**
   - **Ambiguity：** Issue AC「确保 gRPC 透传 idempotency_key」；Core OpenAPI 仍 required。
   - **Choice：** Service 不校验/不消费 key；Core client 不传 key；Core 写 handler 不强制校验（与 Create 同）。外层 `middleware.Idempotency` 有 key 才去重。
   - **Rationale：** 用户明确「service 到 core 写接口也不需要幂等」；避免双层去重与跨层强制透传。

3. **审计 details：`target_id` + `old_role` + `new_role`（均为角色名）**
   - **Ambiguity：** 曾短暂写 `new_role_id`。
   - **Choice：** 改前 Get 得 `old_role`；`resolvePlatformRoleName`（ListPlatformRoles）得 `new_role`；失败/成功同一字段集。
   - **Rationale：** 对齐 SPEC §5.2.4；可读审计；解析失败时回退为 role_id 字符串。

4. **ChangeRole 最后 admin 保护（Core 事务内）**
   - **Ambiguity：** UX 改角色可返回 `LAST_PLATFORM_ADMIN`；早期 ChangeRole 无保护。
   - **Choice：** 当前为 `platform-admin` 且目标非 admin，且其他活跃 admin 数 ≤0 → `ErrLastPlatformAdmin`；与 SoftDelete/SetStatus 共用 `countActivePlatformAdminsTx`。
   - **Rationale：** 防止自锁；UX 已有该错误码提示。

5. **不做三角色白名单；不做四维权限映射**
   - **Choice：** 合法性完全交给 Core `roles` 表；permissions JSONB 原样。
   - **Rationale：** Issue「实现对齐」块；与 #7 / auth-service V2 同源。

## Deviations

| Spec / Issue | 实现 | 原因 |
|---|---|---|
| Issue AC：ChangeRole 入参 `role` 名；审计 `new_role` | 入参 `role_id`；审计仍为角色名 `new_role` | 全链路 id 化；审计可读性保留 name |
| Issue AC：gRPC 透传并消费 idempotency_key | Service 不消费；Core 不强制 | 用户指定幂等仅外层网关 |
| Issue scope：不触碰 Core Store | 本批补 ChangeRole last-admin + Core 写 handler 幂等边界 | 正确性与跨层约定必须在 Core 落地 |
| SPEC §5.2.3 ListRoles 静态四维、不调 Core | 调 Core ListPlatformRoles，JSONB 原样 | Issue 明确废弃四维草稿 |
| Issue：`PlatformRole` required 仅 name+permissions | 另含必填 `id` | 改角色/创建依赖 role_id |
| 可选集成 ChangeRoleFlow | 未做端到端假链路合成测 | 分层单测已覆盖；合入前可补 |

## Tradeoffs

- **改角色前额外 ListPlatformRoles 解析 new_role vs 成功后再 Get：** 选 List——失败路径也能写出正确 `new_role`；代价是多一次 Core 只读（角色表极小）。
- **last-admin 与 SoftDelete 同款「排除当前后活跃数」：** 一致行为；对「已全部 disabled」的边缘态偏保守（与 delete 相同）。
- **Core OpenAPI 仍标 idempotency_key required vs handler 不校验：** 保留契约提示外层/直连客户端带 key；Services→Core 可省略。代价是契约与强制校验不完全一致（与 Create 相同模式）。

## Open Questions

1. 前端权限 Tab：直接展示 JSONB，还是本地映射到 UX 四维矩阵？
2. 是否需要在合入前把 Core OpenAPI 写接口的 `idempotency_key` 改为 optional，以免 SDK 生成强制字段？
3. 并发互降 admin 的 TOCTOU（与 disable/delete 同类）是否接受，或后续加更强约束？
4. Feature batch 四文件：`CURRENT-SPRINT.md` / `ANI-06` Section 零是否在本批合入时一并更新？

## Verification

```bash
# 本会话已执行（全部通过）：
go test ./pkg/adapters/runtime/ -run "ListPlatformRoles|GetPlatformUserPermissions|ChangeRole" -count=1
go test ./services/platform-settings-service/internal/service/ -run "ListPlatformAdminRoles|GetPlatformAdminPermissions|UpdatePlatformAdminRole" -count=1
go test ./services/platform-settings-service/internal/repo/adapters/core/ -count=1
go test ./services/ani-gateway/internal/router/ -run "PlatformAdmin|AdminListPlatformUserRoles|AdminGetPlatformUserPermissions" -count=1

# 待合入前：
cd repo
make test
make validate-architecture
git diff --check
```

## 边界声明

- **本批聚焦 roles / permissions / change_role**；ResetPassword / Disable / Enable / Delete / AuditLogs 仍 501（#9–#11）。
- Core 写 handler 幂等边界调整惠及后续写 issue，但业务体未实现。
- **不声称** live / production ready。
- 全部改动本地未提交；commit/PR 归用户指令。
