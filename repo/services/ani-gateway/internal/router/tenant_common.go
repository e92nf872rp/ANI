package router

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/kubercloud/ani/services/ani-gateway/internal/middleware"
	"google.golang.org/grpc/metadata"
)

// 本文件存放租户相关网关路由的通用常量与工具函数，供 tenant_plans.go、
// tenant_admin_resources.go 等文件共享使用。

const (
	// tenantServiceDefaultAddr 缺省 gRPC 地址，可由 TENANT_SERVICE_ADDR 覆盖（对应 GRPC_PORT=9105）。
	tenantServiceDefaultAddr = "127.0.0.1:9105"
	// tenantCallTimeout 单次 gRPC 调用超时。
	tenantCallTimeout = 5 * time.Second
)

// tenantCallCtx 构造 gRPC 调用 context：注入 5s 超时，并把网关 request_id / user_id 透传到 gRPC metadata。
// 供 tenant-service 审计日志关联请求与操作者。
func tenantCallCtx(ctx context.Context, c *app.RequestContext) (context.Context, context.CancelFunc) {
	callCtx, cancel := context.WithTimeout(ctx, tenantCallTimeout)
	callCtx = metadata.AppendToOutgoingContext(callCtx, "x-request-id", middleware.GetRequestID(c))
	if userID := strings.TrimSpace(middleware.GetUserID(c)); userID != "" {
		callCtx = metadata.AppendToOutgoingContext(callCtx, "x-user-id", userID)
	}
	return callCtx, cancel
}

// idempotencyHeader 读取 Idempotency-Key 请求头（去除首尾空白）。
func idempotencyHeader(c *app.RequestContext) string {
	return strings.TrimSpace(string(c.GetHeader("Idempotency-Key")))
}

// cursorLimit 解析 limit 查询参数，默认 20、上限 100。
func cursorLimit(c *app.RequestContext) int32 {
	limit := int32(20)
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = int32(n)
			if limit > 100 {
				limit = 100
			}
		}
	}
	return limit
}

// nullIfEmpty 空串映射为 JSON null（OpenAPI next_cursor nullable：null = 已无更多）。
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
