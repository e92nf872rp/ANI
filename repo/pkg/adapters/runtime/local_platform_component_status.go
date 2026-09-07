package runtime

import (
	"context"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

// localPlatformComponentStatusProvider local 确定性降级实现的 provider 标识。
const localPlatformComponentStatusProvider = "local-component-status"

// LocalPlatformComponentStatusService 组件状态 local 确定性实现：
// 注册表内全部组件固定 running（1/1 副本、dev-local 版本），service 组
// scrape_status 固定 reachable。用于 gateway 未配置 real provider 时
// 回退，保证接口可启动可联调；dev_profile 明确标注 local 非 real。
type LocalPlatformComponentStatusService struct{}

// NewLocalPlatformComponentStatusService 构造 local 确定性实现。
func NewLocalPlatformComponentStatusService() *LocalPlatformComponentStatusService {
	return &LocalPlatformComponentStatusService{}
}

// GetComponentStatus 返回确定性组件状态快照。
func (s *LocalPlatformComponentStatusService) GetComponentStatus(ctx context.Context) (ports.ComponentStatusSnapshot, error) {
	groups := make([]ports.ComponentGroupStatus, 0, len(componentStatusGroups))
	for _, group := range componentStatusGroups {
		components := make([]ports.ComponentStatus, 0)
		for _, entry := range componentStatusRegistry {
			if entry.Group != group {
				continue
			}
			component := ports.ComponentStatus{
				Name:            entry.Name,
				Kind:            entry.Kind,
				Namespace:       entry.Namespace,
				Group:           entry.Group,
				Status:          ports.ComponentStatusRunning,
				DesiredReplicas: int32Ptr(1),
				ReadyReplicas:   int32Ptr(1),
				Version:         stringPtr("dev-local"),
			}
			if entry.Group == ports.ComponentGroupService {
				component.ScrapeStatus = stringPtr(ports.PlatformServiceScrapeReachable)
			}
			components = append(components, component)
		}
		groups = append(groups, ports.ComponentGroupStatus{
			Name:       group,
			Components: components,
		})
	}
	return ports.ComponentStatusSnapshot{
		ObservedAt: time.Now().UTC(),
		Groups:     groups,
		DevProfile: ports.DevProfileInfo{
			Mode:         "local",
			Provider:     localPlatformComponentStatusProvider,
			RealProvider: false,
			Reason:       "deterministic local fixture",
		},
	}, nil
}
