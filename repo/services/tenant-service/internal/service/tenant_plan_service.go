package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	tenantv1 "github.com/kubercloud/ani/pkg/generated/pb/tenant/v1"
	"github.com/kubercloud/ani/services/tenant-service/internal/repo/ports"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// TenantPlanService 是配额套餐域的 gRPC 服务实现。
// 网关（ani-gateway）经 TenantPlanServiceClient 转发 /api/v1/svc/tenant-plans*；
// 本服务负责入参校验、调用 store/Core，并把领域错误映射为「业务码: 详情」形式的 gRPC status，
// 供网关 mapTenantPlanError 还原为 HTTP 状态与 ErrorResponse.code。
type TenantPlanService struct {
	// 嵌入未实现接口：proto 新增 RPC 时本结构仍可编译（栅栏模式）。
	tenantv1.UnimplementedTenantPlanServiceServer

	plans   ports.TenantPlanStore      // 套餐 + plan_quota_limits 持久化
	audit   ports.TenantPlanAuditStore // 配额套餐域审计（audit_logs）；
	core    ports.QuotaSvcClient       // Core 配额 API（校验维度 / 后续下发限额）
	tenants ports.TenantSvcClient      // Core 租户 API（tenant_count / 删除占用检查）
}

// NewTenantPlanService 装配依赖并返回可注册的 gRPC server。
func NewTenantPlanService(plans ports.TenantPlanStore, audit ports.TenantPlanAuditStore, core ports.QuotaSvcClient, tenants ports.TenantSvcClient) *TenantPlanService {
	return &TenantPlanService{plans: plans, audit: audit, core: core, tenants: tenants}
}

// Register 向 gRPC Server 注册本服务（由 services/pkg/bootstrap.RunGRPC 回调）。
func (s *TenantPlanService) Register(server *grpc.Server) {
	tenantv1.RegisterTenantPlanServiceServer(server, s)
}

// tenantPlanCodePattern 套餐代码格式：小写字母/数字/连字符，长度 3–40（对齐 OpenAPI / issue-005）。
var tenantPlanCodePattern = regexp.MustCompile(`^[a-z0-9-]{3,40}$`)

// ── RPC：ListTenantPlans ────────────────────────────────────────────────
// US-002 查询套餐列表：游标分页 + status 过滤 + search 模糊匹配 name。
func (s *TenantPlanService) ListTenantPlans(ctx context.Context, req *tenantv1.ListTenantPlansRequest) (*tenantv1.ListTenantPlansResponse, error) {
	if req == nil {
		req = &tenantv1.ListTenantPlansRequest{}
	}

	// 步骤 1：status 枚举校验（空=全部）
	statusFilter, err := ports.ParseTenantPlanStatusFilter(req.GetStatus())
	if err != nil {
		return nil, mapStoreError(err)
	}

	// 步骤 2：游标分页入参
	limit := 20
	cursor := ""
	if page := req.GetPage(); page != nil {
		if page.GetLimit() > 0 {
			limit = int(page.GetLimit())
		}
		cursor = page.GetCursor()
	}

	// 步骤 3：store 列表查询
	result, err := s.plans.List(ctx, ports.TenantPlanListFilter{
		Limit:  limit,
		Cursor: cursor,
		Status: statusFilter,
		Search: strings.TrimSpace(req.GetSearch()),
	})
	if err != nil {
		return nil, mapStoreError(err)
	}

	ids := make([]uuid.UUID, 0, len(result.Items))
	for _, it := range result.Items {
		ids = append(ids, it.ID)
	}
	counts, err := s.boundTenantCounts(ctx, ids)
	if err != nil {
		return nil, err
	}

	// 步骤 4：组装响应（items 不含 quota_limits）；tenant_count 来自 Core
	items := make([]*tenantv1.TenantPlan, 0, len(result.Items))
	for _, it := range result.Items {
		it.TenantCount = counts[it.ID]
		items = append(items, listItemToPB(it))
	}
	return &tenantv1.ListTenantPlansResponse{
		Items:      items,
		Total:      int64(result.Total),
		NextCursor: result.NextCursor,
	}, nil
}

// CreateTenantPlan 创建套餐（US-001 / SPEC §5.1.1 / issue-005）。
// 成功返回 IdempotentResult{id=plan_id, message="tenant plan created"}。
// 幂等键由网关中间件处理，本方法不落库幂等表。
func (s *TenantPlanService) CreateTenantPlan(ctx context.Context, req *tenantv1.CreateTenantPlanRequest) (*tenantv1.IdempotentResult, error) {
	const action = "tenant_plan.create"

	if req == nil {
		err := businessError(codes.InvalidArgument, ports.ErrValidationFailed, "request required")
		writeAuditFailure(ctx, s.audit, action, nil, err, nil)
		return nil, err
	}

	// ── 1. 基础字段校验 ────────────────────────────────────────────────
	code := strings.TrimSpace(req.GetCode())
	name := strings.TrimSpace(req.GetName())
	description := strings.TrimSpace(req.GetDescription())

	// code：^[a-z0-9-]{3,40}$；唯一性由库 partial unique index 保证
	if !tenantPlanCodePattern.MatchString(code) {
		err := businessError(codes.InvalidArgument, ports.ErrValidationFailed, "code must match ^[a-z0-9-]{3,40}$")
		writeAuditFailure(ctx, s.audit, action, map[string]any{"code": code}, err, nil)
		return nil, err
	}
	if name == "" || utf8.RuneCountInString(name) > 64 {
		err := businessError(codes.InvalidArgument, ports.ErrValidationFailed, "name must be 1-64 characters")
		writeAuditFailure(ctx, s.audit, action, map[string]any{"code": code}, err, nil)
		return nil, err
	}
	if utf8.RuneCountInString(description) > 512 {
		err := businessError(codes.InvalidArgument, ports.ErrValidationFailed, "description must be <= 512 characters")
		writeAuditFailure(ctx, s.audit, action, map[string]any{"code": code}, err, nil)
		return nil, err
	}

	// ── 2. 配额维度校验────────────
	limits, err := s.mapAndValidateQuotaLimits(ctx, req.GetQuotaLimits())
	if err != nil {
		writeAuditFailure(ctx, s.audit, action, map[string]any{"code": code}, err, nil)
		return nil, err
	}

	// ── 3. 持久化套餐 + 限额（store 内自管事务）──────────────────────
	plan, err := s.plans.Create(ctx, ports.CreateTenantPlanInput{
		Code:        code,
		Name:        name,
		Description: description,
		QuotaLimits: limits,
	})
	if err != nil {
		mapped := mapStoreError(err)
		writeAuditFailure(ctx, s.audit, action, map[string]any{
			"code":         code,
			"quota_limits": quotaLimitsForAudit(limits),
		}, mapped, nil)
		return nil, mapped
	}

	// ── 4. 写成功审计（事后记录；失败不阻断已创建成功）
	writeAuditSuccess(ctx, s.audit, action, map[string]any{
		"plan_id":      plan.ID.String(),
		"code":         plan.Code,
		"quota_limits": quotaLimitsForAudit(limits),
	}, nil)

	return &tenantv1.IdempotentResult{
		Id:      plan.ID.String(),
		Message: "tenant plan created",
	}, nil
}

// GetTenantPlan 查询套餐详情（US-003 / issue-006）。
func (s *TenantPlanService) GetTenantPlan(ctx context.Context, req *tenantv1.GetTenantPlanRequest) (*tenantv1.TenantPlan, error) {
	// 步骤 1：plan_id 校验
	raw := ""
	if req != nil {
		raw = req.GetPlanId()
	}
	id, err := parsePlanID(raw)
	if err != nil {
		return nil, err
	}

	// 步骤 2：按主键查未删除套餐
	plan, err := s.plans.GetByID(ctx, id)
	if err != nil {
		return nil, mapStoreError(err)
	}

	counts, err := s.boundTenantCounts(ctx, []uuid.UUID{id})
	if err != nil {
		return nil, err
	}
	plan.TenantCount = counts[id]

	// 步骤 3：组装响应
	return planToPB(plan), nil
}

// UpdateTenantPlan 更新套餐基本信息 name / description（US-016 / issue-009a）。
// 可选字段：未设置 = 不更新；设置为空串 = 清空。不影响限额、状态、绑定关系。
func (s *TenantPlanService) UpdateTenantPlan(ctx context.Context, req *tenantv1.UpdateTenantPlanRequest) (*tenantv1.IdempotentResult, error) {
	const action = "tenant_plan.update"

	// 步骤 1：校验 plan_id
	rawPlanID := ""
	if req != nil {
		rawPlanID = req.GetPlanId()
	}
	id, err := parsePlanID(rawPlanID)
	if err != nil {
		writeAuditFailure(ctx, s.audit, action, map[string]any{"plan_id": rawPlanID}, err, nil)
		return nil, err
	}

	// 步骤 2：映射可选字段（proto StringValue → *string；未设置保持 nil）
	in := ports.UpdateTenantPlanInput{}
	nameUpdated, descUpdated := false, false
	if req != nil && req.Name != nil {
		v := req.Name.GetValue()
		if v == "" {
			err := businessError(codes.InvalidArgument, ports.ErrValidationFailed, "name must not be empty")
			writeAuditFailure(ctx, s.audit, action, map[string]any{"plan_id": id.String()}, err, nil)
			return nil, err
		}
		if utf8.RuneCountInString(v) > 64 {
			err := businessError(codes.InvalidArgument, ports.ErrValidationFailed, "name must be <= 64 characters")
			writeAuditFailure(ctx, s.audit, action, map[string]any{"plan_id": id.String()}, err, nil)
			return nil, err
		}
		in.Name = &v
		nameUpdated = true
	}
	if req != nil && req.Description != nil {
		v := req.Description.GetValue()
		if utf8.RuneCountInString(v) > 512 {
			err := businessError(codes.InvalidArgument, ports.ErrValidationFailed, "description must be <= 512 characters")
			writeAuditFailure(ctx, s.audit, action, map[string]any{"plan_id": id.String()}, err, nil)
			return nil, err
		}
		in.Description = &v
		descUpdated = true
	}

	// 步骤 3：落库更新（套餐不存在 / 已删 → TENANT_PLAN_NOT_FOUND）
	if _, err := s.plans.Update(ctx, id, in); err != nil {
		mapped := mapStoreError(err)
		writeAuditFailure(ctx, s.audit, action, map[string]any{
			"plan_id":             id.String(),
			"name_updated":        nameUpdated,
			"description_updated": descUpdated,
		}, mapped, nil)
		return nil, mapped
	}

	// 步骤 4：写成功审计（事后记录；失败不阻断已更新成功）
	writeAuditSuccess(ctx, s.audit, action, map[string]any{
		"plan_id":             id.String(),
		"name_updated":        nameUpdated,
		"description_updated": descUpdated,
	}, nil)

	// 步骤 5：返回幂等成功结果
	return &tenantv1.IdempotentResult{
		Id:      id.String(),
		Message: "tenant plan updated",
	}, nil
}

// DeleteTenantPlan 删除套餐（US-007）：软删除，有租户关联返回 TENANT_PLAN_IN_USE。
// 任意状态可删；不要求幂等键。成功/失败均写审计。
func (s *TenantPlanService) DeleteTenantPlan(ctx context.Context, req *tenantv1.DeleteTenantPlanRequest) (*tenantv1.IdempotentResult, error) {
	const action = "tenant_plan.delete"

	rawPlanID := ""
	if req != nil {
		rawPlanID = req.GetPlanId()
	}

	// 步骤 1：校验 plan_id
	id, err := parsePlanID(rawPlanID)
	if err != nil {
		writeAuditFailure(ctx, s.audit, action, map[string]any{"plan_id": rawPlanID}, err, nil)
		return nil, err
	}

	// 步骤 2：套餐须存在且未删除（404 优先于占用检查，保持原错误语义）
	if _, err := s.plans.GetByID(ctx, id); err != nil {
		mapped := mapStoreError(err)
		writeAuditFailure(ctx, s.audit, action, map[string]any{"plan_id": id.String()}, mapped, nil)
		return nil, mapped
	}

	// 步骤 3：Core 统计非 disabled 绑定租户；有则 409 TENANT_PLAN_IN_USE
	counts, err := s.boundTenantCounts(ctx, []uuid.UUID{id})
	if err != nil {
		writeAuditFailure(ctx, s.audit, action, map[string]any{"plan_id": id.String()}, err, nil)
		return nil, err
	}
	if counts[id] > 0 {
		mapped := mapStoreError(ports.ErrTenantPlanInUse)
		writeAuditFailure(ctx, s.audit, action, map[string]any{"plan_id": id.String()}, mapped, nil)
		return nil, mapped
	}

	// 步骤 4：软删除
	if err := s.plans.Delete(ctx, id); err != nil {
		mapped := mapStoreError(err)
		writeAuditFailure(ctx, s.audit, action, map[string]any{"plan_id": id.String()}, mapped, nil)
		return nil, mapped
	}

	// 步骤 5：写成功审计（事后记录；失败不阻断已删除成功）
	writeAuditSuccess(ctx, s.audit, action, map[string]any{"plan_id": id.String()}, nil)

	return &tenantv1.IdempotentResult{
		Id:      id.String(),
		Message: "tenant plan deleted",
	}, nil
}

// GetTenantPlanQuotaLimits 查询套餐限额展示视图（US-004 / issue-008）。
func (s *TenantPlanService) GetTenantPlanQuotaLimits(ctx context.Context, req *tenantv1.GetTenantPlanQuotaLimitsRequest) (*tenantv1.GetTenantPlanQuotaLimitsResponse, error) {
	// 步骤 1：校验 plan_id
	raw := ""
	if req != nil {
		raw = req.GetPlanId()
	}
	id, err := parsePlanID(raw)
	if err != nil {
		return nil, err
	}

	// 步骤 2：组装展示视图（store 原始行 + Core ListQuotaMeta）
	views, err := buildQuotaLimitViews(ctx, s.plans, s.core, id)
	if err != nil {
		return nil, err
	}

	// 步骤 3：映射为 gRPC PlanQuotaLimitView
	items := make([]*tenantv1.PlanQuotaLimitView, 0, len(views))
	for _, v := range views {
		items = append(items, &tenantv1.PlanQuotaLimitView{
			ResourceType: v.ResourceType,
			DisplayName:  v.DisplayName,
			Unit:         v.Unit,
			Total:        v.Total,
		})
	}
	return &tenantv1.GetTenantPlanQuotaLimitsResponse{Items: items}, nil
}

// UpdateTenantPlanQuotaLimits 修改套餐限额并同步存量租户到 Core（US-008 / issue-008）。
// 限额 UPSERT 成功后写审计；Core 同步失败记 tenant.quota_init_failed 并异步重试，不回滚已提交限额。
func (s *TenantPlanService) UpdateTenantPlanQuotaLimits(ctx context.Context, req *tenantv1.UpdateTenantPlanQuotaLimitsRequest) (*tenantv1.IdempotentResult, error) {
	const action = "tenant_plan.update_quota_limits"

	// 步骤 1：校验 plan_id
	rawPlanID := ""
	if req != nil {
		rawPlanID = req.GetPlanId()
	}
	id, err := parsePlanID(rawPlanID)
	if err != nil {
		writeAuditFailure(ctx, s.audit, action, map[string]any{"plan_id": rawPlanID}, err, nil)
		return nil, err
	}

	// 步骤 2：items 至少 1 项
	itemsPB := []*tenantv1.PlanQuotaLimitInput(nil)
	if req != nil {
		itemsPB = req.GetItems()
	}
	if len(itemsPB) == 0 {
		err := businessError(codes.InvalidArgument, ports.ErrValidationFailed, "items required (at least 1)")
		writeAuditFailure(ctx, s.audit, action, map[string]any{"plan_id": id.String()}, err, nil)
		return nil, err
	}

	// 步骤 3：校验维度；nil total 用 Core default_quota 填成具体值再落库
	limits, err := s.mapAndValidateQuotaLimits(ctx, itemsPB)
	if err != nil {
		writeAuditFailure(ctx, s.audit, action, map[string]any{"plan_id": id.String()}, err, nil)
		return nil, err
	}

	// 步骤 4：UPSERT plan_quota_limits（事务内不写审计）
	if err := s.plans.UpdateQuotaLimits(ctx, id, limits); err != nil {
		mapped := mapStoreError(err)
		writeAuditFailure(ctx, s.audit, action, map[string]any{"plan_id": id.String()}, mapped, nil)
		return nil, mapped
	}

	// 步骤 5：同步存量租户到 Core（approved 维度跳过；失败异步重试）
	synced, skipped, tightened, updatedDims := s.syncBoundTenantQuotaLimits(ctx, id, limits)

	// 步骤 6：写成功审计（含同步计数；审计失败不阻断已提交限额）
	writeAuditSuccess(ctx, s.audit, action, map[string]any{
		"plan_id":             id.String(),
		"updated_dimensions":  updatedDims,
		"synced_tenant_count": synced,
		"skipped_approved":    skipped,
		"tightened":           tightened,
	}, nil)

	return &tenantv1.IdempotentResult{
		Id:      id.String(),
		Message: "quota limits updated",
	}, nil
}

// ActivateTenantPlan 发布套餐（US-005）：draft/disabled → active。成功/失败均写审计。
func (s *TenantPlanService) ActivateTenantPlan(ctx context.Context, req *tenantv1.ActivateTenantPlanRequest) (*tenantv1.IdempotentResult, error) {
	const action = "tenant_plan.activate"

	rawPlanID := ""
	if req != nil {
		rawPlanID = req.GetPlanId()
	}

	// 步骤 1：校验 plan_id
	id, err := parsePlanID(rawPlanID)
	if err != nil {
		writeAuditFailure(ctx, s.audit, action, map[string]any{"plan_id": rawPlanID}, err, nil)
		return nil, err
	}

	// 步骤 2：状态转换
	plan, err := s.plans.Activate(ctx, id)
	if err != nil {
		mapped := mapStoreError(err)
		writeAuditFailure(ctx, s.audit, action, map[string]any{"plan_id": id.String()}, mapped, nil)
		return nil, mapped
	}

	// 步骤 3：写成功审计（事后记录；失败不阻断已激活成功）
	writeAuditSuccess(ctx, s.audit, action, map[string]any{
		"plan_id": plan.ID.String(),
		"status":  string(plan.Status),
	}, nil)

	return &tenantv1.IdempotentResult{
		Id:      plan.ID.String(),
		Message: "tenant plan activated",
	}, nil
}

// DisableTenantPlan 禁用套餐（US-006）：active → disabled。成功/失败均写审计。
func (s *TenantPlanService) DisableTenantPlan(ctx context.Context, req *tenantv1.DisableTenantPlanRequest) (*tenantv1.IdempotentResult, error) {
	const action = "tenant_plan.disable"

	rawPlanID := ""
	if req != nil {
		rawPlanID = req.GetPlanId()
	}

	// 步骤 1：校验 plan_id
	id, err := parsePlanID(rawPlanID)
	if err != nil {
		writeAuditFailure(ctx, s.audit, action, map[string]any{"plan_id": rawPlanID}, err, nil)
		return nil, err
	}

	// 步骤 2：状态转换
	plan, err := s.plans.Disable(ctx, id)
	if err != nil {
		mapped := mapStoreError(err)
		writeAuditFailure(ctx, s.audit, action, map[string]any{"plan_id": id.String()}, mapped, nil)
		return nil, mapped
	}

	// 步骤 3：写成功审计（事后记录；失败不阻断已禁用成功）
	writeAuditSuccess(ctx, s.audit, action, map[string]any{
		"plan_id": plan.ID.String(),
		"status":  string(plan.Status),
	}, nil)

	return &tenantv1.IdempotentResult{
		Id:      plan.ID.String(),
		Message: "tenant plan disabled",
	}, nil
}

// ListTenantPlanBoundTenants 查询绑定到套餐的租户摘要列表（US-010 / issue-009）。
func (s *TenantPlanService) ListTenantPlanBoundTenants(ctx context.Context, req *tenantv1.ListTenantPlanBoundTenantsRequest) (*tenantv1.ListTenantPlanBoundTenantsResponse, error) {
	// 步骤 1：校验 plan_id
	raw := ""
	if req != nil {
		raw = req.GetPlanId()
	}
	id, err := parsePlanID(raw)
	if err != nil {
		return nil, err
	}

	// 步骤 2：套餐须存在；租户列表经 Core SDK（不再直查 tenants 表）
	if _, err := s.plans.GetByID(ctx, id); err != nil {
		return nil, mapStoreError(err)
	}
	tenants, err := s.coreBoundTenants(ctx, id)
	if err != nil {
		return nil, err
	}

	// 步骤 3：映射为 gRPC BoundTenant（不分页）
	return &tenantv1.ListTenantPlanBoundTenantsResponse{Items: boundTenantsToPB(tenants)}, nil
}

// ListBindableTenants 查询可绑定该套餐的租户摘要（US-018 / issue-009c）。
func (s *TenantPlanService) ListBindableTenants(ctx context.Context, req *tenantv1.ListBindableTenantsRequest) (*tenantv1.ListBindableTenantsResponse, error) {
	if req == nil {
		req = &tenantv1.ListBindableTenantsRequest{}
	}

	// 步骤 1：校验 plan_id
	id, err := parsePlanID(req.GetPlanId())
	if err != nil {
		return nil, err
	}

	// 步骤 2：套餐须存在；可绑定列表经 Core SDK
	if _, err := s.plans.GetByID(ctx, id); err != nil {
		return nil, mapStoreError(err)
	}
	tenants, err := s.coreBindableTenants(ctx, id)
	if err != nil {
		return nil, err
	}

	// 步骤 3：映射为 gRPC BoundTenant（不分页）
	return &tenantv1.ListBindableTenantsResponse{Items: boundTenantsToPB(tenants)}, nil
}

// ListTenantPlanAuditLogs 查询套餐操作历史（US-011 / issue-010）：游标分页。
func (s *TenantPlanService) ListTenantPlanAuditLogs(ctx context.Context, req *tenantv1.ListTenantPlanAuditLogsRequest) (*tenantv1.ListTenantPlanAuditLogsResponse, error) {
	if req == nil {
		req = &tenantv1.ListTenantPlanAuditLogsRequest{}
	}

	// 步骤 1：校验 plan_id
	id, err := parsePlanID(req.GetPlanId())
	if err != nil {
		return nil, err
	}

	// 步骤 2：套餐须存在且未删除（否则 404 TENANT_PLAN_NOT_FOUND）
	if _, err := s.plans.GetByID(ctx, id); err != nil {
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

	// 步骤 4：按 details.plan_id 查配额套餐域审计（无 action/result 过滤）
	listed, err := s.audit.ListPlanAuditLogs(ctx, id, ports.AuditLogFilter{
		Limit:  limit,
		Cursor: cursor,
	})
	if err != nil {
		return nil, mapStoreError(err)
	}

	// 步骤 5：映射为 gRPC AuditLog
	items := make([]*tenantv1.AuditLog, 0, len(listed.Items))
	for _, it := range listed.Items {
		pb, err := auditLogToPB(it)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "INTERNAL_ERROR: encode audit log: %v", err)
		}
		items = append(items, pb)
	}
	return &tenantv1.ListTenantPlanAuditLogsResponse{
		Items:      items,
		Total:      int64(listed.Total),
		NextCursor: listed.NextCursor,
	}, nil
}

// ListQuotaMeta 查询可用配额维度元数据（US-017 / issue-009b）：透传 Core GET /admin/quota-meta。
func (s *TenantPlanService) ListQuotaMeta(ctx context.Context, _ *tenantv1.ListQuotaMetaRequest) (*tenantv1.ListQuotaMetaResponse, error) {
	// 步骤 1：调用 Core 客户端拉取启用维度
	meta, err := s.core.ListQuotaMeta(ctx)
	if err != nil {
		if errors.Is(err, ports.ErrCoreUnavailable) {
			return nil, businessError(codes.Unavailable, ports.ErrCoreUnavailable, "core quota-meta unavailable")
		}
		return nil, businessError(codes.Unavailable, ports.ErrCoreUnavailable, err.Error())
	}

	// 步骤 2：映射为 gRPC QuotaMetaItem（仅透传展示字段）
	items := make([]*tenantv1.QuotaMetaItem, 0, len(meta))
	for _, m := range meta {
		items = append(items, &tenantv1.QuotaMetaItem{
			ResourceType: m.ResourceType,
			DisplayName:  m.DisplayName,
			Unit:         m.Unit,
			DefaultQuota: m.DefaultQuota,
			IsDiscrete:   m.IsDiscrete,
		})
	}

	// 步骤 3：返回列表
	return &tenantv1.ListQuotaMetaResponse{Items: items}, nil
}

// ── helpers（勿穿插到上方 US/RPC 方法中间）────────────────────────────────

// mapAndValidateQuotaLimits 将 gRPC quota_limits 映射为 ports 入参，并做维度校验。
// Create/Update：nil total 一律用 Core default_quota 填成具体值再落库（不保留 NULL）。
func (s *TenantPlanService) mapAndValidateQuotaLimits(ctx context.Context, items []*tenantv1.PlanQuotaLimitInput) ([]ports.PlanQuotaLimitInput, error) {
	// 步骤 1：空列表直接返回（Create 可选；Update 上层已强制至少 1 项）
	if len(items) == 0 {
		return nil, nil
	}

	// 步骤 2：拉取 Core 启用维度元数据
	meta, err := s.core.ListQuotaMeta(ctx)
	if err != nil {
		if errors.Is(err, ports.ErrCoreUnavailable) {
			return nil, businessError(codes.Unavailable, ports.ErrCoreUnavailable, "core quota-meta unavailable")
		}
		return nil, businessError(codes.Unavailable, ports.ErrCoreUnavailable, err.Error())
	}
	metaByType := make(map[string]ports.QuotaMeta, len(meta))
	for _, m := range meta {
		if m.Enabled {
			metaByType[m.ResourceType] = m
		}
	}

	// 步骤 3：逐项校验 resource_type / 去重 / total，并映射到 ports
	seen := make(map[string]struct{}, len(items))
	out := make([]ports.PlanQuotaLimitInput, 0, len(items))
	for _, it := range items {
		if it == nil {
			return nil, businessError(codes.InvalidArgument, ports.ErrValidationFailed, "quota_limits item required")
		}
		rt := strings.TrimSpace(it.GetResourceType())
		if rt == "" {
			return nil, businessError(codes.InvalidArgument, ports.ErrValidationFailed, "resource_type required")
		}
		if _, dup := seen[rt]; dup {
			return nil, businessError(codes.InvalidArgument, ports.ErrValidationFailed, "duplicate resource_type: "+rt)
		}
		seen[rt] = struct{}{}

		m, ok := metaByType[rt]
		if !ok {
			return nil, businessError(codes.FailedPrecondition, ports.ErrQuotaResourceNotRegistered, "resource_type not registered or disabled: "+rt)
		}

		var total int64
		if it.Total != nil {
			total = it.Total.GetValue()
			if total < 0 {
				return nil, businessError(codes.InvalidArgument, ports.ErrValidationFailed, "total must be null or >= 0")
			}
		} else {
			total = m.DefaultQuota
		}
		v := total
		out = append(out, ports.PlanQuotaLimitInput{ResourceType: rt, Total: &v})
	}
	return out, nil
}

// syncBoundTenantQuotaLimits 把本次更新的维度同步到绑定租户（改限额路径）。
// Core 失败：写 tenant.quota_init_failed 审计并异步重试；不回滚套餐限额。
func (s *TenantPlanService) syncBoundTenantQuotaLimits(ctx context.Context, planID uuid.UUID, limits []ports.PlanQuotaLimitInput) (synced, skippedApproved, tightened int, updatedDims []string) {
	// 步骤 1：收集本次更新的维度列表
	updatedDims = make([]string, 0, len(limits))
	for _, lim := range limits {
		updatedDims = append(updatedDims, lim.ResourceType)
	}

	// 步骤 2：读有效 total（store + Core meta），供下发
	views, err := buildQuotaLimitViews(ctx, s.plans, s.core, planID)
	if err != nil {
		return 0, 0, 0, updatedDims
	}
	totalByType := totalsFromQuotaViews(views)

	// 步骤 3：列出绑定租户（status <> disabled，经 Core SDK）
	tenants, err := s.coreBoundTenants(ctx, planID)
	if err != nil {
		return 0, 0, 0, updatedDims
	}

	// 步骤 4：逐租户调用共用同步（失败审计 + 异步重试）
	for _, t := range tenants {
		res, err := syncPlanQuotaToTenant(ctx, s.plans, s.core, t.ID, totalByType, updatedDims)
		skippedApproved += len(res.SkippedApproved)
		tightened += len(res.Tightened)
		if err != nil {
			writeAuditFailure(ctx, s.audit, "tenant.quota_init_failed", map[string]any{
				"plan_id":   planID.String(),
				"tenant_id": t.ID.String(),
				"items":     coreItemsForAudit(res.Items),
			}, err, nil)
			s.scheduleQuotaSyncRetry(planID, t.ID, totalByType, updatedDims)
			continue
		}
		if len(res.Updated) > 0 {
			synced++
		}
	}
	return synced, skippedApproved, tightened, updatedDims
}

// tenantQuotaSyncResult 是 syncPlanQuotaToTenant 的返回摘要。
type tenantQuotaSyncResult struct {
	Updated         []string              // 实际下发的维度
	SkippedApproved []string              // 因 approved 跳过的维度
	Tightened       []string              // Core 因占用自动收紧（tightened=true）的维度
	Items           []ports.CoreQuotaItem // 下发（或拟下发）的 Core items
}

// syncPlanQuotaToTenant 将套餐限额同步到单个租户配额（绑定 / 改限额同步共用）。
// dims 为待同步维度；totalByType 提供各维度有效 total。
// 入口先校验维度已启用，再跳过 approved，最后 UpsertQuota。
func syncPlanQuotaToTenant(
	ctx context.Context,
	plans ports.TenantPlanStore,
	core ports.QuotaSvcClient,
	tenantID uuid.UUID,
	totalByType map[string]int64,
	dims []string,
) (tenantQuotaSyncResult, error) {
	var out tenantQuotaSyncResult

	// 步骤 1：service 侧先校验待同步维度均已启用（不依赖 Core Upsert 兜底）
	if err := validateEnabledQuotaResourceTypes(ctx, core, dims); err != nil {
		return out, err
	}

	// 步骤 2：读已审批维度
	approved, err := plans.GetApprovedQuotaChanges(ctx, tenantID)
	if err != nil {
		return out, err
	}
	approvedSet := make(map[string]struct{}, len(approved))
	for _, a := range approved {
		approvedSet[a.ResourceType] = struct{}{}
	}

	// 步骤 3：过滤 dims → Core items（跳过 approved / 无 total）
	out.Updated = make([]string, 0, len(dims))
	out.SkippedApproved = make([]string, 0)
	out.Tightened = make([]string, 0)
	out.Items = make([]ports.CoreQuotaItem, 0, len(dims))
	for _, rt := range dims {
		if _, skip := approvedSet[rt]; skip {
			out.SkippedApproved = append(out.SkippedApproved, rt)
			continue
		}
		total, ok := totalByType[rt]
		if !ok {
			continue
		}
		out.Items = append(out.Items, ports.CoreQuotaItem{ResourceType: rt, Total: total})
		out.Updated = append(out.Updated, rt)
	}

	// 步骤 4：无待下发项则结束
	if len(out.Items) == 0 {
		return out, nil
	}

	// 步骤 5：Upsert 下发，并收集 tightened 维度
	results, err := applyTenantQuotaItems(ctx, core, tenantID, out.Items)
	if err != nil {
		return out, err
	}
	for _, r := range results {
		if r.Tightened {
			out.Tightened = append(out.Tightened, r.ResourceType)
		}
	}
	return out, nil
}

// totalsFromQuotaViews 把展示视图压成 resource_type → total 映射。
func totalsFromQuotaViews(views []ports.PlanQuotaLimitView) map[string]int64 {
	// 步骤 1：逐项写入 map
	out := make(map[string]int64, len(views))
	for _, v := range views {
		out[v.ResourceType] = v.Total
	}
	return out
}

// dimsFromQuotaViews 取出视图中的全部维度标识（绑定路径同步全量套餐限额）。
func dimsFromQuotaViews(views []ports.PlanQuotaLimitView) []string {
	// 步骤 1：按视图顺序收集 resource_type
	out := make([]string, 0, len(views))
	for _, v := range views {
		out = append(out, v.ResourceType)
	}
	return out
}

// validateEnabledQuotaResourceTypes 在 service 层校验 resource_type 均已在 Core quota-meta 中启用。
// syncPlanQuotaToTenant 入口第一步调用；未注册或 Enabled=false → ErrQuotaResourceNotRegistered。
// 不修改 buildQuotaLimitViews（展示路径仍可静默跳过未启用维度）。
func validateEnabledQuotaResourceTypes(ctx context.Context, core ports.QuotaSvcClient, resourceTypes []string) error {
	if len(resourceTypes) == 0 {
		return nil
	}
	if core == nil {
		return businessError(codes.Unavailable, ports.ErrCoreUnavailable, "core quota api unavailable")
	}
	meta, err := core.ListQuotaMeta(ctx)
	if err != nil {
		if errors.Is(err, ports.ErrCoreUnavailable) {
			return businessError(codes.Unavailable, ports.ErrCoreUnavailable, "core quota-meta unavailable")
		}
		return businessError(codes.Unavailable, ports.ErrCoreUnavailable, err.Error())
	}
	enabled := make(map[string]struct{}, len(meta))
	for _, m := range meta {
		if m.Enabled {
			enabled[m.ResourceType] = struct{}{}
		}
	}
	for _, rt := range resourceTypes {
		if _, ok := enabled[rt]; !ok {
			return businessError(codes.FailedPrecondition, ports.ErrQuotaResourceNotRegistered, "resource_type not registered or disabled: "+rt)
		}
	}
	return nil
}

// applyTenantQuotaItems 一律走 Core UpsertQuota，不再 GetQuota 后按存在性分流 Put/Create。
// 返回 Core 各维度结果（含 tightened）；BindPlanQuota / 改限额同步共用。
func applyTenantQuotaItems(ctx context.Context, core ports.QuotaSvcClient, tenantID uuid.UUID, items []ports.CoreQuotaItem) ([]ports.CoreQuotaResult, error) {
	if len(items) == 0 {
		return nil, nil
	}
	return core.UpsertQuota(ctx, tenantID, items)
}

// buildQuotaLimitViews 组装展示/下发视图：store.GetQuotaLimits + Core ListQuotaMeta。
// 禁止 store 直连 resource_quota_meta；display_name/unit/default_quota 一律走 SDK。
// 若库中 total 为 NULL，视图已用 default_quota 兜底；并 best-effort 回写库（失败只 Warn，不影响查询展示）。
func buildQuotaLimitViews(ctx context.Context, plans ports.TenantPlanStore, core ports.QuotaSvcClient, planID uuid.UUID) ([]ports.PlanQuotaLimitView, error) {
	// 步骤 1：读套餐原始限额行（保留 NULL）
	raw, err := plans.GetQuotaLimits(ctx, planID)
	if err != nil {
		return nil, mapStoreError(err)
	}

	// 步骤 2：经 Core SDK 拉启用维度元数据
	meta, err := core.ListQuotaMeta(ctx)
	if err != nil {
		if errors.Is(err, ports.ErrCoreUnavailable) {
			return nil, businessError(codes.Unavailable, ports.ErrCoreUnavailable, "core quota-meta unavailable")
		}
		return nil, businessError(codes.Unavailable, ports.ErrCoreUnavailable, err.Error())
	}
	metaByType := make(map[string]ports.QuotaMeta, len(meta))
	for _, m := range meta {
		if m.Enabled {
			metaByType[m.ResourceType] = m
		}
	}

	// 步骤 3：合并为视图；收集 total=NULL 的维度，准备用 default_quota 回写
	out := make([]ports.PlanQuotaLimitView, 0, len(raw))
	backfill := make([]ports.PlanQuotaLimitInput, 0)
	for _, lim := range raw {
		m, ok := metaByType[lim.ResourceType]
		if !ok {
			continue
		}
		total := m.DefaultQuota
		if lim.Total != nil {
			total = *lim.Total
		} else {
			v := m.DefaultQuota
			backfill = append(backfill, ports.PlanQuotaLimitInput{ResourceType: lim.ResourceType, Total: &v})
		}
		out = append(out, ports.PlanQuotaLimitView{
			ResourceType: lim.ResourceType,
			DisplayName:  m.DisplayName,
			Unit:         m.Unit,
			Total:        total,
		})
	}

	// 步骤 4：查询路径 best-effort 回写 NULL → default_quota（失败只告警，仍返回已兜底的视图）
	if len(backfill) > 0 {
		if err := plans.UpdateQuotaLimits(ctx, planID, backfill); err != nil {
			slog.Warn("quota limits null backfill failed; returning coalesced view",
				"plan_id", planID.String(),
				"dimensions", len(backfill),
				"error", err,
			)
		}
	}
	return out, nil
}

// scheduleQuotaSyncRetry 异步重试租户配额同步（复用 syncPlanQuotaToTenant）：最多 3 次，指数退避 1s/2s/4s。
// 单测可将 enablePutQuotaRetry 置 false 以禁用后台重试。
var enablePutQuotaRetry = true

func (s *TenantPlanService) scheduleQuotaSyncRetry(planID, tenantID uuid.UUID, totalByType map[string]int64, dims []string) {
	// 步骤 1：单测开关关闭时不启 goroutine
	if !enablePutQuotaRetry {
		return
	}
	// 步骤 2：拷贝入参，避免 goroutine 与调用方共享可变 map/slice
	totalsCopy := make(map[string]int64, len(totalByType))
	for k, v := range totalByType {
		totalsCopy[k] = v
	}
	dimsCopy := append([]string(nil), dims...)

	go func() {
		// 步骤 3：最多 3 次；失败写 tenant.quota_init_failed（含 attempt）
		backoff := time.Second
		for attempt := 1; attempt <= 3; attempt++ {
			time.Sleep(backoff)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			res, err := syncPlanQuotaToTenant(ctx, s.plans, s.core, tenantID, totalsCopy, dimsCopy)
			cancel()
			if err == nil {
				return
			}
			writeAuditFailure(context.Background(), s.audit, "tenant.quota_init_failed", map[string]any{
				"plan_id":   planID.String(),
				"tenant_id": tenantID.String(),
				"attempt":   attempt,
				"items":     coreItemsForAudit(res.Items),
			}, err, nil)
			backoff *= 2
		}
	}()
}

// mapStoreError 将 store 哨兵错误映射为带业务码前缀的 gRPC status。
// 网关按 message 前缀（如 PLAN_CODE_CONFLICT:）还原 HTTP 状态与 ErrorResponse.code。
func mapStoreError(err error) error {
	switch {
	case errors.Is(err, ports.ErrPlanCodeConflict):
		return businessError(codes.AlreadyExists, ports.ErrPlanCodeConflict, "plan code already exists")
	case errors.Is(err, ports.ErrTenantPlanNotFound):
		return businessError(codes.NotFound, ports.ErrTenantPlanNotFound, "tenant plan not found")
	case errors.Is(err, ports.ErrPlanStateInvalid):
		detail := strings.TrimSpace(strings.TrimPrefix(err.Error(), ports.ErrPlanStateInvalid.Error()+":"))
		detail = strings.TrimSpace(strings.TrimPrefix(detail, ports.ErrPlanStateInvalid.Error()))
		if detail == "" {
			detail = "plan status does not allow this transition"
		}
		return businessError(codes.FailedPrecondition, ports.ErrPlanStateInvalid, detail)
	case errors.Is(err, ports.ErrTenantPlanInUse):
		return businessError(codes.FailedPrecondition, ports.ErrTenantPlanInUse, "tenant plan has bound tenants")
	case errors.Is(err, ports.ErrQuotaResourceNotRegistered):
		return businessError(codes.FailedPrecondition, ports.ErrQuotaResourceNotRegistered, "resource_type not registered or disabled")
	case errors.Is(err, ports.ErrTenantNotFound):
		return businessError(codes.NotFound, ports.ErrTenantNotFound, "tenant not found")
	case errors.Is(err, ports.ErrQuotaNotFound):
		return businessError(codes.NotFound, ports.ErrQuotaNotFound, "tenant quota not found")
	case errors.Is(err, ports.ErrQuotaAlreadyExists):
		return businessError(codes.AlreadyExists, ports.ErrQuotaAlreadyExists, "tenant quota already exists")
	case errors.Is(err, ports.ErrPlanNotActive):
		return businessError(codes.FailedPrecondition, ports.ErrPlanNotActive, "tenant plan is not active")
	case errors.Is(err, ports.ErrTenantStateInvalid):
		return businessError(codes.FailedPrecondition, ports.ErrTenantStateInvalid, "tenant state does not allow this operation")
	case errors.Is(err, ports.ErrValidationFailed):
		detail := strings.TrimSpace(strings.TrimPrefix(err.Error(), ports.ErrValidationFailed.Error()+":"))
		detail = strings.TrimSpace(strings.TrimPrefix(detail, ports.ErrValidationFailed.Error()))
		return businessError(codes.InvalidArgument, ports.ErrValidationFailed, detail)
	case errors.Is(err, ports.ErrCoreUnavailable):
		return businessError(codes.Unavailable, ports.ErrCoreUnavailable, "core tenant api unavailable")
	default:
		return status.Errorf(codes.Internal, "tenant plan operation failed: %v", err)
	}
}

// boundTenantCounts 经 Core SDK 批量查询套餐绑定租户数（status <> disabled）。
func (s *TenantPlanService) boundTenantCounts(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]int64, error) {
	if len(ids) == 0 {
		return map[uuid.UUID]int64{}, nil
	}
	if s.tenants == nil {
		return nil, businessError(codes.Unavailable, ports.ErrCoreUnavailable, "core tenant api unavailable")
	}
	counts, err := s.tenants.CountBoundTenants(ctx, ids)
	if err != nil {
		return nil, mapStoreError(err)
	}
	if counts == nil {
		counts = map[uuid.UUID]int64{}
	}
	return counts, nil
}

func (s *TenantPlanService) coreBoundTenants(ctx context.Context, planID uuid.UUID) ([]ports.BoundTenant, error) {
	if s.tenants == nil {
		return nil, businessError(codes.Unavailable, ports.ErrCoreUnavailable, "core tenant api unavailable")
	}
	tenants, err := s.tenants.ListBoundTenants(ctx, planID)
	if err != nil {
		return nil, mapStoreError(err)
	}
	return tenants, nil
}

func (s *TenantPlanService) coreBindableTenants(ctx context.Context, planID uuid.UUID) ([]ports.BoundTenant, error) {
	if s.tenants == nil {
		return nil, businessError(codes.Unavailable, ports.ErrCoreUnavailable, "core tenant api unavailable")
	}
	tenants, err := s.tenants.ListBindableTenants(ctx, planID)
	if err != nil {
		return nil, mapStoreError(err)
	}
	return tenants, nil
}

func boundTenantsToPB(tenants []ports.BoundTenant) []*tenantv1.BoundTenant {
	items := make([]*tenantv1.BoundTenant, 0, len(tenants))
	for _, t := range tenants {
		items = append(items, &tenantv1.BoundTenant{
			Id:          t.ID.String(),
			Name:        t.Name,
			DisplayName: t.DisplayName,
			Status:      string(t.Status),
		})
	}
	return items
}

// parsePlanID 校验并解析 plan_id（必填 UUID）。
func parsePlanID(raw string) (uuid.UUID, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return uuid.Nil, businessError(codes.InvalidArgument, ports.ErrValidationFailed, "plan_id required")
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, businessError(codes.InvalidArgument, ports.ErrValidationFailed, "plan_id must be a uuid")
	}
	return id, nil
}

// listItemToPB 把列表项映射为 gRPC TenantPlan（列表不含 description 也可带上）。
func listItemToPB(it ports.TenantPlanListItem) *tenantv1.TenantPlan {
	return &tenantv1.TenantPlan{
		Id:          it.ID.String(),
		Code:        it.Code,
		Name:        it.Name,
		Description: it.Description,
		Status:      string(it.Status),
		TenantCount: it.TenantCount,
		CreatedAt:   timestamppb.New(it.CreatedAt),
		UpdatedAt:   timestamppb.New(it.UpdatedAt),
	}
}

// planToPB 把领域套餐映射为 gRPC TenantPlan（详情含 tenant_count）。
func planToPB(plan ports.TenantPlan) *tenantv1.TenantPlan {
	return &tenantv1.TenantPlan{
		Id:          plan.ID.String(),
		Code:        plan.Code,
		Name:        plan.Name,
		Description: plan.Description,
		Status:      string(plan.Status),
		TenantCount: plan.TenantCount,
		CreatedAt:   timestamppb.New(plan.CreatedAt),
		UpdatedAt:   timestamppb.New(plan.UpdatedAt),
	}
}

// auditLogToPB 把审计领域对象映射为 gRPC AuditLog（id/action/result/details/created_at）。
func auditLogToPB(log ports.AuditLog) (*tenantv1.AuditLog, error) {
	// 步骤 1：details 转 protobuf Struct（nil → 空对象）
	details := log.Details
	if details == nil {
		details = map[string]any{}
	}
	st, err := structpb.NewStruct(details)
	if err != nil {
		return nil, fmt.Errorf("details to struct: %w", err)
	}

	// 步骤 2：组装展示字段
	return &tenantv1.AuditLog{
		Id:        log.ID.String(),
		Action:    log.Action,
		Result:    log.Result,
		Details:   st,
		CreatedAt: timestamppb.New(log.CreatedAt),
	}, nil
}

// businessError 构造「CODE: detail」形式的 gRPC 错误。
// sentinel.Error() 即为业务码字符串（如 VALIDATION_FAILED），必须保留前缀供网关解析。
func businessError(code codes.Code, sentinel error, detail string) error {
	msg := sentinel.Error()
	detail = strings.TrimSpace(detail)
	if detail != "" && detail != msg {
		msg = fmt.Sprintf("%s: %s", msg, detail)
	}
	return status.Error(code, msg)
}
