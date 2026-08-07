package service

import (
	"context"

	tenantv1 "github.com/kubercloud/ani/pkg/generated/pb/tenant/v1"
	"github.com/kubercloud/ani/services/tenant-service/internal/repo/ports"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
)

type TenantPlanService struct {
	// 嵌入未实现接口，确保 proto 新增 RPC 后本结构仍能向后兼容（栅栏模式）。
	tenantv1.UnimplementedTenantPlanServiceServer

	plans ports.TenantPlanStore      // 套餐持久化存储（ports 双模型的 store 层）
	audit ports.TenantPlanAuditStore // 审计日志存储（配额套餐域）
	core  ports.QuotaSvcClient       // Core 配额 API 客户端（下发/同步存量租户）
}

// NewTenantPlanService 构造套餐 gRPC 服务实例。
func NewTenantPlanService(plans ports.TenantPlanStore, audit ports.TenantPlanAuditStore, core ports.QuotaSvcClient) *TenantPlanService {
	return &TenantPlanService{plans: plans, audit: audit, core: core}
}

// Register 向 gRPC server 注册本服务（bootstrap.RunGRPC 会调用）。
func (s *TenantPlanService) Register(server *grpc.Server) {
	tenantv1.RegisterTenantPlanServiceServer(server, s)
}

// ── RPC：ListTenantPlans ────────────────────────────────────────────────
// US-002 查询套餐列表：游标分页 + status 过滤 + search 模糊匹配 name。
func (s *TenantPlanService) ListTenantPlans(ctx context.Context, req *tenantv1.ListTenantPlansRequest) (*tenantv1.ListTenantPlansResponse, error) {
	panic("not implemented: issue-005")
}

// ── RPC：CreateTenantPlan ────────────────────────────────────────────────
// US-001 创建套餐：校验 code/name/quota_limits，冲突返回 PLAN_CODE_CONFLICT。
func (s *TenantPlanService) CreateTenantPlan(ctx context.Context, req *tenantv1.CreateTenantPlanRequest) (*tenantv1.TenantPlan, error) {
	panic("not implemented: issue-005")
}

// ── RPC：GetTenantPlan ──────────────────────────────────────────────────
// US-003 查询套餐详情：不存在返回 TENANT_PLAN_NOT_FOUND。
func (s *TenantPlanService) GetTenantPlan(ctx context.Context, req *tenantv1.GetTenantPlanRequest) (*tenantv1.TenantPlan, error) {
	panic("not implemented: issue-005")
}

// ── RPC：DeleteTenantPlan ───────────────────────────────────────────────
// US-007 删除套餐：软删除，有租户关联返回 TENANT_PLAN_IN_USE。
func (s *TenantPlanService) DeleteTenantPlan(ctx context.Context, req *tenantv1.DeleteTenantPlanRequest) (*emptypb.Empty, error) {
	panic("not implemented: issue-005")
}

// ── RPC：GetTenantPlanQuotaLimits ───────────────────────────────────────
// US-004 查询套餐限额展示视图：resource_type/display_name/unit/total（total 已兜底默认值）。
func (s *TenantPlanService) GetTenantPlanQuotaLimits(ctx context.Context, req *tenantv1.GetTenantPlanQuotaLimitsRequest) (*tenantv1.GetTenantPlanQuotaLimitsResponse, error) {
	panic("not implemented: issue-005")
}

// ── RPC：UpdateTenantPlanQuotaLimits ────────────────────────────────────
// issue-006 修改套餐限额并同步存量租户到 Core。
func (s *TenantPlanService) UpdateTenantPlanQuotaLimits(ctx context.Context, req *tenantv1.UpdateTenantPlanQuotaLimitsRequest) (*emptypb.Empty, error) {
	panic("not implemented: issue-006")
}

// ── RPC：ActivateTenantPlan ─────────────────────────────────────────────
// US-005 发布套餐：draft/disabled → active。
func (s *TenantPlanService) ActivateTenantPlan(ctx context.Context, req *tenantv1.ActivateTenantPlanRequest) (*tenantv1.TenantPlan, error) {
	panic("not implemented: issue-005")
}

// ── RPC：DisableTenantPlan ──────────────────────────────────────────────
// US-006 禁用套餐：active → disabled。
func (s *TenantPlanService) DisableTenantPlan(ctx context.Context, req *tenantv1.DisableTenantPlanRequest) (*tenantv1.TenantPlan, error) {
	panic("not implemented: issue-005")
}

// ── RPC：ListTenantPlanBoundTenants ─────────────────────────────────────
// US-010 查询绑定到套餐的租户摘要列表。
func (s *TenantPlanService) ListTenantPlanBoundTenants(ctx context.Context, req *tenantv1.ListTenantPlanBoundTenantsRequest) (*tenantv1.ListTenantPlanBoundTenantsResponse, error) {
	panic("not implemented: issue-005")
}

// ── RPC：ListTenantPlanAuditLogs ────────────────────────────────────────
// US-011 查询套餐操作历史：游标分页 + action/result 过滤。
func (s *TenantPlanService) ListTenantPlanAuditLogs(ctx context.Context, req *tenantv1.ListTenantPlanAuditLogsRequest) (*tenantv1.ListTenantPlanAuditLogsResponse, error) {
	panic("not implemented: issue-005")
}
