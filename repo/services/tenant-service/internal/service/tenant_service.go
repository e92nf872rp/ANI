package service

import (
	"context"
	"strings"

	"github.com/google/uuid"
	tenantv1 "github.com/kubercloud/ani/pkg/generated/pb/tenant/v1"
	"github.com/kubercloud/ani/services/tenant-service/internal/repo/ports"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

// TenantService 是 gRPC TenantService server。
// 目前承载绑定套餐 RPC：BindPlanQuota（US-009 / issue-009）。
type TenantService struct {
	// 嵌入未实现接口，确保 proto 新增 RPC 后本结构仍能向后兼容（栅栏模式）。
	tenantv1.UnimplementedTenantServiceServer

	plans       ports.TenantPlanStore      // 套餐 store（限额原始行；展示/下发经 Core ListQuotaMeta 组装）
	tenants     ports.TenantSvcClient      // Core 租户 API（GetTenant）
	tenantPlans ports.TenantPlanSvcClient  // Core 配额套餐绑定 API（UpdateTenantPlan）
	quota       ports.QuotaSvcClient       // Core 配额 API（Get/Put/Create/Upsert）
	audit       ports.TenantPlanAuditStore // 审计日志（配额套餐域）
}

// NewTenantService 构造租户 gRPC 服务实例。
func NewTenantService(plans ports.TenantPlanStore, tenants ports.TenantSvcClient, tenantPlans ports.TenantPlanSvcClient, quota ports.QuotaSvcClient, audit ports.TenantPlanAuditStore) *TenantService {
	return &TenantService{plans: plans, tenants: tenants, tenantPlans: tenantPlans, quota: quota, audit: audit}
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
		writeAuditFailure(ctx, s.audit, action, map[string]any{"tenant_id": rawTenantID, "plan_id": rawPlanID}, err, nil)
		return nil, err
	}
	planID, err := parsePlanID(rawPlanID)
	if err != nil {
		writeAuditFailure(ctx, s.audit, action, map[string]any{"tenant_id": tenantID.String(), "plan_id": rawPlanID}, err, &tenantID)
		return nil, err
	}

	// 步骤 2：读套餐；不存在/已删 → 404；非 active → 422 PLAN_NOT_ACTIVE
	plan, err := s.plans.GetByID(ctx, planID)
	if err != nil {
		mapped := mapStoreError(err)
		writeAuditFailure(ctx, s.audit, action, map[string]any{"tenant_id": tenantID.String(), "plan_id": planID.String()}, mapped, &tenantID)
		return nil, mapped
	}
	if plan.Status != ports.TenantPlanStatusActive {
		err := businessError(codes.FailedPrecondition, ports.ErrPlanNotActive, "tenant plan status is "+string(plan.Status))
		writeAuditFailure(ctx, s.audit, action, map[string]any{
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
		writeAuditFailure(ctx, s.audit, action, map[string]any{"tenant_id": tenantID.String(), "plan_id": planID.String()}, mapped, &tenantID)
		return nil, mapped
	}
	if tenant.Status == ports.TenantStatusDisabled {
		err := businessError(codes.FailedPrecondition, ports.ErrTenantStateInvalid, "tenant is disabled")
		writeAuditFailure(ctx, s.audit, action, map[string]any{
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
		writeAuditFailure(ctx, s.audit, action, map[string]any{"tenant_id": tenantID.String(), "plan_id": planID.String()}, mapped, &tenantID)
		return nil, mapped
	}
	planDims := make([]string, 0, len(rawLimits))
	for _, lim := range rawLimits {
		planDims = append(planDims, lim.ResourceType)
	}
	if err := validateEnabledQuotaResourceTypes(ctx, s.quota, planDims); err != nil {
		writeAuditFailure(ctx, s.audit, action, map[string]any{"tenant_id": tenantID.String(), "plan_id": planID.String()}, err, &tenantID)
		return nil, err
	}

	// 步骤 5：组装套餐有效限额视图（store + Core meta；NULL total 回写 default）
	views, err := buildQuotaLimitViews(ctx, s.plans, s.quota, planID)
	if err != nil {
		writeAuditFailure(ctx, s.audit, action, map[string]any{"tenant_id": tenantID.String(), "plan_id": planID.String()}, err, &tenantID)
		return nil, err
	}

	// 步骤 6：plan_id 变更时经 Core 租户 API 更新；记下旧值以便配额失败回滚
	prevPlanID := tenant.PlanID
	planChanged := prevPlanID != planID
	if planChanged {
		if _, err := s.tenantPlans.UpdateTenantPlan(ctx, tenantID, planID); err != nil {
			mapped := mapStoreError(err)
			writeAuditFailure(ctx, s.audit, action, map[string]any{"tenant_id": tenantID.String(), "plan_id": planID.String()}, mapped, &tenantID)
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
				writeAuditFailure(ctx, s.audit, action, map[string]any{
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
		writeAuditFailure(ctx, s.audit, action, map[string]any{
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
	writeAuditSuccess(ctx, s.audit, action, map[string]any{
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
