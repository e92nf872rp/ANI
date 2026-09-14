# Services 链路打通：网关全量转发 + 服务装配 + Core SDK 适配

## Document Links
- PRD: `repo/services/tasks/modules/prd/boss/settings/prd-new-boss-platform-admin.md`
- UX: `repo/services/tasks/modules/ux/boss/settings/ux-new-boss-platform-admin.md`
- SPEC: `repo/services/tasks/modules/spec/boss/settings/spec-new-boss-platform-admin.md`

## Description
打通平台运营账号的 **Services 侧链路**（网关 → platform-settings-service → Core SDK），替换 #2 留下的全部 501/Unimplemented 占位中的**链路基建**部分：

- **网关 → Service 链路（全量一次到位）**：新建 `platform_admin_resources.go`，在 `/api/v1/svc` 组注册 `/platform-admins*` 全部 10 条路由（8 个 path 模板，对齐 Services OpenAPI §4.2），gRPC 转发到 platform-settings-service（`PLATFORM_SETTINGS_SERVICE_ADDR` 缺省 `127.0.0.1:9106`），模式对齐 `tenant_plans.go` 先例。转发是纯机械工作，由本 issue 一次完成，避免 #5–#10 逐个改 `router.go`。
- **Service → Core 链路（SDK 适配，全量）**：`platform_user_client.go` 全部方法替换占位为 anisdk 薄封装（SDK 已含 8 个 PlatformUser operation，**无需重新生成**），附共享基建（`mapSDKError` 哨兵分派、游标参数、解码 helper）。
- **Service 层装配 + 错误映射基建 + 一个示例端点**：构造器注入 `CorePlatformUserClient` + `PlatformAdminAuditStore`，落地哨兵→gRPC code 统一映射 helper；以 `ListPlatformAdmins`（只读、无审计/幂等）作为**端到端链路验证示例**。**其余 9 个 RPC 的业务实现（校验、审计写入、响应组装）留在 #5–#10 各端点 issue**，本 issue 交付后它们只填 RPC body，不再碰网关与 SDK 层。

> **范围调整（2026-08-31）**：原 #4 为 Core 层实现（`platform_user_resources.go` handler + `PostgresPlatformUserAdminStore` 全部 SQL）。按用户指示，Core 层实现**推迟到后续 issue**——Core `/admin/platform-users/*` 端点暂不落地。本 issue 完成后 Services 全链路可编译、可联调，SDK 调 Core 会得到 404/502（Core 未实现该路径），经 `mapSDKError` 兜底为可读的 `CORE_UNAVAILABLE`。
>
> **pkg 占位追认（2026-08-31 review）**：实现过程中对 `repo/pkg` 占位做了**非功能性整理**并经 review 追认：
> - `pkg/ports/errors.go`：补齐 6 个平台账号哨兵（`ErrPlatformUserNotFound` / `ErrRoleNotFound` / `ErrEmailAlreadyExists` / `ErrUsernameAlreadyExists` / `ErrLastPlatformAdmin` / `ErrValidationFailed`）——services 侧 `mapDomainError` 需与 pkg 既有哨兵（`ErrRoleChangeInvalid` / `ErrPasswordSameAsOld`）同源，两侧哨兵语义对齐
> - `pkg/adapters/postgres/platform_user_admin.go` → `pkg/adapters/runtime/platform_user_admin.go`：占位随 runtime 包既有惯例归位（对齐 `PostgresUserAdmin` 等先例），构造器改为 `MetadataStore` 注入，**方法仍全部 `ErrNotImplemented`**，SQL 落地仍归后续 Core issue
> - `pkg/ports/platform_user_admin.go`：仅 doc 注释增补（REST surface 清单 + 各方法哨兵行为），接口签名零改动
>
> 以上改动 `pkg go build ./...` 干净、`make validate-architecture` 通过（runtime 目录为既有 bounded 白名单目录）。

## Scope
- Product line: services（网关转发 + Services 装配/错误映射 + Core SDK 适配；Core 层不动；各端点 RPC 业务实现不动）
- Code paths allowed:
  - `repo/services/ani-gateway/internal/router/`（新增 `platform_admin_resources.go` + `router.go` 注册）
  - `repo/services/platform-settings-service/main.go`（装配注入）
  - `repo/services/platform-settings-service/internal/service/platform_admin_service.go`（装配 + List 示例 + 错误映射 helper；其余 RPC 保持占位）
  - `repo/services/platform-settings-service/internal/repo/adapters/core/`
  - `repo/services/platform-settings-service/internal/repo/ports/`（errors 哨兵 + **审计 store 端口**：`PlatformAdminAuditStore{CreateAudit, ListUserAuditLogs}`，替代原规划的通用 `PlatformAdminStore`）
  - `repo/services/platform-settings-service/internal/repo/adapters/postgres/platform_admin_audit_store.go`（**专用审计 store**：复用 `audit_logs` 分区表，平台级 `tenant_id IS NULL` + `resource='platform_user'` + `details->>'target_id'` 关联，`types.EncodeCursor/DecodeCursor` 游标分页）
  - `repo/services/platform-settings-service/go.mod`（+`ani-sdks/core-go` 直接依赖，replace `../../sdks/core/go`，对齐 tenant-service 模式）
  - `repo/api/openapi/services/v1.yaml`（路由落地后移除 `services-route-baseline.yaml` 的 10 条 `/platform-admins*` `spec_not_in_code` 例外）
  - `repo/architecture/` + `repo/scripts/`（allowlist 增补 audit store、route baseline 例外移除、`validate_services_boundary.py` 的 `SERVICES_OWNED_SOURCE_ROOTS` 增加 platform-settings-service）
  - `repo/pkg/ports/errors.go`、`repo/pkg/ports/platform_user_admin.go`、`repo/pkg/adapters/runtime/platform_user_admin.go`（占位非功能性整理，见 Description 追认注记）
- 不触碰：`repo/api/proto/`、`repo/sdks/core/`（SDK 已齐备）、users/roles/user_roles 表的任何 SQL

## Acceptance Criteria

### A. 网关全量转发（ani-gateway）—— 网关→Service 链路
- [ ] 新建 `internal/router/platform_admin_resources.go`，模式对齐 `tenant_plans.go`：`newPlatformAdminsAPI()` 自包含读 `PLATFORM_SETTINGS_SERVICE_ADDR`（缺省 `127.0.0.1:9106`），`grpc.NewClient(addr, insecure)`，conn 失败返回空 struct 由各 handler nil 守卫兜底 502 `GRPC_CLIENT_UNAVAILABLE`
- [ ] 每方法 5s 调用超时（`platformAdminCallTimeout`），`platformAdminCallCtx` 注入超时 + `x-request-id` / `x-user-id` metadata（复用 tenantCallCtx 模式）
- [ ] 路由注册（`router.go` 的 `svc` 组内调用 `registerPlatformAdmins(svc)`），路径参数用 `{userId}` 命名与 Services OpenAPI 一致：
  - `POST /platform-admins` → `CreatePlatformAdmin`
  - `GET /platform-admins` → `ListPlatformAdmins`
  - `GET /platform-admins/roles` → `ListPlatformAdminRoles`
  - `GET /platform-admins/:userId` → `GetPlatformAdmin`
  - `PUT /platform-admins/:userId/role` → `UpdatePlatformAdminRole`
  - `POST /platform-admins/:userId/reset-password` → `ResetPlatformAdminPassword`
  - `POST /platform-admins/:userId/disable` → `DisablePlatformAdmin`
  - `POST /platform-admins/:userId/enable` → `EnablePlatformAdmin`
  - `DELETE /platform-admins/:userId` → `DeletePlatformAdmin`
  - `GET /platform-admins/:userId/audit-logs` → `ListPlatformAdminAuditLogs`
- [ ] **静态段先于参数段注册**：`/platform-admins/roles` 注册在 `GET /platform-admins/:userId` 之前（Hertz 路由树静态节点优先，与 #7 的 Core 侧注册顺序同理）
- [ ] 错误映射 `mapPlatformAdminError`：业务码最长前缀匹配 `"<CODE>: detail"` 还原 HTTP 状态，未命中再按 gRPC code 兜底（模式与 `mapTenantPlanError` 一致）；业务码 → HTTP 全集对齐 SPEC §6.1：`VALIDATION_FAILED` 400 / `PLATFORM_USER_NOT_FOUND` `ROLE_NOT_FOUND` 404 / `EMAIL_ALREADY_EXISTS` `USERNAME_ALREADY_EXISTS` 409 / `LAST_PLATFORM_ADMIN` `PASSWORD_SAME_AS_OLD` `ROLE_CHANGE_INVALID` 422 / `CORE_UNAVAILABLE` 502 / `NOT_IMPLEMENTED`（Unimplemented 兜底）501
- [ ] 响应组装为 `map[string]any` JSON（避免 protobuf Timestamp/omitempty 序列化问题）；游标分页参数 limit 默认 20 上限 100（复用 `cursorLimit`）
- [ ] 幂等：写操作（POST/PUT/DELETE）body 含 `idempotency_key`，由现有 `Idempotency(store)` 中间件按 header/body 键去重，handler 不重复实现
- [ ] RBAC 走现有链路：`inferPermission` 对 `/svc/platform-admins/*` 推导 resource=`platform-admins`，action 按 method（GET→get / POST→create / PUT→update / DELETE→delete），经 `authorizeLegacy` → auth-service `CheckPermission`（roles 命中 `platform-admin` 短路放行；ops/readonly 由 `roles.permissions` 的 `users` resource 条目决定——**当前种子 platform-ops/readonly 未授予 `users` resource，平台账号管理仅 platform-admin 可用**，与 PRD §3 角色定位一致）

### B. Service 层装配 + 示例端点（platform-settings-service）—— Service 侧链路骨架
- [ ] `platform_admin_service.go`：`PlatformAdminService` 增加字段 `coreClient ports.CorePlatformUserClient`、`auditStore ports.PlatformAdminAuditStore`；`NewPlatformAdminService(coreClient, auditStore)` 构造器注入（对齐 tenant-service `NewTenantPlanService` 模式），直接改 `main.go` 装配（不留无参兼容构造）
- [ ] 审计 store（ports + postgres）：`PlatformAdminAuditStore{CreateAudit, ListUserAuditLogs}` + `NewPostgresPlatformAdminAuditStore(deps.DB)`——复用 `audit_logs` 分区表（平台级 `tenant_id IS NULL`、`resource='platform_user'`、`details->>'target_id'` 关联目标账号），游标分页用 `types.EncodeCursor/DecodeCursor`（created_at DESC, id DESC，limit+1 判下一页）；audit 写入方法调用留给 #5/#8/#9/#10，#11 复用 `ListUserAuditLogs`
- [ ] 哨兵错误 → gRPC code 统一映射 helper（全部 RPC 复用）：`ErrPlatformUserNotFound`/`ErrRoleNotFound` → `codes.NotFound`；`ErrEmailAlreadyExists`/`ErrUsernameAlreadyExists` → `codes.AlreadyExists`；`ErrLastPlatformAdmin`/`ErrPasswordSameAsOld`/`ErrRoleChangeInvalid`/`ErrValidationFailed` → `codes.InvalidArgument` 或 `FailedPrecondition`（选定后与网关映射表一致即可）；`ErrCoreUnavailable` → `codes.Unavailable`；message 统一为 `"<BUSINESS_CODE>: <detail>"` 格式（对齐 tenant-service 约定，网关可前缀还原）
- [ ] **示例端点**：`ListPlatformAdmins` RPC 端到端实现——limit/cursor/role/status/source/search 全量透传 `coreClient.List`，结果经映射 helper 返回；其余 9 个 RPC **保持 `unimplemented()` 占位**（业务校验、审计写入、响应组装归 #5–#10 各端点 issue）

### C. Core SDK 适配层（platform-settings-service internal repo）—— Service→Core 链路
- [ ] `errors.go` 新增哨兵（现有仅 `ErrNotImplemented`）：`ErrPlatformUserNotFound` `ErrEmailAlreadyExists` `ErrUsernameAlreadyExists` `ErrLastPlatformAdmin` `ErrPasswordSameAsOld` `ErrRoleChangeInvalid` `ErrRoleNotFound` `ErrValidationFailed` `ErrCoreUnavailable`
- [ ] `platform_user_client.go` 全部方法替换占位为 anisdk 薄封装，模式对齐 tenant-service `sdk_client.go`：
  - `init()` 设 `http.DefaultClient.Timeout = 10s`（SDK 用 DefaultClient 且无 ctx，防 Core 挂起阻塞 handler goroutine）
  - `newCoreSDKClient()`：`CORE_API_BASE_URL` 缺省 `http://127.0.0.1:8080/api/v1` + `CORE_API_TOKEN` Bearer
  - 写操作（Create/ChangeRole/ResetPassword/SetStatus/SoftDelete）：幂等键透传**归 #5–#10 各端点 issue**（经 SDK `RequestOptions.Headers` 携带 `Idempotency-Key`；SDK 的 `IdempotencyOperations` 已含全部 5 个写 operation）——网关侧 `writeIdempotentUserAction` 已提取 body/header 幂等键透传 gRPC，断点仅在适配层；Core 未落地前无重复执行风险
  - `List`：`CursorPaginationOperations` 含 `listPlatformUsers`，limit/cursor/role/status/source/search 全部经 query params
  - `mapSDKError(err)`：`errors.As(err, &anisdk.APIError)` 后按 `apiErr.Code` 分派到哨兵（`PLATFORM_USER_NOT_FOUND`→ErrPlatformUserNotFound 等，见 SPEC §6.1 全集）；网络错误/非 APIError → `ErrCoreUnavailable` 包裹；**Core 端点未实现返回的 404/501 也归入 ErrCoreUnavailable 路径**（Core 层推迟实现的过渡期语义）
  - 解码：`asObject`/`asObjectSlice`/`stringField`/`timeField` 辅助函数（对齐 tenant-service 先例）；`source` 直接取 Core 响应字段，未返回时置空（`oidc:`/`local:` 前缀推断属 Core 层实现，随推迟议题处理）
  - `ListPlatformRoles`：**方法壳保持占位**——SDK 现仅含 8 个基础 operation，roles 查询 operation 由 #7 增补后启用（届时若 SDK 需 `make gen-core-sdk`，归 #7）；本 issue 仅登记 TODO，不引入编译依赖
- [ ] 其余 7 方法（List/Create/Get/ChangeRole/ResetPassword/SetStatus/SoftDelete）全部真实封装；SetStatus 对应 disable/enable 两个 SDK operation

### D. 测试 + 门禁
- [ ] Core 适配层单测（httptest 假 Core）：SDK 请求路径/方法/幂等头断言；`mapSDKError` 业务码全分支；404（Core 未实现）→ ErrCoreUnavailable；List 游标/过滤参数透传断言
- [ ] Services 单测（fake CorePlatformUserClient）：ListPlatformAdmins 成功路径 + 参数透传；哨兵→gRPC code 映射表驱动测试；未实现 RPC 返回 Unimplemented 断言
- [ ] 网关 handler 单测：gRPC stub 各端点转发参数拼装正确；`mapPlatformAdminError` 业务码→HTTP 状态全表驱动；静态/参数路由不冲突（`/roles` 与 `/{userId}`）
- [ ] 集成冒烟（Core 未实现时的过渡期验证）：gateway + platform-settings-service 起服务——`GET /api/v1/svc/platform-admins` 全链路返回 502 `CORE_UNAVAILABLE`（可读业务码，而非裸 500，证明转发链路本身正确）；其余端点（如 `POST /api/v1/svc/platform-admins`）返回 501 `NOT_IMPLEMENTED`（占位 RPC 经网关正常透出）
- [ ] `make test`、`make validate-architecture` 通过（边界校验：Services 不 SQL 操作 users/roles/user_roles——本 issue 不触碰这些表）

## Dependencies
#1 (OpenAPI 契约), #2 (端口+数据模型+服务骨架), #3 (角色种子，联调用)

## Type
backend

## Priority
high

## References
- SPEC: §2.1 三层数据流向 / §2.3 Component Design / §4.2 Services Endpoints / §5.2 Services 层算法 / §6.1 Error Taxonomy
- 先例: [tenant_plans.go](../../../../../../ani-gateway/internal/router/tenant_plans.go)（网关 gRPC 转发模式）、[sdk_client.go](../../../../../../tenant-service/internal/repo/adapters/core/sdk_client.go)（Services→Core SDK 模式）
- SDK: `repo/sdks/core/go/anisdk/client.go` 已含全部 8 个 PlatformUser operation + IdempotencyOperations + CursorPaginationOperations（无需 `make gen-core-sdk`）

## Blocks
#5 (创建), #6 (列表/详情), #8 (角色), #9 (禁用/启用/删除), #10 (重置密码), #11 (操作历史)

**分工边界**（本 issue 合入后，各端点 issue 的剩余工作收敛为 Service 层 RPC body）：
- #5/#8/#9/#10：各自 RPC 的业务校验 + `auditStore.CreateAudit` 审计写入 + 响应组装 + 端点测试 + **SDK 写操作幂等键透传**（网关转发与 SDK 方法已由本 issue 提供，**其 issue 文件中「网关转发 + SDK client 方法」相关 AC 需相应收敛为引用本 issue**）；**#5 还需重生 pb**——proto 已改 `repeated google.protobuf.Struct permissions`，生成 pb 仍为旧 4 维 `PlatformRolePermissions`，roles 落地时 `make gen-proto` 后同步修正网关 `platformRoleJSON` 映射形状（现按旧形状映射，roles RPC 当前 501 无实际影响）
- #6：`GetPlatformAdmin` 详情实现；`ListPlatformAdmins` 主链路已由本 issue 打通，#6 补齐边界用例与前端对接校验
- #11：`ListPlatformAdminAuditLogs` 直查 DB（不走 Core），复用本 issue 落好的 `ListUserAuditLogs` store 方法 + 网关路由 + 服务装配
- **Core 层 handler + Store SQL 整体推迟为独立 issue**（建议新增 issue-core-platform-user，含 #7 的两个 Core 查询端点，Core 合入前本链路对 Core 调用统一返回 `CORE_UNAVAILABLE`）
