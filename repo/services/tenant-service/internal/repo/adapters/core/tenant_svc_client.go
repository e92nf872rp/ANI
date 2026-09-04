package core

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
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

func (c *TenantSvcClient) CreateTenant(ctx context.Context, in ports.CreateTenantInput) (ports.Tenant, error) {
	// 步骤 1：调用 Core POST /admin/tenants（密码已为 bcrypt hash）
	// request_id / actor 经 Headers 统一透传，由 Core Gateway 注入 ctx。
	opts := anisdk.RequestOptions{
		Body: map[string]any{
			"name":                in.Name,
			"display_name":        in.DisplayName,
			"email":               in.ContactEmail,
			"plan_id":             in.PlanID.String(),
			"admin_email":         in.AdminEmail,
			"admin_name":          in.AdminName,
			"admin_password_hash": in.AdminPasswordHash,
		},
		Headers: corePropagateHeaders(ctx),
	}
	raw, err := c.sdk.Request("POST", "/admin/tenants", opts)
	if err != nil {
		// 步骤 2：SDK 错误映射（含 TENANT_NAME_CONFLICT）
		return ports.Tenant{}, mapSDKError(err)
	}
	// 步骤 3：解码 Tenant 视图
	return decodeTenant(raw)
}

func (c *TenantSvcClient) ListTenants(ctx context.Context, filter ports.ListTenantsFilter) (ports.TenantListResult, error) {
	_ = ctx
	q := url.Values{}
	if filter.Limit > 0 {
		q.Set("limit", strconv.Itoa(filter.Limit))
	}
	if strings.TrimSpace(filter.Cursor) != "" {
		q.Set("cursor", strings.TrimSpace(filter.Cursor))
	}
	if filter.Status != "" {
		q.Set("status", string(filter.Status))
	}
	if strings.TrimSpace(filter.Search) != "" {
		q.Set("search", strings.TrimSpace(filter.Search))
	}
	path := "/admin/tenants"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
	raw, err := c.sdk.Request("GET", path, anisdk.RequestOptions{})
	if err != nil {
		return ports.TenantListResult{}, mapSDKError(err)
	}
	obj, err := asObject(raw)
	if err != nil {
		return ports.TenantListResult{}, err
	}
	itemsRaw, err := asObjectSlice(obj["items"])
	if err != nil {
		return ports.TenantListResult{}, err
	}
	items := make([]ports.TenantListItem, 0, len(itemsRaw))
	for _, it := range itemsRaw {
		id, parseErr := uuid.Parse(strings.TrimSpace(stringField(it, "id")))
		if parseErr != nil {
			return ports.TenantListResult{}, fmt.Errorf("%w: tenant id: %v", ports.ErrCoreUnavailable, parseErr)
		}
		planID, parseErr := uuid.Parse(strings.TrimSpace(stringField(it, "plan_id")))
		if parseErr != nil {
			return ports.TenantListResult{}, fmt.Errorf("%w: plan_id: %v", ports.ErrCoreUnavailable, parseErr)
		}
		items = append(items, ports.TenantListItem{
			ID:          id,
			Name:        stringField(it, "name"),
			DisplayName: stringField(it, "display_name"),
			Status:      ports.TenantStatus(stringField(it, "status")),
			PlanID:      planID,
			AdminCount:  int64Field(it, "admin_count"),
			CreatedAt:   parseTimeField(it, "created_at"),
		})
	}
	return ports.TenantListResult{
		Items:      items,
		NextCursor: stringField(obj, "next_cursor"),
	}, nil
}

func (c *TenantSvcClient) UpdateTenant(ctx context.Context, tenantID uuid.UUID, in ports.UpdateTenantInput) (ports.Tenant, error) {
	_ = ctx
	body := map[string]any{}
	if in.DisplayName != nil {
		body["display_name"] = *in.DisplayName
	}
	if in.ContactEmail != nil {
		body["contact_email"] = *in.ContactEmail
	}
	path := fmt.Sprintf("/admin/tenants/%s", tenantID.String())
	raw, err := c.sdk.Request("PUT", path, anisdk.RequestOptions{Body: body})
	if err != nil {
		return ports.Tenant{}, mapSDKError(err)
	}
	return decodeTenant(raw)
}

func (c *TenantSvcClient) FreezeTenant(ctx context.Context, tenantID uuid.UUID) (ports.Tenant, error) {
	path := fmt.Sprintf("/admin/tenants/%s/freeze", tenantID.String())
	raw, err := c.sdk.Request("POST", path, anisdk.RequestOptions{
		Headers: corePropagateHeaders(ctx),
	})
	if err != nil {
		return ports.Tenant{}, mapSDKError(err)
	}
	return decodeTenant(raw)
}

func (c *TenantSvcClient) UnfreezeTenant(ctx context.Context, tenantID uuid.UUID) (ports.Tenant, error) {
	path := fmt.Sprintf("/admin/tenants/%s/unfreeze", tenantID.String())
	raw, err := c.sdk.Request("POST", path, anisdk.RequestOptions{
		Headers: corePropagateHeaders(ctx),
	})
	if err != nil {
		return ports.Tenant{}, mapSDKError(err)
	}
	return decodeTenant(raw)
}

func (c *TenantSvcClient) DisableTenant(ctx context.Context, tenantID uuid.UUID) (ports.Tenant, error) {
	path := fmt.Sprintf("/admin/tenants/%s/disable", tenantID.String())
	raw, err := c.sdk.Request("POST", path, anisdk.RequestOptions{
		Headers: corePropagateHeaders(ctx),
	})
	if err != nil {
		return ports.Tenant{}, mapSDKError(err)
	}
	return decodeTenant(raw)
}

func (c *TenantSvcClient) GetTenantAuth(context.Context, uuid.UUID) (ports.TenantAuth, error) {
	return ports.TenantAuth{}, ports.ErrNotImplemented
}

func (c *TenantSvcClient) UpdateTenantAuth(context.Context, uuid.UUID, ports.TenantAuthPatch) (ports.TenantAuth, error) {
	return ports.TenantAuth{}, ports.ErrNotImplemented
}

func (c *TenantSvcClient) ListTenantLifecycle(context.Context, uuid.UUID, ports.TenantLifecycleFilter) (ports.TenantLifecycleListResult, error) {
	return ports.TenantLifecycleListResult{}, ports.ErrNotImplemented
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
	out := ports.Tenant{
		ID:           id,
		Name:         stringField(obj, "name"),
		DisplayName:  stringField(obj, "display_name"),
		ContactEmail: stringField(obj, "contact_email"),
		Status:       ports.TenantStatus(stringField(obj, "status")),
		PlanID:       planID,
		UserCount:    int64Field(obj, "user_count"),
		AdminCount:   int64Field(obj, "admin_count"),
		CreatedAt:    parseTimeField(obj, "created_at"),
		UpdatedAt:    parseTimeField(obj, "updated_at"),
	}
	if t := parseTimeField(obj, "frozen_at"); !t.IsZero() {
		out.FrozenAt = &t
	}
	if t := parseTimeField(obj, "disabled_at"); !t.IsZero() {
		out.DisabledAt = &t
	}
	if rawAuth, ok := obj["auth"]; ok && rawAuth != nil {
		authObj, authErr := asObject(rawAuth)
		if authErr != nil {
			return ports.Tenant{}, authErr
		}
		out.Auth = &ports.TenantAuthSummary{
			SsoEnabled:  boolField(authObj, "sso_enabled"),
			MfaRequired: boolField(authObj, "mfa_required"),
		}
	} else {
		out.Auth = &ports.TenantAuthSummary{}
	}
	return out, nil
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
