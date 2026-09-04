package service

import (
	"context"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/services/tenant-service/internal/repo/ports"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	auditResourceTenantPlan  = "tenant_plan"
	auditResourceTenantAdmin = "tenant_admin"
)

// writeAuditSuccess 写入 result=success 的审计（best-effort：写失败只告警，不阻断已成功业务）。
// resource 由调用方传入（如 tenant_plan / tenant_admin）；tenantID 可选（平台级操作为 nil）。
func writeAuditSuccess(ctx context.Context, audit ports.AuditStore, resource, action string, details map[string]any, tenantID *uuid.UUID) {
	if audit == nil {
		return
	}
	// 步骤 1：details 缺省空对象
	if details == nil {
		details = map[string]any{}
	}
	// 步骤 2：写入 result=success（含网关透传的 user_id / request_id）
	_, err := audit.Create(ctx, ports.AuditLog{
		TenantID:  tenantID,
		UserID:    userIDFromCtx(ctx),
		RequestID: requestIDFromCtx(ctx),
		Action:    action,
		Resource:  resource,
		Result:    ports.AuditResultSuccess,
		Details:   details,
	})
	if err != nil {
		attrs := []any{"action", action, "resource", resource, "error", err}
		if tenantID != nil {
			attrs = append(attrs, "tenant_id", tenantID.String())
		}
		if planID, ok := details["plan_id"]; ok {
			attrs = append(attrs, "plan_id", planID)
		}
		slog.Warn("audit write failed after success", attrs...)
	}
}

// writeAuditFailure 写入 result=failure 的审计（best-effort：写失败只 Warn，不掩盖业务错误）。
// resource / tenantID 语义同 writeAuditSuccess。
func writeAuditFailure(ctx context.Context, audit ports.AuditStore, resource, action string, details map[string]any, cause error, tenantID *uuid.UUID) {
	if audit == nil {
		return
	}
	// 步骤 1：复制 details，并写入业务码 + 异常信息
	out := map[string]any{}
	for k, v := range details {
		out[k] = v
	}
	code, message := auditErrorParts(cause)
	out["code"] = code
	out["message"] = message
	out["reason"] = auditReason(cause) // 兼容既有「CODE: detail」摘要
	// 步骤 2：best-effort 写入；失败只告警，不影响调用方已返回的业务错误
	_, err := audit.Create(ctx, ports.AuditLog{
		TenantID:  tenantID,
		UserID:    userIDFromCtx(ctx),
		RequestID: requestIDFromCtx(ctx),
		Action:    action,
		Resource:  resource,
		Result:    ports.AuditResultFailure,
		Details:   out,
	})
	if err != nil {
		attrs := []any{"action", action, "resource", resource, "error", err}
		if tenantID != nil {
			attrs = append(attrs, "tenant_id", tenantID.String())
		}
		if planID, ok := out["plan_id"]; ok {
			attrs = append(attrs, "plan_id", planID)
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

// auditErrorParts 拆出业务码与异常信息（message 为 CODE 后的 detail；无前缀时 code 为空）。
func auditErrorParts(err error) (code, message string) {
	raw := auditReason(err)
	if raw == "" {
		return "", ""
	}
	if idx := strings.Index(raw, ":"); idx > 0 {
		code = strings.TrimSpace(raw[:idx])
		message = strings.TrimSpace(raw[idx+1:])
		if code != "" {
			return code, message
		}
	}
	return "", raw
}

// coreItemsForAudit 把 Core 配额项压成可 JSON 序列化的审计 details 片段。
func coreItemsForAudit(items []ports.CoreQuotaItem) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		out = append(out, map[string]any{"resource_type": it.ResourceType, "total": it.Total})
	}
	return out
}

// quotaLimitsForAudit 把套餐限额入参压成可 JSON 序列化的审计 details 片段。
func quotaLimitsForAudit(limits []ports.PlanQuotaLimitInput) []map[string]any {
	out := make([]map[string]any, 0, len(limits))
	for _, lim := range limits {
		item := map[string]any{"resource_type": lim.ResourceType}
		if lim.Total != nil {
			item["total"] = *lim.Total
		}
		out = append(out, item)
	}
	return out
}

// requestIDFromCtx 读取网关经 gRPC metadata 注入的 x-request-id。
// 见 ani-gateway tenantCallCtx：metadata.AppendToOutgoingContext(..., "x-request-id", ...)。
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
// 非法/缺失时返回 nil（audit_logs.user_id 允许 NULL，如后台异步重试）。
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
