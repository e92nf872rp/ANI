package router

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"
	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/services/ani-gateway/internal/middleware"
)

// gpuSchedulingAPI exposes the GPU scheduling queue CRUD endpoints.
type gpuSchedulingAPI struct {
	store ports.GPUSchedulingQueueStore
}

// gpuSchedulingQueueResponse is the JSON shape for GPUSchedulingQueue (matches v1.yaml).
type gpuSchedulingQueueResponse struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	Weight            int       `json:"weight"`
	Reclaimable        bool      `json:"reclaimable"`
	WorkloadClass     string    `json:"workload_class"`
	ProjectID         *string   `json:"project_id,omitempty"`
	IsPlatformDefault bool      `json:"is_platform_default"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type gpuSchedulingQueueListResponse struct {
	Items []gpuSchedulingQueueResponse `json:"items"`
}

type gpuSchedulingQueueCreateRequest struct {
	Name          string `json:"name"`
	Weight        int    `json:"weight"`
	Reclaimable   bool   `json:"reclaimable"`
	WorkloadClass string `json:"workload_class"`
	ProjectID     string `json:"project_id,omitempty"`
}

type gpuSchedulingQueueUpdateRequest struct {
	Weight        *int    `json:"weight,omitempty"`
	Reclaimable   *bool   `json:"reclaimable,omitempty"`
	WorkloadClass *string `json:"workload_class,omitempty"`
	ProjectID     *string `json:"project_id,omitempty"`
}

func newGPUSchedulingAPI() *gpuSchedulingAPI {
	return &gpuSchedulingAPI{store: nil}
}

func newGPUSchedulingAPIWithStore(store ports.GPUSchedulingQueueStore) *gpuSchedulingAPI {
	return &gpuSchedulingAPI{store: store}
}

func registerGPUSchedulingResources(v1 *route.RouterGroup) {
	registerGPUSchedulingResourcesWithStore(v1, nil)
}

func registerGPUSchedulingResourcesWithStore(v1 *route.RouterGroup, store ports.GPUSchedulingQueueStore) {
	api := newGPUSchedulingAPIWithStore(store)
	v1.GET("/gpu-scheduling/queues", api.listGPUSchedulingQueues)
	v1.POST("/gpu-scheduling/queues", api.createGPUSchedulingQueue)
	v1.GET("/gpu-scheduling/queues/:id", api.getGPUSchedulingQueue)
	v1.PATCH("/gpu-scheduling/queues/:id", api.updateGPUSchedulingQueue)
	v1.DELETE("/gpu-scheduling/queues/:id", api.deleteGPUSchedulingQueue)
}

func (api *gpuSchedulingAPI) listGPUSchedulingQueues(ctx context.Context, c *app.RequestContext) {
	if api.store == nil {
		writeGPUSchedulingError(c, ports.ErrQueueStoreUnavailable)
		return
	}
	tenantID := middleware.GetTenantID(c)
	if tenantID == "" {
		writeDemoError(c, http.StatusForbidden, "FORBIDDEN", "tenant context missing")
		return
	}
	queues, err := api.store.List(ctx, tenantID)
	if err != nil {
		writeGPUSchedulingError(c, err)
		return
	}
	items := make([]gpuSchedulingQueueResponse, 0, len(queues))
	for _, q := range queues {
		items = append(items, queueToResponse(q))
	}
	c.JSON(http.StatusOK, gpuSchedulingQueueListResponse{Items: items})
}

func (api *gpuSchedulingAPI) createGPUSchedulingQueue(ctx context.Context, c *app.RequestContext) {
	if api.store == nil {
		writeGPUSchedulingError(c, ports.ErrQueueStoreUnavailable)
		return
	}
	tenantID := middleware.GetTenantID(c)
	if tenantID == "" {
		writeDemoError(c, http.StatusForbidden, "FORBIDDEN", "tenant context missing")
		return
	}
	idempotencyKey := strings.TrimSpace(string(c.GetHeader("Idempotency-Key")))
	if idempotencyKey == "" {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "Idempotency-Key header is required")
		return
	}
	var req gpuSchedulingQueueCreateRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "name is required")
		return
	}
	if strings.TrimSpace(req.WorkloadClass) == "" {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "workload_class is required")
		return
	}
	created, err := api.store.Create(ctx, tenantID, ports.GPUSchedulingQueueCreateRequest{
		Name:          req.Name,
		Weight:        req.Weight,
		Reclaimable:   req.Reclaimable,
		WorkloadClass: ports.WorkloadClass(req.WorkloadClass),
		ProjectID:     req.ProjectID,
	})
	if err != nil {
		writeGPUSchedulingError(c, err)
		return
	}
	c.JSON(http.StatusCreated, queueToResponse(created))
}

func (api *gpuSchedulingAPI) getGPUSchedulingQueue(ctx context.Context, c *app.RequestContext) {
	if api.store == nil {
		writeGPUSchedulingError(c, ports.ErrQueueStoreUnavailable)
		return
	}
	tenantID := middleware.GetTenantID(c)
	if tenantID == "" {
		writeDemoError(c, http.StatusForbidden, "FORBIDDEN", "tenant context missing")
		return
	}
	id := c.Param("id")
	if id == "" {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "id is required")
		return
	}
	queue, err := api.store.Get(ctx, tenantID, id)
	if err != nil {
		writeGPUSchedulingError(c, err)
		return
	}
	c.JSON(http.StatusOK, queueToResponse(queue))
}

func (api *gpuSchedulingAPI) updateGPUSchedulingQueue(ctx context.Context, c *app.RequestContext) {
	if api.store == nil {
		writeGPUSchedulingError(c, ports.ErrQueueStoreUnavailable)
		return
	}
	tenantID := middleware.GetTenantID(c)
	if tenantID == "" {
		writeDemoError(c, http.StatusForbidden, "FORBIDDEN", "tenant context missing")
		return
	}
	id := c.Param("id")
	if id == "" {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "id is required")
		return
	}
	var req gpuSchedulingQueueUpdateRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
		return
	}
	portReq := ports.GPUSchedulingQueueUpdateRequest{
		Weight:      req.Weight,
		Reclaimable: req.Reclaimable,
	}
	if req.WorkloadClass != nil {
		wc := ports.WorkloadClass(*req.WorkloadClass)
		portReq.WorkloadClass = &wc
	}
	if req.ProjectID != nil {
		portReq.ProjectID = req.ProjectID
	}
	updated, err := api.store.Update(ctx, tenantID, id, portReq)
	if err != nil {
		writeGPUSchedulingError(c, err)
		return
	}
	c.JSON(http.StatusOK, queueToResponse(updated))
}

func (api *gpuSchedulingAPI) deleteGPUSchedulingQueue(ctx context.Context, c *app.RequestContext) {
	if api.store == nil {
		writeGPUSchedulingError(c, ports.ErrQueueStoreUnavailable)
		return
	}
	tenantID := middleware.GetTenantID(c)
	if tenantID == "" {
		writeDemoError(c, http.StatusForbidden, "FORBIDDEN", "tenant context missing")
		return
	}
	id := c.Param("id")
	if id == "" {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "id is required")
		return
	}
	if err := api.store.Delete(ctx, tenantID, id); err != nil {
		writeGPUSchedulingError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func queueToResponse(q ports.GPUSchedulingQueue) gpuSchedulingQueueResponse {
	var projectID *string
	if q.ProjectID != "" {
		pid := q.ProjectID
		projectID = &pid
	}
	return gpuSchedulingQueueResponse{
		ID:                q.ID,
		Name:              q.Name,
		Weight:            q.Weight,
		Reclaimable:        q.Reclaimable,
		WorkloadClass:     string(q.WorkloadClass),
		ProjectID:         projectID,
		IsPlatformDefault: q.IsPlatformDefault,
		CreatedAt:         q.CreatedAt,
		UpdatedAt:         q.UpdatedAt,
	}
}

func writeGPUSchedulingError(c *app.RequestContext, err error) {
	switch {
	case errors.Is(err, ports.ErrQueueNotFound):
		writeDemoError(c, http.StatusNotFound, "QueueNotFound", "队列不存在")
	case errors.Is(err, ports.ErrQueueNameConflict):
		writeDemoError(c, http.StatusConflict, "QueueNameConflict", "队列名称已存在")
	case errors.Is(err, ports.ErrPlatformDefaultProtected):
		writeDemoError(c, http.StatusForbidden, "PlatformDefaultProtected", "平台默认队列不可修改或删除")
	case errors.Is(err, ports.ErrQueueStoreUnavailable):
		writeDemoError(c, http.StatusServiceUnavailable, "QueueStoreUnavailable", "队列服务暂时不可用")
	case errors.Is(err, ports.ErrInvalid):
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	default:
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	}
}
