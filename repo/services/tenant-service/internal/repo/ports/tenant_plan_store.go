package ports

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// 配额套餐管理的端口（ports）定义。
// 本文件只声明接口与领域结构体（DTO/Entity），不含任何实现逻辑。
// 具体实现由 postgres adapter（PostgresTenantPlanStore）在后续 issue 填充。

// =============================================================================
// 状态枚举
// =============================================================================

// TenantPlanStatus 套餐状态机：draft → active → disabled。
type TenantPlanStatus string

const (
	TenantPlanStatusDraft    TenantPlanStatus = "draft"
	TenantPlanStatusActive   TenantPlanStatus = "active"
	TenantPlanStatusDisabled TenantPlanStatus = "disabled"
)

// Valid 报告是否为已知套餐状态（不含空串）。
func (s TenantPlanStatus) Valid() bool {
	switch s {
	case TenantPlanStatusDraft, TenantPlanStatusActive, TenantPlanStatusDisabled:
		return true
	default:
		return false
	}
}

// ParseTenantPlanStatusFilter 解析列表过滤用 status：空=全部；非法值报错。
func ParseTenantPlanStatusFilter(raw string) (TenantPlanStatus, error) {
	s := TenantPlanStatus(strings.TrimSpace(raw))
	if s == "" {
		return "", nil
	}
	if !s.Valid() {
		return "", fmt.Errorf("%w: status must be draft, active, or disabled", ErrValidationFailed)
	}
	return s, nil
}

// =============================================================================
// 实体与 DTO
// =============================================================================

// TenantPlan 表示一条配额套餐记录（对应 tenant_plans 表的一行）。
// status 状态机：draft → active → disabled，软删除通过 is_deleted + deleted_at 标记。
type TenantPlan struct {
	ID          uuid.UUID        // 主键；被 tenants.plan_id 外键引用（ON DELETE RESTRICT）
	Code        string           // 业务代码；
	Name        string           // 套餐名称
	Description string           // 描述
	Status      TenantPlanStatus // draft | active | disabled
	IsDeleted   bool             // 软删除标记
	DeletedAt   *time.Time       // 软删除时间；nil = 未删除
	TenantCount int64            // 绑定租户数（仅读路径填充；Create 为 0）
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// PlanQuotaLimit 表示套餐某配额维度的限额原始行（对应 plan_quota_limits 表一行）。
type PlanQuotaLimit struct {
	PlanID       uuid.UUID // 所属套餐
	ResourceType string    // 配额维度标识（语义对齐 Core resource_quota_meta；无 DB 外键，由 service 经 ListQuotaMeta 校验）
	Total        *int64    // 限额值；历史行可能为 NULL，读写路径用 Core default_quota 兜底
}

// PlanQuotaLimitView 表示套餐配额维度的"展示视图"（GET /quota-limits 返回）。
// display_name/unit/default_quota 来自 Core ListQuotaMeta（SDK），不由 store JOIN 本地 meta 表。
// 展示（GET /quota-limits）与绑定下发均使用本视图。
type PlanQuotaLimitView struct {
	ResourceType string // 配额维度标识
	DisplayName  string // 展示名（来自 Core quota-meta）
	Unit         string // 单位（来自 Core quota-meta）
	Total        int64  // 兜底后的具体限额值 COALESCE(plan_quota_limits.total, default_quota)
}

// CreateTenantPlanInput 是创建套餐的入参（POST /tenant-plans）。
type CreateTenantPlanInput struct {
	Code        string                // 必填，业务代码（唯一）
	Name        string                // 必填
	Description string                // 可选
	QuotaLimits []PlanQuotaLimitInput // 可选；各维度配额上限
}

// PlanQuotaLimitInput 是单个配额维度的写入入参（创建/修改限额时使用）。
// Service 层在落库前已将 nil total 替换为 Core default_quota，故 Total 应为具体值。
type PlanQuotaLimitInput struct {
	ResourceType string // 配额维度标识
	Total        *int64 // 限额值（具体数值）
}

// UpdateTenantPlanInput 是更新套餐基本信息的入参（PUT /tenant-plans/{planId}）。
// Name / Description：nil = 不更新；非 nil（含空串）= 写入该值。
type UpdateTenantPlanInput struct {
	Name        *string // nil = 不更新
	Description *string // nil = 不更新
}

// TenantPlanListFilter 是套餐列表查询的过滤条件（游标分页）。
type TenantPlanListFilter struct {
	Limit  int              // 每页数量，default 20，max 100
	Cursor string           // 上一页返回的 next_cursor；空串 = 第一页
	Status TenantPlanStatus // "" = 全部；否则 draft | active | disabled
	Search string           // 模糊匹配 name
}

// TenantPlanListItem 是套餐列表/详情的查询视图（仅在查询接口返回，不进 TenantPlan 实体）。
// 相比 TenantPlan 额外携带 tenant_count（绑定租户数），由 store 通过子查询统计。
type TenantPlanListItem struct {
	ID          uuid.UUID
	Code        string
	Name        string
	Description string
	Status      TenantPlanStatus // draft | active | disabled
	TenantCount int64            // 绑定租户数量（COUNT tenants WHERE plan_id=? AND status != 'disabled'）
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// TenantPlanListResult 是套餐列表查询的返回（游标分页）。
// 风格与项目内其他分页结果一致（Items + Total + NextCursor，具体类型、不使用泛型）。
type TenantPlanListResult struct {
	Items      []TenantPlanListItem // 本页数据
	Total      int                  // 满足过滤条件的总条数（用于前端分页/计数）
	NextCursor string               // 下一页游标；空串 = 已无更多数据
}

// BoundTenant 表示绑定到某套餐的租户摘要（GET /tenant-plans/{planId}/tenants 返回）。
type BoundTenant struct {
	ID          uuid.UUID
	Name        string
	DisplayName string
	Status      TenantStatus // active | frozen | disabled
}

// ApprovedQuotaChange 表示租户已审批通过（status='approved'）的配额变更维度。
// 用于绑定套餐 / 修改限额同步时：这些维度保留不覆盖。
type ApprovedQuotaChange struct {
	TenantID     uuid.UUID
	ResourceType string // 已审批通过的配额维度
}

// =============================================================================
// 存储接口
// =============================================================================

// TenantPlanStore 定义配额套餐的数据访问接口。
// 实现：services/tenant-service/internal/repo/adapters（PostgresTenantPlanStore）。
type TenantPlanStore interface {
	// Create 创建套餐及其配额维度（INSERT tenant_plans + plan_quota_limits，事务内完成）。
	Create(ctx context.Context, in CreateTenantPlanInput) (TenantPlan, error)

	// GetByID 按主键查询未删除的套餐（tenants.plan_id 外键目标查询）。
	GetByID(ctx context.Context, id uuid.UUID) (TenantPlan, error)

	// List 按过滤条件查询未删除的套餐列表，使用游标分页（limit + cursor + next_cursor）。
	// 返回 TenantPlanListItem（不含 quota_limits，需另调 GetQuotaLimits + Core meta 组装视图）。
	List(ctx context.Context, filter TenantPlanListFilter) (TenantPlanListResult, error)

	// Update 更新套餐的可变字段（name / description）。
	// PUT /tenant-plans/{planId} 修改 name/description（nil=不更新，空串=清空）；亦用于 service 层内部。
	Update(ctx context.Context, id uuid.UUID, in UpdateTenantPlanInput) (TenantPlan, error)

	// Activate 将套餐置为 active（draft 或 disabled 均可转为 active）。
	Activate(ctx context.Context, id uuid.UUID) (TenantPlan, error)

	// Disable 将套餐置为 disabled（active → disabled）。
	Disable(ctx context.Context, id uuid.UUID) (TenantPlan, error)

	// Delete 软删除套餐（is_deleted=TRUE, deleted_at=now()）；
	// 若仍有非 disabled 租户绑定则返回 ErrTenantPlanInUse（disabled 租户视为已删除，不阻止）。
	Delete(ctx context.Context, id uuid.UUID) error

	// GetQuotaLimits 读取套餐各维度限额的原始行（保留 NULL 语义）。
	GetQuotaLimits(ctx context.Context, planID uuid.UUID) ([]PlanQuotaLimit, error)

	// UpdateQuotaLimits 更新套餐各维度的限额（UPSERT plan_quota_limits）。
	// 供 TenantPlanService.UpdateQuotaLimits（PUT /tenant-plans/{planId}/quota-limits）使用：
	// 维度已存在则 UPDATE total，不存在则 INSERT；Total 由 service 填为具体值（含 default 兜底）。
	UpdateQuotaLimits(ctx context.Context, planID uuid.UUID, items []PlanQuotaLimitInput) error

	// ListBoundTenants 查询绑定到指定套餐的租户摘要列表（tenants WHERE plan_id=?）。
	ListBoundTenants(ctx context.Context, planID uuid.UUID) ([]BoundTenant, error)

	// ListBindableTenants 查询可绑定指定套餐的租户摘要：
	// status != disabled 且 plan_id IS DISTINCT FROM planID（含未绑定其它套餐）；按 name 排序。
	ListBindableTenants(ctx context.Context, planID uuid.UUID) ([]BoundTenant, error)

	// GetApprovedQuotaChanges 查询指定租户已审批通过（status='approved'）的配额变更维度，
	// 用于绑定套餐 / 修改限额同步时保留不覆盖这些维度。
	GetApprovedQuotaChanges(ctx context.Context, tenantID uuid.UUID) ([]ApprovedQuotaChange, error)
}
