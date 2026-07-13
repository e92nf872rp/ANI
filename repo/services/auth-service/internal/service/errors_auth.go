package service

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// 错误码常量
const (
	ErrCodeInvalidCredentials = "INVALID_CREDENTIALS" // 无效的登录凭证
	ErrCodeTenantNotFound     = "TENANT_NOT_FOUND"    // 租户不存在
)

// 认证错误结构体
type authError struct {
	code    string // 错误码
	message string // 错误消息
}

// 实现error接口，返回错误信息
func (e *authError) Error() string {
	if e == nil {
		return ""
	}
	if e.message != "" {
		return e.code + ": " + e.message
	}
	return e.code
}

// 创建认证错误实例
func newAuthError(code, message string) *authError {
	return &authError{code: code, message: message}
}

// 根据认证码返回对应的gRPC状态码
func grpcCodeForAuth(code string) codes.Code {
	switch code {
	case ErrCodeInvalidCredentials:
		return codes.Unauthenticated
	case ErrCodeTenantNotFound:
		return codes.NotFound
	default:
		return codes.InvalidArgument
	}
}

// 根据认证错误返回对应的gRPC状态
func statusFromAuthError(err error) error {
	if err == nil {
		return nil
	}
	var ae *authError
	if !asAuthError(err, &ae) {
		return err
	}
	return status.Error(grpcCodeForAuth(ae.code), ae.message)
}

// 检查错误是否为认证错误
func asAuthError(err error, target **authError) bool {
	if err == nil {
		return false
	}
	if ae, ok := err.(*authError); ok {
		*target = ae
		return true
	}
	return false
}
