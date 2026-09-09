package core

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	anisdk "github.com/kubercloud/ani-sdks/core-go/anisdk"
	"github.com/kubercloud/ani/services/tenant-service/internal/repo/ports"
)

// MeteringClient 基于 Core Go SDK（anisdk.Client）实现 ports.BillingMeteringClient。
// 用量数据一律经 Core OpenAPI GET /metering/usage/platform 获取（月窗口 + group_by=tenant_id），
// 禁止直查 Core 库 metering_usage_records（CLAUDE.md §3）。
type MeteringClient struct {
	sdk anisdk.Client
}

var _ ports.BillingMeteringClient = (*MeteringClient)(nil)

// NewMeteringClient 从环境变量构造 Core 平台计量客户端（CORE_API_BASE_URL / CORE_API_TOKEN）。
func NewMeteringClient() ports.BillingMeteringClient {
	return &MeteringClient{sdk: newCoreSDKClient()}
}

// GetPlatformUsage 调用 Core GET /metering/usage/platform（平台跨租户聚合）。
// tenantFilter 非 nil 时仅查询该租户（单租户钻取）。
// tenant_id 无法解析的行跳过（不因单个脏行毁掉整月账单折算）；
// 响应缺 items 视为协议错误（ErrCoreUnavailable）。
func (c *MeteringClient) GetPlatformUsage(ctx context.Context, start, end time.Time, tenantFilter *uuid.UUID) ([]ports.BillingUsageRecord, error) {
	_ = ctx
	// 步骤 1：组装月窗口查询（RFC3339 + group_by=tenant_id；可选单租户过滤）
	q := url.Values{}
	q.Set("start_time", start.Format(time.RFC3339))
	q.Set("end_time", end.Format(time.RFC3339))
	q.Set("group_by", "tenant_id")
	if tenantFilter != nil {
		q.Set("tenant_id", tenantFilter.String())
	}

	// 步骤 2：调用 Core API
	raw, err := c.sdk.Request("GET", "/metering/usage/platform?"+q.Encode(), anisdk.RequestOptions{})
	if err != nil {
		return nil, mapSDKError(err)
	}

	// 步骤 3：解析响应对象并校验 items 字段
	obj, err := asObject(raw)
	if err != nil {
		return nil, err
	}
	if _, ok := obj["items"]; !ok {
		return nil, fmt.Errorf("%w: missing items", ports.ErrCoreUnavailable)
	}
	items, err := asObjectSlice(obj["items"])
	if err != nil {
		return nil, err
	}

	// 步骤 4：逐行解析 tenant_id × resource_type × total_quantity
	out := make([]ports.BillingUsageRecord, 0, len(items))
	for _, it := range items {
		tenantID, parseErr := uuid.Parse(strings.TrimSpace(stringField(it, "tenant_id")))
		if parseErr != nil {
			continue
		}
		resourceType := strings.TrimSpace(stringField(it, "resource_type"))
		if resourceType == "" {
			continue
		}
		out = append(out, ports.BillingUsageRecord{
			TenantID:      tenantID,
			ResourceType:  resourceType,
			TotalQuantity: float64Field(it, "total_quantity"),
		})
	}
	return out, nil
}
