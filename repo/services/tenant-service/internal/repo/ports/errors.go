package ports

import "errors"

// 领域哨兵错误：service 层映射为 gRPC status message「CODE: detail」，
// 供网关 mapTenantPlanError 按业务码前缀还原 HTTP 状态。

var (
	// ErrPlanCodeConflict 表示 code 与未删除套餐冲突（HTTP 409）。
	ErrPlanCodeConflict = errors.New("PLAN_CODE_CONFLICT")

	// ErrQuotaResourceNotRegistered 表示 resource_type 未注册或已禁用（HTTP 422）。
	ErrQuotaResourceNotRegistered = errors.New("QUOTA_RESOURCE_NOT_REGISTERED")

	// ErrValidationFailed 表示入参校验失败（HTTP 400）。
	ErrValidationFailed = errors.New("VALIDATION_FAILED")

	// ErrTenantPlanNotFound 表示套餐不存在或已软删除（HTTP 404）。
	ErrTenantPlanNotFound = errors.New("TENANT_PLAN_NOT_FOUND")

	// ErrPlanStateInvalid 表示状态转换不合法（HTTP 409）。
	ErrPlanStateInvalid = errors.New("PLAN_STATE_INVALID")

	// ErrTenantPlanInUse 表示删除时仍有租户绑定该套餐（HTTP 409）。
	ErrTenantPlanInUse = errors.New("TENANT_PLAN_IN_USE")

	// ErrCoreUnavailable 表示 Core 配额 API 不可用（HTTP 502 GRPC_CLIENT_UNAVAILABLE）。
	ErrCoreUnavailable = errors.New("GRPC_CLIENT_UNAVAILABLE")

	// ErrTenantNotFound 表示 Core 侧租户不存在（HTTP 404）。
	ErrTenantNotFound = errors.New("TENANT_NOT_FOUND")

	// ErrQuotaNotFound 表示租户配额行不存在（HTTP 404；需先绑定/CreateQuota）。
	ErrQuotaNotFound = errors.New("QUOTA_NOT_FOUND")

	// ErrQuotaAlreadyExists 表示新建配额时维度行已存在（HTTP 409）。
	ErrQuotaAlreadyExists = errors.New("QUOTA_ALREADY_EXISTS")

	// ErrPlanNotActive 表示绑定套餐时目标套餐非 active（HTTP 422）。
	ErrPlanNotActive = errors.New("PLAN_NOT_ACTIVE")

	// ErrTenantStateInvalid 表示租户状态不允许该操作（如 disabled 绑套餐，HTTP 409）。
	ErrTenantStateInvalid = errors.New("TENANT_STATE_INVALID")
)
