package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

// componentStatusRoundTripper 按请求路径分发固定 JSON body；
// 未注册路径返回 404（对应「workload not found」降级分支）。
type componentStatusRoundTripper struct {
	objects  map[string]string
	requests int
}

func (r *componentStatusRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	r.requests++
	if body, ok := r.objects[req.URL.Path]; ok {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     http.Header{},
		}, nil
	}
	return &http.Response{
		StatusCode: http.StatusNotFound,
		Body:       io.NopCloser(strings.NewReader(`{"kind":"Status","code":404}`)),
		Header:     http.Header{},
	}, nil
}

// componentStatusFakeHealth 固定返回 service_name → scrape_status 映射。
type componentStatusFakeHealth struct {
	fail     bool
	statuses map[string]string
}

func (f *componentStatusFakeHealth) ReadPlatformServiceHealth(context.Context) (ports.PlatformServiceHealth, error) {
	if f.fail {
		return ports.PlatformServiceHealth{}, errors.New("prometheus down")
	}
	components := make([]ports.PlatformServiceHealthComponent, 0, len(f.statuses))
	for name, status := range f.statuses {
		components = append(components, ports.PlatformServiceHealthComponent{
			ServiceName:  name,
			ScrapeStatus: status,
		})
	}
	return ports.PlatformServiceHealth{Components: components}, nil
}

func newComponentStatusTestService(t *testing.T, rt http.RoundTripper, health ports.PlatformServiceHealthReader) *KubernetesComponentStatusService {
	t.Helper()
	client, err := NewKubernetesRESTClient(KubernetesRESTClientConfig{
		Host:       "https://kubernetes.test",
		HTTPClient: &http.Client{Transport: rt},
	})
	if err != nil {
		t.Fatalf("NewKubernetesRESTClient() error = %v", err)
	}
	return NewKubernetesComponentStatusService(client, health, 0)
}

func componentStatusDeploymentBody(replicas, ready int32, available string, image string) string {
	return fmt.Sprintf(`{"spec":{"replicas":%d,"template":{"spec":{"containers":[{"image":%q}]}}},`+
		`"status":{"replicas":%d,"readyReplicas":%d,"conditions":[{"type":"Available","status":%q}]}}`,
		replicas, image, replicas, ready, available)
}

func componentStatusStatefulSetBody(replicas, ready int32, image string) string {
	return fmt.Sprintf(`{"spec":{"replicas":%d,"template":{"spec":{"containers":[{"image":%q}]}}},`+
		`"status":{"replicas":%d,"readyReplicas":%d,"conditions":[{"type":"Available","status":"True"}]}}`,
		replicas, image, replicas, ready)
}

func componentStatusDaemonSetBody(desired, ready int32, image string) string {
	return fmt.Sprintf(`{"spec":{"template":{"spec":{"containers":[{"image":%q}]}}},`+
		`"status":{"desiredNumberScheduled":%d,"numberReady":%d,"conditions":[{"type":"Available","status":"True"}]}}`,
		image, desired, ready)
}

// componentStatusAllRunningObjects 为注册表内全部条目生成 running 对象，
// 再允许单测覆盖个别条目，构造 real_provider=true 的全绿基线。
func componentStatusAllRunningObjects() map[string]string {
	objects := make(map[string]string)
	for _, entry := range componentStatusRegistry {
		var body string
		switch entry.Kind {
		case ports.ComponentKindStatefulSet:
			body = componentStatusStatefulSetBody(1, 1, "reg.example/ani/"+entry.Name+":v1.0.0")
		case ports.ComponentKindDaemonSet:
			body = componentStatusDaemonSetBody(2, 2, "reg.example/ani/"+entry.Name+":v1.0.0")
		default:
			body = componentStatusDeploymentBody(2, 2, "True", "reg.example/ani/"+entry.Name+":v1.0.0")
		}
		objects[componentStatusObjectPath(entry)] = body
	}
	return objects
}

func findComponent(t *testing.T, snapshot ports.ComponentStatusSnapshot, group, name string) ports.ComponentStatus {
	t.Helper()
	for _, g := range snapshot.Groups {
		if g.Name != group {
			continue
		}
		for _, c := range g.Components {
			if c.Name == name {
				return c
			}
		}
	}
	t.Fatalf("component %s/%s not found in snapshot", group, name)
	return ports.ComponentStatus{}
}

func TestKubernetesComponentStatusRegistryServiceEntries(t *testing.T) {
	seen := map[string]bool{}
	for _, entry := range componentStatusRegistry {
		if entry.Group == ports.ComponentGroupService {
			if entry.ServiceName == "" {
				t.Fatalf("service component %s missing ServiceName", entry.Name)
			}
			if seen[entry.ServiceName] {
				t.Fatalf("duplicate ServiceName %s", entry.ServiceName)
			}
			seen[entry.ServiceName] = true
			continue
		}
		if entry.ServiceName != "" {
			t.Fatalf("non-service component %s must not set ServiceName", entry.Name)
		}
	}
	if len(seen) != 7 {
		t.Fatalf("service group size = %d, want 7 (ANI self-built services)", len(seen))
	}
}

func TestKubernetesComponentStatusAllRunning(t *testing.T) {
	rt := &componentStatusRoundTripper{objects: componentStatusAllRunningObjects()}
	service := newComponentStatusTestService(t, rt, &componentStatusFakeHealth{statuses: map[string]string{
		"ani-gateway": ports.PlatformServiceScrapeReachable,
	}})

	snapshot, err := service.GetComponentStatus(context.Background())
	if err != nil {
		t.Fatalf("GetComponentStatus() error = %v", err)
	}
	if !snapshot.DevProfile.RealProvider || snapshot.DevProfile.Mode != "real" {
		t.Fatalf("dev_profile = %+v, want real provider", snapshot.DevProfile)
	}
	gateway := findComponent(t, snapshot, ports.ComponentGroupService, "ani-gateway")
	if gateway.Status != ports.ComponentStatusRunning {
		t.Fatalf("ani-gateway status = %s, want running", gateway.Status)
	}
	if gateway.DesiredReplicas == nil || *gateway.DesiredReplicas != 2 || gateway.ReadyReplicas == nil || *gateway.ReadyReplicas != 2 {
		t.Fatalf("ani-gateway replicas = %v/%v, want 2/2", gateway.DesiredReplicas, gateway.ReadyReplicas)
	}
	if gateway.Version == nil || *gateway.Version != "v1.0.0" {
		t.Fatalf("ani-gateway version = %v, want v1.0.0", gateway.Version)
	}
	if gateway.ScrapeStatus == nil || *gateway.ScrapeStatus != ports.PlatformServiceScrapeReachable {
		t.Fatalf("ani-gateway scrape_status = %v, want reachable", gateway.ScrapeStatus)
	}
	postgres := findComponent(t, snapshot, ports.ComponentGroupDependency, "ani-reconcile-ha-postgres")
	if postgres.Kind != ports.ComponentKindStatefulSet || postgres.Status != ports.ComponentStatusRunning {
		t.Fatalf("postgres = %+v, want running StatefulSet", postgres)
	}
	if postgres.ScrapeStatus != nil {
		t.Fatalf("dependency scrape_status = %v, want nil", postgres.ScrapeStatus)
	}
	if len(snapshot.Groups) != 3 {
		t.Fatalf("groups = %d, want 3", len(snapshot.Groups))
	}
}

func TestKubernetesComponentStatusDegradedStoppedAndMissing(t *testing.T) {
	objects := componentStatusAllRunningObjects()
	// model-service：部分副本 Ready 且 Available=False → degraded。
	objects[componentStatusObjectPath(componentRegistration{Name: "model-service", Namespace: "ani-system"})] =
		componentStatusDeploymentBody(2, 1, "False", "reg.example/ani/model-service:v2.0.0")
	// task-service：期望副本 0 → stopped。
	objects[componentStatusObjectPath(componentRegistration{Name: "task-service", Namespace: "ani-system"})] =
		componentStatusDeploymentBody(0, 0, "False", "reg.example/ani/task-service:v2.0.0")
	// inference-service 从 fake 中移除 → 404 → unknown + reason。
	delete(objects, componentStatusObjectPath(componentRegistration{Name: "inference-service", Namespace: "ani-system"}))
	rt := &componentStatusRoundTripper{objects: objects}
	service := newComponentStatusTestService(t, rt, &componentStatusFakeHealth{})

	snapshot, err := service.GetComponentStatus(context.Background())
	if err != nil {
		t.Fatalf("GetComponentStatus() error = %v", err)
	}
	model := findComponent(t, snapshot, ports.ComponentGroupService, "model-service")
	if model.Status != ports.ComponentStatusDegraded {
		t.Fatalf("model-service status = %s, want degraded", model.Status)
	}
	task := findComponent(t, snapshot, ports.ComponentGroupService, "task-service")
	if task.Status != ports.ComponentStatusStopped {
		t.Fatalf("task-service status = %s, want stopped", task.Status)
	}
	inference := findComponent(t, snapshot, ports.ComponentGroupService, "inference-service")
	if inference.Status != ports.ComponentStatusUnknown {
		t.Fatalf("inference-service status = %s, want unknown", inference.Status)
	}
	if inference.Reason == nil || !strings.Contains(*inference.Reason, "workload not found") {
		t.Fatalf("inference-service reason = %v, want workload not found", inference.Reason)
	}
	if inference.DesiredReplicas != nil || inference.ReadyReplicas != nil {
		t.Fatalf("unknown component must omit replicas, got %v/%v", inference.DesiredReplicas, inference.ReadyReplicas)
	}
	if snapshot.DevProfile.RealProvider {
		t.Fatalf("dev_profile.real_provider = true, want false when component unknown")
	}
	if snapshot.DevProfile.Reason == "" || !strings.Contains(snapshot.DevProfile.Reason, componentStatusDegradedPrefix) {
		t.Fatalf("dev_profile.reason = %q, want degraded prefix", snapshot.DevProfile.Reason)
	}
}

func TestComponentStatusFromObjectDaemonSet(t *testing.T) {
	var object k8sWorkloadObject
	object.Spec.Template.Spec.Containers = []struct {
		Image string `json:"image"`
	}{{Image: "reg.example/ani/agent:v0.9.0"}}
	object.Status.DesiredNumberScheduled = 3
	object.Status.NumberReady = 2
	object.Status.Conditions = []struct {
		Type   string `json:"type"`
		Status string `json:"status"`
	}{{Type: "Available", Status: "True"}}

	component := componentStatusFromObject(
		ports.ComponentStatus{Name: "agent", Kind: ports.ComponentKindDaemonSet, Group: ports.ComponentGroupDependency},
		ports.ComponentKindDaemonSet, object)
	if component.Status != ports.ComponentStatusDegraded {
		t.Fatalf("status = %s, want degraded (ready 2/3)", component.Status)
	}
	if component.DesiredReplicas == nil || *component.DesiredReplicas != 3 ||
		component.ReadyReplicas == nil || *component.ReadyReplicas != 2 {
		t.Fatalf("replicas = %v/%v, want 3/2", component.DesiredReplicas, component.ReadyReplicas)
	}
	if component.Version == nil || *component.Version != "v0.9.0" {
		t.Fatalf("version = %v, want v0.9.0", component.Version)
	}

	object.Status.NumberReady = 3
	component = componentStatusFromObject(
		ports.ComponentStatus{Name: "agent", Kind: ports.ComponentKindDaemonSet, Group: ports.ComponentGroupDependency},
		ports.ComponentKindDaemonSet, object)
	if component.Status != ports.ComponentStatusRunning {
		t.Fatalf("status = %s, want running (ready 3/3)", component.Status)
	}
}

func TestKubernetesComponentStatusScrapeFusion(t *testing.T) {
	rt := &componentStatusRoundTripper{objects: componentStatusAllRunningObjects()}
	service := newComponentStatusTestService(t, rt, &componentStatusFakeHealth{statuses: map[string]string{
		"ani-gateway":   ports.PlatformServiceScrapeReachable,
		"auth-service":  ports.PlatformServiceScrapeUnreachable,
		"metering-service": ports.PlatformServiceScrapeUnknown,
	}})

	snapshot, err := service.GetComponentStatus(context.Background())
	if err != nil {
		t.Fatalf("GetComponentStatus() error = %v", err)
	}
	gateway := findComponent(t, snapshot, ports.ComponentGroupService, "ani-gateway")
	if gateway.ScrapeStatus == nil || *gateway.ScrapeStatus != ports.PlatformServiceScrapeReachable {
		t.Fatalf("gateway scrape_status = %v, want reachable", gateway.ScrapeStatus)
	}
	auth := findComponent(t, snapshot, ports.ComponentGroupService, "ani-auth-service")
	if auth.ScrapeStatus == nil || *auth.ScrapeStatus != ports.PlatformServiceScrapeUnreachable {
		t.Fatalf("auth scrape_status = %v, want unreachable", auth.ScrapeStatus)
	}
	metering := findComponent(t, snapshot, ports.ComponentGroupService, "ani-metering-service")
	if metering.ScrapeStatus == nil || *metering.ScrapeStatus != ports.PlatformServiceScrapeUnknown {
		t.Fatalf("metering scrape_status = %v, want unknown", metering.ScrapeStatus)
	}
	console := findComponent(t, snapshot, ports.ComponentGroupPlatform, "ani-console")
	if console.ScrapeStatus != nil {
		t.Fatalf("platform scrape_status = %v, want nil", console.ScrapeStatus)
	}
}

func TestKubernetesComponentStatusScrapeReaderFailure(t *testing.T) {
	rt := &componentStatusRoundTripper{objects: componentStatusAllRunningObjects()}
	service := newComponentStatusTestService(t, rt, &componentStatusFakeHealth{fail: true})

	snapshot, err := service.GetComponentStatus(context.Background())
	if err != nil {
		t.Fatalf("GetComponentStatus() must not fail when scrape reader fails, got %v", err)
	}
	gateway := findComponent(t, snapshot, ports.ComponentGroupService, "ani-gateway")
	if gateway.ScrapeStatus == nil || *gateway.ScrapeStatus != ports.PlatformServiceScrapeUnknown {
		t.Fatalf("gateway scrape_status = %v, want unknown", gateway.ScrapeStatus)
	}
	if gateway.Reason == nil || !strings.Contains(*gateway.Reason, "scrape reader unavailable") {
		t.Fatalf("gateway reason = %v, want scrape reader unavailable", gateway.Reason)
	}
}

func TestKubernetesComponentStatusTTLCache(t *testing.T) {
	rt := &componentStatusRoundTripper{objects: componentStatusAllRunningObjects()}
	service := newComponentStatusTestService(t, rt, &componentStatusFakeHealth{})
	current := time.Now()
	service.now = func() time.Time { return current }

	if _, err := service.GetComponentStatus(context.Background()); err != nil {
		t.Fatalf("first GetComponentStatus() error = %v", err)
	}
	first := rt.requests
	current = current.Add(5 * time.Second)
	if _, err := service.GetComponentStatus(context.Background()); err != nil {
		t.Fatalf("cached GetComponentStatus() error = %v", err)
	}
	if rt.requests != first {
		t.Fatalf("requests = %d, want %d (cache hit within TTL)", rt.requests, first)
	}
	current = current.Add(componentStatusDefaultTTL)
	if _, err := service.GetComponentStatus(context.Background()); err != nil {
		t.Fatalf("expired GetComponentStatus() error = %v", err)
	}
	if rt.requests <= first {
		t.Fatalf("requests = %d, want > %d (cache expired)", rt.requests, first)
	}
}

func TestKubernetesComponentStatusNilClient(t *testing.T) {
	service := NewKubernetesComponentStatusService(nil, nil, 0)
	_, err := service.GetComponentStatus(context.Background())
	if !errors.Is(err, ports.ErrComponentStatusUnsupported) {
		t.Fatalf("err = %v, want ErrComponentStatusUnsupported", err)
	}
}
