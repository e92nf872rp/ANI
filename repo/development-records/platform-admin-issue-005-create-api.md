# PLATFORM-ADMIN-ISSUE-05：创建平台运营账号 API

> **批次类型：** Feature batch（BOSS 平台运营账号功能流 Issue #5）
> **完成日期：** 2026-09-01（本地未提交；含 review-it 修复与 email 唯一性产品规则调整）
> **Scope：** Create 全链路——Services `CreatePlatformAdmin` + Core SDK Create + Core Gateway `POST /admin/platform-users` + Store Create；契约 email 可重复；软删 username 唯一索引调整
> **Product line：** services + core（Create 依赖 Core handler/store，本批一并落地）
> **本地 issue：** `repo/services/tasks/modules/issue/boss/settings/platform-admin/issue-005-create-api.md`
> **依赖：** #2（接口+SDK）、#4（Services 链路）；Core Create 本批补齐

## 交付内容

1. **Services：** `PlatformAdminService.CreatePlatformAdmin` 替换 501——入参校验（`validate.go`）→ Core SDK Create → best-effort 审计 `platform_admin.create`
2. **Core 客户端：** `CorePlatformUserClient.Create` 仅 POST，响应取 `id`（不回查 Get）；透传 `context`；独立 `http.Client` Timeout（不再改全局 DefaultClient）
3. **Core Gateway：** `admin_platform_user_resources.go` `POST /admin/platform-users`——密码复杂度校验 → bcrypt → `PlatformUserAdminStore.Create`
4. **Core Store：** `PostgresPlatformUserAdminStore.Create`——字段校验、`local:` 前缀、角色解析、INSERT + user_roles；`23505` 映射 `USERNAME_ALREADY_EXISTS`
5. **产品规则：** 平台侧 **email 允许重复**；删除 `EMAIL_ALREADY_EXISTS` 哨兵/映射/测试；OpenAPI 409 仅 `USERNAME_ALREADY_EXISTS` / `IDEMPOTENCY_CONFLICT`
6. **迁移：** `20260901000100_drop_platform_email_unique.sql`——删 `idx_users_platform_email`；重建 `idx_users_platform_username` 为 `WHERE tenant_id IS NULL AND is_deleted = FALSE`（软删后可复用同名）
7. **测试：** Service Create 单测、Core client Create、Gateway AdminCreate（含弱密码）、Store Create/冲突/unique 映射、`TestHandler_CreateFlow`（create→list→detail，假 gRPC）

## Design Decisions

1. **role 不写死枚举，仅非空 + Core roles 表校验**
   - **Ambiguity：** Issue AC 写「role 白名单」；契约已改为不以 OpenAPI enum 写死。
   - **Choice：** Services 只校验 `role` 非空；非法名由 Core 查 `roles`（`tenant_id IS NULL AND name LIKE 'platform-%'`）返回 `ROLE_NOT_FOUND`。
   - **Rationale：** 后续增删平台角色无需改 Services 校验代码；与「角色以 Core 为准」一致。

2. **幂等只在网关中间件，Service/Core Create 不落幂等存储**
   - **Ambiguity：** Issue AC「网关层幂等去重」；SPEC 要求 body `idempotency_key`。
   - **Choice：** 复用 `middleware.Idempotency`；Service 注释标明不消费 key；Core handler 不单独实现幂等。
   - **Rationale：** 用户明确指示幂等已在网关；避免双层去重与 Core 侧未统一幂等表的复杂度。

3. **成功审计 details 仅 `target_id`（不含 role / password）**
   - **Ambiguity：** Issue AC 写「details 含 target_id + role」。
   - **Choice：** 成功只写 `target_id`；校验失败审计可带 `role`；全程禁止 password。
   - **Rationale：** 减少敏感/冗余字段；单测显式断言成功审计无 role。与「明文密码不落审计」同一隐私边界。

4. **Create 响应契约仅 `{id,message}`，不回查详情**
   - **Choice：** Core 返回 IdempotentResult 形；Services client 不二次 GET。
   - **Rationale：** 契约与 issue AC 一致；少一次 RTT；列表/详情后续 issue 负责展示字段。

5. **对外 username 暂保留库内 `local:` 前缀（列表批再剥）**
   - **Choice：** Store/Gateway 响应仍带前缀；代码标注 `TODO(list/detail)`。
   - **Rationale：** 用户指示「列表后续修改，现在注释中注明」；避免本批越界改 List/Detail 契约。

## Deviations

| Spec / Issue | 实现 | 原因 |
|---|---|---|
| PRD/SPEC 早期：email 全局唯一 + `EMAIL_ALREADY_EXISTS` | email 可重复；删除 email 唯一索引与全部 `EMAIL_ALREADY_EXISTS` 路径 | 产品规则调整（迁移 + OpenAPI description）；用户确认删除 email 冲突相关内容 |
| Issue AC：role 白名单 | 非空 + Core DB 校验 | 见 Design Decision 1；契约不再写死 enum |
| Issue AC：成功审计 `target_id + role` | 仅 `target_id` | 见 Design Decision 3 |
| SPEC §5.1.1 Create 仍写查 email 唯一 | Store 不查 email 冲突 | 与现行产品规则一致；SPEC 文档未在本批全文改写 |
| Issue AC：集成测试 create→list→detail「全字段」 | `TestHandler_CreateFlow` 用假 gRPC client，detail 仍为网关转发占位（Get RPC 501 未实现时测的是假响应拼装） | 真 Get 归 #6；本批验证转发与 Create body/幂等键透传 |
| Issue 范围原偏 Services | 本批同时落地 Core Create handler + Store + 迁移 | Create 无 Core 无法联调；用户会话中一并完成 Core 侧 |

## Tradeoffs

- **网关幂等 vs Service/Core 再实现：** 选网关——与用户指示一致；代价是缺 key 时中间件放行（空 key 直接 Next），依赖前端始终带 `idempotency_key`。
- **密码复杂度双层校验（Services + Core Gateway）vs 只在一层：** 双层——Services 早失败；Core 直连仍安全。代价是两处 `validatePassword*` 逻辑重复（未抽公共包，避免跨层 import）。
- **软删后 username 可复用（partial unique `is_deleted=FALSE`）vs 永久占用：** 可复用——运营删错可重建同名；代价是历史软删行与新行同 username 并存，登录侧必须只查 `is_deleted=FALSE`。
- **SDK 注入独立 HTTPClient + Context vs 改 DefaultClient：** 独立 client——避免污染全局；Context 使网关超时可取消 Core 调用。代价是需改生成 SDK（`RequestOptions.Context` / `Client.HTTPClient`）并同步 `gen_sdk_alpha.py`。

## Verification

```bash
# 本会话已执行（全部通过）：
go test ./pkg/adapters/runtime/ -run "PlatformUser|UniqueViolation|ValidatePlatform|InferPlatform" -count=1
go test ./services/platform-settings-service/internal/service/ -run "Create|MapDomain" -count=1
go test ./services/platform-settings-service/internal/repo/adapters/core/ -count=1
go test ./services/ani-gateway/internal/router/ -run "AdminCreate|MapPlatformAdmin|PlatformAdmin" -count=1
go test ./sdks/core/go/anisdk/ -count=1

# 待合入前：
cd repo
atlas migrate hash --dir file://deploy/migrations
atlas migrate apply --dir file://deploy/migrations --url $DATABASE_URL
make test
make validate-architecture
git diff --check
```

## 边界声明

- **本批聚焦 Create**；Get/ChangeRole/ResetPassword/Disable/Enable/Delete/AuditLogs/Roles 仍占位或后续 issue。
- **不声称** live / production ready；未跑全量 `make test` / `validate-architecture` 作为本笔记闭环证明。
- 全部改动本地未提交；commit/PR 归用户指令。
