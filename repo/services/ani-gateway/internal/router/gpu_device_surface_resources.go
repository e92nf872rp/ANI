package router

// GPU 设备台账（device surface）HTTP 层：状态翻转 PATCH、事件流。
// 语义依据 repo/design/gpu-pool-status-surface-gap-plan.md §4（D3/D4 决策）：
//   - PATCH 翻转 maintenance/idle/unavailable + reason 落 PG 平台账（D3/D4）
//   - GET /gpu-inventory/events 设备事件流（BOSS 联动事件块数据源）
//
// GPU 预留为数量型语义（PUT /admin/tenants/{tenant_id}/reservations），
// 不做设备级分配。
//
// 台账合并规则：人工覆盖（maintenance/unavailable）> 自动观测态
//（available/in_use/fault）。

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"
	"github.com/google/uuid"

	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/services/ani-gateway/internal/middleware"
)

// gpuDeviceSurfaceState 是按请求加载的台账合并状态（人工覆盖）。
type gpuDeviceSurfaceState struct {
	overlays map[string]ports.GPUDeviceOverlay
}

func emptySurfaceState() gpuDeviceSurfaceState {
	return gpuDeviceSurfaceState{
		overlays: map[string]ports.GPUDeviceOverlay{},
	}
}

// loadSurfaceState 拉取台账状态；store 未注入（local/dev profile）时返回空态。
func (api *gpuInventoryAPI) loadSurfaceState(ctx context.Context) gpuDeviceSurfaceState {
	state := emptySurfaceState()
	if api.surface == nil {
		return state
	}
	if overlays, err := api.surface.ListDeviceOverlays(ctx); err == nil {
		for _, o := range overlays {
			state.overlays[o.DeviceID] = o
		}
	}
	return state
}

// loadSurfaceStateForRequest 仅平台 token（BOSS）合并台账状态：人工覆盖是
// 平台级跨租户数据（WithPlatformTx RLS bypass 读出），租户侧的清单/占用视图
// 不得携带，防跨租户泄露（照 GET /quotas 的平台隔离先例）。
func (api *gpuInventoryAPI) loadSurfaceStateForRequest(ctx context.Context, c *app.RequestContext) gpuDeviceSurfaceState {
	if middleware.GetScope(c) != "platform" {
		return emptySurfaceState()
	}
	return api.loadSurfaceState(ctx)
}

// applyToRecord 将台账状态合并进设备记录。
func (state gpuDeviceSurfaceState) applyToRecord(record *gpuInventoryRecordResponse) {
	if overlay, ok := state.overlays[record.ID]; ok {
		record.Status = overlay.Status
		record.Reason = overlay.Reason
	}
}

// gpuDeviceRecordID 与 gpuInventoryRecordFromDevice 的记录 id 派生口径一致。
func gpuDeviceRecordID(nodeName string, index int, model string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(nodeName+"/"+strconv.Itoa(index)+"/"+model)).String()
}

// findDeviceRecordByID 在设备清单中解析 device_id 对应的基础记录（未合并台账态）。
func (api *gpuInventoryAPI) findDeviceRecordByID(ctx context.Context, deviceID string) (*gpuInventoryRecordResponse, bool) {
	nodes, err := api.inventory.ListNodeClasses(ctx, ports.GPUDiscoveryFilter{})
	if err != nil {
		return nil, false
	}
	occupancy := api.gpuNodeOccupancyForTenant(ctx, "")
	state := api.loadSurfaceState(ctx)
	for _, node := range nodes {
		for index, device := range node.Devices {
			if gpuDeviceRecordID(node.NodeName, index, firstNonEmpty(device.Model, node.Model, string(device.Vendor))) != deviceID {
				continue
			}
			record := api.gpuInventoryRecordFromDevice(ctx, node, device, index, occupancy, state)
			return &record, true
		}
	}
	return nil, false
}

// registerGPUDeviceSurfaceResources 注册台账相关路由。静态路径必须先于
// /gpu-inventory/:device_id 参数路由注册。
func registerGPUDeviceSurfaceResources(v1 *route.RouterGroup, api *gpuInventoryAPI) {
	v1.GET("/gpu-inventory/events", api.listGPUDeviceEvents)
	v1.PATCH("/gpu-inventory/:device_id", api.updateGPUDeviceStatus)
}

type gpuDeviceStatusUpdateRequest struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}

// updateGPUDeviceStatus 处理 PATCH /gpu-inventory/:device_id：
// maintenance/unavailable 写覆盖，idle 清覆盖，fault 拒绝（自动检测态）。
func (api *gpuInventoryAPI) updateGPUDeviceStatus(ctx context.Context, c *app.RequestContext) {
	deviceID := strings.TrimSpace(c.Param("device_id"))
	if _, err := uuid.Parse(deviceID); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "device_id must be a uuid")
		return
	}
	var req gpuDeviceStatusUpdateRequest
	if err := c.BindJSON(&req); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
		return
	}
	if api.surface == nil {
		writeInstanceError(c, http.StatusNotImplemented, "NOT_IMPLEMENTED", "gpu device surface store is not configured")
		return
	}
	actor := surfaceActor(c)
	switch req.Status {
	case "maintenance", "unavailable":
		overlay := ports.GPUDeviceOverlay{
			DeviceID:  deviceID,
			Status:    req.Status,
			Reason:    strings.TrimSpace(req.Reason),
			UpdatedBy: actor,
		}
		if err := api.surface.SetDeviceOverlay(ctx, overlay); err != nil {
			writeGPUInventoryError(c, err)
			return
		}
	case "idle":
		// 幂等：无覆盖时也视为成功（返回当前合并态）。
		if err := api.surface.DeleteDeviceOverlay(ctx, deviceID); err != nil && err != ports.ErrNotFound {
			writeGPUInventoryError(c, err)
			return
		}
	default:
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "status must be one of maintenance, idle, unavailable")
		return
	}
	// 事件在设备记录解析后追加，带上 node_name/gpu_type 便于 BOSS 事件流直读。
	record, ok := api.findDeviceRecordByID(ctx, deviceID)
	if ok {
		_ = api.surface.AppendDeviceEvent(ctx, ports.GPUDeviceEvent{
			DeviceID:  deviceID,
			NodeName:  record.NodeName,
			GPUType:   record.GPUType,
			EventType: ports.GPUDeviceEventStatusChanged,
			Reason:    strings.TrimSpace(req.Reason),
			Actor:     actor,
		})
		c.JSON(http.StatusOK, record)
		return
	}
	// 记录解析失败不阻塞翻转结果：事件降级为仅有 device_id。
	_ = api.surface.AppendDeviceEvent(ctx, ports.GPUDeviceEvent{
		DeviceID:  deviceID,
		EventType: ports.GPUDeviceEventStatusChanged,
		Reason:    strings.TrimSpace(req.Reason),
		Actor:     actor,
	})
	writeInstanceError(c, http.StatusNotFound, "DEVICE_NOT_FOUND", "gpu device not found")
}

// listGPUDeviceEvents 处理 GET /gpu-inventory/events（BOSS 联动事件流）。
func (api *gpuInventoryAPI) listGPUDeviceEvents(ctx context.Context, c *app.RequestContext) {
	if api.surface == nil {
		c.JSON(http.StatusOK, gpuDeviceEventListResponse{
			Items:      []gpuDeviceEventResponse{},
			DevProfile: api.profile,
		})
		return
	}
	filter := ports.GPUDeviceEventFilter{
		DeviceID:  strings.TrimSpace(c.Query("device_id")),
		EventType: strings.TrimSpace(c.Query("event_type")),
		Limit:     queryInt(c, "limit", 50),
	}
	switch filter.EventType {
	case "", ports.GPUDeviceEventStatusChanged, ports.GPUDeviceEventPartitionApplied:
	default:
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid event_type")
		return
	}
	events, err := api.surface.ListDeviceEvents(ctx, filter)
	if err != nil {
		writeGPUInventoryError(c, err)
		return
	}
	items := make([]gpuDeviceEventResponse, 0, len(events))
	for _, event := range events {
		items = append(items, gpuDeviceEventResponseFromPort(event))
	}
	c.JSON(http.StatusOK, gpuDeviceEventListResponse{
		Items:      items,
		Total:      len(items),
		DevProfile: api.profile,
	})
}

type gpuDeviceEventResponse struct {
	ID        string  `json:"id"`
	DeviceID  *string `json:"device_id"`
	NodeName  *string `json:"node_name"`
	GPUType   *string `json:"gpu_type"`
	EventType string  `json:"event_type"`
	Reason    *string `json:"reason"`
	Actor     *string `json:"actor"`
	CreatedAt string  `json:"created_at"`
}

type gpuDeviceEventListResponse struct {
	Items      []gpuDeviceEventResponse `json:"items"`
	Total      int                      `json:"total"`
	NextCursor *string                  `json:"next_cursor"`
	DevProfile coreDevProfileResponse   `json:"dev_profile"`
}

func optionalStringPtr(raw string) *string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	return &raw
}

func gpuDeviceEventResponseFromPort(event ports.GPUDeviceEvent) gpuDeviceEventResponse {
	return gpuDeviceEventResponse{
		ID:        event.ID,
		DeviceID:  optionalStringPtr(event.DeviceID),
		NodeName:  optionalStringPtr(event.NodeName),
		GPUType:   optionalStringPtr(event.GPUType),
		EventType: event.EventType,
		Reason:    optionalStringPtr(event.Reason),
		Actor:     optionalStringPtr(event.Actor),
		CreatedAt: event.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

// surfaceActor 返回操作者标识（平台用户 ID，缺省 platform）。
func surfaceActor(c *app.RequestContext) string {
	if userID := strings.TrimSpace(middleware.GetUserID(c)); userID != "" {
		return userID
	}
	return "platform"
}
