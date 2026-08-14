package ports

import (
	"context"

	"github.com/google/uuid"
)

// 配额套餐的"Core 配额 API"客户端端口。
// Core 的通用资源配额与计量落地方案中已实现：
//   GET    /api/v1/admin/quota-meta
//   GET    /api/v1/admin/tenants/{tenant_id}/quota
//   POST   /api/v1/admin/tenants/{tenant_id}/quota
//   PUT    /api/v1/admin/tenants/{tenant_id}/quota
//   DELETE /api/v1/admin/tenants/{tenant_id}/quota
// tenant-service 仅作为调用方访问此 Core API，不重复实现配额逻辑。
// 实现：services/tenant-service/internal/repo/adapters/core。

// CoreQuotaItem 表示下发给 Core 的单个配额维度项（请求侧）。
type CoreQuotaItem struct {
	ResourceType string // 配额维度标识
	Total        int64  // 限额值（须为具体数值，禁止 NULL）
}

// CoreQuotaResult 表示 Core 对单个维度下发后的返回结果（响应侧）。
// tightened 为 true 表示 Core 因 total < used+reserved 而自动收紧为当前已占用值（不是错误）。
type CoreQuotaResult struct {
	ResourceType string // 配额维度标识
	Total        int64  // 生效后的限额值
	Used         int64  // 当前已用
	Reserved     int64  // 当前预留
	Tightened    bool   // 是否被自动收紧
}

// QuotaMeta 表示 Core GET /admin/quota-meta 返回的单个维度。
// Core OpenAPI 仅返回 enabled=true 的维度且 schema 无 enabled 字段；
// 客户端将返回项统一标记 Enabled=true，并带上 DefaultQuota / DisplayName / Unit / IsDiscrete。
type QuotaMeta struct {
	ResourceType string
	Enabled      bool
	DefaultQuota int64  // 对应 Core schema default_quota
	DisplayName  string // 对应 Core schema display_name
	Unit         string // 对应 Core schema unit
	IsDiscrete   bool   // 对应 Core schema is_discrete（true=整数计数）
}

// QuotaSvcClient 定义通向 Core 配额 API 的调用客户端接口。
type QuotaSvcClient interface {
	// ListQuotaMeta 查询可用配额元数据（Core GET /admin/quota-meta）。
	// 每次调用均远程请求 Core；Core 不可用时返回 ErrCoreUnavailable。
	ListQuotaMeta(ctx context.Context) ([]QuotaMeta, error)

	// GetQuota 查询租户配额（Core GET /admin/tenants/{id}/quota）。
	// 租户不存在 → ErrTenantNotFound；租户存在但无配额行 → 空切片。
	GetQuota(ctx context.Context, tenantID uuid.UUID) ([]CoreQuotaResult, error)

	// PutQuota 批量更新租户配额上限（Core PUT /admin/tenants/{id}/quota）。
	// 返回各维度结果（含 tightened）；维度行不存在 → ErrQuotaNotFound。
	PutQuota(ctx context.Context, tenantID uuid.UUID, items []CoreQuotaItem) ([]CoreQuotaResult, error)

	// CreateQuota 批量新建租户配额行（Core POST /admin/tenants/{id}/quota）。
	// 供绑定套餐时初始化配额；冲突语义由 Core 映射为 ErrQuotaAlreadyExists。
	CreateQuota(ctx context.Context, tenantID uuid.UUID, items []CoreQuotaItem) ([]CoreQuotaResult, error)

	// DeleteQuota 删除租户全部配额（Core DELETE /admin/tenants/{id}/quota）。
	DeleteQuota(ctx context.Context, tenantID uuid.UUID) error
}
