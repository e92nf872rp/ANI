package core

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	anisdk "github.com/kubercloud/ani-sdks/core-go/anisdk"
	"github.com/kubercloud/ani/services/tenant-service/internal/repo/ports"
)

// TenantSvcClient 基于 Core Go SDK（anisdk.Client）实现 ports.TenantSvcClient。
type TenantSvcClient struct {
	sdk anisdk.Client
}

var _ ports.TenantSvcClient = (*TenantSvcClient)(nil)

// NewTenantSvcClient 从环境变量构造 Core 租户 API 客户端（CORE_API_BASE_URL / CORE_API_TOKEN）。
func NewTenantSvcClient() ports.TenantSvcClient {
	return &TenantSvcClient{sdk: newCoreSDKClient()}
}

// GetTenant 调用 Core GET /admin/tenants/{id}。
func (c *TenantSvcClient) GetTenant(ctx context.Context, tenantID uuid.UUID) (ports.Tenant, error) {
	_ = ctx
	path := fmt.Sprintf("/admin/tenants/%s", tenantID.String())
	raw, err := c.sdk.Request("GET", path, anisdk.RequestOptions{})
	if err != nil {
		return ports.Tenant{}, mapSDKError(err)
	}
	return decodeTenant(raw)
}

// UpdateTenantPlan 调用 Core PUT /admin/tenants/{id}/plan。
func (c *TenantSvcClient) UpdateTenantPlan(ctx context.Context, tenantID uuid.UUID, planID uuid.UUID) (ports.Tenant, error) {
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

func decodeTenant(raw any) (ports.Tenant, error) {
	obj, err := asObject(raw)
	if err != nil {
		return ports.Tenant{}, err
	}
	id, err := uuid.Parse(strings.TrimSpace(stringField(obj, "id")))
	if err != nil {
		return ports.Tenant{}, fmt.Errorf("%w: tenant id: %v", ports.ErrCoreUnavailable, err)
	}
	planID, err := uuid.Parse(strings.TrimSpace(stringField(obj, "plan_id")))
	if err != nil {
		return ports.Tenant{}, fmt.Errorf("%w: plan_id: %v", ports.ErrCoreUnavailable, err)
	}
	return ports.Tenant{
		ID:          id,
		Name:        stringField(obj, "name"),
		DisplayName: stringField(obj, "display_name"),
		Status:      ports.TenantStatus(stringField(obj, "status")),
		PlanID:      planID,
		CreatedAt:   parseTimeField(obj, "created_at"),
		UpdatedAt:   parseTimeField(obj, "updated_at"),
	}, nil
}

func parseTimeField(m map[string]any, key string) time.Time {
	raw := strings.TrimSpace(stringField(m, key))
	if raw == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t
	}
	t, _ := time.Parse(time.RFC3339, raw)
	return t
}
