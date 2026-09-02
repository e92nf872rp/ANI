package service

import (
	"context"
	"strings"

	commonv1 "github.com/kubercloud/ani/pkg/generated/pb/common/v1"
	"github.com/google/uuid"
	tenantv1 "github.com/kubercloud/ani/pkg/generated/pb/tenant/v1"
	"github.com/kubercloud/ani/services/tenant-service/internal/repo/ports"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

// TenantService 是租户域 gRPC 服务：套餐绑定（BindPlanQuota）与租户列表生命周期 RPC。
// 网关经 TenantServiceClient 转发 /api/v1/svc/tenants*。
type TenantService struct {
	tenantv1.UnimplementedTenantServiceServer

	plans        ports.TenantPlanStore
	tenants      ports.TenantSvcClient
	tenantPlans  ports.TenantPlanSvcClient
	quota        ports.QuotaSvcClient
	tenantStore  ports.TenantStore
	audit        ports.AuditStore
	tenantAdmins ports.TenantAdminSvcClient
	ssoLoader    ports.SsoConfigLoader
	oidcTester   ports.OidcDiscoveryTester
}

var _ tenantv1.TenantServiceServer = (*TenantService)(nil)

// NewTenantService 构造租户 gRPC 服务实例。
func NewTenantService(
	plans ports.TenantPlanStore,
	tenants ports.TenantSvcClient,
	tenantPlans ports.TenantPlanSvcClient,
	quota ports.QuotaSvcClient,
	tenantStore ports.TenantStore,
	audit ports.AuditStore,
	tenantAdmins ports.TenantAdminSvcClient,
	ssoLoader ports.SsoConfigLoader,
	oidcTester ports.OidcDiscoveryTester,
) *TenantService {
	return &TenantService{
		plans:        plans,
		tenants:      tenants,
		tenantPlans:  tenantPlans,
		quota:        quota,
		tenantStore:  tenantStore,
		audit:        audit,
		tenantAdmins: tenantAdmins,
		ssoLoader:    ssoLoader,
		oidcTester:   oidcTester,
	}
}

// Register 向 gRPC server 注册本服务（services/pkg/bootstrap.RunGRPC 会调用）。
func (s *TenantService) Register(server *grpc.Server) {
	tenantv1.RegisterTenantServiceServer(server, s)
}

// BindPlanQuota 绑定配额套餐到租户（US-009 / issue-009）：
// 校验 → 更新 plan_id（Core 租户 API）→ 同步 Core 配额；配额失败则回滚 plan_id。
func (s *TenantService) BindPlanQuota(ctx context.Context, req *tenantv1.BindPlanQuotaRequest) (*tenantv1.IdempotentResult, error) {
	const action = "tenant.bind_plan_quota"

	// 步骤 1：校验 tenant_id / plan_id
	rawTenantID, rawPlanID := "", ""
	if req != nil {
		rawTenantID = req.GetTenantId()
		rawPlanID = req.GetPlanId()
	}
	tenantID, err := parseTenantID(rawTenantID)
	if err != nil {
		writeAuditFailure(ctx, s.audit, auditResourceTenantPlan, action, map[string]any{"tenant_id": rawTenantID, "plan_id": rawPlanID}, err, nil)
		return nil, err
	}
	planID, err := parsePlanID(rawPlanID)
	if err != nil {
		writeAuditFailure(ctx, s.audit, auditResourceTenantPlan, action, map[string]any{"tenant_id": tenantID.String(), "plan_id": rawPlanID}, err, &tenantID)
		return nil, err
	}

	// 步骤 2：读套餐；不存在/已删 → 404；非 active → 422 PLAN_NOT_ACTIVE
	plan, err := s.plans.GetByID(ctx, planID)
	if err != nil {
		mapped := mapStoreError(err)
		writeAuditFailure(ctx, s.audit, auditResourceTenantPlan, action, map[string]any{"tenant_id": tenantID.String(), "plan_id": planID.String()}, mapped, &tenantID)
		return nil, mapped
	}
	if plan.Status != ports.TenantPlanStatusActive {
		err := businessError(codes.FailedPrecondition, ports.ErrPlanNotActive, "tenant plan status is "+string(plan.Status))
		writeAuditFailure(ctx, s.audit, auditResourceTenantPlan, action, map[string]any{
			"tenant_id": tenantID.String(),
			"plan_id":   planID.String(),
			"status":    string(plan.Status),
		}, err, &tenantID)
		return nil, err
	}

	// 步骤 3：经 Core 租户 API 读租户；不存在 → 404；disabled → 409 TENANT_STATE_INVALID
	tenant, err := s.tenants.GetTenant(ctx, tenantID)
	if err != nil {
		mapped := mapStoreError(err)
		writeAuditFailure(ctx, s.audit, auditResourceTenantPlan, action, map[string]any{"tenant_id": tenantID.String(), "plan_id": planID.String()}, mapped, &tenantID)
		return nil, mapped
	}
	if tenant.Status == ports.TenantStatusDisabled {
		err := businessError(codes.FailedPrecondition, ports.ErrTenantStateInvalid, "tenant is disabled")
		writeAuditFailure(ctx, s.audit, auditResourceTenantPlan, action, map[string]any{
			"tenant_id":           tenantID.String(),
			"tenant_name":         tenant.Name,
			"tenant_display_name": tenant.DisplayName,
			"plan_id":             planID.String(),
			"status":              string(tenant.Status),
		}, err, &tenantID)
		return nil, err
	}

	// 步骤 4：service 侧先校验套餐内维度均已启用（组装视图之前；不依赖 Core Upsert）
	rawLimits, err := s.plans.GetQuotaLimits(ctx, planID)
	if err != nil {
		mapped := mapStoreError(err)
		writeAuditFailure(ctx, s.audit, auditResourceTenantPlan, action, map[string]any{"tenant_id": tenantID.String(), "plan_id": planID.String()}, mapped, &tenantID)
		return nil, mapped
	}
	planDims := make([]string, 0, len(rawLimits))
	for _, lim := range rawLimits {
		planDims = append(planDims, lim.ResourceType)
	}
	if err := validateEnabledQuotaResourceTypes(ctx, s.quota, planDims); err != nil {
		writeAuditFailure(ctx, s.audit, auditResourceTenantPlan, action, map[string]any{"tenant_id": tenantID.String(), "plan_id": planID.String()}, err, &tenantID)
		return nil, err
	}

	// 步骤 5：组装套餐有效限额视图（store + Core meta；NULL total 回写 default）
	views, err := buildQuotaLimitViews(ctx, s.plans, s.quota, planID)
	if err != nil {
		writeAuditFailure(ctx, s.audit, auditResourceTenantPlan, action, map[string]any{"tenant_id": tenantID.String(), "plan_id": planID.String()}, err, &tenantID)
		return nil, err
	}

	// 步骤 6：plan_id 变更时经 Core 租户 API 更新；记下旧值以便配额失败回滚
	prevPlanID := tenant.PlanID
	planChanged := prevPlanID != planID
	if planChanged {
		if _, err := s.tenantPlans.UpdateTenantPlan(ctx, tenantID, planID); err != nil {
			mapped := mapStoreError(err)
			writeAuditFailure(ctx, s.audit, auditResourceTenantPlan, action, map[string]any{"tenant_id": tenantID.String(), "plan_id": planID.String()}, mapped, &tenantID)
			return nil, mapped
		}
	}

	// 步骤 7：同步套餐配额到该租户（跳过 approved；入口再校验启用后 Upsert）
	syncRes, err := syncPlanQuotaToTenant(ctx, s.plans, s.quota, tenantID, totalsFromQuotaViews(views), dimsFromQuotaViews(views))
	if err != nil {
		mapped := mapStoreError(err)
		// 步骤 7b：配额失败 → 回滚 plan_id（best-effort）
		rolledBack := false
		if planChanged {
			if _, rbErr := s.tenantPlans.UpdateTenantPlan(ctx, tenantID, prevPlanID); rbErr != nil {
				writeAuditFailure(ctx, s.audit, auditResourceTenantPlan, action, map[string]any{
					"tenant_id":           tenantID.String(),
					"tenant_name":         tenant.Name,
					"tenant_display_name": tenant.DisplayName,
					"plan_id":             planID.String(),
					"rollback_plan_id":    prevPlanID.String(),
					"items":               coreItemsForAudit(syncRes.Items),
					"rollback_error":      rbErr.Error(),
				}, mapped, &tenantID)
				return nil, mapped
			}
			rolledBack = true
		}
		writeAuditFailure(ctx, s.audit, auditResourceTenantPlan, action, map[string]any{
			"tenant_id":           tenantID.String(),
			"tenant_name":         tenant.Name,
			"tenant_display_name": tenant.DisplayName,
			"plan_id":             planID.String(),
			"items":               coreItemsForAudit(syncRes.Items),
			"rolled_back":         rolledBack,
		}, mapped, &tenantID)
		return nil, mapped
	}

	// 步骤 8：写成功审计（best-effort：失败只 Warn，不把已生效绑定变成错误）
	writeAuditSuccess(ctx, s.audit, auditResourceTenantPlan, action, map[string]any{
		"plan_id":             planID.String(),
		"tenant_id":           tenantID.String(),
		"tenant_name":         tenant.Name,
		"tenant_display_name": tenant.DisplayName,
		"skipped_approved":    len(syncRes.SkippedApproved),
		"tightened":           len(syncRes.Tightened),
		"updated":             syncRes.Updated,
	}, &tenantID)

	return &tenantv1.IdempotentResult{
		Id:      tenantID.String(),
		Message: "quota bound to plan",
	}, nil
}

// parseTenantID 校验并解析 tenant_id（必填 UUID）。
func parseTenantID(raw string) (uuid.UUID, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return uuid.Nil, businessError(codes.InvalidArgument, ports.ErrValidationFailed, "tenant_id required")
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, businessError(codes.InvalidArgument, ports.ErrValidationFailed, "tenant_id must be a uuid")
	}
	return id, nil
}

func (s *TenantService) ListAvailablePlans(ctx context.Context, req *tenantv1.ListAvailablePlansRequest) (*tenantv1.ListAvailablePlansResponse, error) {
	_ = ctx
	_ = req
	return nil, tenantRPCNotImplemented()
}

func (s *TenantService) CreateTenant(ctx context.Context, req *tenantv1.CreateTenantRequest) (*commonv1.IdempotentResult, error) {
	_ = ctx
	_ = req
	return nil, tenantRPCNotImplemented()
}

func (s *TenantService) ListTenants(ctx context.Context, req *tenantv1.ListTenantsRequest) (*tenantv1.ListTenantsResponse, error) {
	_ = ctx
	_ = req
	return nil, tenantRPCNotImplemented()
}

func (s *TenantService) GetTenantDetail(ctx context.Context, req *tenantv1.GetTenantDetailRequest) (*tenantv1.TenantDetail, error) {
	_ = ctx
	_ = req
	return nil, tenantRPCNotImplemented()
}

func (s *TenantService) UpdateTenant(ctx context.Context, req *tenantv1.UpdateTenantRequest) (*commonv1.IdempotentResult, error) {
	_ = ctx
	_ = req
	return nil, tenantRPCNotImplemented()
}

func (s *TenantService) FreezeTenant(ctx context.Context, req *tenantv1.FreezeTenantRequest) (*commonv1.IdempotentResult, error) {
	_ = ctx
	_ = req
	return nil, tenantRPCNotImplemented()
}

func (s *TenantService) UnfreezeTenant(ctx context.Context, req *tenantv1.UnfreezeTenantRequest) (*commonv1.IdempotentResult, error) {
	_ = ctx
	_ = req
	return nil, tenantRPCNotImplemented()
}

func (s *TenantService) DisableTenant(ctx context.Context, req *tenantv1.DisableTenantRequest) (*commonv1.IdempotentResult, error) {
	_ = ctx
	_ = req
	return nil, tenantRPCNotImplemented()
}

func (s *TenantService) GetTenantAuth(ctx context.Context, req *tenantv1.GetTenantAuthRequest) (*tenantv1.TenantAuthConfig, error) {
	_ = ctx
	_ = req
	return nil, tenantRPCNotImplemented()
}

func (s *TenantService) UpdateTenantSso(ctx context.Context, req *tenantv1.UpdateTenantSsoRequest) (*commonv1.IdempotentResult, error) {
	_ = ctx
	_ = req
	return nil, tenantRPCNotImplemented()
}

func (s *TenantService) TestTenantSso(ctx context.Context, req *tenantv1.TestTenantSsoRequest) (*tenantv1.SsoTestResult, error) {
	_ = ctx
	_ = req
	return nil, tenantRPCNotImplemented()
}

func (s *TenantService) UpdateTenantMfa(ctx context.Context, req *tenantv1.UpdateTenantMfaRequest) (*commonv1.IdempotentResult, error) {
	_ = ctx
	_ = req
	return nil, tenantRPCNotImplemented()
}

func (s *TenantService) GetTenantQuota(ctx context.Context, req *tenantv1.GetTenantQuotaRequest) (*tenantv1.GetTenantQuotaResponse, error) {
	_ = ctx
	_ = req
	return nil, tenantRPCNotImplemented()
}

func (s *TenantService) SubmitQuotaChangeRequest(ctx context.Context, req *tenantv1.SubmitQuotaChangeRequestRequest) (*commonv1.IdempotentResult, error) {
	_ = ctx
	_ = req
	return nil, tenantRPCNotImplemented()
}

func (s *TenantService) ListQuotaChangeRequests(ctx context.Context, req *tenantv1.ListQuotaChangeRequestsRequest) (*tenantv1.ListQuotaChangeRequestsResponse, error) {
	_ = ctx
	_ = req
	return nil, tenantRPCNotImplemented()
}

func (s *TenantService) ReviewQuotaChangeRequest(ctx context.Context, req *tenantv1.ReviewQuotaChangeRequestRequest) (*commonv1.IdempotentResult, error) {
	_ = ctx
	_ = req
	return nil, tenantRPCNotImplemented()
}

func (s *TenantService) ListTenantLifecycle(ctx context.Context, req *tenantv1.ListTenantLifecycleRequest) (*tenantv1.ListTenantLifecycleResponse, error) {
	_ = ctx
	_ = req
	return nil, tenantRPCNotImplemented()
}

func (s *TenantService) ListTenantAuditLogs(ctx context.Context, req *tenantv1.ListTenantAuditLogsRequest) (*tenantv1.ListTenantAuditLogsResponse, error) {
	_ = ctx
	_ = req
	return nil, tenantRPCNotImplemented()
}

func (s *TenantService) ListTenantAdmins(ctx context.Context, req *tenantv1.ListTenantAdminsRequest) (*tenantv1.ListTenantAdminsResponse, error) {
	_ = ctx
	_ = req
	return nil, tenantRPCNotImplemented()
}

func tenantRPCNotImplemented() error {
	return mapStoreError(ports.ErrNotImplemented)
}
