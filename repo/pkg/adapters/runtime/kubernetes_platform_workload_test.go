package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

type fakePlatformWorkloadRuntime struct {
	mu        sync.Mutex
	applies   []ports.PlatformWorkloadCreateSpec
	deletes   int
	applyErr  error
	deleteErr error
	ready     bool
	missing   bool
	caps      *ports.PlatformWorkloadCapabilities
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
	if f.deleteErr != nil {
		return f.deleteErr
	}
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

func (f *fakePlatformWorkloadRuntime) DiscoverCapabilities(context.Context) (ports.PlatformWorkloadCapabilities, error) {
	if f.caps != nil {
		return *f.caps, nil
	}
	return defaultPlatformWorkloadCapabilities(), nil
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
	if provider.deletes != 1 {
		t.Fatalf("failed Create() deletes = %d, want compensation delete", provider.deletes)
	}
	provider.applyErr = nil
	provider.ready = true
	created, err := svc.Create(ctx, tenant, spec)
	if err != nil || created.Name != spec.Name {
		t.Fatalf("retry Create() = %+v, %v", created, err)
	}
}

func TestKubernetesPlatformWorkloadPendingScaleRetriesProvider(t *testing.T) {
	provider := newReadyFakePlatformWorkloadRuntime()
	svc := NewKubernetesPlatformWorkloadService(provider)
	ctx := context.Background()
	tenant := "11111111-1111-1111-1111-111111111111"
	spec := sampleCPUPlatformWorkloadSpec("1df72d71-9d49-46c4-a48a-52bb37b082ab", "inference-cpu-scale-retry")
	created, err := svc.Create(ctx, tenant, spec)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	provider.applyErr = ports.ErrUnavailable
	scaleKey := "8df72d71-9d49-46c4-a48a-52bb37b082ab"
	if _, err := svc.UpdateReplicas(ctx, tenant, created.ID, scaleKey, 2); !errors.Is(err, ports.ErrUnavailable) {
		t.Fatalf("scale error = %v, want unavailable", err)
	}
	if len(provider.applies) != 2 {
		t.Fatalf("applies after failed scale = %d, want 2", len(provider.applies))
	}
	provider.applyErr = nil
	scaled, err := svc.UpdateReplicas(ctx, tenant, created.ID, scaleKey, 2)
	if err != nil || scaled.DesiredReplicas != 2 || scaled.Generation != 2 {
		t.Fatalf("retry scale = %+v err=%v", scaled, err)
	}
	if len(provider.applies) != 3 {
		t.Fatalf("applies after retry scale = %d, want 3", len(provider.applies))
	}
	replay, err := svc.UpdateReplicas(ctx, tenant, created.ID, scaleKey, 2)
	if err != nil || replay.ID != scaled.ID || len(provider.applies) != 3 {
		t.Fatalf("succeeded scale replay = %+v applies=%d err=%v", replay, len(provider.applies), err)
	}
}

func TestKubernetesPlatformWorkloadPendingLifecycleAndDeleteRetryProvider(t *testing.T) {
	provider := newReadyFakePlatformWorkloadRuntime()
	svc := NewKubernetesPlatformWorkloadService(provider)
	ctx := context.Background()
	tenant := "11111111-1111-1111-1111-111111111111"
	spec := sampleCPUPlatformWorkloadSpec("1df72d71-9d49-46c4-a48a-52bb37b082ab", "inference-cpu-lifecycle-retry")
	created, err := svc.Create(ctx, tenant, spec)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	provider.deleteErr = ports.ErrUnavailable
	stopKey := "2df72d71-9d49-46c4-a48a-52bb37b082ab"
	if _, err := svc.ApplyLifecycle(ctx, tenant, created.ID, stopKey, "stop"); !errors.Is(err, ports.ErrUnavailable) {
		t.Fatalf("stop error = %v, want unavailable", err)
	}
	if provider.deletes != 1 {
		t.Fatalf("deletes after failed stop = %d, want 1", provider.deletes)
	}
	provider.deleteErr = nil
	stopped, err := svc.ApplyLifecycle(ctx, tenant, created.ID, stopKey, "stop")
	if err != nil || stopped.State != ports.PlatformWorkloadStopped || provider.deletes != 2 {
		t.Fatalf("retry stop = %+v deletes=%d err=%v", stopped, provider.deletes, err)
	}
	replayStop, err := svc.ApplyLifecycle(ctx, tenant, created.ID, stopKey, "stop")
	if err != nil || replayStop.State != ports.PlatformWorkloadStopped || provider.deletes != 2 {
		t.Fatalf("succeeded stop replay = %+v deletes=%d err=%v", replayStop, provider.deletes, err)
	}

	started, err := svc.ApplyLifecycle(ctx, tenant, created.ID, "3df72d71-9d49-46c4-a48a-52bb37b082ab", "start")
	if err != nil || started.State != ports.PlatformWorkloadRunning {
		t.Fatalf("start = %+v err=%v", started, err)
	}
	provider.deleteErr = ports.ErrUnavailable
	deleteKey := "4df72d71-9d49-46c4-a48a-52bb37b082ab"
	if _, err := svc.Delete(ctx, tenant, created.ID, deleteKey); !errors.Is(err, ports.ErrUnavailable) {
		t.Fatalf("delete error = %v, want unavailable", err)
	}
	provider.deleteErr = nil
	if _, err := svc.Delete(ctx, tenant, created.ID, deleteKey); err != nil {
		t.Fatalf("retry Delete() error = %v", err)
	}
	if provider.deletes != 4 {
		t.Fatalf("deletes = %d, want 4 (failed stop, retry stop, failed delete, retry delete)", provider.deletes)
	}
}

func TestKubernetesPlatformWorkloadCreateKeepsTombstoneWhenCompensateFails(t *testing.T) {
	provider := &fakePlatformWorkloadRuntime{applyErr: ports.ErrUnavailable, deleteErr: ports.ErrUnavailable}
	svc := NewKubernetesPlatformWorkloadService(provider)
	ctx := context.Background()
	tenant := "11111111-1111-1111-1111-111111111111"
	spec := sampleCPUPlatformWorkloadSpec("1df72d71-9d49-46c4-a48a-52bb37b082ab", "inference-cpu-tombstone")
	if _, err := svc.Create(ctx, tenant, spec); !errors.Is(err, ports.ErrUnavailable) {
		t.Fatalf("Create() error = %v, want unavailable", err)
	}
	other := spec
	other.IdempotencyKey = "9df72d71-9d49-46c4-a48a-52bb37b082ab"
	if _, err := svc.Create(ctx, tenant, other); !errors.Is(err, ports.ErrConflict) {
		t.Fatalf("other-key Create() error = %v, want name conflict tombstone", err)
	}
	provider.applyErr = nil
	provider.deleteErr = nil
	provider.ready = true
	created, err := svc.Create(ctx, tenant, spec)
	if err != nil || created.Name != spec.Name {
		t.Fatalf("retry Create() = %+v, %v", created, err)
	}
	if len(provider.applies) != 2 {
		t.Fatalf("applies = %d, want pending create retry", len(provider.applies))
	}
}

func TestKubernetesPlatformWorkloadPendingCreateRetryDoesNotDeleteLiveRuntime(t *testing.T) {
	provider := newReadyFakePlatformWorkloadRuntime()
	store := newMemoryPlatformWorkloadStore()
	svc := NewKubernetesPlatformWorkloadServiceWithStore(provider, store)
	ctx := context.Background()
	tenant := "11111111-1111-1111-1111-111111111111"
	spec := sampleCPUPlatformWorkloadSpec("1df72d71-9d49-46c4-a48a-52bb37b082ab", "inference-cpu-pending-live")
	created, err := svc.Create(ctx, tenant, spec)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	fingerprint, err := platformWorkloadFingerprint("create", spec)
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	if err := store.putIntent(tenant, spec.IdempotencyKey, pendingIntent(fingerprint, created.ID)); err != nil {
		t.Fatalf("putIntent() error = %v", err)
	}
	provider.applyErr = ports.ErrUnavailable
	if _, err := svc.Create(ctx, tenant, spec); !errors.Is(err, ports.ErrUnavailable) {
		t.Fatalf("pending retry error = %v, want unavailable", err)
	}
	if provider.deletes != 0 {
		t.Fatalf("pending create retry deleted live runtime: deletes=%d", provider.deletes)
	}
	got, err := svc.Get(ctx, tenant, created.ID)
	if err != nil || got.ID != created.ID {
		t.Fatalf("Get after pending retry = %+v, %v", got, err)
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

	lws := sampleLeaderWorkerPlatformWorkloadSpec("6df72d71-9d49-46c4-a48a-52bb37b082ab", "inference-lws")
	if _, err := svc.Create(ctx, tenant, lws); !errors.Is(err, ports.ErrFailedPrecondition) {
		t.Fatalf("leader_worker Create() error = %v", err)
	}
}

func TestKubernetesPlatformWorkloadAcceptsAdvertisedGPUAndLeaderWorker(t *testing.T) {
	provider := newReadyFakePlatformWorkloadRuntime()
	provider.caps = &ports.PlatformWorkloadCapabilities{
		SupportedTopologyModes: []string{"single_node", "leader_worker"},
		LeaderWorkerSetReady:   true,
		GangSchedulingReady:    true,
		SupportedProfiles: []ports.PlatformWorkloadTopologyProfile{
			{ID: "container-single-node", Version: "v1", Mode: "single_node"},
			{ID: "container-leader-worker", Version: "v1", Mode: "leader_worker"},
		},
		AcceleratorSpecs: []ports.PlatformWorkloadAcceleratorCapability{{
			SpecID: "gpu-a100", Available: true, MaxSingleNodeCount: 1, MaxWholeCardCount: 1,
		}},
	}
	svc := NewKubernetesPlatformWorkloadService(provider)
	ctx := context.Background()
	tenant := "11111111-1111-1111-1111-111111111111"

	gpu := sampleCPUPlatformWorkloadSpec("5df72d71-9d49-46c4-a48a-52bb37b082ab", "inference-gpu")
	gpu.Resources.AcceleratorSpecID = "gpu-a100-full"
	gpu.Resources.AcceleratorCount = 1
	created, err := svc.Create(ctx, tenant, gpu)
	if err != nil || created.RuntimeShape != "deployment" {
		t.Fatalf("advertised GPU Create() = %+v, %v", created, err)
	}

	lws := sampleLeaderWorkerPlatformWorkloadSpec("6df72d71-9d49-46c4-a48a-52bb37b082ab", "inference-lws")
	createdLWS, err := svc.Create(ctx, tenant, lws)
	if err != nil || createdLWS.RuntimeShape != "leader_worker_set" {
		t.Fatalf("advertised LWS Create() = %+v, %v", createdLWS, err)
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
	podSpec, _ := deployment["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
	if _, ok := podSpec["schedulerName"]; ok {
		t.Fatalf("CPU manifest set schedulerName: %#v", podSpec)
	}
	if strings.Contains(manifests[0].Content, "volcano") {
		t.Fatalf("CPU manifest referenced volcano:\n%s", manifests[0].Content)
	}
	strategy, _ := deployment["spec"].(map[string]any)["strategy"].(map[string]any)
	if strategy["type"] != "Recreate" {
		t.Fatalf("CPU strategy = %#v", strategy)
	}
	if !strings.Contains(manifests[0].Content, `"sizeLimit": "1Gi"`) {
		t.Fatalf("CPU shm should stay 1Gi:\n%s", manifests[0].Content)
	}
	annotations, _ := deployment["spec"].(map[string]any)["template"].(map[string]any)["metadata"].(map[string]any)["annotations"].(map[string]any)
	if annotations["ovn.kubernetes.io/logical_switch"] != kubeOVNDefaultLogicalSwitch {
		t.Fatalf("CPU overlay switch = %#v", annotations)
	}
	if annotations["ovn.kubernetes.io/vpc"] != kubeOVNDefaultVPC {
		t.Fatalf("CPU overlay vpc = %#v", annotations)
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

func TestRenderPlatformWorkloadManifestsRequestsVolcanoVGPUWhenMemorySet(t *testing.T) {
	spec := sampleCPUPlatformWorkloadSpec("5df72d71-9d49-46c4-a48a-52bb37b082ab", "inference-vgpu-example")
	spec.Resources.AcceleratorSpecID = "gpu-nvidia-geforce-rtx-4090"
	spec.Resources.AcceleratorCount = 2
	spec.Resources.AcceleratorMemoryMB = 10240
	manifests := renderPlatformWorkloadManifests("11111111-1111-1111-1111-111111111111", "workload-vgpu-1", spec, nil)
	var deployment map[string]any
	if err := json.Unmarshal([]byte(manifests[0].Content), &deployment); err != nil {
		t.Fatalf("deployment json: %v", err)
	}
	container, _ := deployment["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)["containers"].([]any)[0].(map[string]any)
	resources, _ := container["resources"].(map[string]any)["requests"].(map[string]any)
	if resources["volcano.sh/vgpu-number"] != "2" || resources["volcano.sh/vgpu-memory"] != "1024" {
		t.Fatalf("vgpu request = %#v", resources)
	}
	if _, ok := resources["nvidia.com/gpu"]; ok {
		t.Fatalf("vGPU spec must not request nvidia.com/gpu: %#v", resources)
	}
}

func TestRenderPlatformWorkloadManifestsRequestsWholeCardWhenMemoryOmitted(t *testing.T) {
	spec := sampleCPUPlatformWorkloadSpec("5df72d71-9d49-46c4-a48a-52bb37b082ab", "inference-gpu-example")
	spec.Resources.AcceleratorSpecID = "gpu-nvidia-geforce-rtx-4090-8x"
	spec.Resources.AcceleratorCount = 1
	manifests := renderPlatformWorkloadManifests("11111111-1111-1111-1111-111111111111", "workload-gpu-legacy-1", spec, nil)
	var deployment map[string]any
	if err := json.Unmarshal([]byte(manifests[0].Content), &deployment); err != nil {
		t.Fatalf("deployment json: %v", err)
	}
	container, _ := deployment["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)["containers"].([]any)[0].(map[string]any)
	resources, _ := container["resources"].(map[string]any)["requests"].(map[string]any)
	if resources["nvidia.com/gpu"] != "1" {
		t.Fatalf("whole-card request = %#v", resources)
	}
	if _, ok := resources["volcano.sh/vgpu-number"]; ok {
		t.Fatalf("omitted memory must not request vGPU: %#v", resources)
	}
}

func TestCanonicalAcceleratorSpecIDStripsLegacySuffixes(t *testing.T) {
	cases := map[string]string{
		"gpu-nvidia-geforce-rtx-4090":      "gpu-nvidia-geforce-rtx-4090",
		"gpu-nvidia-geforce-rtx-4090-full": "gpu-nvidia-geforce-rtx-4090",
		"gpu-nvidia-geforce-rtx-4090-8x":   "gpu-nvidia-geforce-rtx-4090",
		"gpu-a100":                         "gpu-a100",
		"GPU-A100-FULL":                    "gpu-a100",
	}
	for in, want := range cases {
		if got := canonicalAcceleratorSpecID(in); got != want {
			t.Fatalf("canonicalAcceleratorSpecID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRenderPlatformWorkloadManifestsInjectsTenantEnv(t *testing.T) {
	spec := sampleCPUPlatformWorkloadSpec("1df72d71-9d49-46c4-a48a-52bb37b082ab", "inference-cpu-example")
	spec.Env = []ports.PlatformWorkloadEnvVar{{Name: "VLLM_LOGGING_LEVEL", Value: "DEBUG"}}
	manifests := renderPlatformWorkloadManifests("11111111-1111-1111-1111-111111111111", "workload-id-1", spec, nil)
	var deployment map[string]any
	if err := json.Unmarshal([]byte(manifests[0].Content), &deployment); err != nil {
		t.Fatalf("deployment json: %v", err)
	}
	container, _ := deployment["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)["containers"].([]any)[0].(map[string]any)
	env, _ := container["env"].([]any)
	if len(env) != 1 {
		t.Fatalf("env = %#v", env)
	}
	item, _ := env[0].(map[string]any)
	if item["name"] != "VLLM_LOGGING_LEVEL" || item["value"] != "DEBUG" {
		t.Fatalf("env item = %#v", item)
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
	podSpec, _ := deployment["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
	if podSpec["schedulerName"] != "volcano" {
		t.Fatalf("GPU schedulerName = %#v", podSpec["schedulerName"])
	}
	if strings.Contains(manifests[0].Content, "ani-inference") || strings.Contains(manifests[0].Content, "queue-name") {
		t.Fatalf("GPU manifest bound a named Volcano queue:\n%s", manifests[0].Content)
	}
	if strings.Contains(manifests[0].Content, "PodGroup") || strings.Contains(manifests[0].Content, "LeaderWorkerSet") {
		t.Fatalf("single-node GPU rendered LWS/PodGroup:\n%s", manifests[0].Content)
	}
	strategy, _ := deployment["spec"].(map[string]any)["strategy"].(map[string]any)
	if strategy["type"] != "Recreate" {
		t.Fatalf("GPU strategy = %#v", strategy)
	}
	if !strings.Contains(manifests[0].Content, `"sizeLimit": "12Gi"`) {
		t.Fatalf("multi-GPU shm should be 12Gi:\n%s", manifests[0].Content)
	}
	labels, _ := deployment["metadata"].(map[string]any)["labels"].(map[string]any)
	if labels["ani.kubercloud.io/accelerator-spec-id"] != "gpu-a100" {
		t.Fatalf("labels = %#v", labels)
	}
	if _, ok := labels["ani.kubercloud.io/instance"]; ok {
		t.Fatalf("rendered instance identity label: %#v", labels)
	}
}

func TestRenderLeaderWorkerPlatformWorkloadUsesLWSPodGroupAndLeaderService(t *testing.T) {
	spec := sampleLeaderWorkerPlatformWorkloadSpec("6df72d71-9d49-46c4-a48a-52bb37b082ab", "inference-lws")
	manifests := renderPlatformWorkloadManifests("11111111-1111-1111-1111-111111111111", "workload-lws-1", spec, nil)
	if len(manifests) != 4 || manifests[0].Kind != "LeaderWorkerSet" || manifests[1].Kind != "PodGroup" || manifests[2].Kind != "Service" || manifests[3].Kind != "NetworkPolicy" {
		t.Fatalf("manifests = %+v", manifests)
	}
	joined := manifests[0].Content + manifests[1].Content + manifests[2].Content
	if !strings.Contains(joined, `"schedulerName": "volcano"`) {
		t.Fatalf("LWS/PodGroup missing volcano scheduler:\n%s", joined)
	}
	if strings.Contains(joined, "ani-inference") {
		t.Fatalf("LWS/PodGroup bound a named Volcano queue:\n%s", joined)
	}
	if !strings.Contains(joined, `"ani.kubercloud.io/inference-role": "leader"`) {
		t.Fatalf("missing leader role label:\n%s", joined)
	}
	if !strings.Contains(manifests[2].Content, `"ani.kubercloud.io/inference-role": "leader"`) {
		t.Fatalf("service does not select leader:\n%s", manifests[2].Content)
	}
	if !strings.Contains(manifests[2].Content, `"name": "inference-lws-http"`) {
		t.Fatalf("LWS ClusterIP must not reuse the LWS headless name:\n%s", manifests[2].Content)
	}
	if strings.Contains(manifests[3].Content, `"port": 53`) == false {
		t.Fatalf("LWS NetworkPolicy missing DNS:\n%s", manifests[3].Content)
	}
	if strings.Contains(manifests[2].Content, `"ani.kubercloud.io/inference-role": "worker"`) {
		t.Fatalf("service selected worker:\n%s", manifests[2].Content)
	}
	if !strings.Contains(manifests[0].Content, "--num-gpus=1") || !strings.Contains(manifests[0].Content, "RAY_EXPERIMENTAL_NOSET_CUDA_VISIBLE_DEVICES") || !strings.Contains(manifests[0].Content, "sitecustomize.py") || !strings.Contains(manifests[0].Content, "PYTHONPATH=/tmp") {
		t.Fatalf("worker launch missing Ray GPU env:\n%s", manifests[0].Content)
	}
	if !strings.Contains(manifests[0].Content, `"name": "CUDA_VISIBLE_DEVICES"`) || !strings.Contains(manifests[0].Content, `"value": "0"`) || !strings.Contains(manifests[0].Content, `"name": "PYTHONPATH"`) {
		t.Fatalf("LWS container missing CUDA_VISIBLE_DEVICES=0 or PYTHONPATH:\n%s", manifests[0].Content)
	}
	if !strings.Contains(manifests[0].Content, `"name": "VLLM_USE_RAY_COMPILED_DAG"`) || !strings.Contains(manifests[0].Content, "VLLM_USE_RAY_COMPILED_DAG=0") {
		t.Fatalf("LWS missing compiled DAG disable:\n%s", manifests[0].Content)
	}
	if !strings.Contains(manifests[0].Content, `"sizeLimit": "12Gi"`) {
		t.Fatalf("LWS shm should be 12Gi:\n%s", manifests[0].Content)
	}
	if strings.Contains(manifests[0].Content, `"name": "NVIDIA_VISIBLE_DEVICES"`) {
		t.Fatalf("Pod spec must not set NVIDIA_VISIBLE_DEVICES:\n%s", manifests[0].Content)
	}
	if !strings.Contains(manifests[1].Content, `"minMember": 2`) || !strings.Contains(manifests[1].Content, `"nvidia.com/gpu": "2"`) {
		t.Fatalf("podgroup = %s", manifests[1].Content)
	}
	if strings.Count(manifests[0].Content, `"nvidia.com/gpu": "1"`) < 2 {
		t.Fatalf("leader and worker must each request nvidia.com/gpu=1:\n%s", manifests[0].Content)
	}
	if !strings.Contains(manifests[0].Content, `"ovn.kubernetes.io/logical_switch": "ovn-default"`) || !strings.Contains(manifests[0].Content, `"ovn.kubernetes.io/vpc": "ovn-cluster"`) {
		t.Fatalf("LWS templates missing cluster default overlay:\n%s", manifests[0].Content)
	}
	if !strings.Contains(manifests[3].Content, `"matchLabels":`) || strings.Count(manifests[3].Content, `"from"`) < 2 {
		t.Fatalf("LWS NetworkPolicy missing same-workload peer ingress:\n%s", manifests[3].Content)
	}
}

func TestRenderLeaderWorkerDoesNotCopyAggregateGPUCountIntoRoles(t *testing.T) {
	spec := sampleLeaderWorkerPlatformWorkloadSpec("7df72d71-9d49-46c4-a48a-52bb37b082ab", "inference-lws-4gpu")
	spec.Resources.AcceleratorCount = 4
	spec.Topology.Workers.Count = 3
	spec.Topology.Leader.Resources.AcceleratorCount = 0
	spec.Topology.Workers.Resources.AcceleratorCount = 0
	manifests := renderPlatformWorkloadManifests("11111111-1111-1111-1111-111111111111", "workload-lws-4", spec, nil)
	if strings.Contains(manifests[0].Content, `"nvidia.com/gpu": "4"`) {
		t.Fatalf("role pods must not request the aggregate GPU count:\n%s", manifests[0].Content)
	}
	if strings.Count(manifests[0].Content, `"nvidia.com/gpu": "1"`) < 2 {
		t.Fatalf("leader and worker templates must each request 1 GPU:\n%s", manifests[0].Content)
	}
}

func TestAcceleratorSpecsFromGPUNodesRequireVolcano(t *testing.T) {
	nodes := []ports.GPUNodeClass{{
		NodeName: "gpu-a", Model: "A100", Ready: true,
		Allocatable: map[string]string{"nvidia.com/gpu": "2"},
		Devices:     []ports.GPUDeviceClass{{Model: "A100", ResourceName: "nvidia.com/gpu"}},
	}}
	withoutVolcano := acceleratorSpecsFromGPUNodes(nodes, false)
	if len(withoutVolcano) != 1 || withoutVolcano[0].SpecID != "gpu-a100" || withoutVolcano[0].Available || withoutVolcano[0].MaxSingleNodeCount != 2 {
		t.Fatalf("without volcano = %#v", withoutVolcano)
	}
	withVolcano := acceleratorSpecsFromGPUNodes(nodes, true)
	if len(withVolcano) != 1 || !withVolcano[0].Available {
		t.Fatalf("with volcano = %#v", withVolcano)
	}
}

func TestAcceleratorSpecsFromGPUNodesAdvertiseVolcanoVGPU(t *testing.T) {
	nodes := mixed4090AcceleratorNodes()
	specs := acceleratorSpecsFromGPUNodes(nodes, true)
	byID := map[string]ports.PlatformWorkloadAcceleratorCapability{}
	for _, spec := range specs {
		byID[spec.SpecID] = spec
	}
	model := byID["gpu-nvidia-geforce-rtx-4090"]
	if !model.Available || model.MaxSingleNodeCount != 4 || model.MaxWholeCardCount != 1 || model.MaxVGPUCount != 4 {
		t.Fatalf("model spec = %#v", model)
	}
	if _, ok := byID["gpu-nvidia-geforce-rtx-4090-full"]; ok {
		t.Fatalf("must not advertise -full: %#v", byID)
	}
	if _, ok := byID["gpu-nvidia-geforce-rtx-4090-4x"]; ok {
		t.Fatalf("must not advertise -Nx: %#v", byID)
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

func TestObjectModelCacheIsPodLocal(t *testing.T) {
	spec := sampleCPUPlatformWorkloadSpec("9df72d71-9d49-46c4-a48a-52bb37b082ab", "cache")
	spec.ModelMaterialization = &ports.PlatformWorkloadModelMaterialization{TenantID: "11111111-1111-1111-1111-111111111111"}
	volumes, _ := platformWorkloadPodVolumes(spec)
	for _, value := range volumes {
		volume := value.(map[string]any)
		if volume["name"] != platformWorkloadModelVolume {
			continue
		}
		if _, shared := volume["persistentVolumeClaim"]; shared {
			t.Fatal("object model cache must not share an RWO claim across Pods")
		}
		cache, ok := volume["emptyDir"].(map[string]any)
		if !ok || cache["sizeLimit"] != "20Gi" {
			t.Fatalf("expected bounded disk cache: %#v", volume)
		}
		return
	}
	t.Fatal("missing model cache")
}

func TestRenderPlatformWorkloadManifestsObjectMaterializationUsesFetcherGate(t *testing.T) {
	tenant := "11111111-1111-1111-1111-111111111111"
	spec := sampleCPUPlatformWorkloadSpec("9df72d71-9d49-46c4-a48a-52bb37b082ab", "inference-object-model")
	spec.Command = []string{"python3", "-m", "vllm.entrypoints.openai.api_server", "--model", "/models"}
	spec.Metadata.OwnerRef = "inference-service/33333333-3333-3333-3333-333333333333"
	spec.ModelMaterialization = &ports.PlatformWorkloadModelMaterialization{
		TenantID: tenant, ModelVersionID: "33333333-3333-3333-3333-333333333333",
		ObjectRef: "object://models/11111111-1111-1111-1111-111111111111/22222222-2222-2222-2222-222222222222/v1/doc/model.safetensors",
		SizeBytes: 12, ChecksumSHA256: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ModelServiceGRPCAddr: "model-service:9090",
		FetcherImageRef:      "registry.local/model-fetcher@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		TargetPath:           "/models/33333333-3333-3333-3333-333333333333/model.safetensors",
	}
	manifests := renderPlatformWorkloadManifests(tenant, "workload-object-1", spec, nil)
	var deployment map[string]any
	if err := json.Unmarshal([]byte(manifests[0].Content), &deployment); err != nil {
		t.Fatalf("deployment json: %v", err)
	}
	podSpec, _ := deployment["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
	initContainers, _ := podSpec["initContainers"].([]any)
	if len(initContainers) != 1 {
		t.Fatalf("initContainers = %#v, want one model-fetcher", initContainers)
	}
	init, _ := initContainers[0].(map[string]any)
	if init["name"] != "model-fetcher" || init["image"] != spec.ModelMaterialization.FetcherImageRef {
		t.Fatalf("init container = %#v", init)
	}
	args, _ := init["args"].([]any)
	wantArgs := []string{
		"--tenant-id=" + tenant,
		"--model-version-id=" + spec.ModelMaterialization.ModelVersionID,
		"--model-service-grpc-addr=" + spec.ModelMaterialization.ModelServiceGRPCAddr,
		"--object-ref=" + spec.ModelMaterialization.ObjectRef,
		"--size-bytes=12",
		"--sha256=" + spec.ModelMaterialization.ChecksumSHA256,
		"--target-path=" + spec.ModelMaterialization.TargetPath,
	}
	if len(args) != len(wantArgs) {
		t.Fatalf("args = %#v", args)
	}
	for index, want := range wantArgs {
		if got := args[index]; got != want || strings.Contains(got.(string), "$(") {
			t.Fatalf("arg[%d] = %v, want %q", index, got, want)
		}
	}
	env, _ := init["env"].([]any)
	if len(env) != 11 {
		t.Fatalf("env = %#v", env)
	}
	envNames := map[string]bool{}
	for _, raw := range env {
		if item, ok := raw.(map[string]any); ok {
			envNames[item["name"].(string)] = true
		}
	}
	for _, name := range []string{"MODEL_SERVICE_TLS_CA_FILE", "MODEL_SERVICE_TLS_CERT_FILE", "MODEL_SERVICE_TLS_KEY_FILE"} {
		if !envNames[name] {
			t.Fatalf("missing TLS env %q", name)
		}
	}
	if got := envValue(env, "MODEL_FETCHER_ALLOW_INSECURE_HTTP"); got != "false" {
		t.Fatalf("default fetcher HTTP opt-in = %q, want false", got)
	}
	if strings.Contains(string(manifests[0].Content), "signed") || strings.Contains(string(manifests[0].Content), "https://") {
		t.Fatalf("manifest contains a signed URL or URL literal: %s", manifests[0].Content)
	}
	resources, _ := init["resources"].(map[string]any)
	requests, _ := resources["requests"].(map[string]any)
	if _, ok := requests[kubernetesNVIDIAGPUResource]; ok {
		t.Fatalf("fetcher must not request GPU: %#v", requests)
	}
	mounts, _ := init["volumeMounts"].([]any)
	main, _ := podSpec["containers"].([]any)[0].(map[string]any)
	mainArgs, _ := main["args"].([]any)
	mainCommand, _ := main["command"].([]any)
	joinedArgs := fmt.Sprint(mainCommand, mainArgs)
	if !strings.Contains(joinedArgs, spec.ModelMaterialization.TargetPath) {
		t.Fatalf("main engine args must target verified model path %q: %#v", spec.ModelMaterialization.TargetPath, mainArgs)
	}
	mainMounts, _ := main["volumeMounts"].([]any)
	if len(mounts) == 0 || len(mainMounts) == 0 {
		t.Fatalf("shared model mounts missing: init=%#v main=%#v", mounts, mainMounts)
	}
	if mounts[len(mounts)-2].(map[string]any)["name"] != mainMounts[len(mainMounts)-1].(map[string]any)["name"] {
		t.Fatalf("init/main do not share final model volume: init=%#v main=%#v", mounts, mainMounts)
	}
	if got, _ := mainMounts[len(mainMounts)-1].(map[string]any)["readOnly"].(bool); !got {
		t.Fatalf("main model mount must be read-only: %#v", mainMounts[len(mainMounts)-1])
	}
	labels, _ := deployment["metadata"].(map[string]any)["labels"].(map[string]any)
	for key, want := range map[string]string{
		platformWorkloadTenantLabel: tenant,
		platformWorkloadIDLabel:     "workload-object-1",
		platformWorkloadOwnerLabel:  spec.Metadata.OwnerRef,
	} {
		if labels[key] != want {
			t.Fatalf("label %s = %v, want %s", key, labels[key], want)
		}
	}
}

func TestRenderPlatformWorkloadObjectMaterializationUsesTenantFetcherIdentityAndMinIOEgress(t *testing.T) {
	tenant := "11111111-1111-1111-1111-111111111111"
	spec := sampleCPUPlatformWorkloadSpec("9ef72d71-9d49-46c4-a48a-52bb37b082ab", "inference-object-policy")
	spec.ModelMaterialization = &ports.PlatformWorkloadModelMaterialization{
		TenantID: tenant, ModelVersionID: "33333333-3333-3333-3333-333333333333",
		ObjectRef: "object://models/11111111-1111-1111-1111-111111111111/22222222-2222-2222-2222-222222222222/v1/doc/model.safetensors",
		SizeBytes: 12, ChecksumSHA256: "sha256:" + strings.Repeat("a", 64),
		ModelServiceGRPCAddr: "model-service:9105",
		FetcherImageRef:      "registry.local/model-fetcher@sha256:" + strings.Repeat("b", 64),
		TargetPath:           "/models/33333333-3333-3333-3333-333333333333/model.safetensors",
	}
	manifests := renderPlatformWorkloadManifests(tenant, "workload-object-policy", spec, nil)
	var deployment, policy, certificateManifest map[string]any
	var serviceAccount bool
	var serviceAccountManifest map[string]any
	for _, item := range manifests {
		switch item.Kind {
		case "Deployment":
			if err := json.Unmarshal([]byte(item.Content), &deployment); err != nil {
				t.Fatalf("deployment json: %v", err)
			}
		case "NetworkPolicy":
			if err := json.Unmarshal([]byte(item.Content), &policy); err != nil {
				t.Fatalf("network policy json: %v", err)
			}
		case "ServiceAccount":
			serviceAccount = true
			if err := json.Unmarshal([]byte(item.Content), &serviceAccountManifest); err != nil {
				t.Fatalf("service account json: %v", err)
			}
		case "Certificate":
			if err := json.Unmarshal([]byte(item.Content), &certificateManifest); err != nil {
				t.Fatalf("certificate json: %v", err)
			}
		}
	}
	if !serviceAccount {
		t.Fatalf("object materialization must render tenant fetcher ServiceAccount: %#v", manifests)
	}
	if serviceAccountManifest["metadata"].(map[string]any)["name"] != "ani-inference-fetcher" || serviceAccountManifest["metadata"].(map[string]any)["namespace"] != "ani-tenant-"+tenant {
		t.Fatalf("tenant fetcher ServiceAccount = %#v", serviceAccountManifest)
	}
	if got := serviceAccountManifest["automountServiceAccountToken"]; got != false {
		t.Fatalf("tenant fetcher ServiceAccount automountServiceAccountToken = %v, want false", got)
	}
	certificateSpec, _ := certificateManifest["spec"].(map[string]any)
	certificateURIs, _ := certificateSpec["uris"].([]any)
	wantURI := "spiffe://ani.dev/ns/ani-tenant-" + tenant + "/sa/ani-inference-fetcher"
	if len(certificateURIs) != 1 || certificateURIs[0] != wantURI {
		t.Fatalf("tenant fetcher Certificate URIs = %#v, want %q", certificateURIs, wantURI)
	}
	certificateUsages, _ := certificateSpec["usages"].([]any)
	if len(certificateUsages) != 1 || certificateUsages[0] != "client auth" {
		t.Fatalf("tenant fetcher Certificate usages = %#v, want [client auth]", certificateSpec["usages"])
	}
	podSpec, _ := deployment["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
	if got := podSpec["serviceAccountName"]; got != "ani-inference-fetcher" {
		t.Fatalf("serviceAccountName = %v, want ani-inference-fetcher", got)
	}
	securityContext, _ := podSpec["securityContext"].(map[string]any)
	if got := securityContext["fsGroup"]; got != float64(65532) {
		t.Fatalf("object materialization fsGroup = %v, want 65532 for tenant PVC write access", got)
	}
	if !networkPolicyAllowsMinIO(policy) {
		t.Fatalf("object materialization policy must allow MinIO app.kubernetes.io/name=minio on TCP/9000: %#v", policy)
	}
}

func TestRenderPlatformWorkloadMaterializationHTTPOptInIsScopedToFetcherInit(t *testing.T) {
	spec := sampleCPUPlatformWorkloadSpec("9af72d71-9d49-46c4-a48a-52bb37b082ab", "inference-object-http")
	spec.ModelMaterialization = &ports.PlatformWorkloadModelMaterialization{
		TenantID: "11111111-1111-1111-1111-111111111111", ModelVersionID: "33333333-3333-3333-3333-333333333333",
		ObjectRef: "object://models/11111111-1111-1111-1111-111111111111/22222222-2222-2222-2222-222222222222/v1/doc/model.safetensors",
		SizeBytes: 12, ChecksumSHA256: "sha256:" + strings.Repeat("a", 64), ModelServiceGRPCAddr: "model-service:9105",
		FetcherImageRef: "registry.local/model-fetcher@sha256:" + strings.Repeat("b", 64), TargetPath: "/models/33333333-3333-3333-3333-333333333333/model.safetensors",
	}
	manifests := renderPlatformWorkloadManifestsWithFetcherConfig("11111111-1111-1111-1111-111111111111", "workload-object-http", spec, nil, true)
	var deployment map[string]any
	if err := json.Unmarshal([]byte(manifests[0].Content), &deployment); err != nil {
		t.Fatalf("deployment json: %v", err)
	}
	podSpec, _ := deployment["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
	initContainers, _ := podSpec["initContainers"].([]any)
	init, _ := initContainers[0].(map[string]any)
	initEnv, _ := init["env"].([]any)
	if got := envValue(initEnv, "MODEL_FETCHER_ALLOW_INSECURE_HTTP"); got != "true" {
		t.Fatalf("fetcher HTTP opt-in = %q, want true", got)
	}
	mainContainers, _ := podSpec["containers"].([]any)
	main, _ := mainContainers[0].(map[string]any)
	mainEnv, _ := main["env"].([]any)
	if got := envValue(mainEnv, "MODEL_FETCHER_ALLOW_INSECURE_HTTP"); got != "" {
		t.Fatalf("main container received fetcher HTTP opt-in = %q", got)
	}

	defaultManifests := renderPlatformWorkloadManifestsWithFetcherConfig("11111111-1111-1111-1111-111111111111", "workload-object-http-default", spec, nil, false)
	if err := json.Unmarshal([]byte(defaultManifests[0].Content), &deployment); err != nil {
		t.Fatalf("default deployment json: %v", err)
	}
	podSpec, _ = deployment["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
	initContainers, _ = podSpec["initContainers"].([]any)
	init, _ = initContainers[0].(map[string]any)
	initEnv, _ = init["env"].([]any)
	if got := envValue(initEnv, "MODEL_FETCHER_ALLOW_INSECURE_HTTP"); got != "false" {
		t.Fatalf("default fetcher HTTP opt-in = %q, want false", got)
	}
}

func envValue(env []any, name string) string {
	for _, raw := range env {
		item, _ := raw.(map[string]any)
		if item["name"] == name {
			value, _ := item["value"].(string)
			return value
		}
	}
	return ""
}

func networkPolicyAllowsMinIO(policy map[string]any) bool {
	spec, _ := policy["spec"].(map[string]any)
	egress, _ := spec["egress"].([]any)
	for _, rawRule := range egress {
		rule, _ := rawRule.(map[string]any)
		ports, _ := rule["ports"].([]any)
		port9000 := false
		for _, rawPort := range ports {
			port, _ := rawPort.(map[string]any)
			if port["protocol"] == "TCP" && port["port"] == float64(9000) {
				port9000 = true
			}
		}
		if !port9000 {
			continue
		}
		to, _ := rule["to"].([]any)
		for _, rawPeer := range to {
			peer, _ := rawPeer.(map[string]any)
			ns, _ := peer["namespaceSelector"].(map[string]any)
			labels, _ := ns["matchLabels"].(map[string]any)
			if labels["kubernetes.io/metadata.name"] != "ani-s05-objectstore" {
				continue
			}
			pod, _ := peer["podSelector"].(map[string]any)
			podLabels, _ := pod["matchLabels"].(map[string]any)
			if podLabels["app.kubernetes.io/name"] == "minio" {
				return true
			}
		}
	}
	return false
}

func TestRenderPlatformWorkloadManifestsImportedArchiveUsesVersionDirectory(t *testing.T) {
	tenant := "11111111-1111-1111-1111-111111111111"
	version := "33333333-3333-3333-3333-333333333333"
	spec := sampleCPUPlatformWorkloadSpec("afd72d71-9d49-46c4-a48a-52bb37b082ab", "inference-object-archive")
	spec.Command = []string{"serve", "--model", "/models/old"}
	spec.ModelMaterialization = &ports.PlatformWorkloadModelMaterialization{
		TenantID: tenant, ModelVersionID: version,
		ObjectRef: "object://models/" + tenant + "/22222222-2222-2222-2222-222222222222/import-44444444-4444-4444-4444-444444444444/archive/model.tar.gz",
		SizeBytes: 12, ChecksumSHA256: "sha256:" + strings.Repeat("a", 64),
		ModelServiceGRPCAddr: "model-service:9090",
		FetcherImageRef:      "registry.local/model-fetcher@sha256:" + strings.Repeat("b", 64),
		TargetPath:           "/models/" + version + "/model.tar.gz",
	}
	manifests := renderPlatformWorkloadManifests(tenant, "workload-object-archive", spec, nil)
	var deployment map[string]any
	if err := json.Unmarshal([]byte(manifests[0].Content), &deployment); err != nil {
		t.Fatalf("deployment json: %v", err)
	}
	podSpec, _ := deployment["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
	initContainers, _ := podSpec["initContainers"].([]any)
	init, _ := initContainers[0].(map[string]any)
	args, _ := init["args"].([]any)
	if args[len(args)-1] != "--target-path=/models/"+version {
		t.Fatalf("fetcher target arg = %#v", args)
	}
	main, _ := podSpec["containers"].([]any)[0].(map[string]any)
	mainArgs, _ := main["args"].([]any)
	mainCommand, _ := main["command"].([]any)
	if !strings.Contains(fmt.Sprint(mainCommand, mainArgs), "/models/"+version) {
		t.Fatalf("engine target = %#v/%#v", mainCommand, mainArgs)
	}
}

func TestRenderPlatformWorkloadManifestsPVCOnlyHasNoFetcher(t *testing.T) {
	spec := sampleCPUPlatformWorkloadSpec("8df72d71-9d49-46c4-a48a-52bb37b082ab", "inference-pvc-only")
	spec.Artifacts = []ports.PlatformWorkloadArtifact{{ObjectRef: "pvc://vllm-model", MountPath: "/models"}}
	manifests := renderPlatformWorkloadManifests("11111111-1111-1111-1111-111111111111", "workload-pvc-only", spec, nil)
	var deployment map[string]any
	if err := json.Unmarshal([]byte(manifests[0].Content), &deployment); err != nil {
		t.Fatalf("deployment json: %v", err)
	}
	podSpec, _ := deployment["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
	if _, ok := podSpec["initContainers"]; ok {
		t.Fatalf("PVC-only workload must not get model-fetcher: %#v", podSpec["initContainers"])
	}
}

func TestKubernetesPlatformWorkloadRuntimeInjectsPublisherMaterializationConfig(t *testing.T) {
	spec := sampleCPUPlatformWorkloadSpec("7df72d71-9d49-46c4-a48a-52bb37b082ab", "inference-configured")
	spec.ModelMaterialization = &ports.PlatformWorkloadModelMaterialization{
		ModelServiceGRPCAddr: "caller.invalid:1",
		FetcherImageRef:      "caller.invalid/fetcher@sha256:" + strings.Repeat("c", 64),
	}
	runtime := NewKubernetesPlatformWorkloadRuntimeWithMaterializationConfig(nil, "model-service:9090", "registry.local/model-fetcher@sha256:"+strings.Repeat("d", 64))
	runtime.ConfigureModelMaterialization(&spec)
	if got := spec.ModelMaterialization.ModelServiceGRPCAddr; got != "model-service:9090" {
		t.Fatalf("model service addr = %q", got)
	}
	if got := spec.ModelMaterialization.FetcherImageRef; got != "registry.local/model-fetcher@sha256:"+strings.Repeat("d", 64) {
		t.Fatalf("fetcher image = %q", got)
	}
}

func TestKubernetesPlatformWorkloadRejectsScalingObjectMaterialization(t *testing.T) {
	provider := newReadyFakePlatformWorkloadRuntime()
	svc := NewKubernetesPlatformWorkloadService(provider)
	spec := sampleCPUPlatformWorkloadSpec("6df72d71-9d49-46c4-a48a-52bb37b082ab", "inference-object-scale")
	spec.ModelMaterialization = &ports.PlatformWorkloadModelMaterialization{TenantID: "11111111-1111-1111-1111-111111111111", ModelVersionID: "33333333-3333-3333-3333-333333333333", ObjectRef: "object://models/11111111-1111-1111-1111-111111111111/22222222-2222-2222-2222-222222222222/v1/doc/model.safetensors", SizeBytes: 1, ChecksumSHA256: "sha256:" + strings.Repeat("a", 64), ModelServiceGRPCAddr: "model-service:9090", FetcherImageRef: "registry.local/model-fetcher@sha256:" + strings.Repeat("b", 64), TargetPath: "/models/33333333-3333-3333-3333-333333333333/model.safetensors"}
	created, err := svc.Create(context.Background(), spec.ModelMaterialization.TenantID, spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateReplicas(context.Background(), created.TenantID, created.ID, "7df72d71-9d49-46c4-a48a-52bb37b082ab", 2); !errors.Is(err, ports.ErrFailedPrecondition) {
		t.Fatalf("scale object materialization error = %v", err)
	}
	if _, err := svc.UpdateReplicas(context.Background(), created.TenantID, created.ID, "7df72d71-9d49-46c4-a48a-52bb37b082ab", 2); !errors.Is(err, ports.ErrFailedPrecondition) {
		t.Fatalf("replayed scale object materialization error = %v", err)
	}
}

func TestPlatformWorkloadMaterializationLaunchRewritesEqualsForm(t *testing.T) {
	spec := sampleCPUPlatformWorkloadSpec("5df72d71-9d49-46c4-a48a-52bb37b082ab", "inference-object-launch")
	spec.Command = []string{"serve", "--model=/models/old", "--model-path=/models/old2"}
	spec.ModelMaterialization = &ports.PlatformWorkloadModelMaterialization{TargetPath: "/models/33333333-3333-3333-3333-333333333333/model.safetensors"}
	command, _ := platformWorkloadMaterializationLaunch(spec)
	joined := strings.Join(command, " ")
	if strings.Contains(joined, "/models/old") || strings.Count(joined, spec.ModelMaterialization.TargetPath) != 2 {
		t.Fatalf("rewritten command = %#v", command)
	}
}

func TestPvcClaimNameAcceptsTenantLocalPVC(t *testing.T) {
	claim, ok := pvcClaimName("pvc://vllm-model#/models/qwen")
	if !ok || claim != "vllm-model" {
		t.Fatalf("claim = %q ok=%v", claim, ok)
	}
	if _, ok := pvcClaimName("object://models/qwen/v1"); ok {
		t.Fatal("object:// must not mount")
	}
	if _, ok := pvcClaimName("hostPath:/data/models"); ok {
		t.Fatal("hostPath must not mount")
	}
	if _, ok := pvcClaimName("pvc://VLLM"); ok {
		t.Fatal("uppercase claim must not mount")
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

func TestKubernetesPlatformWorkloadRuntimeObjectMaterializationAppliesTenantFetcherIdentity(t *testing.T) {
	var paths []string
	client := newTestKubernetesRESTClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPatch {
			paths = append(paths, r.URL.Path)
		}
		if r.Method == http.MethodPatch || r.Method == http.MethodDelete {
			return jsonResponse(http.StatusOK, `{"kind":"Status","status":"Success"}`), nil
		}
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/api/v1/nodes") {
			return jsonResponse(http.StatusOK, `{"items":[]}`), nil
		}
		return jsonResponse(http.StatusNotFound, `{"kind":"Status","status":"Failure"}`), nil
	}))
	runtime := NewKubernetesPlatformWorkloadRuntimeWithMaterializationConfig(client, "model-service:9105", "registry.local/model-fetcher@sha256:"+strings.Repeat("b", 64))
	tenant := "11111111-1111-1111-1111-111111111111"
	spec := sampleCPUPlatformWorkloadSpec("2df72d71-9d49-46c4-a48a-52bb37b082ab", "inference-object-apply")
	spec.ModelMaterialization = &ports.PlatformWorkloadModelMaterialization{
		TenantID: tenant, ModelVersionID: "33333333-3333-3333-3333-333333333333",
		ObjectRef: "object://models/" + tenant + "/22222222-2222-2222-2222-222222222222/v1/doc/model.safetensors",
		SizeBytes: 12, ChecksumSHA256: "sha256:" + strings.Repeat("a", 64),
		ModelServiceGRPCAddr: "model-service:9105",
		FetcherImageRef:      "registry.local/model-fetcher@sha256:" + strings.Repeat("b", 64),
		TargetPath:           "/models/33333333-3333-3333-3333-333333333333/model.safetensors",
	}
	if _, err := runtime.Apply(context.Background(), tenant, "workload-object-apply", spec); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	joined := strings.Join(paths, "\n")
	ns := "/namespaces/ani-tenant-" + tenant + "/"
	for _, want := range []string{
		"/api/v1" + ns + "serviceaccounts/ani-inference-fetcher",
		"/apis/cert-manager.io/v1" + ns + "certificates/ani-model-fetcher-11111111-1111-1111-1",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("Apply() paths = %v, want %s", paths, want)
		}
	}
	indexOf := func(fragment string) int {
		for index, path := range paths {
			if strings.Contains(path, fragment) {
				return index
			}
		}
		return -1
	}
	serviceAccountIndex := indexOf("/serviceaccounts/ani-inference-fetcher")
	certificateIndex := indexOf("/certificates/ani-model-fetcher-")
	deploymentIndex := indexOf("/deployments/inference-object-apply")
	policyIndex := indexOf("/networkpolicies/inference-object-apply")
	if serviceAccountIndex < 0 || certificateIndex < 0 || deploymentIndex < 0 || policyIndex < 0 || serviceAccountIndex > deploymentIndex || certificateIndex > deploymentIndex || policyIndex > deploymentIndex {
		t.Fatalf("Apply() dependency order = %v, want SA/certificate/NetworkPolicy before deployment", paths)
	}
}

func TestKubernetesPlatformWorkloadRuntimeObjectApplyFailureKeepsTenantFetcherIdentity(t *testing.T) {
	var deleted []string
	client := newTestKubernetesRESTClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodDelete {
			deleted = append(deleted, r.URL.Path)
			return jsonResponse(http.StatusOK, `{"kind":"Status","status":"Success"}`), nil
		}
		if r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "/deployments/") {
			return jsonResponse(http.StatusInternalServerError, `{"kind":"Status","status":"Failure"}`), nil
		}
		if r.Method == http.MethodPatch || r.Method == http.MethodDelete {
			return jsonResponse(http.StatusOK, `{"kind":"Status","status":"Success"}`), nil
		}
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/api/v1/nodes") {
			return jsonResponse(http.StatusOK, `{"items":[]}`), nil
		}
		return jsonResponse(http.StatusNotFound, `{"kind":"Status","status":"Failure"}`), nil
	}))
	runtime := NewKubernetesPlatformWorkloadRuntimeWithMaterializationConfig(client, "model-service:9105", "registry.local/model-fetcher@sha256:"+strings.Repeat("b", 64))
	tenant := "11111111-1111-1111-1111-111111111111"
	spec := sampleCPUPlatformWorkloadSpec("4df72d71-9d49-46c4-a48a-52bb37b082ab", "inference-object-apply-failure")
	spec.ModelMaterialization = &ports.PlatformWorkloadModelMaterialization{
		TenantID: tenant, ModelVersionID: "33333333-3333-3333-3333-333333333333",
		ObjectRef: "object://models/" + tenant + "/22222222-2222-2222-2222-222222222222/v1/doc/model.safetensors",
		SizeBytes: 12, ChecksumSHA256: "sha256:" + strings.Repeat("a", 64),
		ModelServiceGRPCAddr: "model-service:9105",
		FetcherImageRef:      "registry.local/model-fetcher@sha256:" + strings.Repeat("b", 64),
		TargetPath:           "/models/33333333-3333-3333-3333-333333333333/model.safetensors",
	}
	if _, err := runtime.Apply(context.Background(), tenant, "workload-object-apply-failure", spec); err == nil {
		t.Fatal("Apply() error = nil, want workload failure")
	}
	joined := strings.Join(deleted, "\n")
	if strings.Contains(joined, "/serviceaccounts/ani-inference-fetcher") || strings.Contains(joined, "/certificates/ani-model-fetcher-") {
		t.Fatalf("Apply() compensation removed tenant-shared fetcher identity: %v", deleted)
	}
}

func TestKubernetesPlatformWorkloadRuntimeDeleteKeepsTenantFetcherIdentity(t *testing.T) {
	var paths []string
	client := newTestKubernetesRESTClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodDelete {
			paths = append(paths, r.Method+" "+r.URL.Path)
			return jsonResponse(http.StatusOK, `{"kind":"Status","status":"Success"}`), nil
		}
		return jsonResponse(http.StatusNotFound, `{"kind":"Status","status":"Failure"}`), nil
	}))
	runtime := NewKubernetesPlatformWorkloadRuntime(client)
	tenant := "11111111-1111-1111-1111-111111111111"
	spec := sampleCPUPlatformWorkloadSpec("3df72d71-9d49-46c4-a48a-52bb37b082ab", "inference-object-delete")
	spec.ModelMaterialization = &ports.PlatformWorkloadModelMaterialization{TenantID: tenant}
	if err := runtime.Delete(context.Background(), tenant, "workload-object-delete", spec); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	joined := strings.Join(paths, "\n")
	if strings.Contains(joined, "/serviceaccounts/ani-inference-fetcher") || strings.Contains(joined, "/certificates/ani-model-fetcher-") {
		t.Fatalf("Delete() removed tenant-shared fetcher identity: %v", paths)
	}
}

func TestKubernetesPlatformWorkloadRuntimeDeleteLeaderWorkerRemovesGeneratedStatefulSets(t *testing.T) {
	var paths []string
	client := newTestKubernetesRESTClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodDelete {
			return jsonResponse(http.StatusOK, `{"kind":"Status","status":"Success"}`), nil
		}
		return jsonResponse(http.StatusNotFound, `{"kind":"Status","status":"Failure"}`), nil
	}))
	spec := sampleLeaderWorkerPlatformWorkloadSpec("6df72d71-9d49-46c4-a48a-52bb37b082ab", "inference-lws")
	if err := NewKubernetesPlatformWorkloadRuntime(client).Delete(context.Background(), "11111111-1111-1111-1111-111111111111", "workload-lws-1", spec); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	joined := strings.Join(paths, "\n")
	ns := "/namespaces/ani-tenant-11111111-1111-1111-1111-111111111111/"
	for _, want := range []string{
		"DELETE /apis/leaderworkerset.x-k8s.io/v1" + ns + "leaderworkersets/inference-lws",
		"DELETE /apis/apps/v1" + ns + "statefulsets/inference-lws",
		"DELETE /apis/apps/v1" + ns + "statefulsets/inference-lws-0",
		"DELETE /apis/scheduling.volcano.sh/v1beta1" + ns + "podgroups/inference-lws",
		"DELETE /api/v1" + ns + "services/inference-lws-http",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("Delete() paths = %v, want %s", paths, want)
		}
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
