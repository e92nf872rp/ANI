package service

import (
	"context"

	tenantv1 "github.com/kubercloud/ani/pkg/generated/pb/tenant/v1"
	"github.com/kubercloud/ani/pkg/ports"
	"google.golang.org/grpc"
)

// TenantService 是 gRPC TenantService server（仿 model-service）。
// 目前承载绑定套餐 RPC：BindPlanQuota（issue-007）。
// 方法体以 panic("not implemented") 占位，仅建立编译通过的类型契约，业务逻辑由 issue-007 填充。
type TenantService struct {
	// 嵌入未实现接口，确保 proto 新增 RPC 后本结构仍能向后兼容（栅栏模式）。
	tenantv1.UnimplementedTenantServiceServer

	store ports.TenantStore     // tenants 表最小访问（GetByID 判状态 / UpdatePlan 换 plan_id）
	plans ports.TenantPlanStore // 套餐 store（GetQuotaLimitViews 取有效限额 / GetApprovedQuotaChanges 取已审批维度）
	core  ports.QuotaSvcClient  // Core 配额 API 客户端（批量下发配额）
	audit ports.AuditStore      // 审计日志
}

// NewTenantService 构造租户 gRPC 服务实例。
func NewTenantService(store ports.TenantStore, plans ports.TenantPlanStore, core ports.QuotaSvcClient, audit ports.AuditStore) *TenantService {
	return &TenantService{store: store, plans: plans, core: core, audit: audit}
}

// Register 向 gRPC server 注册本服务（bootstrap.RunGRPC 会调用）。
func (s *TenantService) Register(server *grpc.Server) {
	tenantv1.RegisterTenantServiceServer(server, s)
}

// BindPlanQuota 绑定配额套餐到租户：读套餐有效限额 → 跳已审批维度 → 批量下发 Core → 更新 tenants.plan_id。
// US-008 绑定套餐：plan 非 active → 404 TENANT_PLAN_NOT_FOUND；租户 disabled → 409 TENANT_STATE_INVALID。
func (s *TenantService) BindPlanQuota(ctx context.Context, req *tenantv1.BindPlanQuotaRequest) (*tenantv1.BindPlanQuotaResponse, error) {
	panic("not implemented: issue-007")
}
