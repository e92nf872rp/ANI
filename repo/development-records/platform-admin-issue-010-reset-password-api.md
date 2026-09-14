# PLATFORM-ADMIN-ISSUE-10：重置密码 API

> **批次类型：** Feature batch（BOSS 平台运营账号功能流 Issue #10）
> **完成日期：** 2026-09-02（本地未提交；含 review-it 契约 path 修正 + Store 单测补全）
> **Scope：** Services `ResetPlatformAdminPassword` RPC + Core client `ResetPassword` + Services Gateway `POST .../reset-password`；OpenAPI 独立 path 修正；Core Store `ResetPassword` 单测
> **Product line：** services（主）+ core（Store bcrypt 强校验；#4 已有 Core Gateway handler）
> **本地 issue：** `repo/services/tasks/modules/issue/boss/settings/platform-admin/issue-010-reset-password-api.md`
> **依赖：** #2（接口+SDK）、#4（Core handler）

## 交付内容

1. **Services：** `ResetPlatformAdminPassword` 替换 501——`parsePlatformAdminUserID` → `validatePasswordComplexity` 预校验 → `coreClient.ResetPassword` → 审计 `platform_admin.reset_password`（成功 details 仅 `target_id`）
2. **Core client：** `POST /admin/platform-users/{userId}/reset-password`，body 仅 `new_password`；不传 `idempotency_key`；`PASSWORD_SAME_AS_OLD` → `ports.ErrPasswordSameAsOld`
3. **Core Store：** 事务内读 `password_hash` → `bcrypt.CompareHashAndPassword` 同旧拒绝 → `GenerateFromPassword` + UPDATE；`password_hash` 为空（OIDC）跳过同旧比对
4. **Services Gateway：** `resetPlatformAdminPassword` 绑定 `new_password` + `idempotency_key`（可回落 header）→ gRPC；`mapPlatformAdminError` 映射 `PASSWORD_SAME_AS_OLD` → 422
5. **契约修复：** `services/v1.yaml` 将 `resetPlatformAdminPassword` 从错误的 `/platform-admins/{userId}/role` POST 挪至独立 `/platform-admins/{userId}/reset-password`；`validate_services_route_contract` 0 errors；`docs/api/services.html` 重生成
6. **测试：** Service Success/SameAsOld/Validation；Core client ResetPassword + SameAsOld 映射；Gateway `TestHandler_ResetPasswordFlow`；Store ResetPassword 五场景（校验/404/同旧/成功/无旧 hash）

## Design Decisions

1. **复杂度双层校验：Services 预检 + Core Store 强校验**
   - **Ambiguity：** Issue 写「前端预校验，Core 强校验」；Services 是否再校验未明说。
   - **Choice：** `platform_admin_service.validatePasswordComplexity` 在调 Core 前失败即 400；Store `ResetPassword` 再次校验（与 Create 链路对称）。
   - **Rationale：** 减少无效 Core 往返；Store 为最终权威，防绕过 Services 直连 Core admin API。

2. **同旧密码在 Core Store 用 bcrypt 比对**
   - **Choice：** `CompareHashAndPassword` 成功（err==nil）→ `ErrPasswordSameAsOld`；不写库。
   - **Rationale：** 对齐 SPEC §5.1.5 / Issue AC 422 `PASSWORD_SAME_AS_OLD`；不在 Services 层重复持 hash。

3. **审计成功仅 `target_id`（与 disable/change_role 对齐）**
   - **Choice：** 成功/失败审计均不含 `new_password`/`password`；单测显式断言。
   - **Rationale：** UX §4.4 明文不落日志/审计/响应；最小化敏感字段。

4. **幂等仅 BOSS 外层网关；Service 与 Services→Core 不消费键**
   - **Choice：** gRPC proto 可带 `idempotency_key`；Service 不校验不消费；Core client 不传；Core handler 不强制（#8 已确立）。
   - **Rationale：** 与 #5/#8/#9 跨层决策一致。

5. **无 `password_hash` 的账号可直接设密**
   - **Choice：** `oldHash == nil || *oldHash == ""` 时跳过同旧比对，直接 bcrypt 写回。
   - **Rationale：** 第三方/OIDC 账号可能尚无本地密码；管理员重置即首次设本地凭据。

6. **禁用账号仍可重置密码**
   - **Choice：** Store 只过滤 `is_deleted = FALSE`，不查 `status`。
   - **Rationale：** 运营场景下管理员帮被禁用账号改密合理；账号仍 disabled，需单独 enable。

## Deviations

| Spec / Issue | 实现 | 原因 |
|---|---|---|
| Issue AC：Service 消费/校验 `idempotency_key` | 网关转发 proto 字段；Service 不校验不消费 | #5/#8/#9 产品决策：幂等仅外层 |
| SPEC §9.3：集成测「旧密码登录失败、新密码登录成功」 | Gateway fake gRPC client 维护 `currentPassword` 内存状态 | 与 #5/#6 CreateFlow 同模式；未连真实认证服务/PG |
| Issue scope 未列 OpenAPI / Core Store 测试 | 修正 `v1.yaml` path 缩进；补 5 个 Store 单测 | review-it 发现契约漂移；SPEC §9.1 要求 Store 覆盖 |
| Issue AC：`make test` 全绿 | 本会话跑聚焦包 + route contract | 合入前仍需全量门禁 |
| 初版 OpenAPI 将 reset 挂在 `/role` POST | 已修正为 `/reset-password` POST | 实现与网关注册一直正确；契约源文件错误 |

## Tradeoffs

- **Services 预校验 vs 仅 Core 校验：** 双层重复 `validatePasswordComplexity`（三处与 GW Create 同规则）——换更早 400 与更少 Core 负载；代价是规则漂移需同步（当前三处逻辑一致）。
- **Core Gateway reset 无 handler 级复杂度预检：** Create 在 handler 预检；Reset 仅 Store——少一层冗余，无效请求仍会打到 Store 读 hash。
- **Gateway 合成流 vs 真实登录 E2E：** fake client `verifyPassword` 模拟改密——快且稳定；不覆盖 Dex/平台登录全链路。
- **同旧 422 vs 幂等 200：** 用户用同一 `idempotency_key` 但改了 `new_password` 会 409 `IDEMPOTENCY_KEY_REUSED`（中间件 fingerprint 含 body）——防误重放，前端需新 key 或相同 body。

## Verification

```bash
# 本会话已执行（全部通过）：
python scripts/validate_services_route_contract.py   # 0 errors
go test ./pkg/adapters/runtime/... -run "ResetPassword" -count=1
go test ./services/platform-settings-service/internal/service/... -run "ResetPassword" -count=1
go test ./services/platform-settings-service/internal/repo/adapters/core/... -run "ResetPassword" -count=1
go test ./services/ani-gateway/internal/router/... -run "ResetPassword" -count=1
python scripts/generate_api_docs.py

# 待合入前：
cd repo
make test
make validate-architecture
git diff --check
```

## 边界声明

- **本批聚焦 reset-password**；AuditLogs 仍 501（#11）。
- **不声称** live / production ready。
- 全部改动本地未提交；commit/PR 归用户指令。
