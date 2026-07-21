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
	templates     ports.SandboxTemplateCatalog
	instanceStore ports.WorkloadInstanceStore
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

func newGPUInventoryAPIWithStore(inventory ports.GPUInventory, store ports.WorkloadInstanceStore, k8sClient *runtimeadapter.KubernetesRESTClient) *gpuInventoryAPI {
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
		inventory:     inventory,
		templates:     runtimeadapter.NewLocalSandboxTemplateCatalog(),
		instanceStore: store,
		k8sClient:     k8sClient,
		profile:       profile,
	}
}

func registerGPUInventoryResourcesWithStore(v1 *route.RouterGroup, inventory ports.GPUInventory, store ports.WorkloadInstanceStore, k8sClient *runtimeadapter.KubernetesRESTClient) {
	api := newGPUInventoryAPIWithStore(inventory, store, k8sClient)
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
	// 当节点 ready 且存在同节点的 GPU 容器实例时，按节点级回填归属实例。
	// 当前实现无法精确到"节点的哪张卡"（planning 阶段未持久化 GPU device
	// index），因此同节点所有卡回显同一实例；多实例共节点时取首个匹配。
	// 详见 PRD §3.1 / US-006。
	if status == "available" {
		if owner, ok := occupancy.lookup(node.NodeName); ok {
			tenantID := owner.TenantID
			instanceID := owner.InstanceID
			record.Status = "in_use"
			record.TenantID = &tenantID
			record.InstanceID = &instanceID
		}
	}
	return record
}

// gpuNodeOccupancyEntry 表示某个 GPU 容器实例在节点上的归属信息。
// 当前只承载节点级映射（instance → node），不含 GPU device index。
type gpuNodeOccupancyEntry struct {
	TenantID   string
	InstanceID string
	NodeName   string
}

// gpuNodeOccupancyMap 是 nodeName → 归属实例 的查询表。
// 当同节点存在多个 GPU 容器实例时，只保留首个（按 InstanceID 排序的稳定顺序），
// 因为当前持久化层不记录"节点上的哪张卡属于哪个实例"。
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
		entry := gpuNodeOccupancyEntry{
			TenantID:   tenantID,
			InstanceID: instanceName,
			NodeName:   nodeName,
		}
		// 同节点多实例时保留 InstanceID 字典序最小的，保证稳定。
		if existing, ok := entries[nodeName]; ok && existing.InstanceID < entry.InstanceID {
			continue
		}
		entries[nodeName] = entry
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
	namespace := demoTenantNamespace(tenantID)
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
