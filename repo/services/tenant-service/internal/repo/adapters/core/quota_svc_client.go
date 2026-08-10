package core

import (
	"context"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/services/tenant-service/internal/repo/ports"
)

// QuotaSvcClient 是 ports.QuotaSvcClient 的 Core API HTTP 客户端实现。
// 本文件当前为【占位实现】：方法体以 panic("not implemented") 标记，
// 仅用于建立编译通过的类型契约，具体调用 Core PUT /quota 的逻辑由 issue-008 填充。
type QuotaSvcClient struct {
	// baseURL    string   // Core API 基址（issue-008 填充）
	// httpClient *http.Client
}

// 编译期断言：确保 QuotaSvcClient 满足 ports.QuotaSvcClient 接口。
var _ ports.QuotaSvcClient = (*QuotaSvcClient)(nil)

// NewQuotaSvcClient 构造 Core 配额 API 客户端实例。
func NewQuotaSvcClient() ports.QuotaSvcClient {
	return &QuotaSvcClient{}
}

// PutQuota 批量下发/更新指定租户的配额（调用 Core PUT /api/v1/admin/tenants/{tenant_id}/quota）。
func (c *QuotaSvcClient) PutQuota(ctx context.Context, tenantID uuid.UUID, items []ports.CoreQuotaItem) ([]ports.CoreQuotaResult, error) {
	panic("not implemented: issue-008")
}
