package service

import (
	"errors"
	"fmt"
	"strings"

	"github.com/kubercloud/ani/services/tenant-service/internal/repo/ports"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// mapStoreError 将 store 哨兵错误映射为带业务码前缀的 gRPC status。
// 网关按 message 前缀（如 PLAN_CODE_CONFLICT:）还原 HTTP 状态与 ErrorResponse.code。
func mapStoreError(err error) error {
	switch {
	case errors.Is(err, ports.ErrPlanCodeConflict):
		return businessError(codes.AlreadyExists, ports.ErrPlanCodeConflict, "plan code already exists")
	case errors.Is(err, ports.ErrTenantPlanNotFound):
		return businessError(codes.NotFound, ports.ErrTenantPlanNotFound, "tenant plan not found")
	case errors.Is(err, ports.ErrPlanStateInvalid):
		detail := strings.TrimSpace(strings.TrimPrefix(err.Error(), ports.ErrPlanStateInvalid.Error()+":"))
		detail = strings.TrimSpace(strings.TrimPrefix(detail, ports.ErrPlanStateInvalid.Error()))
		if detail == "" {
			detail = "plan status does not allow this transition"
		}
		return businessError(codes.FailedPrecondition, ports.ErrPlanStateInvalid, detail)
	case errors.Is(err, ports.ErrTenantPlanInUse):
		return businessError(codes.FailedPrecondition, ports.ErrTenantPlanInUse, "tenant plan has bound tenants")
	case errors.Is(err, ports.ErrQuotaResourceNotRegistered):
		return businessError(codes.FailedPrecondition, ports.ErrQuotaResourceNotRegistered, "resource_type not registered or disabled")
	case errors.Is(err, ports.ErrTenantNotFound):
		return businessError(codes.NotFound, ports.ErrTenantNotFound, "tenant not found")
	case errors.Is(err, ports.ErrQuotaNotFound):
		return businessError(codes.NotFound, ports.ErrQuotaNotFound, "tenant quota not found")
	case errors.Is(err, ports.ErrQuotaAlreadyExists):
		return businessError(codes.AlreadyExists, ports.ErrQuotaAlreadyExists, "tenant quota already exists")
	case errors.Is(err, ports.ErrPlanNotActive):
		return businessError(codes.FailedPrecondition, ports.ErrPlanNotActive, "tenant plan is not active")
	case errors.Is(err, ports.ErrTenantStateInvalid):
		return businessError(codes.FailedPrecondition, ports.ErrTenantStateInvalid, "tenant state does not allow this operation")
	case errors.Is(err, ports.ErrValidationFailed):
		detail := strings.TrimSpace(strings.TrimPrefix(err.Error(), ports.ErrValidationFailed.Error()+":"))
		detail = strings.TrimSpace(strings.TrimPrefix(detail, ports.ErrValidationFailed.Error()))
		return businessError(codes.InvalidArgument, ports.ErrValidationFailed, detail)
	case errors.Is(err, ports.ErrCoreUnavailable):
		return businessError(codes.Unavailable, ports.ErrCoreUnavailable, "core tenant api unavailable")
	case errors.Is(err, ports.ErrStoreUnavailable):
		return businessError(codes.Unavailable, ports.ErrStoreUnavailable, "tenant admin store unavailable")
	case errors.Is(err, ports.ErrTenantAdminNotFound):
		return businessError(codes.NotFound, ports.ErrTenantAdminNotFound, "tenant admin not found")
	case errors.Is(err, ports.ErrTenantAdminAlreadyAdmin):
		return businessError(codes.AlreadyExists, ports.ErrTenantAdminAlreadyAdmin, "user is already a tenant admin")
	case errors.Is(err, ports.ErrTenantInvitationPending):
		return businessError(codes.AlreadyExists, ports.ErrTenantInvitationPending, "pending invitation exists; use resend")
	case errors.Is(err, ports.ErrTenantAdminInvitationNotFound):
		return businessError(codes.NotFound, ports.ErrTenantAdminInvitationNotFound, "tenant admin invitation not found")
	case errors.Is(err, ports.ErrTenantInvitationSettled):
		return businessError(codes.FailedPrecondition, ports.ErrTenantInvitationSettled, "invitation already settled")
	case errors.Is(err, ports.ErrRoleChangeInvalid):
		return businessError(codes.FailedPrecondition, ports.ErrRoleChangeInvalid, "role change invalid")
	case errors.Is(err, ports.ErrPasswordSameAsOld):
		return businessError(codes.FailedPrecondition, ports.ErrPasswordSameAsOld, "password same as old")
	case errors.Is(err, ports.ErrNotImplemented):
		return status.Error(codes.Unimplemented, ports.ErrNotImplemented.Error())
	default:
		return status.Errorf(codes.Internal, "tenant plan operation failed: %v", err)
	}
}

// businessError 构造「CODE: detail」形式的 gRPC 错误。
// sentinel.Error() 即为业务码字符串（如 VALIDATION_FAILED），必须保留前缀供网关解析。
func businessError(code codes.Code, sentinel error, detail string) error {
	msg := sentinel.Error()
	detail = strings.TrimSpace(detail)
	if detail != "" && detail != msg {
		msg = fmt.Sprintf("%s: %s", msg, detail)
	}
	return status.Error(code, msg)
}
