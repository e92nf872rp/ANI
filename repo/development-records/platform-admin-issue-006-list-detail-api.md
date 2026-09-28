# PLATFORM-ADMIN-ISSUE-06：列表 + 详情 API

> **批次类型：** Feature batch（BOSS 平台运营账号功能流 Issue #6）
> **完成日期：** 2026-09-01（本地未提交；含 review-it 修复与 search 语义调整）
> **Scope：** List/Get 全链路——Services `ListPlatformAdmins` / `GetPlatformAdmin` + Core SDK List/Get + Core Gateway GET + Store List/Get；review 轮修复（前缀、超时、校验、单测）
> **Product line：** services + core（List/Get 依赖 Core handler/store；Core Gateway 响应映射本批一并调整）
> **本地 issue：** `repo/services/tasks/modules/issue/boss/settings/platform-admin/issue-006-list-detail-api.md`
> **依赖：** #2（接口+SDK）、#4（Core handler）；Create（#5）已先行落地

## 交付内容

1. **Services：** `ListPlatformAdmins` / `GetPlatformAdmin` 替换 501——分页默认 20/上限 100、`validateListPlatformAdminFilters`（status/source 枚举）→ Core SDK List/Get → 映射剥前缀 + source 推断；只读不写审计
2. **Core 客户端：** `CorePlatformUserClient.List` / `Get` 透传 query/path + `decodePlatformUser`；Get 成功路径单测
3. **Core Gateway：** `GET /admin/platform-users` / `{userId}` 转发 Store；`toAdminPlatformUserResponse` 剥 `local:`/`oidc:`；list limit 上限 100
4. **Core Store：** `PostgresPlatformUserAdminStore.List` / `Get`——游标分页、role/status/source 过滤、**search 仅对外 username**（`REGEXP_REPLACE` 后 ILIKE，不含前缀误命中）；status/source 非法值 → `VALIDATION_FAILED`
5. **Services Gateway：** `listPlatformAdmins` / `getPlatformAdmin` gRPC 转发；`platformAdminCallTimeout` **12s**（覆盖 Core SDK 10s）；`parseCursorLimitQuery` 非法 limit → 400
6. **OpenAPI：** platform-admins / admin platform-users 的 `search` 描述改为「对外 username，不含前缀」；2026-09-24 补充 `PlatformAdminListItem.email`
7. **测试：** Store List（过滤、游标、非法枚举、search SQL）、Service List/Get（含 InvalidSource/Status、email 透传）、Gateway AdminList/AdminGet/CreateFlow/InvalidLimit（含 email JSON）、Core client Get 成功

## Design Decisions

1. **username 前缀在 Gateway/Service 出口剥除，Store 仍存库内 `local:`/`oidc:`**
   - **Ambiguity：** #5 遗留 TODO；列表/详情对外应展示 `ops` 而非 `local:ops`。
   - **Choice：** Services `stripPlatformUsernamePrefix` + Core Gateway `stripAdminPlatformUsernamePrefix`；Store/SQL 仍用带前缀列做 source 过滤与唯一约束。
   - **Rationale：** 存储与登录语义不变；对外 REST 与 BOSS UX 一致；Create 响应仍仅 id/message（#5 决策延续）。

2. **search 仅匹配剥前缀后的 username，不按存储前缀命中**
   - **Ambiguity：** 早期 SPEC/OpenAPI 写 email/username 模糊；review 后改为 username only；用户进一步要求「search 不包含前缀命中」。
   - **Choice：** SQL `REGEXP_REPLACE(u.username, '^(local:|oidc:)', '') ILIKE '%search%'`。
   - **Rationale：** 搜 `ops` 命中 `local:ops`；搜 `local` / `local:ops` 不因前缀误命中；与对外展示名一致。

3. **source/status 双层校验：Services 预检 + Core Store 再检**
   - **Choice：** Services `validateListPlatformAdminFilters`；Store List 对 status/source 非法枚举返回 `VALIDATION_FAILED`。
   - **Rationale：** BOSS 路径早失败、错误语义稳定；Core 直连仍受 Store 保护。

4. **Gateway gRPC 超时 12s（非 tenant 默认 5s）**
   - **Ambiguity：** List/Get 链路过 Gateway → Services → Core SDK（10s），5s 易 504。
   - **Choice：** `platformAdminCallCtx` 独立 12s + metadata 透传；不复用 `tenantCallCtx` 时长。
   - **Rationale：** 覆盖 Core HTTP 超时 + gRPC 一跳余量。

5. **user_roles 一对一：不引入 DISTINCT ON 去重**
   - **Ambiguity：** review 曾建议多 platform 角色 JOIN 去重。
   - **Choice：** 保留简单 `JOIN user_roles`；用户确认表已改为一对一。
   - **Rationale：** 最小 SQL；依赖 DB 约束保证单角色。

6. **只读 List/Get 不写审计**
   - **Choice：** Service 层无 audit 调用。
   - **Rationale：** 与 issue AC / UX「查看不产生操作记录」一致。

## Deviations

| Spec / Issue | 实现 | 原因 |
|---|---|---|
| SPEC §5.1.2：`email ILIKE OR username ILIKE` | 仅对外 username（剥前缀后）ILIKE | 初期产品/review 决策：列表不展示 email、search 不搜 email；2026-09-24 起列表返回并展示 email，search 仍不搜 email |
| SPEC §5.1.2：`source='oidc'` 过滤 | OpenAPI/Services 用 `third_party`；Store 映射为 `oidc:%` | 对外契约 enum 为 `local\|third_party`（与 #5 source 展示一致） |
| Issue scope 原偏 Services Gateway | 本批同时调整 Core Gateway 响应剥前缀 + Store search SQL | BOSS 经 Services；Core 直连调试需一致；search 语义只能在 Store 落地 |
| Issue AC：`TestPlatformUserAdminStore_List` 集成级 | 使用 `quotaFakeTx` 单测覆盖 SQL/过滤/游标 | 与 quota adapter 测试模式一致；未连真实 PG |
| `/platform-admins/roles` | 仍 501（#7） | 用户明确暂不实现；列表 role 筛选需前端硬编码或手填 query |

## Tradeoffs

- **剥前缀在 Gateway+Service 双处 vs 抽公共 pkg：** 双处小函数——避免 Services import Gateway 或 Core import Services；代价是两处逻辑需同步（当前 identical 10 行）。
- **search 在 SQL REGEXP_REPLACE vs 应用层过滤：** SQL——分页/count 正确、性能可接受；代价是 PostgreSQL 特定表达式，换库需重写。
- **Services 预校验 + Core 重复校验 vs 仅 Core：** 双层——BOSS 早失败；Core 直连仍安全。代价是 status/source 规则两处维护（当前一致）。
- **strict limit 仅 platform-admins list vs 全局 cursorLimit：** 仅 list 用 `parseCursorLimitQuery`——不影响 tenant-plans 等已有行为；audit-logs 仍用宽松 `cursorLimit`。
- **Core 列表 internal 含 email vs Services 初期丢弃：** Core OpenAPI `PlatformUser` 一直保留 email；Services 初期映射时省略。2026-09-24 变更后 Services 列表契约与 BOSS 页面均透传 email。

## 2026-09-24 补充：列表返回 email

1. **契约：** Services OpenAPI `PlatformAdminListItem` 增加 required `email`；Proto `PlatformAdminListItem` 追加字段 `email = 9`，不改变既有字段编号。
2. **服务层：** `toPlatformAdminListItem` 直接透传 Core `PlatformUserDTO.Email`；`ListPlatformAdmins` 列表响应完整携带邮箱。
3. **网关与 BOSS：** Services Gateway `platformAdminListItemJSON` 输出 `email`；BOSS 列表类型、映射函数和邮箱列展示同步接入。
4. **不改变的语义：** `search` 仍只匹配剥前缀后的 username，不按 email 模糊搜索；邮箱仅作为列表展示与详情字段。
5. **兼容性：** Core 列表原本已返回 email，本次不涉及数据库和 Core 查询变更；Proto 追加字段对旧客户端向后兼容。
6. **验证：** 2026-09-24 本地通过 platform-settings-service 全包、ani-gateway router 全包、Services OpenAPI 校验与 Services 路由契约；BOSS 相关文件通过 oxfmt。


## Verification

```bash
# 本会话已执行（全部通过）：
go test ./pkg/adapters/runtime/ -run "PlatformUserAdmin" -count=1
go test ./services/platform-settings-service/internal/service/ -run "ListPlatform|GetPlatform" -count=1
go test ./services/platform-settings-service/internal/repo/adapters/core/ -count=1
go test ./services/ani-gateway/internal/router/ -run "PlatformAdmin|AdminList|AdminGet|CreateFlow" -count=1

# 待合入前：
cd repo
make test
make validate-architecture
git diff --check
```

## 边界声明

- **本批聚焦 List/Get 只读路径**；ChangeRole/ResetPassword/Disable/Enable/Delete/AuditLogs/Roles 仍 501 或后续 issue。
- **不声称** live / production ready。
- 全部改动本地未提交；commit/PR 归用户指令。
