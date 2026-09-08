package service

import (
	"errors"
	"fmt"
	"strings"

	"github.com/kubercloud/ani/services/platform-settings-service/internal/repo/ports"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// mapDomainError 将哨兵错误映射为「CODE: detail」形式的 gRPC status。
func mapDomainError(err error) error {
	if err == nil {
		return nil
	}
	detail := strings.TrimSpace(err.Error())
	switch {
	case errors.Is(err, ports.ErrPlatformUserNotFound):
		return businessError(codes.NotFound, ports.ErrPlatformUserNotFound, detail)
	case errors.Is(err, ports.ErrRoleNotFound):
		return businessError(codes.NotFound, ports.ErrRoleNotFound, detail)
	case errors.Is(err, ports.ErrUsernameAlreadyExists):
		return businessError(codes.AlreadyExists, ports.ErrUsernameAlreadyExists, detail)
	case errors.Is(err, ports.ErrLastPlatformAdmin):
		return businessError(codes.FailedPrecondition, ports.ErrLastPlatformAdmin, detail)
	case errors.Is(err, ports.ErrPasswordSameAsOld):
		return businessError(codes.FailedPrecondition, ports.ErrPasswordSameAsOld, detail)
	case errors.Is(err, ports.ErrStatusUnchanged):
		return businessError(codes.FailedPrecondition, ports.ErrStatusUnchanged, detail)
	case errors.Is(err, ports.ErrRoleChangeInvalid):
		return businessError(codes.FailedPrecondition, ports.ErrRoleChangeInvalid, detail)
	case errors.Is(err, ports.ErrValidationFailed):
		return businessError(codes.InvalidArgument, ports.ErrValidationFailed, detail)
	case errors.Is(err, ports.ErrCoreUnavailable):
		return businessError(codes.Unavailable, ports.ErrCoreUnavailable, detail)
	case errors.Is(err, ports.ErrNotImplemented):
		return businessError(codes.Unimplemented, ports.ErrNotImplemented, detail)
	default:
		return status.Error(codes.Internal, detail)
	}
}

// businessError 构造「CODE: detail」形式的 gRPC 错误供网关前缀还原。
func businessError(code codes.Code, sentinel error, detail string) error {
	msg := sentinel.Error()
	detail = strings.TrimSpace(detail)
	if detail != "" && detail != msg && !strings.HasPrefix(detail, msg) {
		msg = fmt.Sprintf("%s: %s", msg, detail)
	} else if strings.HasPrefix(detail, msg) {
		msg = detail
	}
	return status.Error(code, msg)
}
