# [前置] 验证 RLS 双 policy 前提（WithPlatformTx 可见行）

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/quota/prd-quota-service.md`
- UX: N/A
- SPEC: `repo/services/tasks/modules/spec/core/quota/spec-quota-service.md`
- Plan: `repo/services/tasks/modules/plan/plan-quota-service.md`

## Description

对齐 plan §14 第 1 步"先验证 RLS 风险"。plan §13.1 虽标注"已解决，李宇确认"，但要求在写 adapter 代码前先用最小集成测试确认前提：`WithPlatformTx`（不设 `app.current_tenant_id`）在双 policy（`platform_bypass` + `self`）下能看到 resource_quota 行。若前提不成立，§6 的 5 个管理方法全靠 `WithPlatformTx` 就全废，需改用别的事务模型。

## Acceptance Criteria

- [x] 真实环境手动验证：`ani_app_user` SET 租户 001 → 看到 1 行；切换租户 002 → 0 行（self policy 生效）
- [x] 真实环境手动验证：确认 Go `WithPlatformTx` 从未 SET → `current_setting` 返回 NULL → platform_bypass 放行（psql RESET 残留空字符串不影响 Go）
- [x] 真实环境手动验证：确认 `ani_app_user` 有三张表 DML 权限
- [ ] 新增最小集成测试文件（`//go:build integration` build tag），连 PG 实例
- [ ] 测试 1：`WithPlatformTx`（不设 `app.current_tenant_id`）→ `SELECT` resource_quota 能看到所有行
- [ ] 测试 2：`WithTenantTx`（设 `app.current_tenant_id`）→ `SELECT` resource_quota 只看到本租户行
- [ ] 测试 3：`WithTenantTx` 试图 INSERT 别的 tenant_id 行 → RLS 拒绝
- [ ] 测试通过 `go test ./pkg/adapters/runtime/ -v -run RLS -tags integration`
- [ ] 若前提不成立，记录现象并阻塞 #3/#4/#5

## Dependencies

李宇 migration 已落地（外部依赖）

## Type

core

## Priority

high (blocker)

## Labels

core

## Batch

TBD

## References

- SPEC: §10.1 Phase 0, §3.1, §7.2
- Plan: §13.1, §14 第 1 步
