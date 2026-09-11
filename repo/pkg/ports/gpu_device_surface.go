package ports

import (
	"context"
	"time"
)

// GPU 设备台账（device surface）端口：状态覆盖、设备事件流。
// 数据源是平台级 PG 台账（deploy/migrations/20260911_001_gpu_device_surface.sql），
// 语义依据 repo/design/gpu-pool-status-surface-gap-plan.md（D3/D4）：
//   - 状态覆盖：maintenance/unavailable 人工态 + reason 持久化（D3/D4）
//   - 事件流：状态翻转/集群切分统一写事件
//
// GPU 预留为数量型语义（PUT /admin/tenants/{tenant_id}/reservations），
// 不做设备级分配；事件流因此不含预留类事件。
//
// 全部方法自开平台事务（WithPlatformTx，RLS bypass），调用方无需持有租户上下文。
// device_id 为 K8s 节点 × 物理卡 index × 型号派生的稳定 UUID，与
// GET /gpu-inventory 记录 id 同口径。

// GPU 设备人工覆盖状态。fault 是自动检测态，不落覆盖表。
const (
	GPUDeviceOverlayMaintenance = "maintenance"
	GPUDeviceOverlayUnavailable = "unavailable"
)

// 设备事件类型。
const (
	GPUDeviceEventStatusChanged    = "status_changed"
	GPUDeviceEventPartitionApplied = "partition_applied"
)

// GPUDeviceOverlay 是单卡人工状态覆盖行（maintenance/unavailable + reason）。
type GPUDeviceOverlay struct {
	DeviceID  string
	Status    string // maintenance | unavailable
	Reason    string
	UpdatedBy string
	UpdatedAt time.Time
}

// GPUDeviceEvent 是设备台账事件（BOSS 联动事件流数据源）。
type GPUDeviceEvent struct {
	ID        string
	DeviceID  string // 节点级事件（集群切分）为空
	NodeName  string
	GPUType   string
	EventType string // status_changed | partition_applied
	Reason    string
	Actor     string
	CreatedAt time.Time
}

// GPUDeviceEventFilter 是事件流查询过滤条件；零值字段不过滤。
type GPUDeviceEventFilter struct {
	DeviceID  string
	EventType string
	Limit     int
}

// GPUDeviceSurfaceStore 是设备台账 PG 存储 port。
type GPUDeviceSurfaceStore interface {
	// SetDeviceOverlay 写入/更新人工状态覆盖（maintenance/unavailable + reason）。
	SetDeviceOverlay(ctx context.Context, overlay GPUDeviceOverlay) error
	// DeleteDeviceOverlay 清除人工覆盖（idle 翻转）；无覆盖返回 ErrNotFound。
	DeleteDeviceOverlay(ctx context.Context, deviceID string) error
	// GetDeviceOverlay 返回单卡覆盖；无覆盖返回 ErrNotFound。
	GetDeviceOverlay(ctx context.Context, deviceID string) (GPUDeviceOverlay, error)
	// ListDeviceOverlays 返回全部覆盖（平台视图，量级 = 被人工操作过的卡数）。
	ListDeviceOverlays(ctx context.Context) ([]GPUDeviceOverlay, error)

	// AppendDeviceEvent 追加设备事件。
	AppendDeviceEvent(ctx context.Context, event GPUDeviceEvent) error
	// ListDeviceEvents 按过滤条件返回事件（created_at 倒序）。
	ListDeviceEvents(ctx context.Context, filter GPUDeviceEventFilter) ([]GPUDeviceEvent, error)
}
