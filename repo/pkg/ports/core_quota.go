package ports

import (
	"context"

	"github.com/google/uuid"
)

// 配额套餐的"Core 配额 API"客户端端口。
// Core 的通用资源配额与计量落地方案中已实现：
//   PUT /api/v1/admin/tenants/{tenant_id}/quota  （批量下发/更新配额）
// tenant-service 仅作为调用方访问此 Core API，不重复实现配额逻辑。
// 实现：repo/pkg/adapters/core/quota_svc_client.go。

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

// QuotaSvcClient 定义通向 Core 配额 API 的调用客户端接口。
// 用于绑定套餐 / 修改限额同步时批量下发配额。
type QuotaSvcClient interface {
	// PutQuota 批量下发/更新指定租户的配额（调用 Core PUT /api/v1/admin/tenants/{tenant_id}/quota）。
	// 返回各维度的下发结果（含自动收紧标记 tightened）。
	PutQuota(ctx context.Context, tenantID uuid.UUID, items []CoreQuotaItem) ([]CoreQuotaResult, error)
}
