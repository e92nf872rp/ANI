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

	"github.com/kubercloud/ani/pkg/adapters/resilience"
	"github.com/kubercloud/ani/pkg/ports"
)

func TestKubernetesLifecycleExecutorScalesDeploymentStartStop(t *testing.T) {
	var requests []string
	executor := newTestLifecycleExecutor(t, func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.Method+" "+r.URL.String())
		if r.Method != http.MethodPatch {
			t.Fatalf("method = %s, want PATCH", r.Method)
		}
		if !strings.Contains(r.URL.Path, "/scale") {
			t.Fatalf("path = %q, want scale subresource", r.URL.Path)
		}
		return lifecycleResponse(), nil
	})
	record := lifecycleRecord()

	if _, err := executor.Apply(context.Background(), lifecycleRequest(ports.WorkloadLifecycleStop), record); err != nil {
		t.Fatalf("Stop Apply() error = %v", err)
	}
	if _, err := executor.Apply(context.Background(), lifecycleRequest(ports.WorkloadLifecycleStart), record); err != nil {
		t.Fatalf("Start Apply() error = %v", err)
	}
	if len(requests) != 2 {
		t.Fatalf("requests = %#v, want stop and start", requests)
	}
}

func TestKubernetesLifecycleExecutorUsesKubeVirtStartStopSubresources(t *testing.T) {
	var requests []string
	executor := newTestLifecycleExecutor(t, func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if r.Method != http.MethodPut {
			t.Fatalf("method = %s, want PUT", r.Method)
		}
		return lifecycleResponse(), nil
	})
	record := lifecycleRecord()
	record.Kind = ports.WorkloadKindVM
	record.Name = "vm-01"
	record.Provider = "kubevirt"
	record.ResourceRefs = []string{"kubevirt/VirtualMachine/vm-01"}

	if _, err := executor.Apply(context.Background(), lifecycleRequest(ports.WorkloadLifecycleStop), record); err != nil {
		t.Fatalf("Stop Apply() error = %v", err)
	}
	if _, err := executor.Apply(context.Background(), lifecycleRequest(ports.WorkloadLifecycleStart), record); err != nil {
		t.Fatalf("Start Apply() error = %v", err)
	}

	want := []string{
		"PUT /apis/subresources.kubevirt.io/v1/namespaces/ani-tenant-tenant-a/virtualmachines/vm-01/stop",
		"PUT /apis/subresources.kubevirt.io/v1/namespaces/ani-tenant-tenant-a/virtualmachines/vm-01/start",
	}
	if strings.Join(requests, "\n") != strings.Join(want, "\n") {
		t.Fatalf("requests = %#v, want %#v", requests, want)
	}
}

func TestKubernetesLifecycleExecutorHotplugsPVCIntoKubeVirtVM(t *testing.T) {
	var gotPath string
	var gotBody string
	executor := newTestLifecycleExecutor(t, func(r *http.Request) (*http.Response, error) {
		gotPath = r.URL.Path
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		gotBody = string(body)
		if r.Method != http.MethodPut {
			t.Fatalf("method = %s, want PUT", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("content type = %q, want application/json", r.Header.Get("Content-Type"))
		}
		return lifecycleResponse(), nil
	})
	record := lifecycleRecord()
	record.Kind = ports.WorkloadKindVM
	record.Name = "vm-01"
	record.Provider = "kubevirt"
	record.ResourceRefs = []string{"kubevirt/VirtualMachine/vm-01"}
	req := lifecycleRequest(ports.WorkloadLifecycleAttachVolume)
	req.VolumeID = "vol_eec81c75-9204-419f-a7c9-00602812959c"
	req.ReadOnly = boolPointer(true)

	result, err := executor.Apply(context.Background(), req, record)
	if err != nil {
		t.Fatalf("AttachVolume Apply() error = %v", err)
	}
	if !result.Accepted {
		t.Fatalf("Accepted = false, reason = %s", result.Reason)
	}
	if gotPath != "/apis/subresources.kubevirt.io/v1/namespaces/ani-tenant-tenant-a/virtualmachines/vm-01/addvolume" {
		t.Fatalf("path = %q, want VM addvolume subresource", gotPath)
	}
	for _, want := range []string{
		`"name":"volume-vol-eec81c75-9204-419f-a7c9-00602812959c"`,
		`"disk":{"bus":"virtio"}`,
		`"claimName":"vol-vol-eec81c75-9204-419f-a7c9-00602812959c"`,
		`"readOnly":true`,
	} {
		if !strings.Contains(gotBody, want) {
			t.Fatalf("body = %s, want %s", gotBody, want)
		}
	}
}

func TestKubernetesLifecycleExecutorRemovesAttachedKubeVirtVMVolumeByAttachmentName(t *testing.T) {
	var gotPath string
	var gotBody string
	executor := newTestLifecycleExecutor(t, func(r *http.Request) (*http.Response, error) {
		gotPath = r.URL.Path
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		gotBody = string(body)
		return lifecycleResponse(), nil
	})
	record := lifecycleRecord()
	record.Kind = ports.WorkloadKindVM
	record.Name = "vm-01"
	record.Provider = "kubevirt"
	record.ResourceRefs = []string{"kubevirt/VirtualMachine/vm-01"}
	record.Status.Storage = []ports.WorkloadStorageAttachment{{
		Name: "existing-data-disk", ResourceType: "volume", ResourceID: "vol_data_a",
	}}
	req := lifecycleRequest(ports.WorkloadLifecycleDetachVolume)
	req.VolumeID = "vol_data_a"

	result, err := executor.Apply(context.Background(), req, record)
	if err != nil {
		t.Fatalf("DetachVolume Apply() error = %v", err)
	}
	if !result.Accepted {
		t.Fatalf("Accepted = false, reason = %s", result.Reason)
	}
	if gotPath != "/apis/subresources.kubevirt.io/v1/namespaces/ani-tenant-tenant-a/virtualmachines/vm-01/removevolume" {
		t.Fatalf("path = %q, want VM removevolume subresource", gotPath)
	}
	if gotBody != `{"name":"existing-data-disk"}` {
		t.Fatalf("body = %s, want existing attachment name", gotBody)
	}
}

func TestKubernetesLifecycleExecutorTreatsAlreadyRunningStartAsSuccess(t *testing.T) {
	executor := newTestLifecycleExecutor(t, func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusConflict,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(
				`{"kind":"Status","status":"Failure","message":"VM is already running","reason":"Conflict","code":409}`,
			)),
		}, nil
	})
	record := lifecycleRecord()
	record.Kind = ports.WorkloadKindVM
	record.Name = "vm-01"
	record.Provider = "kubevirt"
	record.ResourceRefs = []string{"kubevirt/VirtualMachine/vm-01"}

	result, err := executor.Apply(context.Background(), lifecycleRequest(ports.WorkloadLifecycleStart), record)
	if err != nil {
		t.Fatalf("Start Apply() error = %v", err)
	}
	if !result.Accepted {
		t.Fatalf("Accepted = false, want idempotent start success")
	}
}

func TestKubernetesLifecycleExecutorDeletesResource(t *testing.T) {
	var got string
	executor := newTestLifecycleExecutor(t, func(r *http.Request) (*http.Response, error) {
		got = r.Method + " " + r.URL.Path
		return lifecycleResponse(), nil
	})
	result, err := executor.Apply(context.Background(), lifecycleRequest(ports.WorkloadLifecycleDelete), lifecycleRecord())
	if err != nil {
		t.Fatalf("Delete Apply() error = %v", err)
	}
	if !result.Accepted {
		t.Fatalf("Accepted = false, reason = %s", result.Reason)
	}
	if !strings.HasPrefix(got, "DELETE /apis/apps/v1/namespaces/ani-tenant-tenant-a/deployments/app-01") {
		t.Fatalf("request = %q, want deployment delete", got)
	}
}

func TestKubernetesLifecycleExecutorDeletesAllReferencedResources(t *testing.T) {
	var requests []string
	executor := newTestLifecycleExecutor(t, func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		return lifecycleResponse(), nil
	})
	record := lifecycleRecord()
	record.ResourceRefs = append(record.ResourceRefs, "kubernetes/Secret/ani-wi-instance-a")

	if _, err := executor.Apply(context.Background(), lifecycleRequest(ports.WorkloadLifecycleDelete), record); err != nil {
		t.Fatalf("Delete Apply() error = %v", err)
	}
	if len(requests) != 2 {
		t.Fatalf("requests = %#v, want deployment and secret deletes", requests)
	}
	if !strings.Contains(requests[1], "/api/v1/namespaces/ani-tenant-tenant-a/secrets/ani-wi-instance-a") {
		t.Fatalf("second request = %q, want workload identity secret delete", requests[1])
	}
}

func TestKubernetesLifecycleExecutorDeleteIgnoresAlreadyMissingResources(t *testing.T) {
	var requests []string
	executor := newTestLifecycleExecutor(t, func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if strings.Contains(r.URL.Path, "/deployments/") {
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"reason":"NotFound"}`)),
			}, nil
		}
		return lifecycleResponse(), nil
	})
	record := lifecycleRecord()
	record.ResourceRefs = append(record.ResourceRefs, "kubernetes/Secret/ani-wi-instance-a")

	result, err := executor.Apply(context.Background(), lifecycleRequest(ports.WorkloadLifecycleDelete), record)
	if err != nil {
		t.Fatalf("Delete Apply() error = %v", err)
	}
	if !result.Accepted || len(requests) != 2 {
		t.Fatalf("result = %+v requests = %#v, want accepted cleanup of both refs", result, requests)
	}
}

func TestKubernetesLifecycleExecutorDeleteAttemptsAllResourcesAndJoinsErrors(t *testing.T) {
	errDeployment := errors.New("deployment delete failed")
	errPVC := errors.New("pvc delete failed")
	var requests []string
	executor := newTestLifecycleExecutor(t, func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.URL.Path)
		switch {
		case strings.Contains(r.URL.Path, "/deployments/"):
			return nil, errDeployment
		case strings.Contains(r.URL.Path, "/persistentvolumeclaims/"):
			return nil, errPVC
		default:
			return lifecycleResponse(), nil
		}
	})
	record := lifecycleRecord()
	record.ResourceRefs = append(record.ResourceRefs,
		"kubernetes/Secret/ani-wi-instance-a",
		"kubernetes/PersistentVolumeClaim/app-data",
	)

	_, err := executor.Apply(context.Background(), lifecycleRequest(ports.WorkloadLifecycleDelete), record)
	if len(requests) != 3 {
		t.Fatalf("requests = %#v, want all three deletes", requests)
	}
	if !errors.Is(err, errDeployment) || !errors.Is(err, errPVC) {
		t.Fatalf("Delete Apply() error = %v, want both delete errors", err)
	}
}

func TestKubernetesLifecycleExecutorDisabledDoesNotCallProvider(t *testing.T) {
	called := false
	client := newLifecycleRESTClient(t, func(r *http.Request) (*http.Response, error) {
		called = true
		return lifecycleResponse(), nil
	})
	executor := NewKubernetesLifecycleExecutor(client)
	result, err := executor.Apply(context.Background(), lifecycleRequest(ports.WorkloadLifecycleStart), lifecycleRecord())
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if result.Accepted {
		t.Fatalf("Accepted = true, want disabled")
	}
	if called {
		t.Fatalf("provider called while lifecycle executor disabled")
	}
}

func newTestLifecycleExecutor(t *testing.T, roundTrip roundTripFunc) *KubernetesLifecycleExecutor {
	t.Helper()
	return NewKubernetesLifecycleExecutor(
		newLifecycleRESTClient(t, roundTrip),
		WithKubernetesLifecycleEnabled(true),
		WithKubernetesLifecycleClock(func() time.Time { return time.Unix(1000, 0) }),
	)
}

func newLifecycleRESTClient(t *testing.T, roundTrip roundTripFunc) *KubernetesRESTClient {
	t.Helper()
	client, err := NewKubernetesRESTClient(KubernetesRESTClientConfig{
		Host:       "https://kubernetes.example.test",
		HTTPClient: &http.Client{Transport: roundTrip},
		Now:        func() time.Time { return time.Unix(1000, 0) },
	})
	if err != nil {
		t.Fatalf("NewKubernetesRESTClient() error = %v", err)
	}
	return client
}

func lifecycleRecord() ports.WorkloadInstanceRecord {
	return ports.WorkloadInstanceRecord{
		TenantID:     "tenant-a",
		InstanceID:   "instance-a",
		Name:         "app-01",
		Kind:         ports.WorkloadKindContainer,
		Provider:     "kubernetes",
		ResourceRefs: []string{"kubernetes/Deployment/app-01"},
		Status: ports.WorkloadStatus{
			State: ports.WorkloadStateRunning,
		},
	}
}

func lifecycleRequest(action ports.WorkloadLifecycleAction) ports.WorkloadInstanceLifecycleRequest {
	return ports.WorkloadInstanceLifecycleRequest{
		TenantID:        "tenant-a",
		InstanceID:      "instance-a",
		Action:          action,
		UserID:          "user-a",
		PermissionProof: "rbac:update:workload",
	}
}

func lifecycleResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{}`)),
	}
}

// lifecycleVMStoppedResponse reports the terminal "Stopped" printableStatus:
// the state waitKubeVirtVMStopped requires before the follow-up start.
func lifecycleVMStoppedResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"status":{"printableStatus":"Stopped"}}`)),
	}
}

func TestKubernetesLifecycleExecutorScalesDeploymentReplicas(t *testing.T) {
	var gotPath, gotBody string
	executor := newTestLifecycleExecutor(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPatch {
			t.Fatalf("method = %s, want PATCH", r.Method)
		}
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		return lifecycleResponse(), nil
	})
	record := lifecycleRecord()
	req := lifecycleRequest(ports.WorkloadLifecycleScale)
	three := int32(3)
	req.Replicas = &three

	result, err := executor.Apply(context.Background(), req, record)
	if err != nil {
		t.Fatalf("Scale Apply() error = %v", err)
	}
	if !result.Accepted {
		t.Fatalf("Accepted = false, reason = %s", result.Reason)
	}
	wantPath := "/apis/apps/v1/namespaces/ani-tenant-tenant-a/deployments/app-01/scale"
	if gotPath != wantPath {
		t.Fatalf("path = %q, want %q", gotPath, wantPath)
	}
	if !strings.Contains(gotBody, `"replicas":3`) {
		t.Fatalf("body = %q, want spec replicas=3", gotBody)
	}
}

func TestKubernetesLifecycleExecutorScaleRejectsNonPositiveReplicas(t *testing.T) {
	executor := newTestLifecycleExecutor(t, func(r *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected request issued for invalid scale: %s %s", r.Method, r.URL.Path)
		return lifecycleResponse(), nil
	})
	record := lifecycleRecord()
	req := lifecycleRequest(ports.WorkloadLifecycleScale)
	zero := int32(0)
	req.Replicas = &zero

	_, err := executor.Apply(context.Background(), req, record)
	if err == nil {
		t.Fatalf("expected error for replicas=0 scale, got nil")
	}
	if !errors.Is(err, ports.ErrInvalid) {
		t.Fatalf("error = %v, want ErrInvalid", err)
	}
}

func TestKubernetesLifecycleExecutorResizeWithoutSpecIDPatchesCPUAndMemory(t *testing.T) {
	var gotPath, gotBody, gotContentType string
	executor := newTestLifecycleExecutor(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPatch {
			t.Fatalf("method = %s, want PATCH", r.Method)
		}
		gotPath = r.URL.Path
		gotContentType = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		return lifecycleResponse(), nil
	})
	record := lifecycleRecord()
	req := lifecycleRequest(ports.WorkloadLifecycleResize)
	req.Resources = ports.WorkloadResourceRequest{CPU: "4", Memory: "8Gi"}

	result, err := executor.Apply(context.Background(), req, record)
	if err != nil {
		t.Fatalf("Resize Apply() error = %v", err)
	}
	if !result.Accepted {
		t.Fatalf("Accepted = false, reason = %s", result.Reason)
	}
	wantPath := "/apis/apps/v1/namespaces/ani-tenant-tenant-a/deployments/app-01"
	if gotPath != wantPath {
		t.Fatalf("path = %q, want %q", gotPath, wantPath)
	}
	if gotContentType != "application/strategic-merge-patch+json" {
		t.Fatalf("content-type = %q, want strategic-merge-patch", gotContentType)
	}
	for _, want := range []string{`"cpu":"4"`, `"memory":"8Gi"`} {
		if !strings.Contains(gotBody, want) {
			t.Fatalf("body = %s, want %s", gotBody, want)
		}
	}
}

func TestKubernetesLifecycleExecutorResizeWithSpecIDTranslatesAndPatches(t *testing.T) {
	var gotBody string
	executor := newTestLifecycleExecutor(t, func(r *http.Request) (*http.Response, error) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		return lifecycleResponse(), nil
	})
	executor.translator = NewVolcanoResourceTranslator(byIDSpecStore{
		specs: map[string]ports.GPUSpecCRD{
			"spec-vgpu-4090": {
				ID:         "spec-vgpu-4090",
				GPUType:    "NVIDIA-RTX-4090",
				GPUMode:    "vgpu",
				Shares:     4,
				MBPerShare: 12280,
				NodeAffinity: ports.GPUSpecNodeAffinity{
					GPUMode:          "vgpu",
					GPUSharingSpec:   "NVIDIA-RTX-4090-12285MiB",
					GPUSharingPolicy: "quarter",
				},
				VolcanoResources: ports.GPUSpecVolcanoResources{
					VGPU: map[string]string{
						"volcano.sh/vgpu-number": "{count}",
						"volcano.sh/vgpu-memory": "{mb_per_share}",
						"volcano.sh/vgpu-cores":  "1",
					},
				},
			},
		},
	})
	record := lifecycleRecord()
	record.Kind = ports.WorkloadKindGPUContainer
	record.GPU = &ports.GPUInstanceStatus{Count: 1, QueueName: "ani-inference"}
	req := lifecycleRequest(ports.WorkloadLifecycleResize)
	req.SpecID = "spec-vgpu-4090"

	result, err := executor.Apply(context.Background(), req, record)
	if err != nil {
		t.Fatalf("Resize Apply() error = %v", err)
	}
	if !result.Accepted {
		t.Fatalf("Accepted = false, reason = %s", result.Reason)
	}
	for _, want := range []string{
		`"schedulerName":"volcano"`,
		`"scheduling.volcano.sh/queue-name":"ani-inference"`,
		`"volcano.sh/vgpu-number":"1"`,
		`"volcano.sh/vgpu-memory":"1228"`,
		`"volcano.sh/vgpu-cores":"1"`,
		`"ani.kubercloud.io/gpu-sharing-spec":"NVIDIA-RTX-4090-12285MiB"`,
	} {
		if !strings.Contains(gotBody, want) {
			t.Fatalf("body = %s, want %s", gotBody, want)
		}
	}
}

func TestKubernetesLifecycleExecutorResizeWithSpecIDRequiresTranslator(t *testing.T) {
	executor := newTestLifecycleExecutor(t, func(r *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected request issued without translator: %s %s", r.Method, r.URL.Path)
		return lifecycleResponse(), nil
	})
	record := lifecycleRecord()
	record.Kind = ports.WorkloadKindGPUContainer
	record.GPU = &ports.GPUInstanceStatus{Count: 1}
	req := lifecycleRequest(ports.WorkloadLifecycleResize)
	req.SpecID = "spec-vgpu-4090"

	_, err := executor.Apply(context.Background(), req, record)
	if err == nil {
		t.Fatalf("expected error for spec_id resize without translator, got nil")
	}
	if !errors.Is(err, ports.ErrNotConfigured) {
		t.Fatalf("error = %v, want ErrNotConfigured", err)
	}
}

func TestKubernetesLifecycleExecutorResizeSpecSwitchReplacesNodeSelector(t *testing.T) {
	var gotBody string
	executor := newTestLifecycleExecutor(t, func(r *http.Request) (*http.Response, error) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		return lifecycleResponse(), nil
	})
	executor.translator = NewVolcanoResourceTranslator(byIDSpecStore{
		specs: map[string]ports.GPUSpecCRD{
			"spec-vgpu-4090": {
				ID:         "spec-vgpu-4090",
				GPUType:    "NVIDIA-RTX-4090",
				GPUMode:    "vgpu",
				Shares:     4,
				MBPerShare: 12280,
				NodeAffinity: ports.GPUSpecNodeAffinity{
					GPUMode:          "vgpu",
					GPUSharingSpec:   "NVIDIA-RTX-4090-12285MiB",
					GPUSharingPolicy: "quarter",
				},
				VolcanoResources: ports.GPUSpecVolcanoResources{
					VGPU: map[string]string{
						"volcano.sh/vgpu-number": "{count}",
						"volcano.sh/vgpu-memory": "{mb_per_share}",
					},
				},
			},
			"spec-whole-4090": {
				ID:      "spec-whole-4090",
				GPUType: "NVIDIA-RTX-4090",
				GPUMode: "wholecard",
				Shares:  1,
				NodeAffinity: ports.GPUSpecNodeAffinity{
					GPUMode: "wholecard",
					GPUSpec: "NVIDIA-RTX-4090-49140MiB",
				},
				VolcanoResources: ports.GPUSpecVolcanoResources{
					Wholecard: map[string]string{
						"nvidia.com/gpu": "{count}",
					},
				},
			},
		},
	})
	record := lifecycleRecord()
	record.Kind = ports.WorkloadKindGPUContainer
	record.GPU = &ports.GPUInstanceStatus{Count: 1, QueueName: "ani-inference"}
	req := lifecycleRequest(ports.WorkloadLifecycleResize)
	req.SpecID = "spec-whole-4090"

	if _, err := executor.Apply(context.Background(), req, record); err != nil {
		t.Fatalf("Resize Apply() error = %v", err)
	}
	for _, want := range []string{
		`"$patch":"replace"`,
		`"ani.kubercloud.io/gpu-mode":"wholecard"`,
		`"ani.kubercloud.io/gpu-spec":"NVIDIA-RTX-4090-49140MiB"`,
		`"nvidia.com/gpu":"1"`,
	} {
		if !strings.Contains(gotBody, want) {
			t.Fatalf("body = %s, want %s", gotBody, want)
		}
	}
	for _, stale := range []string{
		`"ani.kubercloud.io/gpu-sharing-policy"`,
		`"ani.kubercloud.io/gpu-sharing-spec"`,
	} {
		if strings.Contains(gotBody, stale) {
			t.Fatalf("body = %s, must not contain stale %s", gotBody, stale)
		}
	}
	// vGPU resource keys must be cleared via null (strategic-merge delete),
	// never requested with a real value alongside the wholecard resource.
	for _, stale := range []string{
		`"volcano.sh/vgpu-number":"1"`,
		`"volcano.sh/vgpu-memory":"1228"`,
		`"volcano.sh/vgpu-cores":"1"`,
	} {
		if strings.Contains(gotBody, stale) {
			t.Fatalf("body = %s, must not request stale %s", gotBody, stale)
		}
	}
	for _, want := range []string{
		`"volcano.sh/vgpu-number":null`,
		`"volcano.sh/vgpu-memory":null`,
		`"volcano.sh/vgpu-cores":null`,
	} {
		if !strings.Contains(gotBody, want) {
			t.Fatalf("body = %s, want vGPU key cleared via %s", gotBody, want)
		}
	}
}

func TestKubernetesLifecycleExecutorResizeVMWithSpecIDUnsupported(t *testing.T) {
	executor := newTestLifecycleExecutor(t, func(r *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected request issued for VM spec resize: %s %s", r.Method, r.URL.Path)
		return lifecycleResponse(), nil
	})
	record := lifecycleRecord()
	record.Kind = ports.WorkloadKindVM
	record.Name = "vm-01"
	record.Provider = "kubevirt"
	record.ResourceRefs = []string{"kubevirt/VirtualMachine/vm-01"}
	req := lifecycleRequest(ports.WorkloadLifecycleResize)
	req.SpecID = "spec-vgpu-4090"

	_, err := executor.Apply(context.Background(), req, record)
	if err == nil {
		t.Fatalf("expected error for VM spec_id resize, got nil")
	}
	if !errors.Is(err, ports.ErrUnsupported) {
		t.Fatalf("error = %v, want ErrUnsupported", err)
	}
}

func TestKubernetesLifecycleExecutorResizeVMWithoutSpecIDRestarts(t *testing.T) {
	var requests []string
	executor := newTestLifecycleExecutor(t, func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		return lifecycleVMStoppedResponse(), nil
	})
	record := lifecycleRecord()
	record.Kind = ports.WorkloadKindVM
	record.Name = "vm-01"
	record.Provider = "kubevirt"
	record.ResourceRefs = []string{"kubevirt/VirtualMachine/vm-01"}
	req := lifecycleRequest(ports.WorkloadLifecycleResize)
	req.Resources = ports.WorkloadResourceRequest{CPU: "4", Memory: "8Gi"}

	if _, err := executor.Apply(context.Background(), req, record); err != nil {
		t.Fatalf("Resize Apply() error = %v", err)
	}
	want := []string{
		"PUT /apis/subresources.kubevirt.io/v1/namespaces/ani-tenant-tenant-a/virtualmachines/vm-01/stop",
		"GET /apis/kubevirt.io/v1/namespaces/ani-tenant-tenant-a/virtualmachines/vm-01",
		"PUT /apis/subresources.kubevirt.io/v1/namespaces/ani-tenant-tenant-a/virtualmachines/vm-01/start",
	}
	if strings.Join(requests, "\n") != strings.Join(want, "\n") {
		t.Fatalf("requests = %#v, want %#v", requests, want)
	}
}

func TestKubernetesLifecycleExecutorRestartsKubeVirtVMViaStopWaitStart(t *testing.T) {
	var requests []string
	executor := newTestLifecycleExecutor(t, func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		return lifecycleVMStoppedResponse(), nil
	})
	record := lifecycleRecord()
	record.Kind = ports.WorkloadKindVM
	record.Name = "vm-01"
	record.Provider = "kubevirt"
	record.ResourceRefs = []string{"kubevirt/VirtualMachine/vm-01"}

	if _, err := executor.Apply(context.Background(), lifecycleRequest(ports.WorkloadLifecycleRestart), record); err != nil {
		t.Fatalf("Restart Apply() error = %v", err)
	}
	want := []string{
		"PUT /apis/subresources.kubevirt.io/v1/namespaces/ani-tenant-tenant-a/virtualmachines/vm-01/stop",
		"GET /apis/kubevirt.io/v1/namespaces/ani-tenant-tenant-a/virtualmachines/vm-01",
		"PUT /apis/subresources.kubevirt.io/v1/namespaces/ani-tenant-tenant-a/virtualmachines/vm-01/start",
	}
	if strings.Join(requests, "\n") != strings.Join(want, "\n") {
		t.Fatalf("requests = %#v, want stop-wait-start sequence %#v", requests, want)
	}
}

func TestKubernetesLifecycleExecutorRestartWaitsForVMToStopBeforeStart(t *testing.T) {
	var gets int
	var requests []string
	executor := newTestLifecycleExecutor(t, func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodGet {
			gets++
			if gets == 1 {
				// First observation still reports Running: shutdown has not landed yet.
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"status":{"printableStatus":"Running"}}`)),
				}, nil
			}
			// Shutdown landed: report the terminal Stopped status.
			return lifecycleVMStoppedResponse(), nil
		}
		return lifecycleVMStoppedResponse(), nil
	})
	record := lifecycleRecord()
	record.Kind = ports.WorkloadKindVM
	record.Name = "vm-01"
	record.Provider = "kubevirt"
	record.ResourceRefs = []string{"kubevirt/VirtualMachine/vm-01"}

	if _, err := executor.Apply(context.Background(), lifecycleRequest(ports.WorkloadLifecycleRestart), record); err != nil {
		t.Fatalf("Restart Apply() error = %v", err)
	}
	want := []string{
		"PUT /apis/subresources.kubevirt.io/v1/namespaces/ani-tenant-tenant-a/virtualmachines/vm-01/stop",
		"GET /apis/kubevirt.io/v1/namespaces/ani-tenant-tenant-a/virtualmachines/vm-01",
		"GET /apis/kubevirt.io/v1/namespaces/ani-tenant-tenant-a/virtualmachines/vm-01",
		"PUT /apis/subresources.kubevirt.io/v1/namespaces/ani-tenant-tenant-a/virtualmachines/vm-01/start",
	}
	if strings.Join(requests, "\n") != strings.Join(want, "\n") {
		t.Fatalf("requests = %#v, want stop-poll-poll-start sequence %#v", requests, want)
	}
}

func TestKubernetesLifecycleExecutorRestartTimesOutWhenVMNeverStops(t *testing.T) {
	var requests []string
	now := time.Unix(1000, 0)
	clock := func() time.Time {
		now = now.Add(kubeVirtRestartStopTimeout + time.Minute)
		return now
	}
	executor := NewKubernetesLifecycleExecutor(
		newLifecycleRESTClient(t, func(r *http.Request) (*http.Response, error) {
			requests = append(requests, r.Method+" "+r.URL.Path)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"status":{"printableStatus":"Running"}}`)),
			}, nil
		}),
		WithKubernetesLifecycleEnabled(true),
		WithKubernetesLifecycleClock(clock),
	)
	record := lifecycleRecord()
	record.Kind = ports.WorkloadKindVM
	record.Name = "vm-01"
	record.Provider = "kubevirt"
	record.ResourceRefs = []string{"kubevirt/VirtualMachine/vm-01"}

	_, err := executor.Apply(context.Background(), lifecycleRequest(ports.WorkloadLifecycleRestart), record)
	if !errors.Is(err, ports.ErrConflict) {
		t.Fatalf("error = %v, want ErrConflict", err)
	}
	for _, req := range requests {
		if strings.HasSuffix(req, "/start") {
			t.Fatalf("start issued before VM stopped: requests = %#v", requests)
		}
	}
}

func TestKubernetesLifecycleExecutorSnapshotVMCreatesKubeVirtSnapshot(t *testing.T) {
	var requests []string
	executor := newTestLifecycleExecutor(t, func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodGet {
			return crPhaseResponse("Succeeded"), nil
		}
		return lifecycleResponse(), nil
	})
	record := lifecycleRecord()
	record.Kind = ports.WorkloadKindVM
	record.Name = "vm-01"
	record.Provider = "kubevirt"
	record.ResourceRefs = []string{"kubevirt/VirtualMachine/vm-01"}
	req := lifecycleRequest(ports.WorkloadLifecycleSnapshot)
	req.IdempotencyKey = "snap-key-01"
	req.SnapshotName = "before-upgrade"

	if _, err := executor.Apply(context.Background(), req, record); err != nil {
		t.Fatalf("Snapshot Apply() error = %v", err)
	}
	want := []string{
		"PATCH /apis/snapshot.kubevirt.io/v1beta1/namespaces/ani-tenant-tenant-a/virtualmachinesnapshots/snap-key-01",
		"GET /apis/snapshot.kubevirt.io/v1beta1/namespaces/ani-tenant-tenant-a/virtualmachinesnapshots/snap-key-01",
	}
	if strings.Join(requests, "\n") != strings.Join(want, "\n") {
		t.Fatalf("requests = %#v, want %#v", requests, want)
	}
}

func TestKubernetesLifecycleExecutorRollbackVMCreatesKubeVirtRestore(t *testing.T) {
	var requests []string
	executor := newTestLifecycleExecutor(t, func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodGet {
			if strings.Contains(r.URL.Path, "/virtualmachinerestores/") {
				return crCompleteResponse(), nil
			}
			return crPhaseResponse("Succeeded"), nil
		}
		return lifecycleResponse(), nil
	})
	record := lifecycleRecord()
	record.Kind = ports.WorkloadKindVM
	record.Name = "vm-01"
	record.Provider = "kubevirt"
	record.ResourceRefs = []string{"kubevirt/VirtualMachine/vm-01"}
	req := lifecycleRequest(ports.WorkloadLifecycleRollback)
	req.SnapshotID = "snap_1fc44f4a-70ea"
	req.IdempotencyKey = "rollback-key-01"

	if _, err := executor.Apply(context.Background(), req, record); err != nil {
		t.Fatalf("Rollback Apply() error = %v", err)
	}
	want := []string{
		"GET /apis/snapshot.kubevirt.io/v1beta1/namespaces/ani-tenant-tenant-a/virtualmachinesnapshots/snap-1fc44f4a-70ea",
		"GET /apis/kubevirt.io/v1/namespaces/ani-tenant-tenant-a/virtualmachines/vm-01",
		"PATCH /apis/snapshot.kubevirt.io/v1beta1/namespaces/ani-tenant-tenant-a/virtualmachinerestores/restore-snap-1fc44f4a-70ea-rollback-key-01",
		"GET /apis/snapshot.kubevirt.io/v1beta1/namespaces/ani-tenant-tenant-a/virtualmachinerestores/restore-snap-1fc44f4a-70ea-rollback-key-01",
	}
	if strings.Join(requests, "\n") != strings.Join(want, "\n") {
		t.Fatalf("requests = %#v, want %#v", requests, want)
	}
}

// Running-VM rollback must power the VM off before KubeVirt will restore it,
// then start it again after the restore completes.
func TestKubernetesLifecycleExecutorRollbackRunningVMStopsRestoresStarts(t *testing.T) {
	var requests []string
	var vmGets int
	executor := newTestLifecycleExecutor(t, func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/virtualmachines/vm-01"):
			vmGets++
			if vmGets == 1 {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"status":{"printableStatus":"Running"}}`)),
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"status":{"printableStatus":"Stopped"}}`)),
			}, nil
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/virtualmachinerestores/"):
			return crCompleteResponse(), nil
		case r.Method == http.MethodGet:
			return crPhaseResponse("Succeeded"), nil
		default:
			return lifecycleResponse(), nil
		}
	})
	record := lifecycleRecord()
	record.Kind = ports.WorkloadKindVM
	record.Name = "vm-01"
	record.Provider = "kubevirt"
	record.ResourceRefs = []string{"kubevirt/VirtualMachine/vm-01"}
	req := lifecycleRequest(ports.WorkloadLifecycleRollback)
	req.SnapshotID = "snap-1fc44f4a-70ea"
	req.IdempotencyKey = "rollback-key-04"

	if _, err := executor.Apply(context.Background(), req, record); err != nil {
		t.Fatalf("Rollback Apply() error = %v", err)
	}
	want := []string{
		"GET /apis/snapshot.kubevirt.io/v1beta1/namespaces/ani-tenant-tenant-a/virtualmachinesnapshots/snap-1fc44f4a-70ea",
		"GET /apis/kubevirt.io/v1/namespaces/ani-tenant-tenant-a/virtualmachines/vm-01",
		"PUT /apis/subresources.kubevirt.io/v1/namespaces/ani-tenant-tenant-a/virtualmachines/vm-01/stop",
		"GET /apis/kubevirt.io/v1/namespaces/ani-tenant-tenant-a/virtualmachines/vm-01",
		"PATCH /apis/snapshot.kubevirt.io/v1beta1/namespaces/ani-tenant-tenant-a/virtualmachinerestores/restore-snap-1fc44f4a-70ea-rollback-key-04",
		"GET /apis/snapshot.kubevirt.io/v1beta1/namespaces/ani-tenant-tenant-a/virtualmachinerestores/restore-snap-1fc44f4a-70ea-rollback-key-04",
		"PUT /apis/subresources.kubevirt.io/v1/namespaces/ani-tenant-tenant-a/virtualmachines/vm-01/start",
	}
	if strings.Join(requests, "\n") != strings.Join(want, "\n") {
		t.Fatalf("requests = %#v, want stop-restore-start sequence %#v", requests, want)
	}
}

func TestKubernetesLifecycleExecutorRollbackVMRejectsMissingProviderSnapshot(t *testing.T) {
	var requests []string
	executor := newTestLifecycleExecutor(t, func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodGet {
			return nil, &resilience.StatusError{
				StatusCode: http.StatusNotFound,
				Body:       `{"reason":"NotFound","message":"virtualmachinesnapshots not found"}`,
			}
		}
		return lifecycleResponse(), nil
	})
	record := lifecycleRecord()
	record.Kind = ports.WorkloadKindVM
	record.Name = "vm-01"
	record.Provider = "kubevirt"
	record.ResourceRefs = []string{"kubevirt/VirtualMachine/vm-01"}
	req := lifecycleRequest(ports.WorkloadLifecycleRollback)
	req.SnapshotID = "snap_metadata_only"
	req.IdempotencyKey = "rollback-key-02"

	_, err := executor.Apply(context.Background(), req, record)
	if !errors.Is(err, ports.ErrConflict) {
		t.Fatalf("error = %v, want ErrConflict", err)
	}
	for _, req := range requests {
		if strings.Contains(req, "virtualmachinerestores") {
			t.Fatalf("restore created without provider snapshot: requests = %#v", requests)
		}
	}
}

func TestKubernetesLifecycleExecutorRollbackVMFailsOnFailedSnapshot(t *testing.T) {
	// VirtualMachineRestore has no status.phase: a restore that never reaches
	// status.complete=true must surface as a conflict timeout instead of
	// blocking forever. A fast clock short-circuits the wait.
	var requests []string
	now := time.Unix(1000, 0)
	clock := func() time.Time {
		now = now.Add(kubeVirtRestoreTimeout + time.Minute)
		return now
	}
	executor := NewKubernetesLifecycleExecutor(
		newLifecycleRESTClient(t, func(r *http.Request) (*http.Response, error) {
			requests = append(requests, r.Method+" "+r.URL.Path)
			if r.Method == http.MethodGet {
				if strings.Contains(r.URL.Path, "/virtualmachinerestores/") {
					return crPhaseResponse("InProgress"), nil
				}
				return crPhaseResponse("Succeeded"), nil
			}
			return lifecycleResponse(), nil
		}),
		WithKubernetesLifecycleEnabled(true),
		WithKubernetesLifecycleClock(clock),
	)
	record := lifecycleRecord()
	record.Kind = ports.WorkloadKindVM
	record.Name = "vm-01"
	record.Provider = "kubevirt"
	record.ResourceRefs = []string{"kubevirt/VirtualMachine/vm-01"}
	req := lifecycleRequest(ports.WorkloadLifecycleRollback)
	req.SnapshotID = "snap_failed"
	req.IdempotencyKey = "rollback-key-03"

	_, err := executor.Apply(context.Background(), req, record)
	if !errors.Is(err, ports.ErrConflict) {
		t.Fatalf("error = %v, want ErrConflict", err)
	}
	sawRestore := false
	for _, req := range requests {
		if strings.Contains(req, "virtualmachinerestores") {
			sawRestore = true
		}
	}
	if !sawRestore {
		t.Fatalf("restore was never applied: requests = %#v", requests)
	}
}

// Rebuild recreates the VirtualMachine CR: stop (when running) → capture spec
// → delete → wait gone → re-apply → start. The containerDisk root is
// image-derived, so the recreated VMI boots a fresh system disk.
func TestKubernetesLifecycleExecutorRebuildRunningVMStopsDeletesReappliesStarts(t *testing.T) {
	var requests []string
	var vmGets int
	var patchBody string
	executor := newTestLifecycleExecutor(t, func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "/virtualmachines/vm-01"):
			body, _ := io.ReadAll(r.Body)
			patchBody = string(body)
			return lifecycleResponse(), nil
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/virtualmachines/vm-01"):
			vmGets++
			switch vmGets {
			case 1: // running check
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"status":{"printableStatus":"Running"}}`)),
				}, nil
			case 2: // wait-stopped poll
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"status":{"printableStatus":"Stopped"}}`)),
				}, nil
			case 3: // spec capture
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body: io.NopCloser(strings.NewReader(`{"metadata":{"name":"vm-01","namespace":"ani-tenant-tenant-a",` +
						`"labels":{"ani.dev/instance":"vm-01"},"annotations":{"ani.kubercloud.io/owner":"tenant-a"}},` +
						`"spec":{"running":false,"template":{"spec":{"domain":{}}}}}`)),
				}, nil
			default: // gone poll
				return nil, &resilience.StatusError{
					StatusCode: http.StatusNotFound,
					Body:       `{"reason":"NotFound","message":"virtualmachines not found"}`,
				}
			}
		default:
			return lifecycleResponse(), nil
		}
	})
	record := lifecycleRecord()
	record.Kind = ports.WorkloadKindVM
	record.Name = "vm-01"
	record.Provider = "kubevirt"
	record.ResourceRefs = []string{"kubevirt/VirtualMachine/vm-01"}
	req := lifecycleRequest(ports.WorkloadLifecycleRebuild)
	req.IdempotencyKey = "rebuild-key-01"

	if _, err := executor.Apply(context.Background(), req, record); err != nil {
		t.Fatalf("Rebuild Apply() error = %v", err)
	}
	want := []string{
		"GET /apis/kubevirt.io/v1/namespaces/ani-tenant-tenant-a/virtualmachines/vm-01",
		"PUT /apis/subresources.kubevirt.io/v1/namespaces/ani-tenant-tenant-a/virtualmachines/vm-01/stop",
		"GET /apis/kubevirt.io/v1/namespaces/ani-tenant-tenant-a/virtualmachines/vm-01",
		"GET /apis/kubevirt.io/v1/namespaces/ani-tenant-tenant-a/virtualmachines/vm-01",
		"DELETE /apis/kubevirt.io/v1/namespaces/ani-tenant-tenant-a/virtualmachines/vm-01",
		"GET /apis/kubevirt.io/v1/namespaces/ani-tenant-tenant-a/virtualmachines/vm-01",
		"PATCH /apis/kubevirt.io/v1/namespaces/ani-tenant-tenant-a/virtualmachines/vm-01",
		"PUT /apis/subresources.kubevirt.io/v1/namespaces/ani-tenant-tenant-a/virtualmachines/vm-01/start",
	}
	if strings.Join(requests, "\n") != strings.Join(want, "\n") {
		t.Fatalf("requests = %#v, want stop-capture-delete-reapply-start sequence %#v", requests, want)
	}
	// Server-side apply derives the object name from the body: a manifest
	// without metadata.name fails with HTTP 400 on the real API (VM-10).
	if !strings.Contains(patchBody, `"name":"vm-01"`) || !strings.Contains(patchBody, `"namespace":"ani-tenant-tenant-a"`) {
		t.Fatalf("re-apply manifest missing name/namespace: body = %s", patchBody)
	}
}

// Filesystem attach rewrites the VM spec with a virtiofs device plus its
// backing PVC volume; a running VM is stopped first and started again.
func TestKubernetesLifecycleExecutorAttachFilesystemRunningVMStopsAppliesStarts(t *testing.T) {
	var requests []string
	var vmGets int
	var patchBody string
	executor := newTestLifecycleExecutor(t, func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "/virtualmachines/vm-01"):
			body, _ := io.ReadAll(r.Body)
			patchBody = string(body)
			return lifecycleResponse(), nil
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/virtualmachines/vm-01"):
			vmGets++
			switch vmGets {
			case 1: // running check
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"status":{"printableStatus":"Running"}}`)),
				}, nil
			case 2: // wait-stopped poll
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"status":{"printableStatus":"Stopped"}}`)),
				}, nil
			case 3: // spec capture
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body: io.NopCloser(strings.NewReader(`{"metadata":{"name":"vm-01","namespace":"ani-tenant-tenant-a"},` +
						`"spec":{"running":false,"template":{"spec":{"domain":{"devices":{"disks":[{"name":"containerdisk","disk":{"bus":"virtio"}}]}},` +
						`"volumes":[{"name":"containerdisk","containerDisk":{"image":"rocky:10"}}]}}}}`)),
				}, nil
			default:
				return lifecycleResponse(), nil
			}
		default:
			return lifecycleResponse(), nil
		}
	})
	record := lifecycleRecord()
	record.Kind = ports.WorkloadKindVM
	record.Name = "vm-01"
	record.Provider = "kubevirt"
	record.ResourceRefs = []string{"kubevirt/VirtualMachine/vm-01"}
	req := lifecycleRequest(ports.WorkloadLifecycleAttachFilesystem)
	req.FilesystemID = "fs_dfe99bff-b68c-4528-88ce-ac8fe62ac734"
	req.MountPath = "/mnt/nfs"
	req.IdempotencyKey = "fs-attach-key-01"

	if _, err := executor.Apply(context.Background(), req, record); err != nil {
		t.Fatalf("AttachFilesystem Apply() error = %v", err)
	}
	want := []string{
		"GET /apis/kubevirt.io/v1/namespaces/ani-tenant-tenant-a/virtualmachines/vm-01",
		"PUT /apis/subresources.kubevirt.io/v1/namespaces/ani-tenant-tenant-a/virtualmachines/vm-01/stop",
		"GET /apis/kubevirt.io/v1/namespaces/ani-tenant-tenant-a/virtualmachines/vm-01",
		"GET /apis/kubevirt.io/v1/namespaces/ani-tenant-tenant-a/virtualmachines/vm-01",
		"PATCH /apis/kubevirt.io/v1/namespaces/ani-tenant-tenant-a/virtualmachines/vm-01",
		"PUT /apis/subresources.kubevirt.io/v1/namespaces/ani-tenant-tenant-a/virtualmachines/vm-01/start",
	}
	if strings.Join(requests, "\n") != strings.Join(want, "\n") {
		t.Fatalf("requests = %#v, want stop-apply-start sequence %#v", requests, want)
	}
	if !strings.Contains(patchBody, `"virtiofs":{}`) {
		t.Fatalf("re-apply manifest missing virtiofs device: body = %s", patchBody)
	}
	if !strings.Contains(patchBody, `"claimName":"fs-fs-dfe99bff-b68c-4528-88ce-ac8fe62ac734"`) {
		t.Fatalf("re-apply manifest missing filesystem claim: body = %s", patchBody)
	}
	// QEMU rejects virtiofs tags longer than 36 bytes; the volume name doubles
	// as the tag, so it must be the dash-free truncated form.
	if !strings.Contains(patchBody, `"name":"fs-fsdfe99bffb68c452888ceac8fe62ac73"`) {
		t.Fatalf("re-apply manifest missing tag-safe filesystem name: body = %s", patchBody)
	}
	if strings.Contains(patchBody, `"name":"fs-dfe99bff-b68c-4528-88ce-ac8fe62ac734"`) {
		t.Fatalf("re-apply manifest uses over-limit virtiofs tag name: body = %s", patchBody)
	}
	if !strings.Contains(patchBody, `"containerDisk":{"image":"rocky:10"}`) {
		t.Fatalf("re-apply manifest dropped existing volumes: body = %s", patchBody)
	}
}

func TestKubernetesLifecycleExecutorDetachFilesystemRemovesVirtiofsDevice(t *testing.T) {
	var patchBody string
	executor := newTestLifecycleExecutor(t, func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "/virtualmachines/vm-01") {
			body, _ := io.ReadAll(r.Body)
			patchBody = string(body)
			return lifecycleResponse(), nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{"metadata":{"name":"vm-01","namespace":"ani-tenant-tenant-a"},` +
				`"spec":{"running":false,"template":{"spec":{"domain":{"devices":{"filesystems":[{"name":"fs-fsdfe99bffb68c452888ceac8fe62ac73","virtiofs":{}}]}},` +
				`"volumes":[{"name":"containerdisk","containerDisk":{"image":"rocky:10"}},{"name":"fs-fsdfe99bffb68c452888ceac8fe62ac73","persistentVolumeClaim":{"claimName":"fs-fs-dfe99bff-b68c-4528-88ce-ac8fe62ac734"}}]}}}}`)),
		}, nil
	})
	record := lifecycleRecord()
	record.Kind = ports.WorkloadKindVM
	record.Name = "vm-01"
	record.Provider = "kubevirt"
	record.ResourceRefs = []string{"kubevirt/VirtualMachine/vm-01"}
	req := lifecycleRequest(ports.WorkloadLifecycleDetachFilesystem)
	req.FilesystemID = "fs_dfe99bff-b68c-4528-88ce-ac8fe62ac734"
	req.IdempotencyKey = "fs-detach-key-01"

	if _, err := executor.Apply(context.Background(), req, record); err != nil {
		t.Fatalf("DetachFilesystem Apply() error = %v", err)
	}
	if strings.Contains(patchBody, "virtiofs") || strings.Contains(patchBody, "fs-fs-dfe99bff") {
		t.Fatalf("re-apply manifest still contains filesystem entries: body = %s", patchBody)
	}
	if !strings.Contains(patchBody, `"containerDisk":{"image":"rocky:10"}`) {
		t.Fatalf("re-apply manifest dropped existing volumes: body = %s", patchBody)
	}
}

func TestKubeVirtFilesystemVolumeNameTagSafe(t *testing.T) {
	got := kubeVirtFilesystemVolumeName("fs_dfe99bff-b68c-4528-88ce-ac8fe62ac734")
	if got != "fs-fsdfe99bffb68c452888ceac8fe62ac73" {
		t.Fatalf("kubeVirtFilesystemVolumeName() = %q", got)
	}
	if len(got) > 36 {
		t.Fatalf("volume name %q exceeds QEMU 36-byte virtiofs tag limit", got)
	}
	if again := kubeVirtFilesystemVolumeName("fs_dfe99bff-b68c-4528-88ce-ac8fe62ac734"); again != got {
		t.Fatalf("volume name derivation is not deterministic: %q vs %q", got, again)
	}
}

func TestKubernetesLifecycleExecutorRebuildRejectsMissingVM(t *testing.T) {
	var requests []string
	executor := newTestLifecycleExecutor(t, func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		return nil, &resilience.StatusError{
			StatusCode: http.StatusNotFound,
			Body:       `{"reason":"NotFound","message":"virtualmachines not found"}`,
		}
	})
	record := lifecycleRecord()
	record.Kind = ports.WorkloadKindVM
	record.Name = "vm-01"
	record.Provider = "kubevirt"
	record.ResourceRefs = []string{"kubevirt/VirtualMachine/vm-01"}
	req := lifecycleRequest(ports.WorkloadLifecycleRebuild)
	req.IdempotencyKey = "rebuild-key-02"

	_, err := executor.Apply(context.Background(), req, record)
	if !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
}

// crPhaseResponse returns a snapshot.kubevirt.io style object with the given
// status.phase.
func crPhaseResponse(phase string) *http.Response {
	body := fmt.Sprintf(`{"status":{"phase":%q}}`, phase)
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// crCompleteResponse returns a VirtualMachineRestore style object whose
// status.complete is true (VirtualMachineRestore has no status.phase).
func crCompleteResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"status":{"complete":true}}`)),
	}
}

// byIDSpecStore returns a GPUSpecCRD by ID for the Volcano translator.
type byIDSpecStore struct {
	specs map[string]ports.GPUSpecCRD
}

func (s byIDSpecStore) List(context.Context) ([]ports.GPUSpecCRD, error) {
	items := make([]ports.GPUSpecCRD, 0, len(s.specs))
	for _, spec := range s.specs {
		items = append(items, spec)
	}
	return items, nil
}

func (s byIDSpecStore) Get(_ context.Context, specID string) (ports.GPUSpecCRD, error) {
	spec, ok := s.specs[specID]
	if !ok {
		return ports.GPUSpecCRD{}, ports.ErrGPUSpecNotFound
	}
	return spec, nil
}

func (s byIDSpecStore) Create(context.Context, string, ports.GPUSpecCRD) (ports.GPUSpecCRD, error) {
	return ports.GPUSpecCRD{}, ports.ErrUnsupported
}

func (s byIDSpecStore) Delete(context.Context, string, string) error {
	return ports.ErrUnsupported
}
