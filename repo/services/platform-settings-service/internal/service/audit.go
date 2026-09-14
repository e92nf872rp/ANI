package service

import (
	"context"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/services/platform-settings-service/internal/repo/ports"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const auditResourcePlatformUser = "platform_user"

// writeAuditSuccess 写入 result=success 的审计（best-effort：写失败只告警，不阻断已成功业务）。
func writeAuditSuccess(ctx context.Context, audit ports.PlatformAdminAuditStore, action string, details map[string]any) {
	// 步骤 1：details 缺省空对象
	if details == nil {
		details = map[string]any{}
	}
	// 步骤 2：写入 result=success（含网关透传的 user_id / request_id）
	_, err := audit.Create(ctx, ports.AuditLog{
		TenantID:  nil,
		UserID:    userIDFromCtx(ctx),
		RequestID: requestIDFromCtx(ctx),
		Action:    action,
		Resource:  auditResourcePlatformUser,
		Result:    "success",
		Details:   details,
	})
	if err != nil {
		attrs := []any{"action", action, "error", err}
		if targetID, ok := details["target_id"]; ok {
			attrs = append(attrs, "target_id", targetID)
		}
		slog.Warn("audit write failed after success", attrs...)
	}
}

// writeAuditFailure 写入 result=failure 的审计（best-effort：写失败只 Warn，不掩盖业务错误）。
func writeAuditFailure(ctx context.Context, audit ports.PlatformAdminAuditStore, action string, details map[string]any, cause error) {
	// 步骤 1：复制 details 并附加 reason
	out := map[string]any{}
	for k, v := range details {
		out[k] = v
	}
	out["reason"] = auditReason(cause)
	// 步骤 2：best-effort 写入；失败只告警，不影响调用方已返回的业务错误
	_, err := audit.Create(ctx, ports.AuditLog{
		TenantID:  nil,
		UserID:    userIDFromCtx(ctx),
		RequestID: requestIDFromCtx(ctx),
		Action:    action,
		Resource:  auditResourcePlatformUser,
		Result:    "failure",
		Details:   out,
	})
	if err != nil {
		attrs := []any{"action", action, "error", err}
		if targetID, ok := out["target_id"]; ok {
			attrs = append(attrs, "target_id", targetID)
		}
		slog.Warn("audit write failed after failure", attrs...)
	}
}

// auditReason 从 gRPC status 提取审计用错误摘要（CODE: message）。
func auditReason(err error) string {
	if err == nil {
		return ""
	}
	if st, ok := status.FromError(err); ok {
		return st.Message()
	}
	return err.Error()
}

// requestIDFromCtx 读取网关经 gRPC metadata 注入的 x-request-id。
func requestIDFromCtx(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	vals := md.Get("x-request-id")
	if len(vals) == 0 {
		return ""
	}
	return strings.TrimSpace(vals[0])
}

// userIDFromCtx 读取网关经 gRPC metadata 注入的 x-user-id（Auth 中间件 user_id）。
// 非法/缺失时返回 nil（audit_logs.user_id 允许 NULL）。
func userIDFromCtx(ctx context.Context) *uuid.UUID {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil
	}
	vals := md.Get("x-user-id")
	if len(vals) == 0 {
		return nil
	}
	id, err := uuid.Parse(strings.TrimSpace(vals[0]))
	if err != nil {
		return nil
	}
	return &id
}
