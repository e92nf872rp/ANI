package ports

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// 租户数据访问端口（ports）定义。
// 本文件只声明接口与领域结构体（DTO/Entity），不含任何实现逻辑。
// 本文件是「配额套餐管理」Issue 链中为满足绑定套餐（issue-007）所需的最小化 TenantStore：
// 只声明绑定套餐依赖的读写 tenants 表方法（GetByID 查状态、UpdatePlan 换 plan_id）。
// - 不含 SSO/MFA 方法：由 TenantAuthStore 承载（独立租户管理 PR 定义）
// - 不含 UpdateQuotas：配额由 Core resource_quota 承载，经 QuotaSvcClient（core_quota.go）访问
// 完整 TenantStore（Create/List/Update/Freeze/Unfreeze/Disable 等）由后续独立租户管理 PR 补充。

// =============================================================================
// 状态枚举
// =============================================================================

// TenantStatus 租户状态机：active → frozen → disabled。
type TenantStatus string

const (
	TenantStatusActive   TenantStatus = "active"
	TenantStatusFrozen   TenantStatus = "frozen"
	TenantStatusDisabled TenantStatus = "disabled"
)

// Valid 报告是否为已知租户状态（不含空串）。
func (s TenantStatus) Valid() bool {
	switch s {
	case TenantStatusActive, TenantStatusFrozen, TenantStatusDisabled:
		return true
	default:
		return false
	}
}

// =============================================================================
// 实体与 DTO
// =============================================================================

// Tenant 表示一条租户记录（对应 tenants 表的一行）。
// PlanID 外键指向 tenant_plans.id（tenants 表不存 plan_code）。
// Status 状态机：active → frozen → disabled。
// 注意：MFA/SSO 与配额均不在本结构体内（分别由 TenantAuthStore / Core QuotaService 承载）。
type Tenant struct {
	ID           uuid.UUID    // 主键
	Name         string       // 租户标识
	DisplayName  string       // 展示名
	ContactEmail string       // 联系邮箱
	Status       TenantStatus // active | frozen | disabled
	PlanID       uuid.UUID    // 外键 → tenant_plans.id
	FrozenAt     *time.Time
	DisabledAt   *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// =============================================================================
// 存储接口
// =============================================================================

// TenantStore 定义租户数据的最小访问接口（配额套餐链所需）。
// 实现：services/tenant-service/internal/repo/adapters（PostgresTenantStore）。
type TenantStore interface {
	// GetByID 按主键查询租户，返回 status / plan_id 等字段。
	// 绑定套餐（issue-007）据此判断租户是否已 disabled（disabled → 409 TENANT_STATE_INVALID）。
	GetByID(ctx context.Context, id uuid.UUID) (Tenant, error)

	// UpdatePlan 切换租户绑定的套餐，仅更新 tenants.plan_id 字段，不影响配额。
	// 绑定套餐（issue-007）在批量下发 Core 配额成功后调用；若新旧 plan_id 相同时由 service 层跳过。
	UpdatePlan(ctx context.Context, id uuid.UUID, planID uuid.UUID) (Tenant, error)
}
