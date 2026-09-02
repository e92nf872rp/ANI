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

// ListAvailableTenants 调用 Core GET /admin/tenant-admins/available-tenants。
func (c *TenantSvcClient) ListAvailableTenants(ctx context.Context) ([]ports.BoundTenant, error) {
	_ = ctx
	// 步骤 1：调用 Core GET /admin/tenant-admins/available-tenants
	raw, err := c.sdk.Request("GET", "/admin/tenant-admins/available-tenants", anisdk.RequestOptions{})
	if err != nil {
		return nil, mapSDKError(err)
	}
	// 步骤 2：解析响应对象并校验 items 字段
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
	// 步骤 3：逐项解码为 BoundTenant（id 必须为 UUID）
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
