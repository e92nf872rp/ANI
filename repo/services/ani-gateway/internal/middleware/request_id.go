package middleware

import (
	"context"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"github.com/google/uuid"
)

const keyRequestID = "request_id"

// RequestID 为每个请求上下文和响应 Header 注入唯一请求 ID。
func RequestID() app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		reqID := string(c.GetHeader("X-Request-ID"))
		if reqID == "" {
			reqID = "req_" + uuid.New().String()
		}
		c.Set(keyRequestID, reqID)
		c.Header("X-Request-ID", reqID)
		c.Next(ctx)
	}
}

// GetRequestID 从请求上下文读取请求 ID。
func GetRequestID(c *app.RequestContext) string {
	v, _ := c.Get(keyRequestID)
	if id, ok := v.(string); ok {
		return id
	}
	return ""
}

// GetTenantID 读取 Auth 中间件写入的租户 ID。
func GetTenantID(c *app.RequestContext) string {
	v, _ := c.Get("tenant_id")
	if id, ok := v.(string); ok {
		return id
	}
	return ""
}

// GetUserID 读取 Auth 中间件写入的用户 ID。
func GetUserID(c *app.RequestContext) string {
	v, _ := c.Get("user_id")
	if id, ok := v.(string); ok {
		return id
	}
	return ""
}

// GetCredentialScheme 读取生成式认证写入的凭据类型。传统 bearer 认证会
// 留空；调用方必须与 GetPrincipalKind 组合判断，不能把空值视为匿名。
func GetCredentialScheme(c *app.RequestContext) string {
	return strings.TrimSpace(c.GetString("credential_scheme"))
}

// respondError 写入标准化的 ANI 错误响应。
func respondError(c *app.RequestContext, statusCode int, code, message string) {
	c.JSON(statusCode, utils.H{
		"code":       code,
		"message":    message,
		"request_id": GetRequestID(c),
	})
	c.Abort()
}
