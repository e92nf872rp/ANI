package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

type fakePlatformWorkloadRuntime struct {
	mu       sync.Mutex
	applies  []ports.PlatformWorkloadCreateSpec
	deletes  int
	applyErr error
	ready    bool
	missing  bool
}

func newReadyFakePlatformWorkloadRuntime() *fakePlatformWorkloadRuntime {
	return &fakePlatformWorkloadRuntime{ready: true}
}

func (f *fakePlatformWorkloadRuntime) Apply(_ context.Context, tenantID, workloadID string, spec ports.PlatformWorkloadCreateSpec) (platformWorkloadObservation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applies = append(f.applies, spec)
	if f.applyErr != nil {
		return platformWorkloadObservation{}, f.applyErr
	}
	f.missing = false
	return f.observation(tenantID, spec), nil
}

func (f *fakePlatformWorkloadRuntime) Observe(_ context.Context, tenantID, _ string, spec ports.PlatformWorkloadCreateSpec) (platformWorkloadObservation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.missing {
		return platformWorkloadObservation{Reason: "NotFound"}, nil
	}
	return f.observation(tenantID, spec), nil
}

func (f *fakePlatformWorkloadRuntime) Delete(context.Context, string, string, ports.PlatformWorkloadCreateSpec) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deletes++
	f.missing = true
	return nil
}

func (f *fakePlatformWorkloadRuntime) Logs(context.Context, string, string, ports.PlatformWorkloadCreateSpec, int, string, string) (ports.PlatformWorkloadLogList, error) {
	return ports.PlatformWorkloadLogList{Items: []ports.PlatformWorkloadLogEntry{{
		Timestamp: time.Date(2026, 8, 15, 7, 0, 0, 0, time.UTC),
		Level:     "info",
		Message:   "vllm worker ready",
		Container: "inference-cpu-example",
		Stream:    "stdout",
	}}}, nil
}

func (f *fakePlatformWorkloadRuntime) observation(tenantID string, spec ports.PlatformWorkloadCreateSpec) platformWorkloadObservation {
	obs := platformWorkloadObservation{Endpoint: platformWorkloadEndpoint(tenantID, spec)}
	if f.ready {
		obs.ReadyReplicas = spec.Replicas
		obs.Ready = true
	}
	return obs
}

func TestKubernetesPlatformWorkloadCPUCreateGetStopStartScaleDelete(t *testing.T) {
	provider := newReadyFakePlatformWorkloadRuntime()
	svc := NewKubernetesPlatformWorkloadService(provider)
	ctx := context.Background()
	tenant := "11111111-1111-1111-1111-111111111111"
	spec := sampleCPUPlatformWorkloadSpec("1df72d71-9d49-46c4-a48a-52bb37b082ab", "inference-cpu-example")

	created, err := svc.Create(ctx, tenant, spec)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.State != ports.PlatformWorkloadRunning || created.InternalEndpoint == "" || created.RuntimeShape != "deployment" {
		t.Fatalf("created = %+v", created)
	}
	if !strings.Contains(created.InternalEndpoint, "inference-cpu-example.ani-tenant-"+tenant+".svc:8000") {
		t.Fatalf("endpoint = %q", created.InternalEndpoint)
	}
	if len(provider.applies) != 1 {
		t.Fatalf("applies = %d, want 1", len(provider.applies))
	}
	replay, err := svc.Create(ctx, tenant, spec)
	if err != nil || replay.ID != created.ID || len(provider.applies) != 1 {
		t.Fatalf("idempotent Create() = %+v applies=%d err=%v", replay, len(provider.applies), err)
	}

	got, err := svc.Get(ctx, tenant, created.ID)
	if err != nil || got.ID != created.ID || got.State != ports.PlatformWorkloadRunning {
		t.Fatalf("Get() = %+v, %v", got, err)
	}
	if _, err := svc.Get(ctx, "22222222-2222-2222-2222-222222222222", created.ID); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("cross-tenant Get() error = %v", err)
	}

	scaled, err := svc.UpdateReplicas(ctx, tenant, created.ID, "8df72d71-9d49-46c4-a48a-52bb37b082ab", 2)
	if err != nil || scaled.DesiredReplicas != 2 || len(provider.applies) != 2 {
		t.Fatalf("scale = %+v applies=%d err=%v", scaled, len(provider.applies), err)
	}

	stopped, err := svc.ApplyLifecycle(ctx, tenant, created.ID, "2df72d71-9d49-46c4-a48a-52bb37b082ab", "stop")
	if err != nil || stopped.State != ports.PlatformWorkloadStopped || stopped.InternalEndpoint != "" || provider.deletes != 1 {
		t.Fatalf("stop = %+v deletes=%d err=%v", stopped, provider.deletes, err)
	}
	still, err := svc.Get(ctx, tenant, created.ID)
	if err != nil || still.State != ports.PlatformWorkloadStopped || still.InternalEndpoint != "" {
		t.Fatalf("Get after stop = %+v, %v", still, err)
	}

	started, err := svc.ApplyLifecycle(ctx, tenant, created.ID, "3df72d71-9d49-46c4-a48a-52bb37b082ab", "start")
	if err != nil || started.State != ports.PlatformWorkloadRunning || started.InternalEndpoint == "" || len(provider.applies) != 3 {
		t.Fatalf("start = %+v applies=%d err=%v", started, len(provider.applies), err)
	}

	if _, err := svc.Delete(ctx, tenant, created.ID, "4df72d71-9d49-46c4-a48a-52bb37b082ab"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if provider.deletes != 2 {
		t.Fatalf("deletes = %d, want 2", provider.deletes)
	}
	if _, err := svc.Get(ctx, tenant, created.ID); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("Get after delete error = %v", err)
	}
}

func TestKubernetesPlatformWorkloadUsesUUIDNameAsStableID(t *testing.T) {
	svc := NewKubernetesPlatformWorkloadService(newReadyFakePlatformWorkloadRuntime())
	name := "9df72d71-9d49-46c4-a48a-52bb37b082ab"
	spec := sampleCPUPlatformWorkloadSpec("1df72d71-9d49-46c4-a48a-52bb37b082ab", name)
	created, err := svc.Create(context.Background(), "11111111-1111-1111-1111-111111111111", spec)
	if err != nil || created.ID != name {
		t.Fatalf("Create() = %+v, %v, want id %s", created, err, name)
	}
}

func TestKubernetesPlatformWorkloadCreateAppliesThenObservesNotImmediatelyLocalRunning(t *testing.T) {
	provider := &fakePlatformWorkloadRuntime{}
	svc := NewKubernetesPlatformWorkloadService(provider)
	spec := sampleCPUPlatformWorkloadSpec("1df72d71-9d49-46c4-a48a-52bb37b082ab", "inference-cpu-pending")
	created, err := svc.Create(context.Background(), "11111111-1111-1111-1111-111111111111", spec)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.State != ports.PlatformWorkloadProvisioning || created.ReadyReplicas != 0 {
		t.Fatalf("created = %+v, want provisioning until Observe reports ready", created)
	}
	if len(provider.applies) != 1 {
		t.Fatalf("applies = %d, want provider.Apply", len(provider.applies))
	}

	provider.ready = true
	got, err := svc.Get(context.Background(), created.TenantID, created.ID)
	if err != nil || got.State != ports.PlatformWorkloadRunning || got.ReadyReplicas != 1 {
		t.Fatalf("Get after ready observe = %+v, %v", got, err)
	}
}

func TestKubernetesPlatformWorkloadApplyFailureDoesNotReserveName(t *testing.T) {
	provider := &fakePlatformWorkloadRuntime{applyErr: ports.ErrUnavailable}
	svc := NewKubernetesPlatformWorkloadService(provider)
	ctx := context.Background()
	tenant := "11111111-1111-1111-1111-111111111111"
	spec := sampleCPUPlatformWorkloadSpec("1df72d71-9d49-46c4-a48a-52bb37b082ab", "inference-cpu-fail")
	if _, err := svc.Create(ctx, tenant, spec); !errors.Is(err, ports.ErrUnavailable) {
		t.Fatalf("Create() error = %v, want unavailable", err)
	}
	provider.applyErr = nil
	provider.ready = true
	created, err := svc.Create(ctx, tenant, spec)
	if err != nil || created.Name != spec.Name {
		t.Fatalf("retry Create() = %+v, %v", created, err)
	}
}

func TestKubernetesPlatformWorkloadAcceptsAcceleratorAndRejectsLeaderWorker(t *testing.T) {
	svc := NewKubernetesPlatformWorkloadService(newReadyFakePlatformWorkloadRuntime())
	ctx := context.Background()
	tenant := "11111111-1111-1111-1111-111111111111"

	gpu := sampleCPUPlatformWorkloadSpec("5df72d71-9d49-46c4-a48a-52bb37b082ab", "inference-gpu")
	gpu.Resources.AcceleratorSpecID = "gpu-a100"
	gpu.Resources.AcceleratorCount = 1
	if _, err := svc.Create(ctx, tenant, gpu); !errors.Is(err, ports.ErrFailedPrecondition) {
		t.Fatalf("unavailable accelerator Create() error = %v", err)
	}

	incomplete := sampleCPUPlatformWorkloadSpec("7df72d71-9d49-46c4-a48a-52bb37b082ab", "inference-gpu-bad")
	incomplete.Resources.AcceleratorCount = 1
	if _, err := svc.Create(ctx, tenant, incomplete); !errors.Is(err, ports.ErrInvalid) {
		t.Fatalf("incomplete accelerator Create() error = %v", err)
	}

	lws := sampleCPUPlatformWorkloadSpec("6df72d71-9d49-46c4-a48a-52bb37b082ab", "inference-lws")
	lws.Topology.Mode = "leader_worker"
	lws.Topology.HasLeader = true
	lws.Scheduling.Gang = true
	if _, err := svc.Create(ctx, tenant, lws); !errors.Is(err, ports.ErrFailedPrecondition) {
		t.Fatalf("leader_worker Create() error = %v", err)
	}
}

func TestKubernetesPlatformWorkloadSurvivesServiceRestartWithSharedStore(t *testing.T) {
	store := newMemoryPlatformWorkloadStore()
	provider := newReadyFakePlatformWorkloadRuntime()
	ctx := context.Background()
	tenant := "11111111-1111-1111-1111-111111111111"
	spec := sampleCPUPlatformWorkloadSpec("1df72d71-9d49-46c4-a48a-52bb37b082ab", "inference-cpu-restart")

	created, err := NewKubernetesPlatformWorkloadServiceWithStore(provider, store).Create(ctx, tenant, spec)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	restarted := NewKubernetesPlatformWorkloadServiceWithStore(provider, store)
	got, err := restarted.Get(ctx, tenant, created.ID)
	if err != nil || got.ID != created.ID || got.Name != spec.Name {
		t.Fatalf("Get after restart = %+v, %v", got, err)
	}
}

func TestKubernetesPlatformWorkloadRejectsTagImage(t *testing.T) {
	svc := NewKubernetesPlatformWorkloadService(newReadyFakePlatformWorkloadRuntime())
	spec := sampleCPUPlatformWorkloadSpec("7df72d71-9d49-46c4-a48a-52bb37b082ab", "inference-latest")
	spec.ImageRef = "registry.ani.internal/platform/runtime:latest"
	if _, err := svc.Create(context.Background(), "11111111-1111-1111-1111-111111111111", spec); !errors.Is(err, ports.ErrInvalid) {
		t.Fatalf("tag image Create() error = %v", err)
	}
}

func TestPlatformWorkloadResourceNamePrefixesNumericDNS1035(t *testing.T) {
	if got := platformWorkloadResourceName("2ad7be41-d22a-46c9-ab22-27dbea961c66"); got != "pw-2ad7be41-d22a-46c9-ab22-27dbea961c66" {
		t.Fatalf("numeric name = %q", got)
	}
	if got := platformWorkloadResourceName("a25ac5a3-4ea4-4455-87c8-c6b10712773e"); got != "a25ac5a3-4ea4-4455-87c8-c6b10712773e" {
		t.Fatalf("alpha name = %q", got)
	}
	spec := sampleCPUPlatformWorkloadSpec("1df72d71-9d49-46c4-a48a-52bb37b082ab", "2ad7be41-d22a-46c9-ab22-27dbea961c66")
	manifests := renderPlatformWorkloadManifests("11111111-1111-1111-1111-111111111111", "workload-id-1", spec, nil)
	var service map[string]any
	if err := json.Unmarshal([]byte(manifests[1].Content), &service); err != nil {
		t.Fatalf("service json: %v", err)
	}
	name, _ := service["metadata"].(map[string]any)["name"].(string)
	if name != "pw-2ad7be41-d22a-46c9-ab22-27dbea961c66" {
		t.Fatalf("service metadata.name = %q", name)
	}
	endpoint := platformWorkloadEndpoint("11111111-1111-1111-1111-111111111111", spec)
	if !strings.Contains(endpoint, "pw-2ad7be41-d22a-46c9-ab22-27dbea961c66.") {
		t.Fatalf("endpoint = %q", endpoint)
	}
}

func TestRenderPlatformWorkloadManifestsUsesClusterIPAndInferenceLabels(t *testing.T) {
	tenant := "11111111-1111-1111-1111-111111111111"
	spec := sampleCPUPlatformWorkloadSpec("1df72d71-9d49-46c4-a48a-52bb37b082ab", "inference-cpu-example")
	manifests := renderPlatformWorkloadManifests(tenant, "workload-id-1", spec, nil)
	if len(manifests) != 3 || manifests[0].Kind != "Deployment" || manifests[1].Kind != "Service" || manifests[2].Kind != "NetworkPolicy" {
		t.Fatalf("manifests = %+v", manifests)
	}

	var deployment map[string]any
	if err := json.Unmarshal([]byte(manifests[0].Content), &deployment); err != nil {
		t.Fatalf("deployment json: %v", err)
	}
	var service map[string]any
	if err := json.Unmarshal([]byte(manifests[1].Content), &service); err != nil {
		t.Fatalf("service json: %v", err)
	}
	labels, _ := deployment["metadata"].(map[string]any)["labels"].(map[string]any)
	if labels[platformWorkloadClassLabel] != "inference" || labels[platformWorkloadTenantLabel] != tenant {
		t.Fatalf("labels = %#v", labels)
	}
	if labels[platformWorkloadIDLabel] != "workload-id-1" || labels[platformWorkloadOwnerLabel] != spec.Metadata.OwnerRef {
		t.Fatalf("owner/id labels = %#v", labels)
	}
	if _, ok := labels["ani.kubercloud.io/instance"]; ok {
		t.Fatalf("rendered instance identity label: %#v", labels)
	}
	specMap, _ := service["spec"].(map[string]any)
	if specMap["type"] != "ClusterIP" {
		t.Fatalf("service spec = %#v", specMap)
	}
	container, _ := deployment["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)["containers"].([]any)[0].(map[string]any)
	image, _ := container["image"].(string)
	if !strings.Contains(image, "@sha256:") {
		t.Fatalf("image = %q, want digest-pinned", image)
	}
	resources, _ := container["resources"].(map[string]any)["requests"].(map[string]any)
	if _, ok := resources["nvidia.com/gpu"]; ok {
		t.Fatalf("CPU manifest requested GPU: %#v", resources)
	}
}

func TestRenderPlatformWorkloadNetworkPolicyDeniesExternalAndForeignNamespace(t *testing.T) {
	tenant := "11111111-1111-1111-1111-111111111111"
	spec := sampleCPUPlatformWorkloadSpec("1df72d71-9d49-46c4-a48a-52bb37b082ab", "inference-cpu-example")
	manifests := renderPlatformWorkloadManifests(tenant, "workload-id-1", spec, nil)
	if len(manifests) != 3 || manifests[2].Kind != "NetworkPolicy" {
		t.Fatalf("manifests = %+v", manifests)
	}
	var policy map[string]any
	if err := json.Unmarshal([]byte(manifests[2].Content), &policy); err != nil {
		t.Fatalf("networkpolicy json: %v", err)
	}
	if policy["apiVersion"] != "networking.k8s.io/v1" {
		t.Fatalf("apiVersion = %#v", policy["apiVersion"])
	}
	content := manifests[2].Content
	if strings.Contains(content, "0.0.0.0/0") {
		t.Fatalf("network policy opened a public ingress:\n%s", content)
	}
	if strings.Contains(content, "ipBlock") {
		t.Fatalf("network policy included node ipBlock without node CIDRs:\n%s", content)
	}
	if strings.Contains(content, "NodePort") || strings.Contains(content, "LoadBalancer") {
		t.Fatalf("network policy leaked a public service type:\n%s", content)
	}
	specMap, _ := policy["spec"].(map[string]any)
	types, _ := specMap["policyTypes"].([]any)
	if len(types) != 1 || types[0] != "Ingress" {
		t.Fatalf("policyTypes = %#v", types)
	}
	if !strings.Contains(content, "kube-system") || !strings.Contains(content, "ani-system") {
		t.Fatalf("network policy missing control-plane allow list:\n%s", content)
	}
	if !strings.Contains(content, `"podSelector": {}`) {
		t.Fatalf("network policy missing same-namespace allow:\n%s", content)
	}
}

func TestNodeInternalCIDRsFromListIncludesOVNAnnotation(t *testing.T) {
	got, err := nodeInternalCIDRsFromList([]byte(`{"items":[{"metadata":{"annotations":{"ovn.kubernetes.io/ip_address":"192.0.2.20/16"}},"status":{"addresses":[{"type":"InternalIP","address":"192.0.2.10"},{"type":"Hostname","address":"node-a"}]}}]}`))
	if err != nil {
		t.Fatalf("nodeInternalCIDRsFromList() error = %v", err)
	}
	if len(got) != 2 || got[0] != "192.0.2.10/32" || got[1] != "192.0.2.20/32" {
		t.Fatalf("cidrs = %#v", got)
	}
}

func TestRenderPlatformWorkloadNetworkPolicyAllowsNodeInternalIPBlocks(t *testing.T) {
	spec := sampleCPUPlatformWorkloadSpec("1df72d71-9d49-46c4-a48a-52bb37b082ab", "inference-cpu-example")
	manifests := renderPlatformWorkloadManifests("11111111-1111-1111-1111-111111111111", "workload-id-1", spec, []string{"192.0.2.10", "10.0.0.0/8", "2001:db8::1"})
	content := manifests[2].Content
	if !strings.Contains(content, `"cidr": "192.0.2.10/32"`) {
		t.Fatalf("missing node /32 ipBlock:\n%s", content)
	}
	if strings.Contains(content, "0.0.0.0/0") || strings.Contains(content, "10.0.0.0/8") || strings.Contains(content, "2001:db8") {
		t.Fatalf("network policy accepted a non-node cidr:\n%s", content)
	}
}

func TestRenderPlatformWorkloadManifestsRequestsGPUForAccelerator(t *testing.T) {
	spec := sampleCPUPlatformWorkloadSpec("5df72d71-9d49-46c4-a48a-52bb37b082ab", "inference-gpu-example")
	spec.Resources.AcceleratorSpecID = "gpu-a100"
	spec.Resources.AcceleratorCount = 2
	manifests := renderPlatformWorkloadManifests("11111111-1111-1111-1111-111111111111", "workload-gpu-1", spec, nil)
	var deployment map[string]any
	if err := json.Unmarshal([]byte(manifests[0].Content), &deployment); err != nil {
		t.Fatalf("deployment json: %v", err)
	}
	container, _ := deployment["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)["containers"].([]any)[0].(map[string]any)
	resources, _ := container["resources"].(map[string]any)["requests"].(map[string]any)
	if resources["nvidia.com/gpu"] != "2" {
		t.Fatalf("gpu request = %#v", resources)
	}
	labels, _ := deployment["metadata"].(map[string]any)["labels"].(map[string]any)
	if labels["ani.kubercloud.io/accelerator-spec-id"] != "gpu-a100" {
		t.Fatalf("labels = %#v", labels)
	}
	if _, ok := labels["ani.kubercloud.io/instance"]; ok {
		t.Fatalf("rendered instance identity label: %#v", labels)
	}
}

func TestRenderPlatformWorkloadManifestsMountsPVCArtifact(t *testing.T) {
	spec := sampleCPUPlatformWorkloadSpec("8df72d71-9d49-46c4-a48a-52bb37b082ab", "inference-cpu-model")
	spec.Artifacts = []ports.PlatformWorkloadArtifact{{ObjectRef: "pvc://vllm-model", MountPath: "/models"}}
	manifests := renderPlatformWorkloadManifests("11111111-1111-1111-1111-111111111111", "workload-model-1", spec, nil)
	var deployment map[string]any
	if err := json.Unmarshal([]byte(manifests[0].Content), &deployment); err != nil {
		t.Fatalf("deployment json: %v", err)
	}
	podSpec, _ := deployment["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
	volumes, _ := podSpec["volumes"].([]any)
	if len(volumes) < 2 {
		t.Fatalf("volumes = %#v", volumes)
	}
	container, _ := podSpec["containers"].([]any)[0].(map[string]any)
	mounts, _ := container["volumeMounts"].([]any)
	found := false
	for _, raw := range mounts {
		mount, _ := raw.(map[string]any)
		if mount["mountPath"] == "/models" {
			found = true
		}
	}
	if !found {
		t.Fatalf("volumeMounts = %#v", mounts)
	}
	if _, ok := container["livenessProbe"]; ok {
		t.Fatalf("livenessProbe must be omitted for long model loads: %#v", container["livenessProbe"])
	}
	probe, _ := container["readinessProbe"].(map[string]any)
	if probe["failureThreshold"] != float64(90) && probe["failureThreshold"] != 90 {
		t.Fatalf("readinessProbe = %#v", probe)
	}
}

func TestKubernetesPlatformWorkloadRuntimeApplyObserveDelete(t *testing.T) {
	var methods []string
	var paths []string
	client := newTestKubernetesRESTClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		methods = append(methods, r.Method)
		paths = append(paths, r.URL.Path)
		switch {
		case r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "/namespaces/") && !strings.Contains(r.URL.Path, "/deployments/") && !strings.Contains(r.URL.Path, "/services/") && !strings.Contains(r.URL.Path, "/networkpolicies/"):
			return jsonResponse(http.StatusOK, `{"kind":"Namespace"}`), nil
		case r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "/deployments/"):
			return jsonResponse(http.StatusOK, `{"kind":"Deployment"}`), nil
		case r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "/services/"):
			return jsonResponse(http.StatusOK, `{"kind":"Service"}`), nil
		case r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "/networkpolicies/"):
			return jsonResponse(http.StatusOK, `{"kind":"NetworkPolicy"}`), nil
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/api/v1/nodes"):
			return jsonResponse(http.StatusOK, `{"items":[{"status":{"addresses":[{"type":"InternalIP","address":"192.0.2.10"}]}}]}`), nil
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/deployments/"):
			return jsonResponse(http.StatusOK, `{"status":{"readyReplicas":1}}`), nil
		case r.Method == http.MethodDelete:
			return jsonResponse(http.StatusOK, `{"kind":"Status","status":"Success"}`), nil
		default:
			return jsonResponse(http.StatusNotFound, `{"kind":"Status","status":"Failure"}`), nil
		}
	}))
	runtime := NewKubernetesPlatformWorkloadRuntime(client)
	ctx := context.Background()
	tenant := "11111111-1111-1111-1111-111111111111"
	spec := sampleCPUPlatformWorkloadSpec("1df72d71-9d49-46c4-a48a-52bb37b082ab", "inference-cpu-example")

	if _, err := runtime.Apply(ctx, tenant, "workload-id-1", spec); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	obs, err := runtime.Observe(ctx, tenant, "workload-id-1", spec)
	if err != nil || !obs.Ready || obs.ReadyReplicas != 1 {
		t.Fatalf("Observe() = %+v, %v", obs, err)
	}
	if err := runtime.Delete(ctx, tenant, "workload-id-1", spec); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	joined := strings.Join(paths, " ")
	if !strings.Contains(joined, "/api/v1/namespaces/ani-tenant-"+tenant) {
		t.Fatalf("paths = %v, want tenant namespace apply", paths)
	}
	if !strings.Contains(joined, "/apis/apps/v1/namespaces/ani-tenant-"+tenant+"/deployments/inference-cpu-example") {
		t.Fatalf("paths = %v, want deployment path", paths)
	}
	if !strings.Contains(joined, "/api/v1/namespaces/ani-tenant-"+tenant+"/services/inference-cpu-example") {
		t.Fatalf("paths = %v, want service path", paths)
	}
	if !strings.Contains(joined, "/apis/networking.k8s.io/v1/namespaces/ani-tenant-"+tenant+"/networkpolicies/inference-cpu-example") {
		t.Fatalf("paths = %v, want networkpolicy path", paths)
	}
	if strings.Count(strings.Join(methods, ","), http.MethodDelete) < 3 {
		t.Fatalf("methods = %v, want service, networkpolicy, and deployment deletes", methods)
	}
}

func TestKubernetesPlatformWorkloadLogsReadsPodLinesAndRedactsSecrets(t *testing.T) {
	var paths []string
	var queries []string
	client := newTestKubernetesRESTClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.URL.Path)
		queries = append(queries, r.URL.RawQuery)
		switch {
		case strings.Contains(r.URL.Path, "/pods") && !strings.HasSuffix(r.URL.Path, "/log"):
			return jsonResponse(http.StatusOK, `{"items":[{"metadata":{"name":"pw-pod-1"},"spec":{"containers":[{"name":"inference-cpu-example"}]}}]}`), nil
		case strings.HasSuffix(r.URL.Path, "/log"):
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("2026-08-15T07:00:00.000000000Z vllm worker ready\n2026-08-15T07:00:01.000000000Z Authorization: Bearer secret\n")), Header: make(http.Header)}, nil
		default:
			return jsonResponse(http.StatusNotFound, `{"kind":"Status","status":"Failure"}`), nil
		}
	}))
	runtime := NewKubernetesPlatformWorkloadRuntime(client)
	name := "1df72d71-9d49-46c4-a48a-52bb37b082ab"
	page, err := runtime.Logs(context.Background(), "11111111-1111-1111-1111-111111111111", "workload-id-1", sampleCPUPlatformWorkloadSpec(name, name), 20, "", "")
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("Logs() = %+v, %v", page, err)
	}
	if page.Items[0].Message != "vllm worker ready" || page.Items[0].Container != "inference-cpu-example" {
		t.Fatalf("first log = %+v", page.Items[0])
	}
	if page.Items[1].Message != "[redacted]" {
		t.Fatalf("secret log = %+v", page.Items[1])
	}
	joined := strings.Join(paths, " ")
	if !strings.Contains(joined, "/pods") || !strings.Contains(joined, "/log") {
		t.Fatalf("paths = %v", paths)
	}
	if !strings.Contains(strings.Join(queries, " "), name) || strings.Contains(strings.Join(queries, " "), "pw-"+name) {
		t.Fatalf("pod selector used resource name instead of spec name: %v", queries)
	}
}

func TestKubernetesPlatformWorkloadLogsSkipsPendingPod(t *testing.T) {
	client := newTestKubernetesRESTClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.Contains(r.URL.Path, "/pods") && !strings.HasSuffix(r.URL.Path, "/log"):
			return jsonResponse(http.StatusOK, `{"items":[{"metadata":{"name":"ready-pod"},"spec":{"containers":[{"name":"pw"}]}},{"metadata":{"name":"pending-pod"},"spec":{"containers":[{"name":"pw"}]}}]}`), nil
		case strings.HasSuffix(r.URL.Path, "/ready-pod/log"):
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("2026-08-15T07:00:00.000000000Z vllm worker ready\n")), Header: make(http.Header)}, nil
		case strings.HasSuffix(r.URL.Path, "/pending-pod/log"):
			return jsonResponse(http.StatusBadRequest, `{"kind":"Status","status":"Failure","message":"container is waiting to start","code":400}`), nil
		default:
			return jsonResponse(http.StatusNotFound, `{"kind":"Status","status":"Failure"}`), nil
		}
	}))
	page, err := NewKubernetesPlatformWorkloadRuntime(client).Logs(
		context.Background(),
		"11111111-1111-1111-1111-111111111111",
		"workload-id-1",
		sampleCPUPlatformWorkloadSpec("1df72d71-9d49-46c4-a48a-52bb37b082ab", "1df72d71-9d49-46c4-a48a-52bb37b082ab"),
		20, "", "",
	)
	if err != nil || len(page.Items) != 1 || page.Items[0].Message != "vllm worker ready" {
		t.Fatalf("Logs() = %+v, %v", page, err)
	}
}

func TestKubernetesPlatformWorkloadServiceLogsAfterCreate(t *testing.T) {
	svc := NewKubernetesPlatformWorkloadService(newReadyFakePlatformWorkloadRuntime())
	tenant := "11111111-1111-1111-1111-111111111111"
	created, err := svc.Create(context.Background(), tenant, sampleCPUPlatformWorkloadSpec("1df72d71-9d49-46c4-a48a-52bb37b082ab", "inference-cpu-example"))
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	page, err := svc.Logs(context.Background(), tenant, created.ID, 10, "", "")
	if err != nil || len(page.Items) != 1 || page.Items[0].Message != "vllm worker ready" {
		t.Fatalf("Logs() = %+v, %v", page, err)
	}
}
