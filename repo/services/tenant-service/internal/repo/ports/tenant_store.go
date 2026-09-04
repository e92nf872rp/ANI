package ports

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// 租户领域结构体与本地表 store 端口。
// tenants / tenant_auth / tenant_lifecycle 主数据经 Core（TenantSvcClient）；
// 本包保留领域 DTO 供 Core HTTP 客户端反序列化与 service 层状态判断，
// 并承载 tenant_quota_change 本地持久化。

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

// ParseTenantStatusFilter 解析列表过滤用 status：空=全部；非法值报错。
func ParseTenantStatusFilter(raw string) (TenantStatus, error) {
	s := TenantStatus(strings.TrimSpace(raw))
	if s == "" {
		return "", nil
	}
	if !s.Valid() {
		return "", fmt.Errorf("%w: status must be active, frozen, or disabled", ErrValidationFailed)
	}
	return s, nil
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
	UserCount    int64
	AdminCount   int64
	Auth         *TenantAuthSummary // getTenant 连查；仅 sso_enabled / mfa_required；nil → 双 false
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// TenantAuthSummary 是详情内嵌的 auth 摘要（仅开关）。
type TenantAuthSummary struct {
	SsoEnabled  bool
	MfaRequired bool
}

// QuotaChangeRequest 表示 tenant_quota_change 表一行（US-012~014）。
// 同一次提交的多维度共享 RequestID；复合主键 (tenant_id, request_id, resource_type)。
type QuotaChangeRequest struct {
	TenantID     uuid.UUID
	RequestID    uuid.UUID
	ResourceType string
	OldValue     *int64 // NULL = 首次设置
	NewValue     int64
	Status       string // pending | approved | rejected
	RequestedBy  uuid.UUID
	ReviewedBy   *uuid.UUID
	ReviewedAt   *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// QuotaChangePendingInput 是 UpsertPendingQuotaChanges 单维度写入输入。
type QuotaChangePendingInput struct {
	ResourceType string
	OldValue     *int64
	NewValue     int64
	RequestedBy  uuid.UUID
}

// =============================================================================
// Store 接口（仅 tenant_quota_change）
// =============================================================================

// TenantStore 定义租户模块对本地表的读写访问。
// 实现：internal/repo/adapters/postgres/tenant_store.go。
//
// tenants / tenant_auth / tenant_lifecycle 经 TenantSvcClient（Core API）；禁止在本 store 直接 SQL 操作。
type TenantStore interface {
	// UpsertPendingQuotaChanges 单事务：同批共用 requestID。
	// 逐维度 UPDATE WHERE tenant_id+resource_type+status='pending'（覆盖并改写 request_id）；0 行则 INSERT。
	UpsertPendingQuotaChanges(ctx context.Context, tenantID, requestID uuid.UUID, items []QuotaChangePendingInput) error

	// ListQuotaChangesByTenant 按租户查询；status 空串表示不过滤；不分页，按 created_at DESC。
	ListQuotaChangesByTenant(ctx context.Context, tenantID uuid.UUID, status string) ([]QuotaChangeRequest, error)

	// ListQuotaChangesByRequestID 按 request_id 列出该批全部维度行；无行 → ErrQuotaChangeRequestNotFound。
	ListQuotaChangesByRequestID(ctx context.Context, tenantID, requestID uuid.UUID) ([]QuotaChangeRequest, error)

	// SetQuotaChangeStatusByRequestID 乐观锁：将该 request_id 下全部 pending 行更新为 approved/rejected；
	// 返回受影响行数（0 → 无 pending 行：不存在或已审）。
	SetQuotaChangeStatusByRequestID(ctx context.Context, tenantID, requestID uuid.UUID, status string, reviewedBy uuid.UUID) (int64, error)
}
