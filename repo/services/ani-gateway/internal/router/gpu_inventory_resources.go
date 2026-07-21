package router

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"
	"github.com/google/uuid"
	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/ports"
)

type gpuInventoryAPI struct {
	inventory ports.GPUInventory
	templates ports.SandboxTemplateCatalog
	profile   coreDevProfileResponse
}

type gpuInventoryListResponse struct {
	Items      []gpuInventoryRecordResponse `json:"items"`
	Total      int                          `json:"total"`
	NextCursor *string                      `json:"next_cursor"`
	DevProfile coreDevProfileResponse       `json:"dev_profile"`
}

type gpuInventoryRecordResponse struct {
	ID            string                 `json:"id"`
	NodeName      string                 `json:"node_name"`
	GPUType       string                 `json:"gpu_type"`
	GPUIndex      int                    `json:"gpu_index"`
	MemoryTotalMB int                    `json:"memory_total_mb,omitempty"`
	DriverVersion string                 `json:"driver_version,omitempty"`
	Status        string                 `json:"status"`
	TenantID      *string                `json:"tenant_id"`
	InstanceID    *string                `json:"instance_id"`
	DevProfile    coreDevProfileResponse `json:"dev_profile"`
}

type gpuOccupancyResponse struct {
	Total      int                      `json:"total"`
	InUse      int                      `json:"in_use"`
	Available  int                      `json:"available"`
	Fault      int                      `json:"fault"`
	ByGPUType  []gpuOccupancyTypeBucket `json:"by_gpu_type"`
	DevProfile coreDevProfileResponse   `json:"dev_profile"`
}

type gpuOccupancyTypeBucket struct {
	GPUType   string `json:"gpu_type"`
	Total     int    `json:"total"`
	InUse     int    `json:"in_use"`
	Available int    `json:"available"`
}

type sandboxTemplateListResponse struct {
	Items      []sandboxTemplateResponse `json:"items"`
	Total      int                       `json:"total"`
	NextCursor *string                   `json:"next_cursor"`
	DevProfile coreDevProfileResponse    `json:"dev_profile"`
}

type sandboxTemplateResponse struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	Image       string                 `json:"image"`
	Description string                 `json:"description,omitempty"`
	CPUCores    *float64               `json:"cpu_cores"`
	MemoryGB    *float64               `json:"memory_gb"`
	StorageGB   *float64               `json:"storage_gb"`
	IsBuiltin   bool                   `json:"is_builtin"`
	CreatedAt   string                 `json:"created_at"`
	DevProfile  coreDevProfileResponse `json:"dev_profile"`
}

func newGPUInventoryAPI() *gpuInventoryAPI {
	return newGPUInventoryAPIWithInventory(nil)
}

func newGPUInventoryAPIWithInventory(inventory ports.GPUInventory) *gpuInventoryAPI {
	profile := localCoreDevProfile("local-gpu-inventory", "Core dev/local profile; real GPU discovery is gated separately")
	if inventory == nil {
		inventory = runtimeadapter.NewLocalGPUInventory()
	} else {
		profile = coreDevProfileResponse{
			Mode:         "real",
			Provider:     "kubernetes-gpu-inventory",
			RealProvider: true,
			Reason:       "GPU inventory is read from the configured Kubernetes provider",
		}
	}
	return &gpuInventoryAPI{
		inventory: inventory,
		templates: runtimeadapter.NewLocalSandboxTemplateCatalog(),
		profile:   profile,
	}
}

func registerGPUInventoryResourcesWithInventory(v1 *route.RouterGroup, inventory ports.GPUInventory) {
	api := newGPUInventoryAPIWithInventory(inventory)
	v1.GET("/gpu-inventory", api.listGPUInventory)
	v1.GET("/gpu-inventory/occupancy", api.getGPUOccupancy)
	v1.GET("/sandbox-templates", api.listSandboxTemplates)
}

func (api *gpuInventoryAPI) listGPUInventory(ctx context.Context, c *app.RequestContext) {
	nodes, err := api.inventory.ListNodeClasses(ctx, api.gpuFilter(c.Query("gpu_type"), c.Query("status"), c.Query("node_name")))
	if err != nil {
		writeGPUInventoryError(c, err)
		return
	}
	response := api.gpuInventoryListFromNodes(nodes, c.Query("gpu_type"), c.Query("status"), c.Query("node_name"))
	c.JSON(http.StatusOK, response)
}

func (api *gpuInventoryAPI) getGPUOccupancy(ctx context.Context, c *app.RequestContext) {
	nodes, err := api.inventory.ListNodeClasses(ctx, ports.GPUDiscoveryFilter{})
	if err != nil {
		writeGPUInventoryError(c, err)
		return
	}
	c.JSON(http.StatusOK, api.gpuOccupancyFromNodes(nodes))
}

func (api *gpuInventoryAPI) listSandboxTemplates(ctx context.Context, c *app.RequestContext) {
	result, err := api.templates.ListSandboxTemplates(ctx, api.sandboxTemplateListRequest(queryInt(c, "limit", 20), c.Query("cursor")))
	if err != nil {
		writeGPUInventoryError(c, err)
		return
	}
	c.JSON(http.StatusOK, api.sandboxTemplateListFromResult(result))
}

func (api *gpuInventoryAPI) gpuFilter(gpuType string, _ string, nodeName string) ports.GPUDiscoveryFilter {
	filter := ports.GPUDiscoveryFilter{}
	if strings.TrimSpace(gpuType) != "" {
		filter.Labels = map[string]string{"nvidia.com/gpu.product": strings.TrimSpace(gpuType)}
	}
	if strings.TrimSpace(nodeName) != "" {
		filter.Labels = cloneRouterStringMap(filter.Labels)
		filter.Labels["kubernetes.io/hostname"] = strings.TrimSpace(nodeName)
	}
	return filter
}

func (api *gpuInventoryAPI) gpuInventoryListFromNodes(nodes []ports.GPUNodeClass, gpuType string, status string, nodeName string) gpuInventoryListResponse {
	items := make([]gpuInventoryRecordResponse, 0)
	for _, node := range nodes {
		if strings.TrimSpace(nodeName) != "" && node.NodeName != strings.TrimSpace(nodeName) {
			continue
		}
		for index, device := range node.Devices {
			item := api.gpuInventoryRecordFromDevice(node, device, index)
			if strings.TrimSpace(gpuType) != "" && !strings.EqualFold(item.GPUType, strings.TrimSpace(gpuType)) {
				continue
			}
			if strings.TrimSpace(status) != "" && item.Status != strings.TrimSpace(status) {
				continue
			}
			items = append(items, item)
		}
	}
	return gpuInventoryListResponse{
		Items:      items,
		Total:      len(items),
		NextCursor: nil,
		DevProfile: api.profile,
	}
}

func (api *gpuInventoryAPI) gpuOccupancyFromNodes(nodes []ports.GPUNodeClass) gpuOccupancyResponse {
	response := gpuOccupancyResponse{
		ByGPUType:  []gpuOccupancyTypeBucket{},
		DevProfile: api.profile,
	}
	buckets := map[string]*gpuOccupancyTypeBucket{}
	for _, node := range nodes {
		for index, device := range node.Devices {
			item := api.gpuInventoryRecordFromDevice(node, device, index)
			response.Total++
			switch item.Status {
			case "available":
				response.Available++
			case "in_use":
				response.InUse++
			case "fault":
				response.Fault++
			}
			bucket := buckets[item.GPUType]
			if bucket == nil {
				bucket = &gpuOccupancyTypeBucket{GPUType: item.GPUType}
				buckets[item.GPUType] = bucket
			}
			bucket.Total++
			if item.Status == "available" {
				bucket.Available++
			}
			if item.Status == "in_use" {
				bucket.InUse++
			}
		}
	}
	for _, bucket := range buckets {
		response.ByGPUType = append(response.ByGPUType, *bucket)
	}
	return response
}

func (api *gpuInventoryAPI) sandboxTemplateListRequest(limit int, cursor string) ports.SandboxTemplateListRequest {
	return ports.SandboxTemplateListRequest{TenantID: "demo-tenant", Limit: limit, Cursor: cursor}
}

func (api *gpuInventoryAPI) sandboxTemplateListFromResult(result ports.SandboxTemplateListResult) sandboxTemplateListResponse {
	items := make([]sandboxTemplateResponse, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, sandboxTemplateResponse{
			ID:          item.ID,
			Name:        item.Name,
			Image:       item.Image,
			Description: item.Description,
			CPUCores:    item.CPUCores,
			MemoryGB:    item.MemoryGB,
			StorageGB:   item.StorageGB,
			IsBuiltin:   item.IsBuiltin,
			CreatedAt:   item.CreatedAt.Format(time.RFC3339),
			DevProfile:  coreDevProfileFromPort(item.DevProfile),
		})
	}
	return sandboxTemplateListResponse{
		Items:      items,
		Total:      result.Total,
		NextCursor: optionalString(result.NextCursor),
		DevProfile: coreDevProfileFromPort(result.DevProfile),
	}
}

func (api *gpuInventoryAPI) gpuInventoryRecordFromDevice(node ports.GPUNodeClass, device ports.GPUDeviceClass, index int) gpuInventoryRecordResponse {
	status := "available"
	if !node.Ready {
		status = "fault"
	}
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(node.NodeName+"/"+strconv.Itoa(index)+"/"+device.Model)).String()
	return gpuInventoryRecordResponse{
		ID:            id,
		NodeName:      node.NodeName,
		GPUType:       firstNonEmpty(device.Model, node.Model, string(device.Vendor)),
		GPUIndex:      index,
		MemoryTotalMB: int(device.MemoryMiB),
		DriverVersion: device.DriverVersion,
		Status:        status,
		TenantID:      nil,
		InstanceID:    nil,
		DevProfile:    api.profile,
	}
}

func cloneRouterStringMap(input map[string]string) map[string]string {
	out := make(map[string]string, len(input)+1)
	for key, value := range input {
		out[key] = value
	}
	return out
}

func writeGPUInventoryError(c *app.RequestContext, err error) {
	switch {
	case errors.Is(err, ports.ErrNotFound):
		writeDemoError(c, http.StatusNotFound, "NOT_FOUND", err.Error())
	case errors.Is(err, ports.ErrConflict):
		writeDemoError(c, http.StatusConflict, "CONFLICT", err.Error())
	case errors.Is(err, ports.ErrInvalid):
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	case errors.Is(err, ports.ErrUnsupported):
		writeDemoError(c, http.StatusBadRequest, "UNSUPPORTED", err.Error())
	default:
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	}
}
