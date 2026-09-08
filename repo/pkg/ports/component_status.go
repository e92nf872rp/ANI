package ports

import (
	"context"
	"errors"
	"time"
)

// 平台组件状态（平台「组件状态」页）只读端口。
// 状态来自 Kubernetes 工作负载对象（Deployment/StatefulSet/DaemonSet），
// service 组额外融合 Prometheus 抓取可达性（scrape_status）。
var (
	// ErrComponentStatusUnsupported 表示当前 provider 不支持组件状态查询。
	ErrComponentStatusUnsupported = errors.New("component status: unsupported provider")
	// ErrComponentStatusInvalid 表示组件状态请求参数非法（当前只读无参数，预留）。
	ErrComponentStatusInvalid = errors.New("component status: invalid request")
)

// 组件分组与状态枚举。
const (
	ComponentGroupService    = "service"
	ComponentGroupDependency = "dependency"
	ComponentGroupPlatform   = "platform"

	ComponentStatusRunning  = "running"
	ComponentStatusDegraded = "degraded"
	ComponentStatusStopped  = "stopped"
	ComponentStatusUnknown  = "unknown"
)

// ComponentKind K8s 工作负载对象类型。
type ComponentKind string

const (
	ComponentKindDeployment  ComponentKind = "Deployment"
	ComponentKindStatefulSet ComponentKind = "StatefulSet"
	ComponentKindDaemonSet   ComponentKind = "DaemonSet"
)

// ComponentStatus 单个组件运行状态。
// DesiredReplicas/ReadyReplicas/Version/ScrapeStatus/Reason 不可得时为 nil。
type ComponentStatus struct {
	Name            string
	Kind            ComponentKind
	Namespace       string
	Group           string
	Status          string
	DesiredReplicas *int32
	ReadyReplicas   *int32
	Version         *string
	// ScrapeStatus 仅 service 组非 nil；值域同 PlatformServiceHealth 组件的
	// scrape_status（reachable/unreachable/unknown），语义为 Prometheus 抓取
	// 可达性，不表示业务健康。
	ScrapeStatus *string
	Reason       *string
}

// ComponentGroupStatus 组件分组状态。
type ComponentGroupStatus struct {
	Name       string
	Components []ComponentStatus
}

// ComponentStatusSnapshot 组件状态聚合快照。
type ComponentStatusSnapshot struct {
	ObservedAt time.Time
	Groups     []ComponentGroupStatus
	DevProfile DevProfileInfo
}

// ComponentStatusService 平台级（跨租户）只读组件状态端口。
type ComponentStatusService interface {
	GetComponentStatus(ctx context.Context) (ComponentStatusSnapshot, error)
}
