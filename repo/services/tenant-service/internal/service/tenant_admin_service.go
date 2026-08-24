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

// TenantAdminService 是租户管理员域的 gRPC 服务骨架。
// 网关（ani-gateway）经 TenantAdminServiceClient 转发 /api/v1/svc/tenant-admins*
// 与 /tenants/{tenantId}/admins*；RPC 业务仍返回 UNIMPLEMENTED（HTTP 501）。
type TenantAdminService struct {
	tenantv1.UnimplementedTenantAdminServiceServer
	core ports.TenantAdminSvcClient
}

var _ tenantv1.TenantAdminServiceServer = (*TenantAdminService)(nil)

// NewTenantAdminService 装配 Core 租户管理员客户端并返回可注册的 gRPC server。
func NewTenantAdminService(core ports.TenantAdminSvcClient) *TenantAdminService {
	return &TenantAdminService{core: core}
}

// Register 向 gRPC Server 注册本服务（由 services/pkg/bootstrap.RunGRPC 回调）。
func (s *TenantAdminService) Register(server *grpc.Server) {
	tenantv1.RegisterTenantAdminServiceServer(server, s)
}

func unimplemented() error {
	return status.Error(codes.Unimplemented, ports.ErrNotImplemented.Error())
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
