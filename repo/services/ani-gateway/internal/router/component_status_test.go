package router

import (
	"context"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/ports"
)

func TestComponentStatusLocalFallbackProfile(t *testing.T) {
	api := newComponentStatusAPI(nil)

	snapshot, err := api.service.GetComponentStatus(context.Background())
	if err != nil {
		t.Fatalf("GetComponentStatus() error = %v", err)
	}
	response := componentStatusResponseFromSnapshot(snapshot)
	if len(response.Groups) != 3 {
		t.Fatalf("groups = %d, want 3", len(response.Groups))
	}
	for _, group := range response.Groups {
		if len(group.Components) == 0 {
			t.Fatalf("group %s has no components, want non-empty", group.Name)
		}
		for _, component := range group.Components {
			if component.Status != ports.ComponentStatusRunning {
				t.Fatalf("%s/%s status = %s, want deterministic running", component.Group, component.Name, component.Status)
			}
			if component.Group == ports.ComponentGroupService {
				if component.ScrapeStatus == nil || *component.ScrapeStatus != ports.PlatformServiceScrapeReachable {
					t.Fatalf("%s scrape_status = %v, want reachable", component.Name, component.ScrapeStatus)
				}
			} else if component.ScrapeStatus != nil {
				t.Fatalf("%s scrape_status = %v, want nil", component.Name, component.ScrapeStatus)
			}
		}
	}
	if response.DevProfile.Mode != "local" || response.DevProfile.RealProvider {
		t.Fatalf("dev_profile = %+v, want local non-real profile", response.DevProfile)
	}
}

func TestComponentStatusResponseFromSnapshotDeviates(t *testing.T) {
	desired, ready := int32(2), int32(1)
	version, scrape, reason := "v9.9.9", ports.PlatformServiceScrapeUnreachable, "workload not found"
	snapshot := ports.ComponentStatusSnapshot{
		ObservedAt: time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC),
		Groups: []ports.ComponentGroupStatus{{
			Name: ports.ComponentGroupService,
			Components: []ports.ComponentStatus{{
				Name: "ani-gateway", Kind: ports.ComponentKindDeployment, Namespace: "ani-system",
				Group: ports.ComponentGroupService, Status: ports.ComponentStatusDegraded,
				DesiredReplicas: &desired, ReadyReplicas: &ready,
				Version: &version, ScrapeStatus: &scrape, Reason: &reason,
			}},
		}},
		DevProfile: ports.DevProfileInfo{
			Mode: "real", Provider: "kubernetes-component-status", RealProvider: true,
			Reason: "degraded components: ani-system/inference-service: workload not found",
		},
	}

	response := componentStatusResponseFromSnapshot(snapshot)
	if _, err := time.Parse(time.RFC3339, response.ObservedAt); err != nil {
		t.Fatalf("observed_at = %q, want RFC3339: %v", response.ObservedAt, err)
	}
	component := response.Groups[0].Components[0]
	if component.Status != ports.ComponentStatusDegraded || component.DesiredReplicas == nil || *component.DesiredReplicas != 2 ||
		component.ReadyReplicas == nil || *component.ReadyReplicas != 1 {
		t.Fatalf("component = %+v, want degraded 2/1 passthrough", component)
	}
	if component.Version == nil || *component.Version != "v9.9.9" ||
		component.ScrapeStatus == nil || *component.ScrapeStatus != ports.PlatformServiceScrapeUnreachable ||
		component.Reason == nil || *component.Reason != "workload not found" {
		t.Fatalf("component optional fields = %+v, want passthrough", component)
	}
	if !response.DevProfile.RealProvider || response.DevProfile.Provider != "kubernetes-component-status" {
		t.Fatalf("dev_profile = %+v, want real passthrough", response.DevProfile)
	}
}

func TestComponentStatusRegisterOptionsWiresService(t *testing.T) {
	// RegisterOptions.ComponentStatusService 注入为 nil 时 handler 走 local 回退；
	// 注入非 nil 时透传。这里验证字段装配路径编译与回退语义。
	options := RegisterOptions{ComponentStatusService: runtime.NewLocalPlatformComponentStatusService()}
	if options.ComponentStatusService == nil {
		t.Fatal("ComponentStatusService = nil, want injected service")
	}
	api := newComponentStatusAPI(options.ComponentStatusService)
	if api.service != options.ComponentStatusService {
		t.Fatal("api.service should passthrough the injected service")
	}
}
