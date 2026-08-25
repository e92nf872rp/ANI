package service

import (
	"context"

	commonv1 "github.com/kubercloud/ani/pkg/generated/pb/common/v1"
	tenantv1 "github.com/kubercloud/ani/pkg/generated/pb/tenant/v1"
	"github.com/kubercloud/ani/services/tenant-service/internal/repo/ports"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TenantAdminService 是租户管理员域的 gRPC 服务。
// 网关经 TenantAdminServiceClient 转发 /api/v1/svc/tenant-admins* 与 /tenants/{tenantId}/admins*。
// ListAvailableTenants 已实现；其余 RPC 仍返回 UNIMPLEMENTED。
type TenantAdminService struct {
	tenantv1.UnimplementedTenantAdminServiceServer
	core    ports.TenantAdminSvcClient
	tenants ports.TenantSvcClient
}

var _ tenantv1.TenantAdminServiceServer = (*TenantAdminService)(nil)

// NewTenantAdminService 装配 Core 客户端并返回可注册的 gRPC server。
// ListAvailableTenants 走 tenants（Core TenantService）；用户/角色后续走 core。
func NewTenantAdminService(core ports.TenantAdminSvcClient, tenants ports.TenantSvcClient) *TenantAdminService {
	return &TenantAdminService{core: core, tenants: tenants}
}

// Register 向 gRPC Server 注册本服务（由 services/pkg/bootstrap.RunGRPC 回调）。
func (s *TenantAdminService) Register(server *grpc.Server) {
	tenantv1.RegisterTenantAdminServiceServer(server, s)
}

func unimplemented() error {
	return status.Error(codes.Unimplemented, ports.ErrNotImplemented.Error())
}

// ListAvailableTenants 返回非 disabled 租户列表（SPEC §5.1.11 / US-011）。
// 只读、无审计；经 Core GET /admin/tenant-admins/available-tenants（TenantService）。
func (s *TenantAdminService) ListAvailableTenants(ctx context.Context, _ *tenantv1.ListAvailableTenantsRequest) (*tenantv1.ListAvailableTenantsResponse, error) {
	// 步骤 1：校验 Core 租户客户端已注入
	if s.tenants == nil {
		return nil, businessError(codes.Unavailable, ports.ErrCoreUnavailable, "core tenant api unavailable")
	}
	// 步骤 2：调用 Core TenantSvcClient 拉取非 disabled 租户
	items, err := s.tenants.ListAvailableTenants(ctx)
	if err != nil {
		return nil, mapStoreError(err)
	}
	// 步骤 3：映射为 gRPC AvailableTenant 列表并返回
	out := make([]*tenantv1.AvailableTenant, 0, len(items))
	for _, t := range items {
		out = append(out, &tenantv1.AvailableTenant{
			Id:          t.ID.String(),
			Name:        t.Name,
			DisplayName: t.DisplayName,
			Status:      string(t.Status),
		})
	}
	return &tenantv1.ListAvailableTenantsResponse{Items: out}, nil
}

func (s *TenantAdminService) InviteTenantAdmin(context.Context, *tenantv1.InviteTenantAdminRequest) (*tenantv1.InvitationResult, error) {
	return nil, unimplemented()
}

func (s *TenantAdminService) ResendTenantAdminInvitation(context.Context, *tenantv1.ResendTenantAdminInvitationRequest) (*tenantv1.InvitationResult, error) {
	return nil, unimplemented()
}

func (s *TenantAdminService) ListAllTenantAdmins(context.Context, *tenantv1.ListAllTenantAdminsRequest) (*tenantv1.ListAllTenantAdminsResponse, error) {
	return nil, unimplemented()
}

func (s *TenantAdminService) GetTenantAdminDetail(context.Context, *tenantv1.GetTenantAdminDetailRequest) (*tenantv1.AdminWithTenant, error) {
	return nil, unimplemented()
}

func (s *TenantAdminService) UpdateTenantAdminRole(context.Context, *tenantv1.UpdateTenantAdminRoleRequest) (*commonv1.IdempotentResult, error) {
	return nil, unimplemented()
}

func (s *TenantAdminService) GetTenantAdminRole(context.Context, *tenantv1.GetTenantAdminRoleRequest) (*tenantv1.UserPermissions, error) {
	return nil, unimplemented()
}

func (s *TenantAdminService) GetChangeableRoles(context.Context, *tenantv1.GetChangeableRolesRequest) (*tenantv1.GetChangeableRolesResponse, error) {
	return nil, unimplemented()
}

func (s *TenantAdminService) ResetTenantAdminPassword(context.Context, *tenantv1.ResetTenantAdminPasswordRequest) (*commonv1.IdempotentResult, error) {
	return nil, unimplemented()
}

func (s *TenantAdminService) DisableTenantAdmin(context.Context, *tenantv1.DisableTenantAdminRequest) (*commonv1.IdempotentResult, error) {
	return nil, unimplemented()
}

func (s *TenantAdminService) EnableTenantAdmin(context.Context, *tenantv1.EnableTenantAdminRequest) (*commonv1.IdempotentResult, error) {
	return nil, unimplemented()
}

func (s *TenantAdminService) DeleteTenantAdmin(context.Context, *tenantv1.DeleteTenantAdminRequest) (*commonv1.IdempotentResult, error) {
	return nil, unimplemented()
}

func (s *TenantAdminService) ListTenantAdminAuditLogs(context.Context, *tenantv1.ListTenantAdminAuditLogsRequest) (*tenantv1.ListTenantAdminAuditLogsResponse, error) {
	return nil, unimplemented()
}
