package core

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"
	anisdk "github.com/kubercloud/ani-sdks/core-go/anisdk"
	"github.com/kubercloud/ani/services/tenant-service/internal/repo/ports"
)

// TenantPlanSvcClient 基于 Core Go SDK（anisdk.Client）实现 ports.TenantPlanSvcClient。
type TenantPlanSvcClient struct {
	sdk anisdk.Client
}

var _ ports.TenantPlanSvcClient = (*TenantPlanSvcClient)(nil)

// NewTenantPlanSvcClient 从环境变量构造 Core 配额套餐绑定 API 客户端。
func NewTenantPlanSvcClient() ports.TenantPlanSvcClient {
	return &TenantPlanSvcClient{sdk: newCoreSDKClient()}
}

// UpdateTenantPlan 调用 Core PUT /admin/tenants/{id}/plan。
func (c *TenantPlanSvcClient) UpdateTenantPlan(ctx context.Context, tenantID uuid.UUID, planID uuid.UUID) (ports.Tenant, error) {
	_ = ctx
	headers, err := idempotencyHeaders()
	if err != nil {
		return ports.Tenant{}, err
	}
	path := fmt.Sprintf("/admin/tenants/%s/plan", tenantID.String())
	raw, err := c.sdk.Request("PUT", path, anisdk.RequestOptions{
		Body:    map[string]any{"plan_id": planID.String()},
		Headers: headers,
	})
	if err != nil {
		return ports.Tenant{}, mapSDKError(err)
	}
	return decodeTenant(raw)
}

// CountBoundTenants 调用 Core GET /admin/plans/bound-tenant-counts。
func (c *TenantPlanSvcClient) CountBoundTenants(ctx context.Context, planIDs []uuid.UUID) (map[uuid.UUID]int64, error) {
	_ = ctx
	out := make(map[uuid.UUID]int64, len(planIDs))
	if len(planIDs) == 0 {
		return out, nil
	}
	q := url.Values{}
	for _, id := range planIDs {
		out[id] = 0
		q.Add("plan_id", id.String())
	}
	raw, err := c.sdk.Request("GET", "/admin/plans/bound-tenant-counts?"+q.Encode(), anisdk.RequestOptions{})
	if err != nil {
		return nil, mapSDKError(err)
	}
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
	for _, it := range items {
		id, parseErr := uuid.Parse(strings.TrimSpace(stringField(it, "plan_id")))
		if parseErr != nil {
			continue
		}
		out[id] = int64Field(it, "count")
	}
	return out, nil
}

// ListBoundTenants 调用 Core GET /admin/plans/{id}/bound-tenants。
func (c *TenantPlanSvcClient) ListBoundTenants(ctx context.Context, planID uuid.UUID) ([]ports.BoundTenant, error) {
	return c.listTenantSummaries(ctx, fmt.Sprintf("/admin/plans/%s/bound-tenants", planID.String()))
}

// ListBindableTenants 调用 Core GET /admin/plans/{id}/bindable-tenants。
func (c *TenantPlanSvcClient) ListBindableTenants(ctx context.Context, planID uuid.UUID) ([]ports.BoundTenant, error) {
	return c.listTenantSummaries(ctx, fmt.Sprintf("/admin/plans/%s/bindable-tenants", planID.String()))
}

func (c *TenantPlanSvcClient) listTenantSummaries(ctx context.Context, path string) ([]ports.BoundTenant, error) {
	_ = ctx
	raw, err := c.sdk.Request("GET", path, anisdk.RequestOptions{})
	if err != nil {
		return nil, mapSDKError(err)
	}
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
	out := make([]ports.BoundTenant, 0, len(items))
	for _, it := range items {
		id, parseErr := uuid.Parse(strings.TrimSpace(stringField(it, "id")))
		if parseErr != nil {
			return nil, fmt.Errorf("%w: tenant id: %v", ports.ErrCoreUnavailable, parseErr)
		}
		out = append(out, ports.BoundTenant{
			ID:          id,
			Name:        stringField(it, "name"),
			DisplayName: stringField(it, "display_name"),
			Status:      ports.TenantStatus(stringField(it, "status")),
		})
	}
	return out, nil
}
