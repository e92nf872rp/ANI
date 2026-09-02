package service

import (
	"context"

	commonv1 "github.com/kubercloud/ani/pkg/generated/pb/common/v1"
	tenantv1 "github.com/kubercloud/ani/pkg/generated/pb/tenant/v1"
	"github.com/kubercloud/ani/services/tenant-service/internal/repo/ports"
	"google.golang.org/grpc"
)

// TenantListService 是租户列表域的 gRPC 服务。
// 网关经 TenantListServiceClient 转发 /api/v1/svc/tenants*。
// 当前各 RPC 仅声明占位，业务实现归 issue-005 ~ issue-014。
type TenantListService struct {
	tenantv1.UnimplementedTenantListServiceServer

	plans        ports.TenantPlanStore      // 可用套餐 / plan_code 装配
	tenants      ports.TenantSvcClient      // Core 租户 API
	tenantAdmins ports.TenantAdminSvcClient // Core 租户成员 API（US-017）
	quota        ports.QuotaSvcClient       // Core 配额 API
	tenantStore  ports.TenantStore          // tenant_lifecycle / tenant_quota_change 持久化
	audit        ports.TenantPlanAuditStore // audit_logs 读写（复用既有 store）
	ssoLoader    ports.SsoConfigLoader      // SSO 配置加载（K8s Secret）
	oidcTester   ports.OidcDiscoveryTester  // OIDC discovery 测试
}

var _ tenantv1.TenantListServiceServer = (*TenantListService)(nil)

// NewTenantListService 装配租户列表域依赖并返回可注册的 gRPC server。
func NewTenantListService(
	plans ports.TenantPlanStore,
	tenants ports.TenantSvcClient,
	tenantAdmins ports.TenantAdminSvcClient,
	quota ports.QuotaSvcClient,
	tenantStore ports.TenantStore,
	audit ports.TenantPlanAuditStore,
	ssoLoader ports.SsoConfigLoader,
	oidcTester ports.OidcDiscoveryTester,
) *TenantListService {
	return &TenantListService{
		plans:        plans,
		tenants:      tenants,
		tenantAdmins: tenantAdmins,
		quota:        quota,
		tenantStore:  tenantStore,
		audit:        audit,
		ssoLoader:    ssoLoader,
		oidcTester:   oidcTester,
	}
}

// Register 向 gRPC Server 注册本服务（由 services/pkg/bootstrap.RunGRPC 回调）。
func (s *TenantListService) Register(server *grpc.Server) {
	tenantv1.RegisterTenantListServiceServer(server, s)
}

// ListAvailablePlans 返回 active 套餐列表（US-001）。
func (s *TenantListService) ListAvailablePlans(ctx context.Context, req *tenantv1.ListAvailablePlansRequest) (*tenantv1.ListAvailablePlansResponse, error) {
	_ = ctx
	_ = req
	return nil, tenantListNotImplemented()
}

// CreateTenant 创建租户（US-002）。
func (s *TenantListService) CreateTenant(ctx context.Context, req *tenantv1.CreateTenantRequest) (*commonv1.IdempotentResult, error) {
	_ = ctx
	_ = req
	return nil, tenantListNotImplemented()
}

// ListTenants 游标分页租户列表（US-003）。
func (s *TenantListService) ListTenants(ctx context.Context, req *tenantv1.ListTenantsRequest) (*tenantv1.ListTenantsResponse, error) {
	_ = ctx
	_ = req
	return nil, tenantListNotImplemented()
}

// GetTenantDetail 查询租户详情（US-004）。
func (s *TenantListService) GetTenantDetail(ctx context.Context, req *tenantv1.GetTenantDetailRequest) (*tenantv1.TenantDetail, error) {
	_ = ctx
	_ = req
	return nil, tenantListNotImplemented()
}

// UpdateTenant 更新租户基本信息（US-005）。
func (s *TenantListService) UpdateTenant(ctx context.Context, req *tenantv1.UpdateTenantRequest) (*commonv1.IdempotentResult, error) {
	_ = ctx
	_ = req
	return nil, tenantListNotImplemented()
}

// FreezeTenant 冻结租户（US-006）。
func (s *TenantListService) FreezeTenant(ctx context.Context, req *tenantv1.FreezeTenantRequest) (*commonv1.IdempotentResult, error) {
	_ = ctx
	_ = req
	return nil, tenantListNotImplemented()
}

// UnfreezeTenant 解冻租户（US-006）。
func (s *TenantListService) UnfreezeTenant(ctx context.Context, req *tenantv1.UnfreezeTenantRequest) (*commonv1.IdempotentResult, error) {
	_ = ctx
	_ = req
	return nil, tenantListNotImplemented()
}

// DisableTenant 禁用租户（US-007）。
func (s *TenantListService) DisableTenant(ctx context.Context, req *tenantv1.DisableTenantRequest) (*commonv1.IdempotentResult, error) {
	_ = ctx
	_ = req
	return nil, tenantListNotImplemented()
}

// GetTenantAuth 查询 SSO/MFA 配置（US-008）。
func (s *TenantListService) GetTenantAuth(ctx context.Context, req *tenantv1.GetTenantAuthRequest) (*tenantv1.TenantAuthConfig, error) {
	_ = ctx
	_ = req
	return nil, tenantListNotImplemented()
}

// UpdateTenantSso 更新 SSO 配置（US-009）。
func (s *TenantListService) UpdateTenantSso(ctx context.Context, req *tenantv1.UpdateTenantSsoRequest) (*commonv1.IdempotentResult, error) {
	_ = ctx
	_ = req
	return nil, tenantListNotImplemented()
}

// TestTenantSso 测试 SSO OIDC 连接（US-009）。
func (s *TenantListService) TestTenantSso(ctx context.Context, req *tenantv1.TestTenantSsoRequest) (*tenantv1.SsoTestResult, error) {
	_ = ctx
	_ = req
	return nil, tenantListNotImplemented()
}

// UpdateTenantMfa 切换租户 MFA 强制开关（US-010）。
func (s *TenantListService) UpdateTenantMfa(ctx context.Context, req *tenantv1.UpdateTenantMfaRequest) (*commonv1.IdempotentResult, error) {
	_ = ctx
	_ = req
	return nil, tenantListNotImplemented()
}

// GetTenantQuota 查询租户配额视图（US-011）。
func (s *TenantListService) GetTenantQuota(ctx context.Context, req *tenantv1.GetTenantQuotaRequest) (*tenantv1.GetTenantQuotaResponse, error) {
	_ = ctx
	_ = req
	return nil, tenantListNotImplemented()
}

// SubmitQuotaChangeRequest 提交配额变更申请（US-012）。
func (s *TenantListService) SubmitQuotaChangeRequest(ctx context.Context, req *tenantv1.SubmitQuotaChangeRequestRequest) (*commonv1.IdempotentResult, error) {
	_ = ctx
	_ = req
	return nil, tenantListNotImplemented()
}

// ListQuotaChangeRequests 查询配额变更申请列表（US-013）。
func (s *TenantListService) ListQuotaChangeRequests(ctx context.Context, req *tenantv1.ListQuotaChangeRequestsRequest) (*tenantv1.ListQuotaChangeRequestsResponse, error) {
	_ = ctx
	_ = req
	return nil, tenantListNotImplemented()
}

// ReviewQuotaChangeRequest 审批配额变更申请（US-014）。
func (s *TenantListService) ReviewQuotaChangeRequest(ctx context.Context, req *tenantv1.ReviewQuotaChangeRequestRequest) (*commonv1.IdempotentResult, error) {
	_ = ctx
	_ = req
	return nil, tenantListNotImplemented()
}

// ListTenantLifecycle 查询租户生命周期记录（US-015）。
// 读路径：tenantStore.ListLifecycle（直读 tenant_lifecycle 表，不经 Core SDK）。
func (s *TenantListService) ListTenantLifecycle(ctx context.Context, req *tenantv1.ListTenantLifecycleRequest) (*tenantv1.ListTenantLifecycleResponse, error) {
	_ = ctx
	_ = req
	return nil, tenantListNotImplemented()
}

// ListTenantAuditLogs 查询租户操作历史（US-016）。
func (s *TenantListService) ListTenantAuditLogs(ctx context.Context, req *tenantv1.ListTenantAuditLogsRequest) (*tenantv1.ListTenantAuditLogsResponse, error) {
	_ = ctx
	_ = req
	return nil, tenantListNotImplemented()
}

// ListTenantAdmins 查询租户内管理员列表（US-017）。
func (s *TenantListService) ListTenantAdmins(ctx context.Context, req *tenantv1.ListTenantAdminsRequest) (*tenantv1.ListTenantAdminsResponse, error) {
	_ = ctx
	_ = req
	return nil, tenantListNotImplemented()
}

func tenantListNotImplemented() error {
	return mapStoreError(ports.ErrNotImplemented)
}
