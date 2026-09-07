// component_status.go 实现平台组件状态只读接口：
// GET /api/v1/platform/components（平台「组件状态」页）。
// 状态由 ComponentStatusService 按静态注册表聚合（K8s 工作负载 + service 组
// scrape_status 融合）。
package router

import (
	"context"
	"net/http"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"
	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/ports"
)

type componentStatusAPI struct {
	service ports.ComponentStatusService
}

type componentStatusResponse struct {
	ObservedAt string                            `json:"observed_at"`
	Groups     []componentGroupResponse          `json:"groups"`
	DevProfile coreDevProfileResponse            `json:"dev_profile"`
}

type componentGroupResponse struct {
	Name       string                  `json:"name"`
	Components []componentItemResponse `json:"components"`
}

type componentItemResponse struct {
	Name            string  `json:"name"`
	Kind            string  `json:"kind"`
	Namespace       string  `json:"namespace"`
	Group           string  `json:"group"`
	Status          string  `json:"status"`
	DesiredReplicas *int32  `json:"desired_replicas"`
	ReadyReplicas   *int32  `json:"ready_replicas"`
	Version         *string `json:"version"`
	ScrapeStatus    *string `json:"scrape_status"`
	Reason          *string `json:"reason"`
}

// newComponentStatusAPI 注入为 nil 时回退 local 确定性实现（与 platform-capacity
// fallback 惯例一致），保证 gateway 未配置 provider 时仍可启动并返回 200。
func newComponentStatusAPI(service ports.ComponentStatusService) *componentStatusAPI {
	if service == nil {
		service = runtimeadapter.NewLocalPlatformComponentStatusService()
	}
	return &componentStatusAPI{service: service}
}

func registerComponentStatus(v1 *route.RouterGroup, service ports.ComponentStatusService) {
	api := newComponentStatusAPI(service)
	v1.GET("/platform/components", api.getComponentStatus)
}

func (api *componentStatusAPI) getComponentStatus(ctx context.Context, c *app.RequestContext) {
	snapshot, err := api.service.GetComponentStatus(ctx)
	if err != nil {
		writeInstanceError(c, http.StatusInternalServerError, "COMPONENT_STATUS_FAILED", err.Error())
		return
	}
	c.JSON(http.StatusOK, componentStatusResponseFromSnapshot(snapshot))
}

func componentStatusResponseFromSnapshot(snapshot ports.ComponentStatusSnapshot) componentStatusResponse {
	groups := make([]componentGroupResponse, 0, len(snapshot.Groups))
	for _, group := range snapshot.Groups {
		components := make([]componentItemResponse, 0, len(group.Components))
		for _, component := range group.Components {
			components = append(components, componentItemResponse{
				Name:            component.Name,
				Kind:            string(component.Kind),
				Namespace:       component.Namespace,
				Group:           component.Group,
				Status:          component.Status,
				DesiredReplicas: component.DesiredReplicas,
				ReadyReplicas:   component.ReadyReplicas,
				Version:         component.Version,
				ScrapeStatus:    component.ScrapeStatus,
				Reason:          component.Reason,
			})
		}
		groups = append(groups, componentGroupResponse{
			Name:       group.Name,
			Components: components,
		})
	}
	return componentStatusResponse{
		ObservedAt: snapshot.ObservedAt.UTC().Format(time.RFC3339),
		Groups:     groups,
		DevProfile: coreDevProfileResponse{
			Mode:         snapshot.DevProfile.Mode,
			Provider:     snapshot.DevProfile.Provider,
			RealProvider: snapshot.DevProfile.RealProvider,
			Reason:       snapshot.DevProfile.Reason,
		},
	}
}
