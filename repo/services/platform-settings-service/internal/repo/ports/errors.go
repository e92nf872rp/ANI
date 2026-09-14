package ports

import "errors"

// 领域哨兵错误：service 层映射为 gRPC status message「CODE: detail」，
// 供网关 mapPlatformAdminError 按业务码前缀还原 HTTP 状态。

var (
	// ErrNotImplemented 表示接口已声明但业务尚未实现（gRPC UNIMPLEMENTED / HTTP 501）。
	ErrNotImplemented = errors.New("NOT_IMPLEMENTED")

	// ErrPlatformUserNotFound 表示平台账号不存在或已软删除（HTTP 404）。
	ErrPlatformUserNotFound = errors.New("PLATFORM_USER_NOT_FOUND")

	// ErrRoleNotFound 表示平台角色不存在（HTTP 404）。
	ErrRoleNotFound = errors.New("ROLE_NOT_FOUND")

	// ErrUsernameAlreadyExists 表示平台用户名冲突（HTTP 409）；平台侧 email 允许重复。
	ErrUsernameAlreadyExists = errors.New("USERNAME_ALREADY_EXISTS")

	// ErrLastPlatformAdmin 表示唯一活跃 platform-admin 不可禁用/删除（HTTP 422）。
	ErrLastPlatformAdmin = errors.New("LAST_PLATFORM_ADMIN")

	// ErrPasswordSameAsOld 表示新密码与旧密码相同（HTTP 422）。
	ErrPasswordSameAsOld = errors.New("PASSWORD_SAME_AS_OLD")

	// ErrStatusUnchanged 表示目标状态与当前相同，不可重复启用/停用（HTTP 422）。
	ErrStatusUnchanged = errors.New("STATUS_UNCHANGED")

	// ErrRoleChangeInvalid 表示目标角色非法（HTTP 422）。
	ErrRoleChangeInvalid = errors.New("ROLE_CHANGE_INVALID")

	// ErrValidationFailed 表示入参校验失败（HTTP 400）。
	ErrValidationFailed = errors.New("VALIDATION_FAILED")

	// ErrCoreUnavailable 表示 Core 平台用户 API 不可用或未实现（HTTP 502）。
	ErrCoreUnavailable = errors.New("CORE_UNAVAILABLE")
)
