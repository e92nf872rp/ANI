package ports

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// 配额套餐管理的端口（ports）定义。
// 本文件只声明接口与领域结构体（DTO/Entity），不含任何实现逻辑。
// 具体实现由 postgres adapter（PostgresTenantPlanStore）在后续 issue 填充。

// =============================================================================
// 实体与 DTO
// =============================================================================

// TenantPlan 表示一条配额套餐记录（对应 tenant_plans 表的一行）。
// status 状态机：draft → active → disabled，软删除通过 is_deleted + deleted_at 标记。
type TenantPlan struct {
	ID          uuid.UUID  // 主键；被 tenants.plan_id 外键引用（ON DELETE RESTRICT）
	Code        string     // 业务代码；
	Name        string     // 套餐名称
	Description string     // 描述
	Status      string     // draft | active | disabled
	IsDeleted   bool       // 软删除标记
	DeletedAt   *time.Time // 软删除时间；nil = 未删除
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// PlanQuotaLimit 表示套餐某配额维度的限额原始行（对应 plan_quota_limits 表一行）。
type PlanQuotaLimit struct {
	PlanID       uuid.UUID // 所属套餐
	ResourceType string    // 配额维度标识（外键 → resource_quota_meta.resource_type）
	Total        *int64    // 限额值；
}

// PlanQuotaLimitView 表示套餐配额维度的"展示视图"（GET /quota-limits 返回）。
// 已 JOIN resource_quota_meta 补充分展示名与单位，并把未设置的维度用
// 展示（GET /quota-limits）与绑定下发均使用本视图。
type PlanQuotaLimitView struct {
	ResourceType string // 配额维度标识
	DisplayName  string // 展示名（来自 resource_quota_meta）
	Unit         string // 单位（来自 resource_quota_meta）
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
// Total 为 nil 表示"用该维度默认值"（resource_quota_meta.default_quota）。
type PlanQuotaLimitInput struct {
	ResourceType string // 配额维度标识
	Total        *int64 // 限额值；nil = 用默认值
}

// UpdateTenantPlanInput 是更新套餐的入参。
// 注意：PUT /tenant-plans 编辑端点已删除；本类型保留供 service 层内部使用
// （例如 bind-plan 等需要更新套餐字段的逻辑），不再作为对外 API 的入参。
type UpdateTenantPlanInput struct {
	Name        *string // nil = 不更新
	Description *string // nil = 不更新
}

// TenantPlanListFilter 是套餐列表查询的过滤条件（游标分页）。
type TenantPlanListFilter struct {
	Limit  int    // 每页数量，default 20，max 100
	Cursor string // 上一页返回的 next_cursor；空串 = 第一页
	Status string // "" = 全部；否则取值 draft | active | disabled
	Search string // 模糊匹配 name（HEAD 查询）
}

// TenantPlanListItem 是套餐列表/详情的查询视图（仅在查询接口返回，不进 TenantPlan 实体）。
// 相比 TenantPlan 额外携带 tenant_count（绑定租户数），由 store 通过子查询统计。
type TenantPlanListItem struct {
	ID          uuid.UUID
	Code        string
	Name        string
	Description string
	Status      string // draft | active | disabled
	TenantCount int64  // 绑定租户数量（COUNT tenants WHERE plan_id=? AND status != 'disabled'）
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
	Status      string // active | frozen | disabled
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

	// GetByCode 按业务代码 code 查询未删除的套餐（不参与 tenants 外键）。
	GetByCode(ctx context.Context, code string) (TenantPlan, error)

	// List 按过滤条件查询未删除的套餐列表，使用游标分页（limit + cursor + next_cursor）。
	// 返回 TenantPlanListItem（不含 quota_limits，需另调 GetQuotaLimitViews）。
	List(ctx context.Context, filter TenantPlanListFilter) (TenantPlanListResult, error)

	// Update 更新套餐的可变字段（name / description）。
	// 注意：PUT /tenant-plans 端点已删除，本方法供 service 层内部使用。
	Update(ctx context.Context, id uuid.UUID, in UpdateTenantPlanInput) (TenantPlan, error)

	// Activate 将套餐置为 active（draft 或 disabled 均可转为 active）。
	Activate(ctx context.Context, id uuid.UUID) (TenantPlan, error)

	// Disable 将套餐置为 disabled（active → disabled）。
	Disable(ctx context.Context, id uuid.UUID) (TenantPlan, error)

	// Delete 软删除套餐（is_deleted=TRUE, deleted_at=now()）；需校验无租户关联。
	Delete(ctx context.Context, id uuid.UUID) error

	// GetQuotaLimits 读取套餐各维度限额的原始行（保留 NULL 语义）。
	GetQuotaLimits(ctx context.Context, planID uuid.UUID) ([]PlanQuotaLimit, error)

	// GetQuotaLimitViews 读取套餐各维度限额的展示视图：
	// JOIN resource_quota_meta 并 COALESCE(total, default_quota) 兜底为具体数值。
	// GET /tenant-plans/{planId}/quota-limits 展示与绑定下发均使用本方法。
	GetQuotaLimitViews(ctx context.Context, planID uuid.UUID) ([]PlanQuotaLimitView, error)

	// UpdateQuotaLimits 更新套餐各维度的限额（UPSERT plan_quota_limits）。
	// 供 TenantPlanService.UpdateQuotaLimits（issue-006，PATCH /tenant-plans/{planId}/quota-limits）使用：
	// 维度已存在则 UPDATE total，不存在则 INSERT；Total 为 nil 表示用默认值。
	UpdateQuotaLimits(ctx context.Context, planID uuid.UUID, items []PlanQuotaLimitInput) error

	// ListBoundTenants 查询绑定到指定套餐的租户摘要列表（tenants WHERE plan_id=?）。
	ListBoundTenants(ctx context.Context, planID uuid.UUID) ([]BoundTenant, error)

	// GetApprovedQuotaChanges 查询指定租户已审批通过（status='approved'）的配额变更维度，
	// 用于绑定套餐 / 修改限额同步时保留不覆盖这些维度。
	GetApprovedQuotaChanges(ctx context.Context, tenantID uuid.UUID) ([]ApprovedQuotaChange, error)
}
