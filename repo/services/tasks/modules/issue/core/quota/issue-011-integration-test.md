# 集成测试（连 PG 实例，双角色验证 RLS）

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/quota/prd-quota-service.md`
- UX: N/A
- SPEC: `repo/services/tasks/modules/spec/core/quota/spec-quota-service.md`
- Plan: `repo/services/tasks/modules/plan/plan-quota-service.md`

## Description

编写集成测试，连本地 docker-compose PG，用管理员和租户双角色连接验证 RLS 隔离和 bypass 行为。覆盖扣减 12 场景 + 管理 11 场景 + SDK 端到端。

## Scope

- Product line: core
- Code paths allowed: `pkg/adapters/runtime/integration_test.go`

## Acceptance Criteria

- [ ] 新增 `pkg/adapters/runtime/integration_test.go`（`//go:build integration` build tag）
- [ ] 前置：PG 实例可用（docker-compose 本地 PG），DSN 通过 `ANI_TEST_ADMIN_DSN`、`ANI_TEST_TENANT_DSN` 环境变量覆盖
- [ ] Setup：管理员连接建三张表 + RLS policy + seed meta + GRANT 权限给 ani_app_user
- [ ] 扣减场景 1：租户 A Try 成功（RLS 放行 INSERT + UPDATE）
- [ ] 扣减场景 2：租户 A GetMy 查自己配额（RLS 放行）
- [ ] 扣减场景 3：租户 A 查租户 B 配额返回 0 行（RLS 拦截）
- [ ] 扣减场景 4-7：Confirm/Cancel/Release 幂等（含跨租户 RLS 拦截）
- [ ] 扣减场景 8：租户 A 试图 INSERT tenant_id='B' 流水被 RLS 拒绝
- [ ] 扣减场景 9：并发 Try 不超卖（N 个租户连接并发 Try，reserved 不超过 total）
- [ ] 扣减场景 10-12：TryMany 端到端、Confirm/Cancel/Release 幂等、Release 端到端 used 归零
- [ ] 管理场景 13-15：Put/List/Delete 用管理员连接 bypass RLS 成功
- [ ] 管理场景 16-17：CreateTenantQuota 批量新建 + 幂等（ON CONFLICT DO NOTHING 不覆盖）
- [ ] 管理场景 18-19：UpdateTenantQuota 改 total + 缩容（GREATEST clamp，tightened=true，Try 新建→ErrQuotaExceeded）
- [ ] 管理场景 20：GetTenantQuota JOIN meta（unit/display_name/is_discrete 正确）
- [ ] 管理场景 21：DeleteTenantQuota（resource_quota + resource_reservations 均清空）
- [ ] 管理场景 22：ListQuotaMeta 返回 enabled=true 维度列表
- [ ] 管理场景 23：SDK 端到端（启动 ani-gateway，SDK 调 5 端点 → DB 验证）
- [ ] 测试后清理数据（TRUNCATE，用管理员连接）
- [ ] 集成测试通过 `go test ./pkg/adapters/runtime/ -v -run Integration -tags integration`
- [ ] 集成测试用 `//go:build integration` build tag 隔离，不阻塞默认 `make test`
- [ ] Typecheck/lint 通过

## Dependencies

#3, #4, #5

## Type

core

## Priority

medium

## Labels

core

## Batch

TBD

## References

- SPEC: §9.4
- Plan: §11.4
