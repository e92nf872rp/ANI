package router

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"
	"github.com/google/uuid"
	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/services/ani-gateway/internal/middleware"
)

type gpuInventoryAPI struct {
	inventory     ports.GPUInventory
	specs         ports.GPUSpecService
	specStore     ports.GPUSpecStore
	templates     ports.SandboxTemplateCatalog
	instanceStore ports.WorkloadInstanceStore
	quotaStore    ports.QuotaStoreService
	quotaAdmin    ports.QuotaAdminService
	k8sClient     *runtimeadapter.KubernetesRESTClient
	// podOccupancyFetcher is overrideable in tests; production code leaves it
	// nil and gpuNodeOccupancy falls back to querying k8sClient directly.
	podOccupancyFetcher func(ctx context.Context, tenantID string) []gpuPodOccupancy
	profile             coreDevProfileResponse
}

// gpuPodOccupancy is the minimal info extracted from a K8s Pod for GPU
// inventory ownership echo: the instance name (from label
// ani.kubercloud.io/instance), the node name (spec.nodeName), and the pod
// phase. Only Running pods with non-empty node and instance name produce
// an occupancy entry.
type gpuPodOccupancy struct {
	InstanceName string
	NodeName     string
	Phase        string
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
	// GPU mode / spec / sharing fields derived from node labels
	// (ani.kubercloud.io/gpu-mode, gpu-spec, gpu-sharing-spec,
	// gpu-sharing-policy). These align the frontend gpu_type picker
	// with the server-side GPUTypeNotInNodes validation (SPEC §5.2).
	GPUMode          string `json:"gpu_mode,omitempty"`
	GPUSpec          string `json:"gpu_spec,omitempty"`
	GPUSharingSpec   string `json:"gpu_sharing_spec,omitempty"`
	GPUSharingPolicy string `json:"gpu_sharing_policy,omitempty"`
}

type gpuSpecResponse struct {
	ID               string                       `json:"id"`
	Name             string                       `json:"name"`
	GPUType          string                       `json:"gpu_type"`
	GPUMode          string                       `json:"gpu_mode,omitempty"`
	MemoryTotalMB    int64                        `json:"memory_total_mb,omitempty"`
	Shares           int                          `json:"shares"`
	MBPerShare       int                          `json:"mb_per_share"`
	Available        bool                         `json:"available"`
	NodeAffinity     *gpuSpecNodeAffinityResponse `json:"node_affinity,omitempty"`
	VolcanoResources *gpuSpecVolcanoResResponse   `json:"volcano_resources,omitempty"`
}

type gpuSpecListResponse struct {
	Items      []gpuSpecResponse `json:"items"`
	Total      int               `json:"total"`
	NextCursor *string           `json:"next_cursor"`
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
	return newGPUInventoryAPIWithStore(inventory, nil, nil)
}

func newGPUInventoryAPIWithStore(inventory ports.GPUInventory, store ports.WorkloadInstanceStore, k8sClient *runtimeadapter.KubernetesRESTClient, specServices ...ports.GPUSpecService) *gpuInventoryAPI {
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
	var specs ports.GPUSpecService
	if len(specServices) > 0 {
		specs = specServices[0]
	}
	if specs == nil {
		specs = runtimeadapter.NewLocalGPUSpecService(inventory)
	}
	return &gpuInventoryAPI{
		inventory:     inventory,
		specs:         specs,
		templates:     runtimeadapter.NewLocalSandboxTemplateCatalog(),
		instanceStore: store,
		k8sClient:     k8sClient,
		profile:       profile,
	}
}

func registerGPUInventoryResourcesWithStore(v1 *route.RouterGroup, inventory ports.GPUInventory, store ports.WorkloadInstanceStore, k8sClient *runtimeadapter.KubernetesRESTClient, specStore ports.GPUSpecStore, quotaStore ports.QuotaStoreService, quotaAdmin ports.QuotaAdminService, specServices ...ports.GPUSpecService) {
	api := newGPUInventoryAPIWithStore(inventory, store, k8sClient, specServices...)
	api.specStore = specStore
	api.quotaStore = quotaStore
	api.quotaAdmin = quotaAdmin
	v1.GET("/gpu-inventory", api.listGPUInventory)
	v1.GET("/gpu-inventory/occupancy", api.getGPUOccupancy)
	v1.GET("/gpu-specs", api.listGPUSpecs)
	// /gpu-specs/availability must be registered BEFORE /gpu-specs/:spec_id
	// so the static path takes precedence over the param route.
	v1.GET("/gpu-specs/availability", api.listGPUSpecAvailability)
	v1.GET("/gpu-specs/:spec_id", api.getGPUSpec)
	v1.GET("/sandbox-templates", api.listSandboxTemplates)
}

func (api *gpuInventoryAPI) listGPUSpecs(ctx context.Context, c *app.RequestContext) {
	var available *bool
	if raw := strings.TrimSpace(c.Query("available")); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "available must be a boolean")
			return
		}
		available = &value
	}
	// Prefer the CRD-backed GPUSpecStore when injected (returns the full
	// GPUSpec schema including gpu_mode/node_affinity/volcano_resources).
	// Fall back to the local in-memory GPUSpecService for dev/local profile.
	if api.specStore != nil {
		crdItems, err := api.specStore.List(ctx)
		if err != nil {
			writeGPUInventoryError(c, err)
			return
		}
		gpuTypeFilter := strings.TrimSpace(c.Query("gpu_type"))
		response := gpuSpecListResponse{Items: make([]gpuSpecResponse, 0, len(crdItems)), Total: 0}
		for _, crd := range crdItems {
			if gpuTypeFilter != "" && crd.GPUType != gpuTypeFilter {
				continue
			}
			if available != nil && crd.Available != *available {
				continue
			}
			response.Items = append(response.Items, gpuSpecResponseFromCRD(crd))
			response.Total++
		}
		c.JSON(http.StatusOK, response)
		return
	}
	items, err := api.specs.ListGPUSpecs(ctx, ports.GPUSpecListRequest{GPUType: strings.TrimSpace(c.Query("gpu_type")), Available: available, Limit: queryInt(c, "limit", 50), Cursor: c.Query("cursor")})
	if err != nil {
		writeGPUInventoryError(c, err)
		return
	}
	response := gpuSpecListResponse{Items: make([]gpuSpecResponse, 0, len(items)), Total: len(items)}
	for _, item := range items {
		response.Items = append(response.Items, gpuSpecResponse{ID: item.ID, Name: item.Name, GPUType: item.GPUType, MemoryTotalMB: item.MemoryTotalMB, Shares: item.Shares, MBPerShare: item.MBPerShare, Available: item.Available})
	}
	c.JSON(http.StatusOK, response)
}

// gpuSpecAvailabilityResponse matches the GPUSpecAvailability schema (v1.yaml).
type gpuSpecAvailabilityResponse struct {
	SpecID           string `json:"spec_id"`
	Status           string `json:"status"`
	AvailableCount   int    `json:"available_count"`
	HasMatchingNodes bool   `json:"has_matching_nodes"`
	HasIdleDevices   bool   `json:"has_idle_devices"`
	DeviceIdleCount  int    `json:"device_idle_count"`
	GPUCount         int    `json:"gpu_count,omitempty"`
}

// gpuSpecAvailabilityListResponse matches GPUSpecAvailabilityListResponse (v1.yaml).
type gpuSpecAvailabilityListResponse struct {
	Items          []gpuSpecAvailabilityResponse `json:"items"`
	QuotaRemaining int64                         `json:"quota_remaining"`
}

// listGPUSpecAvailability handles GET /gpu-specs/availability.
// It delegates to GPUInventory.ListSpecAvailability (real K8s provider path)
// and falls back to a local computation from GPUSpecService + node inventory
// when the inventory adapter returns ErrUnsupported (local/dev profile).
// quota_remaining is queried from QuotaStoreService when available; otherwise
// defaults to 0 (no quota configured).
func (api *gpuInventoryAPI) listGPUSpecAvailability(ctx context.Context, c *app.RequestContext) {
	tenantID := middleware.GetTenantID(c)
	if tenantID == "" {
		tenantID = "demo-tenant"
	}

	// Query quota_remaining = allocated_gpu_count - used - reserved
	// (plan.md §4.4.1). When quotaAdmin is available, use the tenant's
	// allocated_gpu_count (BOSS reservation). Fall back to total when
	// quotaAdmin is not configured or the reservation row doesn't exist.
	var quotaRemaining int64
	if api.quotaStore != nil {
		view, err := api.quotaStore.GetMy(ctx, tenantID)
		if err == nil {
			total := view.Total[ports.QuotaGPUCount]
			used := view.Used[ports.QuotaGPUCount]
			reserved := view.Reserved[ports.QuotaGPUCount]
			allocatedLimit := total
			if api.quotaAdmin != nil {
				reservation, err := api.quotaAdmin.GetReservation(ctx, tenantID)
				if err == nil && reservation.AllocatedGPUCount > 0 {
					allocatedLimit = reservation.AllocatedGPUCount
				}
			}
			quotaRemaining = allocatedLimit - used - reserved
		}
	}

	// Try the inventory adapter's ListSpecAvailability first (real K8s path).
	items, err := api.inventory.ListSpecAvailability(ctx, tenantID)
	if err == nil {
		response := gpuSpecAvailabilityListResponse{
			Items:          make([]gpuSpecAvailabilityResponse, 0, len(items)),
			QuotaRemaining: quotaRemaining,
		}
		for _, item := range items {
			response.Items = append(response.Items, gpuSpecAvailabilityResponse{
				SpecID:           item.SpecID,
				Status:           string(item.Status),
				AvailableCount:   item.AvailableCount,
				HasMatchingNodes: item.HasMatchingNodes,
				HasIdleDevices:   item.HasIdleDevices,
				DeviceIdleCount:  item.DeviceIdleCount,
				GPUCount:         item.GPUCount,
			})
		}
		c.JSON(http.StatusOK, response)
		return
	}

	// Fallback: compute locally from GPUSpecService + node inventory (dev/local profile).
	availabilityItems := api.computeLocalSpecAvailability(ctx, quotaRemaining)
	c.JSON(http.StatusOK, gpuSpecAvailabilityListResponse{
		Items:          availabilityItems,
		QuotaRemaining: quotaRemaining,
	})
}

// computeLocalSpecAvailability computes spec availability from the
// GPUSpecService (or CRD specStore) and local node inventory. This is the
// dev/local profile fallback when ListSpecAvailability returns ErrUnsupported.
func (api *gpuInventoryAPI) computeLocalSpecAvailability(ctx context.Context, quotaRemaining int64) []gpuSpecAvailabilityResponse {
	// Get specs from specStore (CRD) or local GPUSpecService.
	var specIDs []struct {
		id      string
		gpuType string
		shares  int
	}
	if api.specStore != nil {
		crdItems, err := api.specStore.List(ctx)
		if err == nil {
			for _, crd := range crdItems {
				specIDs = append(specIDs, struct {
					id      string
					gpuType string
					shares  int
				}{id: crd.ID, gpuType: crd.GPUType, shares: crd.Shares})
			}
		}
	} else {
		all := false
		items, err := api.specs.ListGPUSpecs(ctx, ports.GPUSpecListRequest{Available: &all})
		if err == nil {
			for _, item := range items {
				specIDs = append(specIDs, struct {
					id      string
					gpuType string
					shares  int
				}{id: item.ID, gpuType: item.GPUType, shares: item.Shares})
			}
		}
	}

	// Get node inventory for device matching.
	nodes, err := api.inventory.ListNodeClasses(ctx, ports.GPUDiscoveryFilter{})
	if err != nil {
		nodes = nil
	}

	result := make([]gpuSpecAvailabilityResponse, 0, len(specIDs))
	for _, spec := range specIDs {
		hasMatchingNodes := false
		deviceIdleCount := 0
		for _, node := range nodes {
			if !node.Ready {
				continue
			}
			for _, device := range node.Devices {
				gpuType := device.Model
				if gpuType == "" {
					gpuType = node.Model
				}
				if !strings.EqualFold(gpuType, spec.gpuType) {
					continue
				}
				hasMatchingNodes = true
				deviceIdleCount++
			}
		}

		availability := gpuSpecAvailabilityResponse{
			SpecID:           spec.id,
			HasMatchingNodes: hasMatchingNodes,
			DeviceIdleCount:  deviceIdleCount,
			HasIdleDevices:   deviceIdleCount > 0,
			GPUCount:         spec.shares,
		}

		// Four-state determination.
		if !hasMatchingNodes {
			availability.Status = string(ports.GPUSpecStatusUnavailable)
			availability.AvailableCount = 0
		} else if quotaRemaining <= 0 {
			availability.Status = string(ports.GPUSpecStatusFull)
			availability.AvailableCount = 0
		} else if deviceIdleCount <= 0 {
			availability.Status = string(ports.GPUSpecStatusDeviceFull)
			availability.AvailableCount = 0
		} else {
			availability.Status = string(ports.GPUSpecStatusAvailable)
			availableCount := quotaRemaining
			if int64(deviceIdleCount) < availableCount {
				availableCount = int64(deviceIdleCount)
			}
			availability.AvailableCount = int(availableCount)
		}
		result = append(result, availability)
	}
	return result
}

func (api *gpuInventoryAPI) getGPUSpec(ctx context.Context, c *app.RequestContext) {
	specID := c.Param("spec_id")
	if api.specStore != nil {
		crd, err := api.specStore.Get(ctx, specID)
		if err != nil {
			writeGPUInventoryError(c, err)
			return
		}
		c.JSON(http.StatusOK, gpuSpecResponseFromCRD(crd))
		return
	}
	item, err := api.specs.GetGPUSpec(ctx, specID)
	if err != nil {
		writeGPUInventoryError(c, err)
		return
	}
	c.JSON(http.StatusOK, gpuSpecResponse{ID: item.ID, Name: item.Name, GPUType: item.GPUType, MemoryTotalMB: item.MemoryTotalMB, Shares: item.Shares, MBPerShare: item.MBPerShare, Available: item.Available})
}

// gpuSpecResponseFromCRD converts a CRD-backed GPUSpecCRD into the extended
// gpuSpecResponse (with gpu_mode/node_affinity/volcano_resources) returned by
// GET /gpu-specs and GET /gpu-specs/:spec_id when a GPUSpecStore is injected.
func gpuSpecResponseFromCRD(crd ports.GPUSpecCRD) gpuSpecResponse {
	resp := gpuSpecResponse{
		ID:            crd.ID,
		Name:          crd.Name,
		GPUType:       crd.GPUType,
		GPUMode:       crd.GPUMode,
		MemoryTotalMB: crd.MemoryTotalMB,
		Shares:        crd.Shares,
		MBPerShare:    crd.MBPerShare,
		Available:     crd.Available,
	}
	if crd.NodeAffinity != (ports.GPUSpecNodeAffinity{}) {
		resp.NodeAffinity = &gpuSpecNodeAffinityResponse{
			GPUSpec:          crd.NodeAffinity.GPUSpec,
			GPUSharingSpec:   crd.NodeAffinity.GPUSharingSpec,
			GPUSharingPolicy: crd.NodeAffinity.GPUSharingPolicy,
			GPUMode:          crd.NodeAffinity.GPUMode,
		}
	}
	if len(crd.VolcanoResources.Wholecard) > 0 || len(crd.VolcanoResources.VGPU) > 0 {
		resp.VolcanoResources = &gpuSpecVolcanoResResponse{
			Wholecard: crd.VolcanoResources.Wholecard,
			VGPU:      crd.VolcanoResources.VGPU,
		}
	}
	return resp
}

func (api *gpuInventoryAPI) listGPUInventory(ctx context.Context, c *app.RequestContext) {
	nodes, err := api.inventory.ListNodeClasses(ctx, api.gpuFilter(c.Query("gpu_type"), c.Query("status"), c.Query("node_name")))
	if err != nil {
		writeGPUInventoryError(c, err)
		return
	}
	occupancy := api.gpuNodeOccupancy(ctx, c)
	response := api.gpuInventoryListFromNodes(nodes, c.Query("gpu_type"), c.Query("status"), c.Query("node_name"), occupancy)
	c.JSON(http.StatusOK, response)
}

func (api *gpuInventoryAPI) getGPUOccupancy(ctx context.Context, c *app.RequestContext) {
	nodes, err := api.inventory.ListNodeClasses(ctx, ports.GPUDiscoveryFilter{})
	if err != nil {
		writeGPUInventoryError(c, err)
		return
	}
	c.JSON(http.StatusOK, api.gpuOccupancyFromNodes(nodes, api.gpuNodeOccupancy(ctx, c)))
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

func (api *gpuInventoryAPI) gpuInventoryListFromNodes(nodes []ports.GPUNodeClass, gpuType string, status string, nodeName string, occupancy gpuNodeOccupancyMap) gpuInventoryListResponse {
	items := make([]gpuInventoryRecordResponse, 0)
	for _, node := range nodes {
		if strings.TrimSpace(nodeName) != "" && node.NodeName != strings.TrimSpace(nodeName) {
			continue
		}
		for index, device := range node.Devices {
			item := api.gpuInventoryRecordFromDevice(node, device, index, occupancy)
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

func (api *gpuInventoryAPI) gpuOccupancyFromNodes(nodes []ports.GPUNodeClass, occupancy gpuNodeOccupancyMap) gpuOccupancyResponse {
	response := gpuOccupancyResponse{
		ByGPUType:  []gpuOccupancyTypeBucket{},
		DevProfile: api.profile,
	}
	buckets := map[string]*gpuOccupancyTypeBucket{}
	for _, node := range nodes {
		for index, device := range node.Devices {
			item := api.gpuInventoryRecordFromDevice(node, device, index, occupancy)
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

func (api *gpuInventoryAPI) gpuInventoryRecordFromDevice(node ports.GPUNodeClass, device ports.GPUDeviceClass, index int, occupancy gpuNodeOccupancyMap) gpuInventoryRecordResponse {
	status := "available"
	if !node.Ready {
		status = "fault"
	}
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(node.NodeName+"/"+strconv.Itoa(index)+"/"+device.Model)).String()
	record := gpuInventoryRecordResponse{
		ID:               id,
		NodeName:         node.NodeName,
		GPUType:          firstNonEmpty(device.Model, node.Model, string(device.Vendor)),
		GPUIndex:         index,
		MemoryTotalMB:    int(device.MemoryMiB),
		DriverVersion:    device.DriverVersion,
		Status:           status,
		TenantID:         nil,
		InstanceID:       nil,
		DevProfile:       api.profile,
		GPUMode:          node.GPUMode,
		GPUSpec:          node.GPUSpec,
		GPUSharingSpec:   node.GPUSharingSpec,
		GPUSharingPolicy: node.GPUSharingPolicy,
	}
	// 当节点 ready 且存在同节点的 Running GPU Pod 时，按设备索引顺序
	// 标记前 PodCount 个设备为 in_use（每个 Pod 占用 1 个设备记录）。
	// 当前实现无法精确到"节点的哪张卡"（planning 阶段未持久化 GPU device
	// index），因此按索引顺序分配；多实例共节点时 InstanceID 取字典序最小。
	// 详见 PRD §3.1 / US-006。
	if status == "available" {
		if owner, ok := occupancy.lookup(node.NodeName); ok && index < owner.PodCount {
			tenantID := owner.TenantID
			instanceID := owner.InstanceID
			record.Status = "in_use"
			record.TenantID = &tenantID
			record.InstanceID = &instanceID
		}
	}
	return record
}

// gpuNodeOccupancyEntry 表示某个节点上运行的 GPU 实例占用信息。
// PodCount 是该节点上 Running 状态的 GPU Pod 数量（每个 Pod 占用 1 个
// GPU 设备记录——整卡节点 1 Pod = 1 张物理卡，vGPU 节点 1 Pod = 1 个切片）。
// InstanceID 取字典序最小的实例名（展示用），实际 in_use 计数由 PodCount 决定。
type gpuNodeOccupancyEntry struct {
	TenantID   string
	InstanceID string
	NodeName   string
	PodCount   int
}

// gpuNodeOccupancyMap 是 nodeName → 归属信息 的查询表。
// PodCount 表示该节点上 Running GPU Pod 的数量，用于决定有多少设备
// 记录应标记为 in_use。前 PodCount 个设备标记为 in_use，其余为 available。
type gpuNodeOccupancyMap struct {
	entries map[string]gpuNodeOccupancyEntry
}

func (m gpuNodeOccupancyMap) lookup(nodeName string) (gpuNodeOccupancyEntry, bool) {
	entry, ok := m.entries[nodeName]
	return entry, ok
}

// gpuNodeOccupancy 查询本租户在 K8s 集群中所有 GPU 容器实例对应的 Pod，
// 构建 nodeName → 归属实例 映射。不依赖 InstanceStore——直接从 K8s API 查
// Pod label ani.kubercloud.io/instance + spec.nodeName。
//
// 数据来源：
//   - K8s Pod（按 ani.kubercloud.io/tenant-id=<tenant> label 过滤）
//   - Pod label ani.kubercloud.io/instance 作为实例名（回显到 instance_id 字段）
//   - Pod spec.nodeName 作为节点名
//
// InstanceStore 中的正式实例（inst_xxx）会与 Pod 反查结果合并；
// orphan Pod（不在 InstanceStore 中的）用 deployment 名作为 instance_id。
// 同节点多实例时取字典序最小的 instance_id，保证稳定。
//
// 没有 k8sClient 注入时返回空 map，行为等同于旧的硬编码 nil。
func (api *gpuInventoryAPI) gpuNodeOccupancy(ctx context.Context, c *app.RequestContext) gpuNodeOccupancyMap {
	empty := gpuNodeOccupancyMap{entries: map[string]gpuNodeOccupancyEntry{}}
	tenantID := middleware.GetTenantID(c)
	if strings.TrimSpace(tenantID) == "" {
		tenantID = "demo-tenant"
	}
	// 获取 Pod 占用列表：测试时用注入的 fetcher，生产时查 K8s API。
	var pods []gpuPodOccupancy
	if api.podOccupancyFetcher != nil {
		pods = api.podOccupancyFetcher(ctx, tenantID)
	} else if api.k8sClient != nil {
		pods = api.fetchPodOccupancyFromK8s(ctx, tenantID)
	}
	if len(pods) == 0 {
		return empty
	}
	entries := make(map[string]gpuNodeOccupancyEntry, len(pods))
	for _, pod := range pods {
		instanceName := strings.TrimSpace(pod.InstanceName)
		if instanceName == "" {
			continue
		}
		// 只处理 Running phase 的 Pod（Pending/Failed 等不占用 GPU）。
		if !strings.EqualFold(pod.Phase, "Running") {
			continue
		}
		nodeName := strings.TrimSpace(pod.NodeName)
		if nodeName == "" {
			continue
		}
		// 累计同节点的 Running Pod 数量；InstanceID 取字典序最小的（展示用）。
		if existing, ok := entries[nodeName]; ok {
			existing.PodCount++
			if instanceName < existing.InstanceID {
				existing.InstanceID = instanceName
			}
			entries[nodeName] = existing
		} else {
			entries[nodeName] = gpuNodeOccupancyEntry{
				TenantID:   tenantID,
				InstanceID: instanceName,
				NodeName:   nodeName,
				PodCount:   1,
			}
		}
	}
	return gpuNodeOccupancyMap{entries: entries}
}

// fetchPodOccupancyFromK8s 查询 K8s API 获取本租户命名空间下所有带
// ani.kubercloud.io/tenant-id label 的 Pod，提取 instance name、node name
// 和 phase 用于 occupancy 构建。
func (api *gpuInventoryAPI) fetchPodOccupancyFromK8s(ctx context.Context, tenantID string) []gpuPodOccupancy {
	if api.k8sClient == nil {
		return nil
	}
	namespace := instanceTenantNamespace(tenantID)
	selector := url.QueryEscape("ani.kubercloud.io/tenant-id=" + tenantID)
	endpoint := api.k8sClient.Host() + "/api/v1/namespaces/" + url.PathEscape(namespace) + "/pods?labelSelector=" + selector
	body, _, err := api.k8sClient.Do(ctx, http.MethodGet, endpoint, "", nil)
	if err != nil || len(body) == 0 {
		return nil
	}
	var podList struct {
		Items []struct {
			Metadata struct {
				Name   string            `json:"name"`
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
			Spec struct {
				NodeName string `json:"nodeName"`
			} `json:"spec"`
			Status struct {
				Phase string `json:"phase"`
			} `json:"status"`
		} `json:"items"`
	}
	if json.Unmarshal(body, &podList) != nil {
		return nil
	}
	pods := make([]gpuPodOccupancy, 0, len(podList.Items))
	for _, pod := range podList.Items {
		pods = append(pods, gpuPodOccupancy{
			InstanceName: pod.Metadata.Labels["ani.kubercloud.io/instance"],
			NodeName:     pod.Spec.NodeName,
			Phase:        pod.Status.Phase,
		})
	}
	return pods
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
		writeInstanceError(c, http.StatusNotFound, "NOT_FOUND", err.Error())
	case errors.Is(err, ports.ErrConflict):
		writeInstanceError(c, http.StatusConflict, "CONFLICT", err.Error())
	case errors.Is(err, ports.ErrInvalid):
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	case errors.Is(err, ports.ErrUnsupported):
		writeInstanceError(c, http.StatusBadRequest, "UNSUPPORTED", err.Error())
	default:
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	}
}
