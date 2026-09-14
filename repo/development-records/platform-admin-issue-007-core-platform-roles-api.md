# PLATFORM-ADMIN-ISSUE-07：Core 平台角色列表 + 账号权限查询 API

> **批次类型：** Feature batch（BOSS 平台运营账号功能流 Issue #7）
> **完成日期：** 2026-09-01（本地未提交；含 review-it 对齐 GetPermissions SQL）
> **Scope：** Core `GET /admin/platform-users/roles` + `GET /admin/platform-users/{userId}/permissions`——ports/Store/Gateway/Core SDK/Services Core client；**不含** Services 业务 RPC（归 #8）
> **Product line：** core（主）+ services（仅 Core client 封装）
> **本地 issue：** `repo/services/tasks/modules/issue/boss/settings/platform-admin/issue-007-core-platform-roles-api.md`
> **依赖：** #1（契约）、#2（端口骨架）、#3（platform-* 角色种子）；Blocks #8

## 交付内容

1. **OpenAPI（Core）：** `listPlatformUserRoles` / `getPlatformUserPermissions`；schema `PlatformRole`（`id`+`name`+`permissions`）、`PlatformUserPermissionsResponse`（`user_id`+`role_id`+`role`+`permissions`）
2. **ports：** `ListPlatformRoles` / `GetPlatformUserPermissions`；`PlatformRole` / `PlatformUserPermissions` 含 `ID`/`RoleID`
3. **Store：** `ListPlatformRoles`——`tenant_id IS NULL AND name LIKE 'platform-%'`，permissions JSONB 原样解码；`GetPlatformUserPermissions`——JOIN 过滤与 Get 对齐（平台角色绑定 + 未软删）
4. **Core Gateway：** 静态 `/roles` 先于 `/:userId`；permissions 子路径注册；错误映射 404
5. **Services Core client：** `ListPlatformRoles` / `GetPlatformUserPermissions` + DTO decode
6. **测试：** Store ListRoles（含空表、非 platform 过滤）、GetPermissions（成功/空 permissions/NotFound/软删）；Gateway AdminListRoles / AdminGetPermissions

## Design Decisions

1. **不做 label / description 中文映射**
   - **Ambiguity：** Issue AC 草案要求 Core 内置 label/description。
   - **Choice：** 契约与实现均省略；仅 `id`/`name`/`permissions`（及 permissions 响应用 `role_id`/`role`）。
   - **Rationale：** 用户确认以当前实现为准；避免硬编码中文与 roles 表双源；展示名可由前端按 `name` 映射。

2. **permissions 原样透出 JSONB，不做 UX 四维矩阵**
   - **Ambiguity：** SPEC/UX 草稿有 `tenant_ops`/`resource_pool`/`platform_user`/`audit_export` 静态四维。
   - **Choice：** 返回 `resource`/`actions`/`scope` 数组（与 auth-service V2 授权面一致）。
   - **Rationale：** DB 种子与运行时授权同源；四维属展示层，留给前端或后续 Services 映射（#8 明确废弃）。

3. **按 `platform-%` 前缀过滤，不写死三角色 IN 列表**
   - **Choice：** SQL `name LIKE 'platform-%'` + `ORDER BY name`。
   - **Rationale：** 新增平台角色无需改代码；tenant 角色不会泄漏进列表。

4. **GetPermissions SQL 与 Get 平台账号判定对齐**
   - **Ambiguity：** 初版 JOIN 条件可能漏 `r.tenant_id IS NULL AND r.name LIKE 'platform-%'`。
   - **Choice：** review 后与 Get/ChangeRole 绑定过滤一致；无平台角色绑定 → `PLATFORM_USER_NOT_FOUND`。
   - **Rationale：** 避免租户角色误当作平台权限返回。

5. **响应带 `role_id`（UUID）**
   - **Choice：** 列表项与权限响应均含角色 id。
   - **Rationale：** 创建/改角色已切到 `role_id` 入参（#5/#8）；下拉框必须用稳定 id 而非仅 name。

## Deviations

| Spec / Issue | 实现 | 原因 |
|---|---|---|
| Issue AC：`PlatformRole` 含 label/description | 无此二字段 | 产品确认；减少双源与契约膨胀 |
| Issue AC：`PlatformUserPermissionsResponse` 含 label/description | 无；改为含 `role_id` | 同上；并服务 role_id 改角色链路 |
| Issue path 写 `pkg/adapters/postgres/` | 实现在 `pkg/adapters/runtime/` | 与既有 PlatformUserAdmin Store 落点一致 |
| Issue：Services 仅 client；Roles RPC 501 | 本批仅 Core + client；Services RPC 归 #8 | 边界清晰，Blocks 关系保留 |

## Tradeoffs

- **原样 JSONB vs 四维映射：** 选原样——与授权同源、零映射漂移；代价是 BOSS 权限 Tab 需前端适配或另做展示映射。
- **不分页角色列表 vs cursor：** 选不分页——平台角色极少（当前 3）；代价是未来角色暴涨需再加分页（可接受）。
- **GetPermissions 404 吞并「无绑定」vs 区分错误码：** 与 Get 一致用 `PLATFORM_USER_NOT_FOUND`——客户端简单；代价是运维无法区分「人不存在」与「无平台角色」。

## Verification

```bash
# 本会话已执行（全部通过）：
go test ./pkg/adapters/runtime/ -run "ListPlatformRoles|GetPlatformUserPermissions" -count=1
go test ./services/platform-settings-service/internal/repo/adapters/core/ -run "ListPlatformRoles|GetPlatformUserPermissions" -count=1
go test ./services/ani-gateway/internal/router/ -run "AdminListPlatformUserRoles|AdminGetPlatformUserPermissions" -count=1

# 待合入前：
cd repo
make test
make validate-architecture
git diff --check
```

## 边界声明

- **本批聚焦 Core 只读 roles/permissions**；Services `ListPlatformAdminRoles` / `GetPlatformAdminPermissions` / `UpdatePlatformAdminRole` 见 #8。
- **不声称** live / production ready。
- 全部改动本地未提交；commit/PR 归用户指令。
