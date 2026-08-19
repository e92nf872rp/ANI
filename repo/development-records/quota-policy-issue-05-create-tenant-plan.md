# QUOTA-POLICY-ISSUE-05：CreateTenantPlan 服务实现 — 维度校验 + 事务一致性 + 审计

> **批次类型：** Feature batch（BOSS 租户配额套餐功能流 Issue #5）
> **完成日期：** 2026-08-11
> **Scope：** `repo/services/tenant-service/internal/service/tenant_plan_service.go`、`repo/services/tenant-service/internal/repo/adapters/postgres/tenant_plan_store.go`、`repo/services/tenant-service/internal/repo/adapters/core/quota_svc_client.go`、`repo/services/tenant-service/internal/repo/ports/`
> **依赖：** #2 gRPC 接口与 ports、#4 网关接入
> **Product line：** boss

## 交付内容

实现 `CreateTenantPlan` gRPC 方法的完整业务逻辑：基础校验 → Core 维度校验 → 事务内写入套餐 + 限额 + 审计。同时实现 Core `QuotaSvcClient.ListQuotaMeta` 客户端和 store 层 `Create` 方法。

### CreateTenantPlan 服务实现

- **基础校验：** code/name 非空，返回 `VALIDATION_FAILED`。
- **维度校验（事务外）：** `mapAndValidateQuotaLimits` 调 `QuotaSvcClient.ListQuotaMeta` 获取已注册维度，校验 resource_type 已注册、total 非负、无重复维度，total 为 nil 时用 `default_quota` 兜底为具体值。校验放事务外避免占 DB 连接等 Core。
- **事务内写入：** `WithinTx` 内先 `store.Create` 写套餐 + 限额，再 `auditStore.Create` 写审计日志，审计失败回滚套餐写入。
- **错误映射：** `mapStoreError` 把 store 错误映射为带业务码前缀的 gRPC status（unique violation → `PLAN_CODE_CONFLICT`，FK violation → `QUOTA_RESOURCE_NOT_REGISTERED`）。
- **businessError 统一格式：** `status.Error(code, "CODE: detail")`，网关按 `CODE:` 前缀解析。

### Core QuotaSvcClient.ListQuotaMeta 实现

- HTTP GET `{baseURL}/admin/quota-meta`，解析 Core 返回的 5 字段（resource_type/display_name/unit/default_quota/is_discrete）。
- 非 2xx / 网络错误 / 解析错误统一包装 `ErrCoreUnavailable`。
- 无本地缓存，每次远程调用（配额元数据变更需即时生效）。
- Core 不返回 `enabled` 字段，客户端统一标记 `Enabled=true`。

### Store 层 Create 实现

- `PostgresTenantPlanStore.Create` 使用 `querierFrom(ctx, s.db)` 正确参与外层事务。
- `description` 用 `NULLIF($3, '')` 处理空串。
- unique violation → `ErrPlanCodeConflict`，FK violation → `ErrQuotaResourceNotRegistered`。

### request_id 读取

- `requestIDFromCtx` 从 gRPC metadata 读取 `x-request-id`，供审计日志 request_id 字段。

### 测试覆盖

- service 边界测试 10 例：正常创建、total 兜底、request_id 透传、code 冲突、维度未注册、Core 不可用、审计一致性（回滚）、重复维度、负数 total、空 quota_limits 跳过 Core。
- Core 客户端测试 3 例：正常 fetch + 无缓存验证 + 不可用场景。

## Acceptance Criteria 验证

| AC | 证据 | 结果 |
|---|---|---|
| CreateTenantPlan 实现 | service 层完整业务逻辑 | ✅ |
| 维度校验用 SDK | `mapAndValidateQuotaLimits` 调 ListQuotaMeta | ✅ |
| total 兜底 default_quota | `it.Total == nil` 时取 meta.DefaultQuota | ✅ |
| 维度校验放事务外 | `mapAndValidateQuotaLimits` 在 `WithinTx` 前 | ✅ |
| 事务一致性 | create + audit 同事务，审计失败回滚 | ✅ |
| businessError 格式 | `CODE: detail`，网关可解析 | ✅ |
| mapStoreError 映射 | unique → CONFLICT，FK → NOT_REGISTERED | ✅ |
| ListQuotaMeta 实现 | HTTP GET + 5 字段解析 + ErrCoreUnavailable | ✅ |
| ListQuotaMeta 无缓存 | 每次远程调用，测试验证 | ✅ |
| request_id 读取 | `requestIDFromCtx` 读 metadata | ✅ |
| 编译 | `go build ./services/tenant-service/...` → EXIT=0 | ✅ |
| 测试 | `go test ./services/tenant-service/internal/...` → PASS | ✅ |
| review-it | clean，无 actionable findings | ✅ |

## 验证命令

```bash
cd repo
go build ./services/tenant-service/...
go test ./services/tenant-service/internal/...
```

## 边界声明

- 本 Issue 仅实现 `CreateTenantPlan` + `ListQuotaMeta`，其余 RPC（Get/List/Activate/Disable/Delete/Update/Bind）仍为 panic 占位，属后续 Issue。
- `QuotaSvcClient.PutQuota` 仍为 panic 占位（issue-008/009 绑定套餐时需实现）。
- Core `/admin/quota-meta` 端点需 Core 服务侧已实现并可达，否则返回 `GRPC_CLIENT_UNAVAILABLE`。

## Open Questions

1. **PutQuota 占位：** `QuotaSvcClient.PutQuota` 方法体 `panic("not implemented: issue-008")`，绑定套餐时需先完成。
2. **QuotaMeta enabled 字段：** Core API 仅返回 enabled=true 的维度且 schema 无此字段，客户端统一标记 `Enabled=true`。待确认 Core 是否应返回 enabled 字段以支持禁用维度过滤。
