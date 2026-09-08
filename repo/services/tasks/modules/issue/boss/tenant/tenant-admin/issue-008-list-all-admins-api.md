# 后端：跨租户管理员列表 API

## Document Links
- PRD: `repo/services/tasks/modules/prd/boss/tenant/prd-new-boss-tenant-admin.md`
- UX: `repo/services/tasks/modules/ux/boss/tenant/ux-boss-tenant-admin.md`
- SPEC: `repo/services/tasks/modules/spec/boss/tenant/spec-new-boss-tenant-admin.md`

## Description
实现跨租户管理员列表：`GET /api/v1/svc/tenant-admins`（对应 SPEC §4.1 / US-003）。跨所有租户游标分页查询，返回所有 tenant-admin 以及正在邀请或已过期的 user/auditor，返回租户对象与 is_inviting / is_expired 标记。

## Scope
- Product line: boss
- Code paths allowed: `repo/services/tenant-service/internal/service/`、`repo/services/tenant-service/internal/repo/adapters/postgres/`、`repo/services/tenant-service/internal/repo/adapters/core/`

## Acceptance Criteria
- [ ] `GET /api/v1/svc/tenant-admins`，支持 limit（默认 20，最大 100）/ cursor / tenant_id / search / role / source；可选过滤参数 status / is_inviting / is_expired **三选一**（同时传入多个返回 400 VALIDATION_FAILED），响应 CursorPage（items + next_cursor）
- [ ] 通过 Core SDK `ListTenantAdmins` + `BatchGetUsers` 查询（Core DB 层 JOIN users + user_roles + roles + tenants，WHERE is_deleted=FALSE）；传入 tenant_id 时按该租户过滤
- [ ] **返回所有 tenant-admin（role 为 tenant-admin 的用户）以及正在被邀请或邀请已过期的 user/auditor**；未传入过滤参数时返回全部 tenant-admin + 邀请中（`tenant_admin_invitation.status='inviting'`）+ 已过期（`tenant_admin_invitation.status='expired'`）的用户；传入 status 时按该状态过滤，传入 is_inviting=true 时仅返回邀请中用户，传入 is_expired=true 时仅返回已过期用户
- [ ] items 每项含 id/email/username/display_name/role/status/is_inviting/is_expired/source/last_login_at/tenant{id,name,display_name}；is_inviting / is_expired 仅作标记，不影响 role/status（邀请中或已过期用户仍展示原有角色，可为 user）
- [ ] search 非空时按 username / email / display_name 模糊匹配（对齐 SPEC §5.1.3）
- [ ] source 推断：oidc: → third_party，local: → local（对齐 SPEC §5.1.3）
- [ ] role 过滤仅支持 tenant-admin / user / auditor，source 过滤仅支持 local / third_party（非法值返回 400 VALIDATION_FAILED）
- [ ] 游标分页按 created_at DESC，不写审计（只读）
- [ ] 单元/集成测试覆盖：返回所有 admin/auditor、邀请中/已过期 user 出现且 role 保持不变、普通 user 不出现、三选一校验（SPEC §9.3 Test_List_AllAdminsAndInviting / Test_List_InvitingUserKeepsRole / Test_List_ExpiredUserKeepsRole / Test_List_FilterMutualExclusion）

## Dependencies
#2, #3

## Type
backend

## Priority
high

## References
- SPEC: §5.1.3 ListAllTenantAdmins / §4.1 listAllTenantAdmins / §4.2 CursorPage / §9
- Plan: 租户管理plan v3.0 §6.3.17 / §6.3.17d
