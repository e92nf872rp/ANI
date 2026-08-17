package ports

import (
	"time"

	"github.com/google/uuid"
)

// 租户领域结构体（DTO）。
// tenants 表的读写已迁到 Core（GET/PUT /admin/tenants/...）；本包仅保留 BindPlanQuota
// 所需的领域类型，供 Core HTTP 客户端反序列化与 service 层状态判断使用。

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

// Tenant 表示一条租户记录（对应 tenants 表的一行；由 Core API 返回）。
// PlanID 外键指向 tenant_plans.id（tenants 表不存 plan_code）。
// Status 状态机：active → frozen → disabled。
type Tenant struct {
	ID           uuid.UUID    // 主键
	Name         string       // 租户标识
	DisplayName  string       // 展示名
	ContactEmail string       // 联系邮箱（Core 最小视图可能为空）
	Status       TenantStatus // active | frozen | disabled
	PlanID       uuid.UUID    // 外键 → tenant_plans.id
	FrozenAt     *time.Time
	DisabledAt   *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}
