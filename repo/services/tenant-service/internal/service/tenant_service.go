package service

import (
	"context"
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"unicode"

	"github.com/google/uuid"
	commonv1 "github.com/kubercloud/ani/pkg/generated/pb/common/v1"
	tenantv1 "github.com/kubercloud/ani/pkg/generated/pb/tenant/v1"
	"github.com/kubercloud/ani/services/tenant-service/internal/repo/ports"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"
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

// auditResourceTenant 审计资源类型：租户
const auditResourceTenant = "tenant"

// tenantNamePattern 租户名称正则
var (
	tenantNamePattern = regexp.MustCompile(`^[a-z0-9-]{3,40}$`)
)

// ListAvailablePlans 返回 status=active 套餐摘要（供创建向导 Step2；不分页、不调 Core）。
func (s *TenantService) ListAvailablePlans(ctx context.Context, req *tenantv1.ListAvailablePlansRequest) (*tenantv1.ListAvailablePlansResponse, error) {
	_ = req
	// 步骤 1：依赖校验
	if s.plans == nil {
		return nil, businessError(codes.Unavailable, ports.ErrStoreUnavailable, "tenant plan store unavailable")
	}
	// 步骤 2：经 store.ListActivePlans 一次取齐全部 active 套餐（不分页）
	listed, err := s.plans.ListActivePlans(ctx)
	if err != nil {
		return nil, mapStoreError(err)
	}
	// 步骤 3：组装 proto AvailableTenantPlan（仅 id/code/name）；空列表合法
	items := make([]*tenantv1.AvailableTenantPlan, 0, len(listed))
	for _, p := range listed {
		items = append(items, &tenantv1.AvailableTenantPlan{
			Id:   p.ID.String(),
			Code: p.Code,
			Name: p.Name,
		})
	}
	return &tenantv1.ListAvailablePlansResponse{Items: items}, nil
}

// CreateTenant 编排创建租户：校验 → 套餐 → bcrypt → Core 事务 → 事务外配额初始化 → 审计。
func (s *TenantService) CreateTenant(ctx context.Context, req *tenantv1.CreateTenantRequest) (*commonv1.IdempotentResult, error) {
	const action = "tenant.create"

	// 步骤 1：请求体必填
	if req == nil {
		err := businessError(codes.InvalidArgument, ports.ErrValidationFailed, "request required")
		writeAuditFailure(ctx, s.audit, auditResourceTenant, action, nil, err, nil)
		return nil, err
	}

	// 步骤 2：入参校验（name 正则 / email / admin_password 强度）→ 400 VALIDATION_FAILED
	name, displayName, contactEmail, adminEmail, adminName, err := validateCreateTenantInput(
		req.GetName(), req.GetDisplayName(), req.GetEmail(),
		req.GetAdminEmail(), req.GetAdminName(), req.GetAdminPassword(),
	)
	if err != nil {
		writeAuditFailure(ctx, s.audit, auditResourceTenant, action, map[string]any{
			"name": name, "email": contactEmail, "admin_email": adminEmail,
		}, err, nil)
		return nil, err
	}
	planID, err := parsePlanID(req.GetPlanId())
	if err != nil {
		writeAuditFailure(ctx, s.audit, auditResourceTenant, action, map[string]any{"plan_id": req.GetPlanId()}, err, nil)
		return nil, err
	}

	// 步骤 3：套餐存在且 status=active；否则 404 / 422 PLAN_NOT_ACTIVE
	plan, err := s.plans.GetByID(ctx, planID)
	if err != nil {
		mapped := mapStoreError(err)
		writeAuditFailure(ctx, s.audit, auditResourceTenant, action, map[string]any{"plan_id": planID.String()}, mapped, nil)
		return nil, mapped
	}
	if plan.Status != ports.TenantPlanStatusActive {
		err := businessError(codes.FailedPrecondition, ports.ErrPlanNotActive, "tenant plan status is "+string(plan.Status))
		writeAuditFailure(ctx, s.audit, auditResourceTenant, action, map[string]any{
			"plan_id": planID.String(), "status": string(plan.Status),
		}, err, nil)
		return nil, err
	}

	// 步骤 4：组装套餐配额视图（plan_quota_limits + Core meta default_quota 兜底）
	views, err := buildQuotaLimitViews(ctx, s.plans, s.quota, planID)
	if err != nil {
		writeAuditFailure(ctx, s.audit, auditResourceTenant, action, map[string]any{"plan_id": planID.String()}, err, nil)
		return nil, err
	}

	// 步骤 5：bcrypt(admin_password, 12)；明文不出 service 边界
	hash, err := bcrypt.GenerateFromPassword([]byte(req.GetAdminPassword()), 12)
	if err != nil {
		mapped := businessError(codes.Internal, ports.ErrValidationFailed, "password hash failed")
		writeAuditFailure(ctx, s.audit, auditResourceTenant, action, map[string]any{"name": name}, mapped, nil)
		return nil, mapped
	}

	// 步骤 6：经 Core SDK 创建租户（事务内 5 表）；name UNIQUE → 409 TENANT_NAME_CONFLICT
	// request_id / actor 由 SDK 从 gRPC metadata 统一透传 Headers，Core Gateway 注入 ctx。
	created, err := s.tenants.CreateTenant(ctx, ports.CreateTenantInput{
		Name:              name,
		DisplayName:       displayName,
		ContactEmail:      contactEmail,
		PlanID:            planID,
		AdminEmail:        adminEmail,
		AdminName:         adminName,
		AdminPasswordHash: string(hash),
	})
	if err != nil {
		mapped := mapStoreError(err)
		writeAuditFailure(ctx, s.audit, auditResourceTenant, action, map[string]any{
			"name": name, "email": contactEmail, "admin_email": adminEmail, "username": adminName,
			"plan_id": planID.String(),
		}, mapped, nil)
		return nil, mapped
	}

	// 步骤 7：事务外配额初始化；失败不回滚租户，审计 failure + 异步重试（1s/2s/4s）
	totals := totalsFromQuotaViews(views)
	dims := dimsFromQuotaViews(views)
	syncRes, syncErr := syncPlanQuotaToTenant(ctx, s.plans, s.quota, created.ID, totals, dims)
	if syncErr != nil {
		mapped := mapStoreError(syncErr)
		writeAuditFailure(ctx, s.audit, auditResourceTenant, "tenant.quota_init_failed", map[string]any{
			"tenant_id": created.ID.String(),
			"plan_id":   planID.String(),
			"items":     coreItemsForAudit(syncRes.Items),
		}, mapped, &created.ID)
		scheduleQuotaSyncRetry(s.audit, s.plans, s.quota, planID, created.ID, totals, dims)
	}

	// 步骤 8：成功审计（details 含 email/username，不含密码）+ 返回 { id, message }
	writeAuditSuccess(ctx, s.audit, auditResourceTenant, action, map[string]any{
		"tenant_id":     created.ID.String(),
		"name":          created.Name,
		"email":         contactEmail,
		"admin_email":   adminEmail,
		"username":      adminName,
		"plan_id":       planID.String(),
		"quota_init_ok": syncErr == nil,
		"quota_updated": syncRes.Updated,
		"quota_skipped": syncRes.SkippedApproved,
	}, &created.ID)

	return &commonv1.IdempotentResult{
		Id:      created.ID.String(),
		Message: "tenant created",
	}, nil
}

func validateCreateTenantInput(
	rawName, rawDisplay, rawEmail, rawAdminEmail, rawAdminName, rawPassword string,
) (name, displayName, contactEmail, adminEmail, adminName string, err error) {
	// 步骤 1：trim 入参
	name = strings.TrimSpace(rawName)
	displayName = strings.TrimSpace(rawDisplay)
	contactEmail = strings.TrimSpace(rawEmail)
	adminEmail = strings.TrimSpace(rawAdminEmail)
	adminName = strings.TrimSpace(rawAdminName)

	// 步骤 2：逐字段校验
	if !tenantNamePattern.MatchString(name) {
		return name, displayName, contactEmail, adminEmail, adminName,
			businessError(codes.InvalidArgument, ports.ErrValidationFailed, "name must match ^[a-z0-9-]{3,40}$")
	}
	if displayName == "" || len(displayName) > 128 {
		return name, displayName, contactEmail, adminEmail, adminName,
			businessError(codes.InvalidArgument, ports.ErrValidationFailed, "display_name required (1-128)")
	}
	if !validEmail(contactEmail) {
		return name, displayName, contactEmail, adminEmail, adminName,
			businessError(codes.InvalidArgument, ports.ErrValidationFailed, "email invalid")
	}
	if !validEmail(adminEmail) {
		return name, displayName, contactEmail, adminEmail, adminName,
			businessError(codes.InvalidArgument, ports.ErrValidationFailed, "admin_email invalid")
	}
	if adminName == "" || len(adminName) > 128 {
		return name, displayName, contactEmail, adminEmail, adminName,
			businessError(codes.InvalidArgument, ports.ErrValidationFailed, "admin_name required (1-128)")
	}
	// 步骤 3：密码强度（8-64 且 ≥3 类）
	if err := validateAdminPassword(rawPassword); err != nil {
		return name, displayName, contactEmail, adminEmail, adminName, err
	}
	return name, displayName, contactEmail, adminEmail, adminName, nil
}

func validEmail(raw string) bool {
	if raw == "" || len(raw) > 254 {
		return false
	}
	addr, err := mail.ParseAddress(raw)
	return err == nil && addr.Address == raw
}

// validateAdminPassword：8-64 字符，且至少满足大写/小写/数字/特殊 四类中的三类。
func validateAdminPassword(password string) error {
	// 步骤 1：长度边界
	n := len(password)
	if n < 8 || n > 64 {
		return businessError(codes.InvalidArgument, ports.ErrValidationFailed, "admin_password must be 8-64 characters")
	}
	// 步骤 2：统计字符类别
	var upper, lower, digit, special bool
	for _, r := range password {
		switch {
		case unicode.IsUpper(r):
			upper = true
		case unicode.IsLower(r):
			lower = true
		case unicode.IsDigit(r):
			digit = true
		case unicode.IsPunct(r) || unicode.IsSymbol(r):
			special = true
		}
	}
	classes := 0
	for _, ok := range []bool{upper, lower, digit, special} {
		if ok {
			classes++
		}
	}
	// 步骤 3：至少三类
	if classes < 3 {
		return businessError(codes.InvalidArgument, ports.ErrValidationFailed, "admin_password must include at least 3 of upper/lower/digit/special")
	}
	return nil
}

func (s *TenantService) ListTenants(ctx context.Context, req *tenantv1.ListTenantsRequest) (*tenantv1.ListTenantsResponse, error) {
	if req == nil {
		req = &tenantv1.ListTenantsRequest{}
	}
	// 步骤 1：依赖校验
	if s.tenants == nil {
		return nil, businessError(codes.Unavailable, ports.ErrStoreUnavailable, "tenant client unavailable")
	}
	if s.plans == nil {
		return nil, businessError(codes.Unavailable, ports.ErrStoreUnavailable, "tenant plan store unavailable")
	}
	// 步骤 2：status 枚举（空=全部）
	status, err := ports.ParseTenantStatusFilter(req.GetStatus())
	if err != nil {
		return nil, mapStoreError(err)
	}
	// 步骤 3：游标分页入参
	limit := 20
	cursor := ""
	if page := req.GetPage(); page != nil {
		if page.GetLimit() > 0 {
			limit = int(page.GetLimit())
		}
		cursor = page.GetCursor()
	}
	// 步骤 4：经 Core 列表（含 admin_count）
	listed, err := s.tenants.ListTenants(ctx, ports.ListTenantsFilter{
		Limit:  limit,
		Cursor: cursor,
		Status: status,
		Search: strings.TrimSpace(req.GetSearch()),
	})
	if err != nil {
		return nil, mapStoreError(err)
	}
	// 步骤 5：批量装配 plan_code
	planIDs := make([]uuid.UUID, 0, len(listed.Items))
	seen := make(map[uuid.UUID]struct{}, len(listed.Items))
	for _, it := range listed.Items {
		if _, ok := seen[it.PlanID]; ok || it.PlanID == uuid.Nil {
			continue
		}
		seen[it.PlanID] = struct{}{}
		planIDs = append(planIDs, it.PlanID)
	}
	codes, err := s.plans.MapPlanCodes(ctx, planIDs)
	if err != nil {
		return nil, mapStoreError(err)
	}
	// 步骤 6：组装 proto 列表项
	items := make([]*tenantv1.TenantListItem, 0, len(listed.Items))
	for _, it := range listed.Items {
		items = append(items, &tenantv1.TenantListItem{
			Id:          it.ID.String(),
			Name:        it.Name,
			DisplayName: it.DisplayName,
			PlanId:      it.PlanID.String(),
			PlanCode:    codes[it.PlanID],
			Status:      string(it.Status),
			AdminCount:  it.AdminCount,
			CreatedAt:   timestamppb.New(it.CreatedAt.UTC()),
		})
	}
	return &tenantv1.ListTenantsResponse{Items: items, NextCursor: listed.NextCursor}, nil
}

// GetTenantDetail 经 Core GetTenant 返回完整租户详情（含 counts / auth 摘要 / plan_code）。
func (s *TenantService) GetTenantDetail(ctx context.Context, req *tenantv1.GetTenantDetailRequest) (*tenantv1.TenantDetail, error) {
	// 步骤 1：校验 tenant_id
	rawID := ""
	if req != nil {
		rawID = req.GetTenantId()
	}
	tenantID, err := parseTenantID(rawID)
	if err != nil {
		return nil, err
	}
	if s.tenants == nil {
		return nil, businessError(codes.Unavailable, ports.ErrStoreUnavailable, "tenant client unavailable")
	}
	// 步骤 2：经 Core SDK 查询租户（含 user_count/admin_count/auth）
	t, err := s.tenants.GetTenant(ctx, tenantID)
	if err != nil {
		return nil, mapStoreError(err)
	}
	// 步骤 3：装配 plan_code（批量接口单 id）
	planCode := ""
	if s.plans != nil && t.PlanID != uuid.Nil {
		codes, mapErr := s.plans.MapPlanCodes(ctx, []uuid.UUID{t.PlanID})
		if mapErr != nil {
			return nil, mapStoreError(mapErr)
		}
		planCode = codes[t.PlanID]
	}
	// 步骤 4：组装详情（auth 缺省双 false）
	auth := &tenantv1.TenantAuthSummary{}
	if t.Auth != nil {
		auth.SsoEnabled = t.Auth.SsoEnabled
		auth.MfaRequired = t.Auth.MfaRequired
	}
	out := &tenantv1.TenantDetail{
		Id:          t.ID.String(),
		Name:        t.Name,
		DisplayName: t.DisplayName,
		PlanId:      t.PlanID.String(),
		PlanCode:    planCode,
		Status:      string(t.Status),
		UserCount:   t.UserCount,
		AdminCount:  t.AdminCount,
		Auth:        auth,
		CreatedAt:   timestamppb.New(t.CreatedAt.UTC()),
		UpdatedAt:   timestamppb.New(t.UpdatedAt.UTC()),
	}
	if strings.TrimSpace(t.ContactEmail) != "" {
		out.ContactEmail = wrapperspb.String(t.ContactEmail)
	}
	if t.FrozenAt != nil {
		out.FrozenAt = timestamppb.New(t.FrozenAt.UTC())
	}
	if t.DisabledAt != nil {
		out.DisabledAt = timestamppb.New(t.DisabledAt.UTC())
	}
	return out, nil
}

func (s *TenantService) UpdateTenant(ctx context.Context, req *tenantv1.UpdateTenantRequest) (*commonv1.IdempotentResult, error) {
	const action = "tenant.update"

	// 步骤 1：依赖与 tenant_id
	if s.tenants == nil {
		err := businessError(codes.Unavailable, ports.ErrStoreUnavailable, "tenant client unavailable")
		writeAuditFailure(ctx, s.audit, auditResourceTenant, action, nil, err, nil)
		return nil, err
	}
	rawID := ""
	if req != nil {
		rawID = req.GetTenantId()
	}
	tenantID, err := parseTenantID(rawID)
	if err != nil {
		writeAuditFailure(ctx, s.audit, auditResourceTenant, action, map[string]any{"tenant_id": rawID}, err, nil)
		return nil, err
	}

	// 步骤 2：部分更新映射；两者均未传 → 400 VALIDATION_FAILED（空更新拒绝）
	in := ports.UpdateTenantInput{}
	if req != nil && req.DisplayName != nil {
		v := strings.TrimSpace(req.DisplayName.GetValue())
		if v == "" || len(v) > 128 {
			err := businessError(codes.InvalidArgument, ports.ErrValidationFailed, "display_name required (1-128)")
			writeAuditFailure(ctx, s.audit, auditResourceTenant, action, map[string]any{"tenant_id": tenantID.String()}, err, &tenantID)
			return nil, err
		}
		in.DisplayName = &v
	}
	if req != nil && req.ContactEmail != nil {
		v := strings.TrimSpace(req.ContactEmail.GetValue())
		if !validEmail(v) {
			err := businessError(codes.InvalidArgument, ports.ErrValidationFailed, "contact_email invalid")
			writeAuditFailure(ctx, s.audit, auditResourceTenant, action, map[string]any{"tenant_id": tenantID.String()}, err, &tenantID)
			return nil, err
		}
		in.ContactEmail = &v
	}
	if in.DisplayName == nil && in.ContactEmail == nil {
		err := businessError(codes.InvalidArgument, ports.ErrValidationFailed, "display_name or contact_email required")
		writeAuditFailure(ctx, s.audit, auditResourceTenant, action, map[string]any{"tenant_id": tenantID.String()}, err, &tenantID)
		return nil, err
	}

	// 步骤 3：读当前租户；不存在 → 404；disabled → 409 TENANT_STATE_INVALID
	before, err := s.tenants.GetTenant(ctx, tenantID)
	if err != nil {
		mapped := mapStoreError(err)
		writeAuditFailure(ctx, s.audit, auditResourceTenant, action, map[string]any{"tenant_id": tenantID.String()}, mapped, &tenantID)
		return nil, mapped
	}
	if before.Status == ports.TenantStatusDisabled {
		err := businessError(codes.FailedPrecondition, ports.ErrTenantStateInvalid, "tenant is disabled")
		writeAuditFailure(ctx, s.audit, auditResourceTenant, action, map[string]any{
			"tenant_id": tenantID.String(),
			"status":    string(before.Status),
		}, err, &tenantID)
		return nil, err
	}

	// 步骤 4：经 Core 部分更新（不触碰 name / status）
	after, err := s.tenants.UpdateTenant(ctx, tenantID, in)
	if err != nil {
		mapped := mapStoreError(err)
		writeAuditFailure(ctx, s.audit, auditResourceTenant, action, map[string]any{"tenant_id": tenantID.String()}, mapped, &tenantID)
		return nil, mapped
	}

	// 步骤 5：成功审计（变更前后非敏感字段）
	writeAuditSuccess(ctx, s.audit, auditResourceTenant, action, map[string]any{
		"tenant_id": tenantID.String(),
		"before": map[string]any{
			"display_name":  before.DisplayName,
			"contact_email": before.ContactEmail,
		},
		"after": map[string]any{
			"display_name":  after.DisplayName,
			"contact_email": after.ContactEmail,
		},
	}, &tenantID)

	return &commonv1.IdempotentResult{
		Id:      tenantID.String(),
		Message: "tenant updated",
	}, nil
}

func (s *TenantService) FreezeTenant(ctx context.Context, req *tenantv1.FreezeTenantRequest) (*commonv1.IdempotentResult, error) {
	rawID := ""
	if req != nil {
		rawID = req.GetTenantId()
	}
	return s.transitionTenantState(ctx, "tenant.freeze", "tenant frozen", rawID,
		func(ctx context.Context, tenantID uuid.UUID) (ports.Tenant, error) {
			return s.tenants.FreezeTenant(ctx, tenantID)
		})
}

func (s *TenantService) UnfreezeTenant(ctx context.Context, req *tenantv1.UnfreezeTenantRequest) (*commonv1.IdempotentResult, error) {
	rawID := ""
	if req != nil {
		rawID = req.GetTenantId()
	}
	return s.transitionTenantState(ctx, "tenant.unfreeze", "tenant unfrozen", rawID,
		func(ctx context.Context, tenantID uuid.UUID) (ports.Tenant, error) {
			return s.tenants.UnfreezeTenant(ctx, tenantID)
		})
}

func (s *TenantService) DisableTenant(ctx context.Context, req *tenantv1.DisableTenantRequest) (*commonv1.IdempotentResult, error) {
	const action = "tenant.disable"

	if s.tenants == nil {
		err := businessError(codes.Unavailable, ports.ErrStoreUnavailable, "tenant client unavailable")
		writeAuditFailure(ctx, s.audit, auditResourceTenant, action, nil, err, nil)
		return nil, err
	}
	if s.quota == nil {
		err := businessError(codes.Unavailable, ports.ErrStoreUnavailable, "quota client unavailable")
		writeAuditFailure(ctx, s.audit, auditResourceTenant, action, nil, err, nil)
		return nil, err
	}

	rawID := ""
	if req != nil {
		rawID = req.GetTenantId()
	}
	tenantID, err := parseTenantID(rawID)
	if err != nil {
		writeAuditFailure(ctx, s.audit, auditResourceTenant, action, map[string]any{"tenant_id": rawID}, err, nil)
		return nil, err
	}

	// 步骤 1：禁用前置 — 仅 gpu/cpu/memory/storage 四维 used+reserved>0 拒绝（其余维度忽略；不释放资源）
	items, err := s.quota.GetQuota(ctx, tenantID)
	if err != nil {
		mapped := mapStoreError(err)
		writeAuditFailure(ctx, s.audit, auditResourceTenant, action, map[string]any{"tenant_id": tenantID.String()}, mapped, &tenantID)
		return nil, mapped
	}
	if blocked, dim := disableBlockedByComputeQuota(items); blocked {
		err := businessError(codes.FailedPrecondition, ports.ErrTenantHasRunningResources,
			fmt.Sprintf("%s used+reserved > 0", dim))
		writeAuditFailure(ctx, s.audit, auditResourceTenant, action, map[string]any{
			"tenant_id":   tenantID.String(),
			"blocked_dim": dim,
			"quota_dims":  quotaUsedSnapshot(items),
		}, err, &tenantID)
		return nil, err
	}

	// 步骤 2：读转换前状态 → Core disable → 审计
	before, err := s.tenants.GetTenant(ctx, tenantID)
	if err != nil {
		mapped := mapStoreError(err)
		writeAuditFailure(ctx, s.audit, auditResourceTenant, action, map[string]any{"tenant_id": tenantID.String()}, mapped, &tenantID)
		return nil, mapped
	}
	after, err := s.tenants.DisableTenant(ctx, tenantID)
	if err != nil {
		mapped := mapStoreError(err)
		writeAuditFailure(ctx, s.audit, auditResourceTenant, action, map[string]any{
			"tenant_id":     tenantID.String(),
			"before_status": string(before.Status),
		}, mapped, &tenantID)
		return nil, mapped
	}

	writeAuditSuccess(ctx, s.audit, auditResourceTenant, action, map[string]any{
		"tenant_id":     tenantID.String(),
		"before_status": string(before.Status),
		"after_status":  string(after.Status),
	}, &tenantID)

	return &commonv1.IdempotentResult{Id: tenantID.String(), Message: "tenant disabled"}, nil
}

// transitionTenantState 编排 freeze/unfreeze：读 before → Core 转换 → 审计前后状态。
func (s *TenantService) transitionTenantState(
	ctx context.Context,
	action, successMsg, rawTenantID string,
	fn func(context.Context, uuid.UUID) (ports.Tenant, error),
) (*commonv1.IdempotentResult, error) {
	if s.tenants == nil {
		err := businessError(codes.Unavailable, ports.ErrStoreUnavailable, "tenant client unavailable")
		writeAuditFailure(ctx, s.audit, auditResourceTenant, action, nil, err, nil)
		return nil, err
	}
	tenantID, err := parseTenantID(rawTenantID)
	if err != nil {
		writeAuditFailure(ctx, s.audit, auditResourceTenant, action, map[string]any{"tenant_id": rawTenantID}, err, nil)
		return nil, err
	}

	before, err := s.tenants.GetTenant(ctx, tenantID)
	if err != nil {
		mapped := mapStoreError(err)
		writeAuditFailure(ctx, s.audit, auditResourceTenant, action, map[string]any{"tenant_id": tenantID.String()}, mapped, &tenantID)
		return nil, mapped
	}

	after, err := fn(ctx, tenantID)
	if err != nil {
		mapped := mapStoreError(err)
		writeAuditFailure(ctx, s.audit, auditResourceTenant, action, map[string]any{
			"tenant_id":     tenantID.String(),
			"before_status": string(before.Status),
		}, mapped, &tenantID)
		return nil, mapped
	}

	writeAuditSuccess(ctx, s.audit, auditResourceTenant, action, map[string]any{
		"tenant_id":     tenantID.String(),
		"before_status": string(before.Status),
		"after_status":  string(after.Status),
	}, &tenantID)

	return &commonv1.IdempotentResult{Id: tenantID.String(), Message: successMsg}, nil
}

// disableQuotaGuardDims 禁用前置校验的计算/存储维度（其余维度暂不参与）。
var disableQuotaGuardDims = map[string]struct{}{
	"gpu_count":  {},
	"cpu_core":   {},
	"memory_gb":  {},
	"storage_gb": {},
}

func disableBlockedByComputeQuota(items []ports.CoreQuotaResult) (blocked bool, dim string) {
	for _, it := range items {
		if _, ok := disableQuotaGuardDims[it.ResourceType]; !ok {
			continue
		}
		if it.Used+it.Reserved > 0 {
			return true, it.ResourceType
		}
	}
	return false, ""
}

func quotaUsedSnapshot(items []ports.CoreQuotaResult) map[string]any {
	out := make(map[string]any, len(items))
	for _, it := range items {
		out[it.ResourceType] = map[string]int64{
			"used":     it.Used,
			"reserved": it.Reserved,
		}
	}
	return out
}

func (s *TenantService) GetTenantAuth(ctx context.Context, req *tenantv1.GetTenantAuthRequest) (*tenantv1.TenantAuthConfig, error) {
	rawID := ""
	if req != nil {
		rawID = req.GetTenantId()
	}
	tenantID, err := parseTenantID(rawID)
	if err != nil {
		return nil, err
	}
	if s.tenants == nil {
		return nil, businessError(codes.Unavailable, ports.ErrStoreUnavailable, "tenant client unavailable")
	}
	auth, err := s.tenants.GetTenantAuth(ctx, tenantID)
	if err != nil {
		return nil, mapStoreError(err)
	}
	return toProtoTenantAuthConfig(auth), nil
}

func (s *TenantService) UpdateTenantSso(ctx context.Context, req *tenantv1.UpdateTenantSsoRequest) (*commonv1.IdempotentResult, error) {
	const action = "tenant.sso.update"

	if s.tenants == nil {
		err := businessError(codes.Unavailable, ports.ErrStoreUnavailable, "tenant client unavailable")
		writeAuditFailure(ctx, s.audit, auditResourceTenant, action, nil, err, nil)
		return nil, err
	}
	rawID := ""
	if req != nil {
		rawID = req.GetTenantId()
	}
	tenantID, err := parseTenantID(rawID)
	if err != nil {
		writeAuditFailure(ctx, s.audit, auditResourceTenant, action, map[string]any{"tenant_id": rawID}, err, nil)
		return nil, err
	}

	// 步骤 1：至少提供一个 SSO 字段（部分更新）
	hasEnabled := req != nil && req.SsoEnabled != nil
	hasProvider := req != nil && req.Provider != nil
	if !hasEnabled && !hasProvider {
		err := businessError(codes.InvalidArgument, ports.ErrValidationFailed, "sso_enabled or provider required")
		writeAuditFailure(ctx, s.audit, auditResourceTenant, action, map[string]any{"tenant_id": tenantID.String()}, err, &tenantID)
		return nil, err
	}

	// 步骤 2：读当前配置并计算更新后有效值。
	// provider 仅在更新后 sso_enabled=true 时必填非空（可沿用原 provider）；
	// sso_enabled=false（关闭）时 provider 非必传，可省略或一并清空。
	// disabled 终态由 Core UpdateTenantAuth 拒绝（TENANT_STATE_INVALID → 409）。
	before, err := s.tenants.GetTenantAuth(ctx, tenantID)
	if err != nil {
		mapped := mapStoreError(err)
		writeAuditFailure(ctx, s.audit, auditResourceTenant, action, map[string]any{"tenant_id": tenantID.String()}, mapped, &tenantID)
		return nil, mapped
	}
	effectiveEnabled := before.SsoEnabled
	if hasEnabled {
		effectiveEnabled = req.SsoEnabled.GetValue()
	}
	effectiveProvider := ""
	if before.SsoProvider != nil {
		effectiveProvider = strings.TrimSpace(*before.SsoProvider)
	}
	if hasProvider {
		effectiveProvider = strings.TrimSpace(req.Provider.GetValue())
	}
	if effectiveEnabled && effectiveProvider == "" {
		err := businessError(codes.FailedPrecondition, ports.ErrTenantSsoConfigInvalid, "sso enabled requires provider")
		writeAuditFailure(ctx, s.audit, auditResourceTenant, action, map[string]any{
			"tenant_id":   tenantID.String(),
			"sso_enabled": effectiveEnabled,
			"provider":    effectiveProvider,
		}, err, &tenantID)
		return nil, err
	}

	// 步骤 3：映射 Core 部分更新
	patch := ports.TenantAuthPatch{}
	if hasEnabled {
		v := req.SsoEnabled.GetValue()
		patch.SsoEnabled = &v
	}
	if hasProvider {
		v := strings.TrimSpace(req.Provider.GetValue())
		patch.SsoProvider = &v
	}
	after, err := s.tenants.UpdateTenantAuth(ctx, tenantID, patch)
	if err != nil {
		mapped := mapStoreError(err)
		writeAuditFailure(ctx, s.audit, auditResourceTenant, action, map[string]any{"tenant_id": tenantID.String()}, mapped, &tenantID)
		return nil, mapped
	}

	providerOut := ""
	if after.SsoProvider != nil {
		providerOut = *after.SsoProvider
	}
	writeAuditSuccess(ctx, s.audit, auditResourceTenant, action, map[string]any{
		"tenant_id":   tenantID.String(),
		"sso_enabled": after.SsoEnabled,
		"provider":    providerOut,
	}, &tenantID)
	return &commonv1.IdempotentResult{Id: tenantID.String(), Message: "tenant sso updated"}, nil
}

func (s *TenantService) UpdateTenantMfa(ctx context.Context, req *tenantv1.UpdateTenantMfaRequest) (*commonv1.IdempotentResult, error) {
	const action = "tenant.mfa.update"

	if s.tenants == nil {
		err := businessError(codes.Unavailable, ports.ErrStoreUnavailable, "tenant client unavailable")
		writeAuditFailure(ctx, s.audit, auditResourceTenant, action, nil, err, nil)
		return nil, err
	}
	rawID := ""
	if req != nil {
		rawID = req.GetTenantId()
	}
	tenantID, err := parseTenantID(rawID)
	if err != nil {
		writeAuditFailure(ctx, s.audit, auditResourceTenant, action, map[string]any{"tenant_id": rawID}, err, nil)
		return nil, err
	}
	if req == nil {
		err := businessError(codes.InvalidArgument, ports.ErrValidationFailed, "mfa_required required")
		writeAuditFailure(ctx, s.audit, auditResourceTenant, action, map[string]any{"tenant_id": tenantID.String()}, err, &tenantID)
		return nil, err
	}

	// disabled 终态由 Core UpdateTenantAuth 拒绝（TENANT_STATE_INVALID → 409）。
	mfa := req.GetMfaRequired()
	after, err := s.tenants.UpdateTenantAuth(ctx, tenantID, ports.TenantAuthPatch{MfaRequired: &mfa})
	if err != nil {
		mapped := mapStoreError(err)
		writeAuditFailure(ctx, s.audit, auditResourceTenant, action, map[string]any{
			"tenant_id":    tenantID.String(),
			"mfa_required": mfa,
		}, mapped, &tenantID)
		return nil, mapped
	}

	writeAuditSuccess(ctx, s.audit, auditResourceTenant, action, map[string]any{
		"tenant_id":    tenantID.String(),
		"mfa_required": after.MfaRequired,
	}, &tenantID)
	return &commonv1.IdempotentResult{Id: tenantID.String(), Message: "tenant mfa updated"}, nil
}

func toProtoTenantAuthConfig(auth ports.TenantAuth) *tenantv1.TenantAuthConfig {
	out := &tenantv1.TenantAuthConfig{
		SsoEnabled:  auth.SsoEnabled,
		MfaRequired: auth.MfaRequired,
		UpdatedAt:   timestamppb.New(auth.UpdatedAt.UTC()),
	}
	if auth.SsoProvider != nil && strings.TrimSpace(*auth.SsoProvider) != "" {
		out.Provider = wrapperspb.String(strings.TrimSpace(*auth.SsoProvider))
	}
	return out
}

func (s *TenantService) TestTenantSso(ctx context.Context, req *tenantv1.TestTenantSsoRequest) (*tenantv1.SsoTestResult, error) {
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
