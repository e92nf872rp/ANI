package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	anisdk "github.com/kubercloud/ani-sdks/core-go/anisdk"
	"github.com/kubercloud/ani/services/tenant-service/internal/repo/ports"
)

// QuotaSvcClient 基于 Core Go SDK（anisdk.Client）实现 ports.QuotaSvcClient。
type QuotaSvcClient struct {
	sdk anisdk.Client
}

var _ ports.QuotaSvcClient = (*QuotaSvcClient)(nil)

// NewQuotaSvcClient 从环境变量构造 Core 配额 API 客户端（CORE_API_BASE_URL / CORE_API_TOKEN）。
func NewQuotaSvcClient() ports.QuotaSvcClient {
	return &QuotaSvcClient{sdk: newCoreSDKClient()}
}

// ListQuotaMeta 调用 Core GET /admin/quota-meta。
func (c *QuotaSvcClient) ListQuotaMeta(ctx context.Context) ([]ports.QuotaMeta, error) {
	_ = ctx
	raw, err := c.sdk.Request("GET", "/admin/quota-meta", anisdk.RequestOptions{})
	if err != nil {
		return nil, mapSDKError(err)
	}
	obj, err := asObject(raw)
	if err != nil {
		return nil, err
	}
	items, err := asObjectSlice(obj["items"])
	if err != nil {
		return nil, err
	}
	out := make([]ports.QuotaMeta, 0, len(items))
	for _, it := range items {
		rt := strings.TrimSpace(stringField(it, "resource_type"))
		if rt == "" {
			continue
		}
		out = append(out, ports.QuotaMeta{
			ResourceType: rt,
			Enabled:      true,
			DefaultQuota: int64Field(it, "default_quota"),
			DisplayName:  stringField(it, "display_name"),
			Unit:         stringField(it, "unit"),
			IsDiscrete:   boolField(it, "is_discrete"),
		})
	}
	return out, nil
}

// GetQuota 调用 Core GET /admin/tenants/{id}/quota。
func (c *QuotaSvcClient) GetQuota(ctx context.Context, tenantID uuid.UUID) ([]ports.CoreQuotaResult, error) {
	_ = ctx
	path := fmt.Sprintf("/admin/tenants/%s/quota", tenantID.String())
	raw, err := c.sdk.Request("GET", path, anisdk.RequestOptions{})
	if err != nil {
		return nil, mapSDKError(err)
	}
	return decodeQuotaItems(raw)
}

// PutQuota 调用 Core PUT /admin/tenants/{id}/quota。
func (c *QuotaSvcClient) PutQuota(ctx context.Context, tenantID uuid.UUID, items []ports.CoreQuotaItem) ([]ports.CoreQuotaResult, error) {
	_ = ctx
	path := fmt.Sprintf("/admin/tenants/%s/quota", tenantID.String())
	raw, err := c.sdk.Request("PUT", path, anisdk.RequestOptions{
		Body: map[string]any{"items": encodeQuotaItems(items)},
	})
	if err != nil {
		return nil, mapSDKError(err)
	}
	return decodeQuotaItems(raw)
}

// CreateQuota 调用 Core POST /admin/tenants/{id}/quota。
func (c *QuotaSvcClient) CreateQuota(ctx context.Context, tenantID uuid.UUID, items []ports.CoreQuotaItem) ([]ports.CoreQuotaResult, error) {
	_ = ctx
	path := fmt.Sprintf("/admin/tenants/%s/quota", tenantID.String())
	raw, err := c.sdk.Request("POST", path, anisdk.RequestOptions{
		Body: map[string]any{"items": encodeQuotaItems(items)},
	})
	if err != nil {
		return nil, mapSDKError(err)
	}
	return decodeQuotaItems(raw)
}

// UpsertQuota 调用 Core PUT /admin/tenants/{id}/quota/upsert。
func (c *QuotaSvcClient) UpsertQuota(ctx context.Context, tenantID uuid.UUID, items []ports.CoreQuotaItem) ([]ports.CoreQuotaResult, error) {
	_ = ctx
	path := fmt.Sprintf("/admin/tenants/%s/quota/upsert", tenantID.String())
	raw, err := c.sdk.Request("PUT", path, anisdk.RequestOptions{
		Body: map[string]any{"items": encodeQuotaItems(items)},
	})
	if err != nil {
		return nil, mapSDKError(err)
	}
	return decodeQuotaItems(raw)
}

// DeleteQuota 调用 Core DELETE /admin/tenants/{id}/quota。
func (c *QuotaSvcClient) DeleteQuota(ctx context.Context, tenantID uuid.UUID) error {
	_ = ctx
	path := fmt.Sprintf("/admin/tenants/%s/quota", tenantID.String())
	_, err := c.sdk.Request("DELETE", path, anisdk.RequestOptions{})
	if err != nil {
		return mapSDKError(err)
	}
	return nil
}

func encodeQuotaItems(items []ports.CoreQuotaItem) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		out = append(out, map[string]any{
			"resource_type": it.ResourceType,
			"total":         it.Total,
		})
	}
	return out
}

func decodeQuotaItems(raw any) ([]ports.CoreQuotaResult, error) {
	obj, err := asObject(raw)
	if err != nil {
		return nil, err
	}
	items, err := asObjectSlice(obj["items"])
	if err != nil {
		return nil, err
	}
	out := make([]ports.CoreQuotaResult, 0, len(items))
	for _, it := range items {
		out = append(out, ports.CoreQuotaResult{
			ResourceType: stringField(it, "resource_type"),
			Total:        int64Field(it, "total"),
			Used:         int64Field(it, "used"),
			Reserved:     int64Field(it, "reserved"),
			Tightened:    boolField(it, "tightened"),
		})
	}
	return out, nil
}
