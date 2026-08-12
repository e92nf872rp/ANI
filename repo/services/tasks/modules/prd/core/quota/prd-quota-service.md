# PRD: 配额服务落地（扣减 + 配置查询 + 租户生命周期管理）

> 来源计划: `repo/services/tasks/modules/plan/plan-quota-service.md`
> 前置文档: `通用资源配额与计量落地方案.md` §4.2、§5；`core-quota-port-contract.md`（嘉明契约）；`core-quota-api.md`（李宇需求）
> 范围确认: 三部分全覆盖（QuotaService 扣减 + QuotaStoreService 配置查询 + QuotaAdminService 租户生命周期管理 + Core API 契约 + handler + 鉴权扩展 + SDK 生成 + 测试）

---

## 1. Introduction/Overview

ANI 平台需要一个完整的配额服务来管理多租户资源配额，覆盖 GPU、CPU、内存、存储、Token、KB 查询、成员、推理服务 8 个维度。当前配额能力散落在 `tenants` 表旧字段（如 `max_gpu_count`），缺乏统一的 TCC（Try-Confirm-Cancel）预占/实扣状态机和租户生命周期管理。

本 PRD 落地完整的配额服务，包含三部分能力，作为一个任务交付：

- **扣减（QuotaService）**：Try / TryMany / Confirm / Cancel / Release + PG adapter，实现 TCC 预占/实扣状态机，保证并发不超卖。调用方：demo_instances 创建实例、嘉明 reconciler
- **配置查询（QuotaStoreService）**：Put（UPSERT total）/ List（分页查）/ GetMy（自查）/ GetTotalForUpdateTx（FOR UPDATE 锁行查 total）+ PG adapter。调用方：嘉明 BOSS/Console handler、GPU 预留校验 handler
- **租户生命周期管理（QuotaAdminService）**：CreateTenantQuota / UpdateTenantQuota / GetTenantQuota / DeleteTenantQuota / ListQuotaMeta + PG adapter + Core API 契约 `/admin/tenants/{id}/quota` + `/admin/quota-meta` + handler + Core SDK 生成。调用方：李宇 tenant-service 经 SDK 调 REST

三部分操作同一组表（`resource_quota` / `resource_quota_meta` / `resource_reservations`），但语义、调用方、事务模型不同，因此拆成三个 port，在同一个任务里交付。一个 PG adapter struct 实现三个 interface，共享表结构和测试基础设施。

**设计原则**：契约先行（先改 `v1.yaml`，再写实现）；port 接口隔离（三个 interface 各司其职）；一个 adapter struct 实现三个 interface（共享表结构）；TCC 状态机保证并发安全和幂等；RLS 双 policy（platform_bypass + self）支持平台管理员和租户两种上下文。

---

## 2. Goals

- 实现 `QuotaService` 接口（Try/TryMany/Confirm/Cancel/Release）+ PG adapter，保证并发不超卖、Confirm/Cancel/Release 幂等
- 实现 `QuotaStoreService` 接口（Put/List/GetMy/GetTotalForUpdateTx）+ PG adapter，支持 BOSS 运营配额管理和 GPU 预留校验锁行查询
- 实现 `QuotaAdminService` 接口（CreateTenantQuota/UpdateTenantQuota/GetTenantQuota/DeleteTenantQuota/ListQuotaMeta）+ PG adapter，支持租户生命周期管理
- 新增 Core API 契约：`/admin/tenants/{tenant_id}/quota` 4 个端点 + `/admin/quota-meta` 1 个端点（契约先行）
- 实现 5 个 Core API handler + 扩展鉴权 `scopeAllowedForPath` 放行 `/api/v1/admin/*`
- 重新生成 Core SDK，新增 quotas operation
- 通过 `make test`、`make validate-architecture`、`make validate-services`、`make gen-core-sdk && git diff --check -- sdks/core`、`git diff --check`
- 集成测试覆盖 RLS 双角色（管理员 bypass + 租户隔离）验证，含扣减/配置查询/管理全场景

---

## 3. User Stories

### US-001: 新增 Port 契约定义（三个 interface + 哨兵错误）
**Description:** 作为开发者，我需要先定义三个 port interface 和相关类型/哨兵错误，作为 adapter 和 handler 的契约边界。

**Acceptance Criteria:**
- [ ] 新增 `pkg/ports/quota.go`，定义 `QuotaService` interface（Try/TryMany/Confirm/Cancel/Release）+ `QuotaStoreService` interface（Put/List/GetMy/GetTotalForUpdateTx）+ `ResourceType` 常量（8 维度）+ `QuotaTryRequest`/`QuotaReservation`/`QuotaView`/`QuotaPutRequest`/`QuotaListRequest`/`QuotaListResult` 类型
- [ ] 新增 `pkg/ports/quota_admin.go`，定义 `QuotaAdminService` interface（CreateTenantQuota/UpdateTenantQuota/GetTenantQuota/DeleteTenantQuota/ListQuotaMeta）+ `QuotaItemInput`/`QuotaItemUpdate`/`QuotaMeta`/`QuotaInfo` 类型
- [ ] 修改 `pkg/ports/errors.go`，追加哨兵错误：`ErrQuotaExceeded`、`ErrQuotaResourceNotRegistered`、`ErrQuotaIdempotencyConflict`、`ErrQuotaNotFound`、`ErrQuotaAlreadyExists`（`ErrTenantNotFound` 复用已有定义）
- [ ] `QuotaService.Confirm/Cancel/Release` 接收外部 `MetadataTx`，不在 port 内自开事务
- [ ] `QuotaStoreService.GetTotalForUpdateTx` 接收外部 `MetadataTx`，返回 int64 total
- [ ] Typecheck/lint 通过

### US-002: 实现 QuotaService 扣减 adapter（Try/TryMany/Confirm/Cancel/Release）
**Description:** 作为开发者，我需要实现 `QuotaService` 的 PG adapter，包含 TCC 预占/实扣状态机，保证并发不超卖和幂等。

**Acceptance Criteria:**
- [ ] 新增 `pkg/adapters/quota/postgres_quota.go`，定义 `PostgresQuota` struct（持有 `ports.MetadataStore`）+ `NewPostgresQuota` 构造函数 + 编译期接口断言（三个 interface）
- [ ] `tryInTx` 内部方法：校验 meta enabled → lazy init（ON CONFLICT DO NOTHING）→ 单行原子 UPDATE（WHERE 余量校验）→ 插入预占流水返回 tx_id
- [ ] `Try`：自开 `WithTenantTx`，单维度预占，返回 `QuotaReservation`
- [ ] `TryMany`：自开 `WithTenantTx`，单事务内循环 `tryInTx`，任一失败则事务回滚无悬挂预占；校验所有 req 的 tenant_id 一致
- [ ] `Confirm`：接收外部 tx，流水 reserved→confirmed（WHERE state='reserved' 守卫幂等）+ reserved→used 转账
- [ ] `Cancel`：接收外部 tx，流水 reserved→cancelled（WHERE state='reserved' 守卫幂等）+ 释放 reserved
- [ ] `Release`：接收外部 tx，流水 confirmed→released（WHERE state='confirmed' 守卫幂等）+ used 减回
- [ ] Confirm/Cancel/Release 对已终态流水（pgx.ErrNoRows）continue 跳过，不重复扣减
- [ ] Typecheck/lint 通过

### US-003: 实现 QuotaStoreService 配置查询 adapter（Put/List/GetMy/GetTotalForUpdateTx）
**Description:** 作为开发者，我需要实现 `QuotaStoreService` 的 PG adapter，支持 BOSS 运营配额管理、Console 自查和 GPU 预留校验锁行查询。

**Acceptance Criteria:**
- [ ] `Put`：自开 `WithPlatformTx`，UPSERT 覆盖 total（不 clamp，撞 CHECK 报错透传）；校验 meta enabled；回读所有维度返回 `QuotaView`
- [ ] `List`：自开 `WithPlatformTx`，无 tenant_id 时按租户级 keyset 分页（cursor=tenant_id），有 tenant_id 时直接调 GetMy 不分页；分页 limit 默认 50、上限 100；多查 1 条判断 hasMore
- [ ] `GetMy`：自开 `WithTenantTx`，RLS 自动过滤只看本租户，返回 `QuotaView`
- [ ] `GetTotalForUpdateTx`：接收外部 tx，`SELECT total ... FOR UPDATE` 锁行，行不存在返回 `ErrQuotaNotFound`
- [ ] `Put` 不做 GREATEST clamp，total < used+reserved 撞 CHECK 约束时透传错误
- [ ] Typecheck/lint 通过

### US-004: 实现 QuotaAdminService 租户生命周期管理 adapter
**Description:** 作为开发者，我需要实现 `QuotaAdminService` 的 PG adapter，支持批量新建/修改/查询/删除租户配额和查询配额元数据。

**Acceptance Criteria:**
- [ ] `CreateTenantQuota`：自开 `WithPlatformTx`，校验租户存在（tenants 表）+ meta enabled → total<=0 取 default_quota → ON CONFLICT DO NOTHING 跳过已存在 → 回读 items 涉及维度
- [ ] `UpdateTenantQuota`：自开 `WithPlatformTx`，校验 meta enabled → `SET total = GREATEST($3, reserved + used)` 缩容 clamp → 行不存在返回 `ErrQuotaNotFound` → 回读计算 tightened 标记
- [ ] `GetTenantQuota`：自开 `WithPlatformTx`，JOIN resource_quota_meta 返回 unit/display_name/is_discrete
- [ ] `DeleteTenantQuota`：自开 `WithPlatformTx`，校验租户存在 → 删除 resource_reservations + resource_quota（不守卫 used/reserved）
- [ ] `ListQuotaMeta`：自开 `WithPlatformTx`，返回 enabled=true 的维度列表（含 display_name/unit/default_quota/is_discrete），ORDER BY resource_type
- [ ] Typecheck/lint 通过

### US-005: 新增 Core API 契约（v1.yaml 5 个端点 + schema）
**Description:** 作为开发者，我需要先改 Core API 契约，新增 `/admin/tenants/{tenant_id}/quota` 4 个端点 + `/admin/quota-meta` 1 个端点 + 相关 schema，遵循契约先行原则。

**Acceptance Criteria:**
- [ ] `repo/api/openapi/v1.yaml` 新增 5 个端点：POST/PUT/GET/DELETE `/admin/tenants/{tenant_id}/quota` + GET `/admin/quota-meta`
- [ ] 新增 schema：`QuotaCreateRequest`/`QuotaCreateItem`/`QuotaUpdateRequest`/`QuotaUpdateItem`/`Quota`/`QuotaItem`/`QuotaDeleteResponse`/`QuotaMetaListResponse`/`QuotaMeta`
- [ ] POST/PUT/DELETE 支持 `idempotency_key` header
- [ ] `QuotaItem` 包含 resource_type/total/used/reserved/tightened/unit/display_name/is_discrete 字段
- [ ] 错误码对齐 core-quota-api.md §5：TENANT_NOT_FOUND(404)/QUOTA_NOT_FOUND(404)/QUOTA_ALREADY_EXISTS(409)/QUOTA_RESOURCE_NOT_REGISTERED(422)/VALIDATION_FAILED(400)
- [ ] 不删除/不修改现有端点和 schema（兼容性）
- [ ] `python scripts/validate_yaml.py api/openapi/v1.yaml` 通过

### US-006: 实现 QuotaAdminService 5 个 Core API handler
**Description:** 作为开发者，我需要实现 5 个 Core API handler，持有 `QuotaAdminService` 接口，照搬 demo_instances.go 模式。

**Acceptance Criteria:**
- [ ] 新增 `repo/services/ani-gateway/internal/router/quota_resources.go`，定义 `quotaAPI` struct + `registerQuotaResources` 函数注册 5 个路由
- [ ] `createTenantQuota`：解析 `QuotaCreateRequest` → 调 `CreateTenantQuota` → 响应 `Quota`(200) 或错误码
- [ ] `updateTenantQuota`：解析 `QuotaUpdateRequest` → 调 `UpdateTenantQuota` → 响应 `Quota`(200)，保留 tightened 字段
- [ ] `getTenantQuota`：调 `GetTenantQuota` → 响应 `Quota`(200) 或 TENANT_NOT_FOUND(404)，tightened 用 omitempty 省略
- [ ] `deleteTenantQuota`：调 `DeleteTenantQuota` → 响应 `QuotaDeleteResponse`(200) 或 TENANT_NOT_FOUND(404)
- [ ] `listQuotaMeta`：调 `ListQuotaMeta` → 响应 `QuotaMetaListResponse`(200)
- [ ] 错误统一用 `writeDemoError` 三段式 + `middleware.GetRequestID(c)`
- [ ] tenant_id 全部从路径参数 `c.Param("tenant_id")` 取
- [ ] 哨兵错误映射：ErrTenantNotFound→404/TENANT_NOT_FOUND、ErrQuotaNotFound→404/QUOTA_NOT_FOUND、ErrQuotaResourceNotRegistered→422/QUOTA_RESOURCE_NOT_REGISTERED、ErrQuotaAlreadyExists→409/QUOTA_ALREADY_EXISTS
- [ ] Typecheck/lint 通过

### US-007: 扩展鉴权放行 /api/v1/admin/* + router 接线
**Description:** 作为开发者，我需要扩展 `scopeAllowedForPath` 放行 `/api/v1/admin/*` 路径要求 platform scope，并在 router 注册 QuotaAdminService。

**Acceptance Criteria:**
- [ ] 修改 `repo/services/ani-gateway/internal/middleware/auth.go` 的 `scopeAllowedForPath`，新增 `/api/v1/admin/` 前缀放行 platform scope
- [ ] 修改 `repo/services/ani-gateway/internal/router/router.go` 的 `RegisterOptions` 新增 `QuotaAdminService ports.QuotaAdminService` 字段
- [ ] `RegisterWithOptions` 新增 `registerQuotaResources(v1, options.QuotaAdminService)` 调用
- [ ] 调研确认无现有 `/api/v1/admin/` 路由被误伤
- [ ] Typecheck/lint 通过

### US-008: 重新生成 Core SDK
**Description:** 作为开发者，我需要在契约改完后重新生成 Core SDK，确保新增 quotas operation 不漂移。

**Acceptance Criteria:**
- [ ] 执行 `make gen-core-sdk` 重新生成 `sdks/core/go/anisdk/client.go`
- [ ] 生成后 `Operations` 切片包含 createTenantQuota/updateTenantQuota/getTenantQuota/deleteTenantQuota/listQuotaMeta
- [ ] `make validate-sdk-beta` 通过（SDK 无漂移）
- [ ] `git diff --check -- sdks/core` 无空白错误

### US-009: 扣减单元测试（fake/mock）
**Description:** 作为开发者，我需要为 QuotaService 扣减 adapter 编写单元测试，覆盖成功/失败/幂等/原子性场景。

**Acceptance Criteria:**
- [ ] 新增 `pkg/adapters/quota/postgres_quota_test.go`
- [ ] 定义 `fakeMetadataTx` 实现 `ports.MetadataTx`，模拟 QueryRow/Exec 返回值
- [ ] Try 成功：meta enabled、lazy init、预占成功
- [ ] Try 失败：meta disabled → `ErrQuotaResourceNotRegistered`
- [ ] Try 失败：余量不足 → `ErrQuotaExceeded`
- [ ] TryMany 成功：多维度全占成功
- [ ] TryMany 原子性：第二维度不足 → 第一维度预占回滚（验证 fake tx rollback 调用）
- [ ] Confirm 幂等：state=reserved→confirmed，重复 Confirm→ErrNoRows→跳过
- [ ] Cancel 幂等：state=reserved→cancelled，重复 Cancel→跳过
- [ ] Release 幂等：state=confirmed→released，重复 Release→ErrNoRows→跳过
- [ ] Confirm 后 reserved 减少、used 增加
- [ ] Cancel 后 reserved 减少
- [ ] Release 后 used 减少
- [ ] Release 对非 confirmed 流水（reserved/cancelled）→ ErrNoRows→跳过，不改账本
- [ ] Typecheck/lint 通过

### US-010: 配置查询单元测试（QuotaStoreService）
**Description:** 作为开发者，我需要为 QuotaStoreService adapter 编写单元测试，覆盖 Put/List/GetMy/GetTotalForUpdateTx 场景。

**Acceptance Criteria:**
- [ ] 新增 `pkg/adapters/quota/postgres_quota_store_test.go`
- [ ] Put 新增（行不存在）→ UPSERT 建行成功
- [ ] Put 修改（行存在）→ UPSERT 覆盖 total 成功
- [ ] Put 资源未注册/enabled=false → `ErrQuotaResourceNotRegistered`
- [ ] Put total < used+reserved → 撞 CHECK 约束报错（不 clamp，透传错误）
- [ ] Put 多维度同时 PUT → 全部成功
- [ ] List 无过滤 → 按租户级分页返回，每页含完整多维度 QuotaView
- [ ] List tenant_id 过滤 → 直接返回指定租户全部维度（不分页）
- [ ] List 分页 cursor 衔接：第一页 NextCursor=末尾 tenant_id，第二页正确衔接不漏不重
- [ ] List 空表 → 返回空 items、空 cursor
- [ ] List 超过 limit 的一页 → hasMore=true，NextCursor 指向本页最后一个租户
- [ ] GetMy 返回当前租户多维度 map
- [ ] GetTotalForUpdateTx 行存在 → 返回 total
- [ ] GetTotalForUpdateTx 行不存在 → `ErrQuotaNotFound`
- [ ] Typecheck/lint 通过

### US-011: 管理单元测试（QuotaAdminService）
**Description:** 作为开发者，我需要为 QuotaAdminService adapter 编写单元测试，覆盖 Create/Update/Get/Delete/ListQuotaMeta 场景。

**Acceptance Criteria:**
- [ ] 新增 `pkg/adapters/quota/postgres_quota_admin_test.go`
- [ ] CreateTenantQuota 批量新建成功（含 total 省略取 default_quota）
- [ ] CreateTenantQuota 租户不存在 → `ErrTenantNotFound`
- [ ] CreateTenantQuota 资源未注册/enabled=false → `ErrQuotaResourceNotRegistered`
- [ ] CreateTenantQuota 已存在的维度 → ON CONFLICT DO NOTHING 跳过，其余正常创建
- [ ] CreateTenantQuota items 为空 → 校验错误
- [ ] UpdateTenantQuota 批量改 total 成功
- [ ] UpdateTenantQuota 维度行不存在 → `ErrQuotaNotFound`
- [ ] UpdateTenantQuota 资源未注册 → `ErrQuotaResourceNotRegistered`
- [ ] UpdateTenantQuota total < used（缩容）→ 成功，返回 tightened=true + 收紧后的 total
- [ ] UpdateTenantQuota total >= used+reserved → 成功，返回 tightened=false
- [ ] UpdateTenantQuota items 为空 → 校验错误
- [ ] GetTenantQuota 返回多行 + unit/display_name/is_discrete（JOIN meta）正确解析
- [ ] GetTenantQuota 租户存在但无配额行 → 返回空 items
- [ ] DeleteTenantQuota 删除成功（连同 resource_reservations 流水）
- [ ] DeleteTenantQuota 租户不存在 → `ErrTenantNotFound`
- [ ] DeleteTenantQuota used>0 时仍可删除（不守卫）
- [ ] ListQuotaMeta 返回 enabled=true 的维度列表（含 display_name/unit/default_quota/is_discrete）
- [ ] ListQuotaMeta enabled=false 的维度不返回
- [ ] ListQuotaMeta 空表 → 返回空 items
- [ ] Typecheck/lint 通过

### US-012: 集成测试（连 PG 实例，双角色验证 RLS）
**Description:** 作为开发者，我需要编写集成测试，连本地 docker-compose PG，用管理员和租户双角色连接验证 RLS 隔离和 bypass 行为。

**Acceptance Criteria:**
- [ ] 新增 `pkg/adapters/quota/integration_test.go`（`//go:build integration` build tag）
- [ ] 前置：PG 实例可用（docker-compose 本地 PG），DSN 通过 `ANI_TEST_ADMIN_DSN`、`ANI_TEST_TENANT_DSN` 环境变量覆盖
- [ ] Setup：管理员连接建三张表 + RLS policy + seed meta + GRANT 权限给 ani_app_user
- [ ] 扣减场景（验证 RLS 写权限）：租户 A Try 成功、GetMy 查自己配额、租户 A 查租户 B 配额返回 0 行、Confirm/Cancel/Release 幂等、租户 A 试图 INSERT tenant_id='B' 流水被 RLS 拒绝
- [ ] 并发 Try 不超卖：N 个租户连接并发 Try，reserved 不超过 total
- [ ] TryMany 端到端：占多维度→Confirm→验证 used；TryMany→Confirm→Release→used 归零
- [ ] 管理场景（验证 RLS bypass）：Put/List/Delete 用管理员连接 bypass RLS 成功
- [ ] CreateTenantQuota 批量新建 + 幂等（ON CONFLICT DO NOTHING 不覆盖）
- [ ] UpdateTenantQuota 改 total + 缩容（GREATEST clamp，tightened=true，Try 新建→ErrQuotaExceeded）
- [ ] GetTenantQuota JOIN meta（unit/display_name/is_discrete 正确）
- [ ] DeleteTenantQuota（resource_quota + resource_reservations 均清空）
- [ ] ListQuotaMeta 返回 enabled=true 维度列表
- [ ] SDK 端到端：启动 ani-gateway，SDK 调 POST/PUT/GET/DELETE + GET quota-meta → DB 验证
- [ ] 测试后清理数据（TRUNCATE，用管理员连接）
- [ ] 集成测试通过 `go test ./pkg/adapters/quota/ -v -run Integration -tags integration`
- [ ] 集成测试用 `//go:build integration` build tag 隔离，不阻塞默认 `make test`
- [ ] Typecheck/lint 通过

---

## 4. Functional Requirements

- FR-1: The system must define `QuotaService` interface in `pkg/ports/quota.go` with Try/TryMany/Confirm/Cancel/Release methods, where Try/TryMany self-open `WithTenantTx` and Confirm/Cancel/Release receive external `MetadataTx`
- FR-2: The system must define `QuotaStoreService` interface in `pkg/ports/quota.go` with Put/List/GetMy/GetTotalForUpdateTx methods, where Put/List self-open `WithPlatformTx`, GetMy self-open `WithTenantTx`, GetTotalForUpdateTx receives external `MetadataTx`
- FR-3: The system must define `QuotaAdminService` interface in `pkg/ports/quota_admin.go` with CreateTenantQuota/UpdateTenantQuota/GetTenantQuota/DeleteTenantQuota/ListQuotaMeta methods, all using `WithPlatformTx`
- FR-4: The system must define 8 `ResourceType` constants: `gpu_count`/`cpu_core`/`memory_gb`/`storage_gb`/`token_count`/`kb_query_count`/`member_count`/`inference_service_count`
- FR-5: The system must append sentinel errors to `pkg/ports/errors.go`: `ErrQuotaExceeded`/`ErrQuotaResourceNotRegistered`/`ErrQuotaIdempotencyConflict`/`ErrQuotaNotFound`/`ErrQuotaAlreadyExists`
- FR-6: The system must implement `PostgresQuota` struct in `pkg/adapters/quota/postgres_quota.go` that implements all three interfaces (`QuotaService` + `QuotaStoreService` + `QuotaAdminService`) with compile-time interface assertions
- FR-7: The `tryInTx` internal method must validate meta enabled, lazy init with ON CONFLICT DO NOTHING, single-row atomic UPDATE with WHERE balance check, and insert reservation returning tx_id
- FR-8: The `Try` method must self-open `WithTenantTx` and return `QuotaReservation` with TxID and ExpiresAt
- FR-9: The `TryMany` method must self-open `WithTenantTx`, loop `tryInTx` in the same transaction, and rollback all on any failure (no dangling reservations)
- FR-10: The `Confirm` method must transition reservation state from `reserved` to `confirmed` (WHERE state='reserved' guard for idempotency) and transfer reserved→used
- FR-11: The `Cancel` method must transition reservation state from `reserved` to `cancelled` (WHERE state='reserved' guard for idempotency) and release reserved
- FR-12: The `Release` method must transition reservation state from `confirmed` to `released` (WHERE state='confirmed' guard for idempotency) and decrement used
- FR-13: The `Put` method must UPSERT total (no clamp, CHECK constraint violation propagates as error) and return `QuotaView` with all dimensions
- FR-14: The `List` method must paginate by tenant-level keyset (cursor=tenant_id), default limit 50, max 100, and fetch one extra to determine hasMore
- FR-15: The `GetMy` method must use `WithTenantTx` for RLS auto-filtering to only return the current tenant's data
- FR-16: The `GetTotalForUpdateTx` method must `SELECT total ... FOR UPDATE` to lock the row and serialize concurrent reservation validation, returning `ErrQuotaNotFound` if row not found
- FR-17: The `CreateTenantQuota` method must validate tenant exists, validate meta enabled, use default_quota when total<=0, ON CONFLICT DO NOTHING for existing dimensions, and read back involved dimensions
- FR-18: The `UpdateTenantQuota` method must use `GREATEST($3, reserved + used)` to clamp total (shrink floor = current usage), return `ErrQuotaNotFound` if row not found, and calculate tightened flag
- FR-19: The `GetTenantQuota` method must JOIN resource_quota_meta to return unit/display_name/is_discrete
- FR-20: The `DeleteTenantQuota` method must validate tenant exists, delete resource_reservations + resource_quota (no used/reserved guard)
- FR-21: The `ListQuotaMeta` method must return enabled=true dimensions with display_name/unit/default_quota/is_discrete, ORDER BY resource_type
- FR-22: The system must add 5 endpoints to `repo/api/openapi/v1.yaml`: POST/PUT/GET/DELETE `/admin/tenants/{tenant_id}/quota` + GET `/admin/quota-meta`, with idempotency_key header support on POST/PUT/DELETE
- FR-23: The system must implement `quotaAPI` handler in `repo/services/ani-gateway/internal/router/quota_resources.go` with 5 route registrations
- FR-24: The system must extend `scopeAllowedForPath` in `middleware/auth.go` to allow platform scope for `/api/v1/admin/` prefix
- FR-25: The system must add `QuotaAdminService` field to `RegisterOptions` and call `registerQuotaResources` in `RegisterWithOptions`
- FR-26: The system must regenerate Core SDK via `make gen-core-sdk` to include quotas operations
- FR-27: The system must add unit tests for QuotaService covering Try success/failure, TryMany atomicity, Confirm/Cancel/Release idempotency
- FR-28: The system must add unit tests for QuotaStoreService covering Put UPSERT, List pagination, GetMy, GetTotalForUpdateTx
- FR-29: The system must add unit tests for QuotaAdminService covering Create/Update/Get/Delete/ListQuotaMeta with shrink clamp behavior
- FR-30: The system must add integration tests with dual-role PG connections (admin bypass + tenant RLS) covering deduction, configuration, and management scenarios
- FR-31: The integration tests must be isolated via `//go:build integration` build tag and not block default `make test`

---

## 5. Non-Goals (Out of Scope)

- 三张表 migration 文件提交（由李宇负责）
- TTL 孤儿预占回收 worker（后续 PR）
- `resource_quota_meta` 的注册/启用/禁用/改 default_quota（写入，后续 PR 的 QuotaMetaService）
- reconciler 接线 Confirm/Cancel（嘉明负责）
- BOSS/Console 的 `/quotas`、`/quotas/me` handler 实现（嘉明负责，本任务只实现 QuotaStoreService port + adapter）
- GPU 预留校验 handler（调 GetTotalForUpdateTx + 查 gpu_slices + 判断，嘉明负责，本任务只提供 GetTotalForUpdateTx 锁行查询）
- GPUSliceStore（CountByTenantTx / AssignToTenantTx，嘉明负责）
- WorkloadInstanceStore.UpsertStatusTx 扩展（后续 PR）
- tenants 表旧字段迁移/废弃（后续 PR）
- 租户管理 service 的独立拆分落地（后续，本方案只保证调用链可用）

---

## 6. Design Considerations (Optional)

- **三 port 分离理由**：QuotaService（扣减）、QuotaStoreService（配置查询）、QuotaAdminService（租户生命周期管理）操作同一组表，但调用方、操作语义、事务模型、UPSERT 策略都不同，放一个 interface 会臃肿并违反接口隔离；一个 `PostgresQuota` adapter struct 实现三个 interface，共享表结构和测试基础设施
- **TCC 状态机**：`reserved → confirmed`（Confirm）、`reserved → cancelled`（Cancel）、`reserved → expired`（TTL worker）、`confirmed → released`（Release）；cancelled/expired/released 均为终态
- **事务模型**：Try/TryMany 自开 `WithTenantTx`（创建前无业务行可挂载）；Confirm/Cancel/Release 接外部 tx（与 UpsertStatus + outbox 同事务原子）；Put/List 用 `WithPlatformTx`；GetMy 用 `WithTenantTx`；GetTotalForUpdateTx 接外部 tx；管理 CRUD 全用 `WithPlatformTx`
- **RLS 双 policy**：`platform_bypass`（current_setting IS NULL 放行所有行）+ `self`（tenant_id = current_setting 只看自己行）；`WithPlatformTx` 不设 tenant_id 走 bypass，`WithTenantTx` 设 tenant_id 走 self
- **缩容策略**：UpdateTenantQuota 用 `GREATEST($3, reserved + used)` 在 SQL 层 clamp，保证 total >= used+reserved 不违反 CHECK 约束；Put 不 clamp（BOSS 运营场景撞 CHECK 报错）
- **幂等**：Try 不幂等（按方案原文）；Confirm/Cancel/Release 用状态机 WHERE 子句守卫幂等；CreateTenantQuota 用 ON CONFLICT DO NOTHING

---

## 7. Technical Considerations (Optional)

- **前置依赖**：`tenants` 表已建好（resource_quota.tenant_id 外键引用）；配额三张表由李宇 migration 创建；`resource_quota_meta` seed 数据 8 维度由李宇 migration 含
- **PG 扩展**：`gen_random_uuid()` 依赖 pgcrypto extension，需确认李宇 migration 是否启用
- **CHECK 约束**：`resource_reservations.state` CHECK 必须含 5 态（reserved/confirmed/cancelled/released/expired），其中 `released` 是 Release 依赖的转移目标，不得遗漏
- **FOR UPDATE 死锁风险**：GetTotalForUpdateTx 锁 resource_quota 行串行化并发预留；嘉明 handler 需保证锁顺序：先锁 resource_quota，再操作 gpu_slices
- **SDK 自动生成覆盖**：`sdks/core/go/anisdk/client.go` 是 DO NOT EDIT 文件，改契约后必须 `make gen-core-sdk`，否则 `validate-sdk-beta` 报漂移
- **鉴权扩展影响面**：新增 `/api/v1/admin/` 前缀要求 platform scope，调研未见现有此路径路由，无误伤
- **集成测试环境**：需要本地 docker-compose PG 或远程 PG 实例，双角色连接（管理员 ani + 租户 ani_app_user）

---

## 8. Success Metrics

- 并发 Try 不超卖：N 个并发预占，reserved 不超过 total
- Confirm/Cancel/Release 幂等：重复调用不重复扣减/释放
- RLS 隔离生效：租户只能操作自己的行，平台管理员 bypass 可见所有行
- 缩容不报错：total < used 时 GREATEST clamp，不违反 CHECK 约束
- SDK 无漂移：`make gen-core-sdk && git diff --check -- sdks/core` 通过
- 集成测试全场景通过：扣减 12 场景 + 管理 11 场景

---

## 9. Open Questions

- 李宇的 migration 表结构是否与方案附录 A 的 SQL 完全一致？（adapter 以方案 §3 表结构为准，不一致则调整）
- `scopeAllowedForPath` 扩展后是否有其他 `/api/v1/admin/` 路由被误伤？（调研未见，需确认）
- 集成测试 PG 实例使用本地 docker-compose 还是远程 `10.10.1.66:30945`？

---

## 10. ANI Boundaries

| Item | Value |
|------|-------|
| Product line | core |
| Code scope | `pkg/ports/`、`pkg/adapters/quota/`、`repo/services/ani-gateway/internal/`、`repo/api/openapi/v1.yaml`、`sdks/core/go/anisdk/` |
| OpenAPI authority | Core change batch（新增 `/admin/tenants/{id}/quota` + `/admin/quota-meta` 端点） |
| Frozen exclusions | Services backend（Services 层不得 import pkg/ports/pkg/adapters，只能通过 SDK 调 Core） |
| idempotency_key | required on: POST `/admin/tenants/{tenant_id}/quota`、PUT `/admin/tenants/{tenant_id}/quota`、DELETE `/admin/tenants/{tenant_id}/quota` |
| Module main doc | N/A（纯后端 Core 服务，无 UI 模块主文档） |
