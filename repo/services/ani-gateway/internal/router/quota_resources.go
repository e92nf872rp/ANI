package router

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"
	"github.com/kubercloud/ani/pkg/ports"
)

// quotaAPI 持有 QuotaAdminService 接口，构造时注入 adapter（照搬 demo_instances.go 模式）。
type quotaAPI struct {
	admin ports.QuotaAdminService
}

// registerQuotaResources 注册 QuotaAdminService 的管理端点：
// 4 个 /admin/tenants/{tenant_id}/quota + 1 个 /admin/quota-meta。
// 这些路径属于平台/管理路由，由 scopeAllowedForPath 守卫要求 platform scope。
func registerQuotaResources(v1 *route.RouterGroup, admin ports.QuotaAdminService) {
	api := quotaAPI{admin: admin}
	v1.POST("/admin/tenants/:tenant_id/quota", api.createTenantQuota)
	v1.PUT("/admin/tenants/:tenant_id/quota", api.updateTenantQuota)
	v1.GET("/admin/tenants/:tenant_id/quota", api.getTenantQuota)
	v1.DELETE("/admin/tenants/:tenant_id/quota", api.deleteTenantQuota)
	v1.GET("/admin/quota-meta", api.listQuotaMeta)
	v1.PUT("/admin/tenants/:tenant_id/quota/upsert", api.upsertTenantQuota)
}

// ---- 请求/响应结构（对齐 core-quota-api 契约） ----

type quotaCreateItem struct {
	ResourceType ports.ResourceType `json:"resource_type"`
	Total        int64              `json:"total"`
}

type quotaCreateRequest struct {
	Items []quotaCreateItem `json:"items"`
}

type quotaUpdateItem struct {
	ResourceType ports.ResourceType `json:"resource_type"`
	Total        int64              `json:"total"`
}

type quotaUpdateRequest struct {
	Items []quotaUpdateItem `json:"items"`
}

type quotaUpsertItem struct {
	ResourceType ports.ResourceType `json:"resource_type"`
	Total        *int64             `json:"total,omitempty"`
}

type quotaUpsertRequest struct {
	Items []quotaUpsertItem `json:"items"`
}

type quotaItem struct {
	ResourceType ports.ResourceType `json:"resource_type"`
	Total        int64              `json:"total"`
	Used         int64              `json:"used"`
	Reserved     int64              `json:"reserved"`
	Tightened    bool               `json:"tightened,omitempty"`
	Unit         string             `json:"unit,omitempty"`
	DisplayName  string             `json:"display_name,omitempty"`
	IsDiscrete   bool               `json:"is_discrete,omitempty"`
}

type quotaResponse struct {
	TenantID string      `json:"tenant_id"`
	Items    []quotaItem `json:"items"`
}

type quotaDeleteResponse struct {
	TenantID string `json:"tenant_id"`
	Message  string `json:"message"`
}

type quotaMeta struct {
	ResourceType ports.ResourceType `json:"resource_type"`
	DisplayName  string             `json:"display_name"`
	Unit         string             `json:"unit"`
	DefaultQuota int64              `json:"default_quota"`
	IsDiscrete   bool               `json:"is_discrete"`
}

type quotaMetaListResponse struct {
	Items []quotaMeta `json:"items"`
}

// ---- handlers ----

func (api *quotaAPI) createTenantQuota(ctx context.Context, c *app.RequestContext) {
	tenantID := c.Param("tenant_id")
	var req quotaCreateRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "VALIDATION_FAILED", "invalid quota create request")
		return
	}
	items := make([]ports.QuotaItemInput, 0, len(req.Items))
	for _, it := range req.Items {
		items = append(items, ports.QuotaItemInput{
			ResourceType: it.ResourceType,
			Total:        it.Total,
		})
	}
	info, err := api.admin.CreateTenantQuota(ctx, tenantID, items)
	if err != nil {
		writeQuotaError(c, err)
		return
	}
	c.JSON(http.StatusOK, quotaResponse{TenantID: tenantID, Items: toQuotaItems(info)})
}

func (api *quotaAPI) updateTenantQuota(ctx context.Context, c *app.RequestContext) {
	tenantID := c.Param("tenant_id")
	var req quotaUpdateRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "VALIDATION_FAILED", "invalid quota update request")
		return
	}
	items := make([]ports.QuotaItemUpdate, 0, len(req.Items))
	for _, it := range req.Items {
		items = append(items, ports.QuotaItemUpdate{
			ResourceType: it.ResourceType,
			Total:        it.Total,
		})
	}
	info, err := api.admin.UpdateTenantQuota(ctx, tenantID, items)
	if err != nil {
		writeQuotaError(c, err)
		return
	}
	c.JSON(http.StatusOK, quotaResponse{TenantID: tenantID, Items: toQuotaItems(info)})
}

func (api *quotaAPI) upsertTenantQuota(ctx context.Context, c *app.RequestContext) {
	tenantID := c.Param("tenant_id")
	var req quotaUpsertRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "VALIDATION_FAILED", "invalid quota upsert request")
		return
	}
	items := make([]ports.QuotaItemInput, 0, len(req.Items))
	for _, it := range req.Items {
		var total int64
		if it.Total != nil {
			total = *it.Total
		}
		items = append(items, ports.QuotaItemInput{
			ResourceType: it.ResourceType,
			Total:        total,
		})
	}
	info, err := api.admin.UpsertTenantQuota(ctx, tenantID, items)
	if err != nil {
		writeQuotaUpsertError(c, tenantID, err)
		return
	}
	c.JSON(http.StatusOK, quotaResponse{TenantID: tenantID, Items: toQuotaItems(info)})
}

func (api *quotaAPI) getTenantQuota(ctx context.Context, c *app.RequestContext) {
	tenantID := c.Param("tenant_id")
	info, err := api.admin.GetTenantQuota(ctx, tenantID)
	if err != nil {
		writeQuotaError(c, err)
		return
	}
	c.JSON(http.StatusOK, quotaResponse{TenantID: tenantID, Items: toQuotaItems(info)})
}

func (api *quotaAPI) deleteTenantQuota(ctx context.Context, c *app.RequestContext) {
	tenantID := c.Param("tenant_id")
	if err := api.admin.DeleteTenantQuota(ctx, tenantID); err != nil {
		writeQuotaError(c, err)
		return
	}
	c.JSON(http.StatusOK, quotaDeleteResponse{TenantID: tenantID, Message: "quota deleted"})
}

func (api *quotaAPI) listQuotaMeta(ctx context.Context, c *app.RequestContext) {
	metaList, err := api.admin.ListQuotaMeta(ctx)
	if err != nil {
		writeQuotaError(c, err)
		return
	}
	items := make([]quotaMeta, 0, len(metaList))
	for _, m := range metaList {
		items = append(items, quotaMeta{
			ResourceType: m.ResourceType,
			DisplayName:  m.DisplayName,
			Unit:         m.Unit,
			DefaultQuota: m.DefaultQuota,
			IsDiscrete:   m.IsDiscrete,
		})
	}
	c.JSON(http.StatusOK, quotaMetaListResponse{Items: items})
}

// ---- helpers ----

func toQuotaItems(info []ports.QuotaInfo) []quotaItem {
	items := make([]quotaItem, 0, len(info))
	for _, q := range info {
		items = append(items, quotaItem{
			ResourceType: q.ResourceType,
			Total:        q.Total,
			Used:         q.Used,
			Reserved:     q.Reserved,
			Tightened:    q.Tightened,
			Unit:         q.Unit,
			DisplayName:  q.DisplayName,
			IsDiscrete:   q.IsDiscrete,
		})
	}
	return items
}

// writeQuotaError 将 adapter 哨兵错误映射为 HTTP 三段式错误响应。
func writeQuotaError(c *app.RequestContext, err error) {
	switch {
	case errors.Is(err, ports.ErrInvalid):
		writeDemoError(c, http.StatusBadRequest, "VALIDATION_FAILED", err.Error())
	case errors.Is(err, ports.ErrTenantNotFound):
		writeDemoError(c, http.StatusNotFound, "TENANT_NOT_FOUND", err.Error())
	case errors.Is(err, ports.ErrQuotaNotFound):
		writeDemoError(c, http.StatusNotFound, "QUOTA_NOT_FOUND", err.Error())
	case errors.Is(err, ports.ErrQuotaResourceNotRegistered):
		writeDemoError(c, http.StatusUnprocessableEntity, "QUOTA_RESOURCE_NOT_REGISTERED", err.Error())
	case errors.Is(err, ports.ErrQuotaAlreadyExists):
		writeDemoError(c, http.StatusConflict, "QUOTA_ALREADY_EXISTS", err.Error())
	default:
		writeDemoError(c, http.StatusInternalServerError, "INTERNAL", err.Error())
	}
}

func writeQuotaUpsertError(c *app.RequestContext, tenantID string, err error) {
	switch {
	case errors.Is(err, ports.ErrInvalid):
		writeDemoError(c, http.StatusBadRequest, "VALIDATION_FAILED", "配额更新失败，已回滚。"+quotaErrorDetail(err, ports.ErrInvalid))
	case errors.Is(err, ports.ErrTenantNotFound):
		writeDemoError(c, http.StatusNotFound, "TENANT_NOT_FOUND", "租户不存在: tenant_id="+tenantID)
	case errors.Is(err, ports.ErrQuotaResourceNotRegistered):
		writeDemoError(c, http.StatusUnprocessableEntity, "QUOTA_RESOURCE_NOT_REGISTERED", "配额更新失败，已回滚。"+quotaErrorDetail(err, ports.ErrQuotaResourceNotRegistered))
	case errors.Is(err, ports.ErrQuotaUpdateUncertain):
		writeDemoError(c, http.StatusNetworkAuthenticationRequired, "QUOTA_UPDATE_UNCERTAIN", "配额更新失败，无法确认事务状态，可能已部分提交，请联系管理员人工核对租户配额")
	default:
		writeDemoError(c, http.StatusInternalServerError, "INTERNAL", "internal server error")
	}
}

func quotaErrorDetail(err error, sentinel error) string {
	detail := strings.TrimPrefix(err.Error(), sentinel.Error()+": ")
	if detail == err.Error() {
		return err.Error()
	}
	return detail
}

// writeDemoError 输出标准 ANI 三段式错误响应。
// 原定义位于 demo_instances.go，该文件已在 main 中移除，此处本地化保留。
func writeDemoError(c *app.RequestContext, status int, code string, message string) {
	c.JSON(status, map[string]any{
		"code":    code,
		"message": message,
	})
}
