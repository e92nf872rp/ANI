package runtime

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

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

func TestKubernetesLifecycleExecutorDeleteRemovesSecretAndDeployment(t *testing.T) {
	var requests []string
	executor := newTestLifecycleExecutor(t, func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		return lifecycleResponse(), nil
	})
	record := lifecycleRecord()
	record.ResourceRefs = []string{
		"kubernetes/Secret/app-01-identity",
		"kubernetes/Deployment/app-01",
	}
	result, err := executor.Apply(context.Background(), lifecycleRequest(ports.WorkloadLifecycleDelete), record)
	if err != nil {
		t.Fatalf("Delete Apply() error = %v", err)
	}
	if !result.Accepted {
		t.Fatalf("Accepted = false, reason = %s", result.Reason)
	}
	if len(requests) != 2 {
		t.Fatalf("requests = %#v, want deployment and secret delete", requests)
	}
	if !strings.Contains(requests[0], "/deployments/app-01") {
		t.Fatalf("first request = %q, want deployment delete first", requests[0])
	}
	if !strings.Contains(requests[1], "/secrets/app-01-identity") {
		t.Fatalf("second request = %q, want secret delete second", requests[1])
	}
}

func TestKubernetesLifecycleExecutorUsesKubeVirtVMStartStopSubresources(t *testing.T) {
	var requests []string
	var bodies []string
	executor := newTestLifecycleExecutor(t, func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(body))
		return lifecycleResponse(), nil
	})
	record := lifecycleRecord()
	record.Kind = ports.WorkloadKindVM
	record.ResourceRefs = []string{"kubevirt/VirtualMachine/vm-test"}

	if _, err := executor.Apply(context.Background(), lifecycleRequest(ports.WorkloadLifecycleStop), record); err != nil {
		t.Fatalf("Stop Apply() error = %v", err)
	}
	if _, err := executor.Apply(context.Background(), lifecycleRequest(ports.WorkloadLifecycleStart), record); err != nil {
		t.Fatalf("Start Apply() error = %v", err)
	}

	if len(requests) != 2 {
		t.Fatalf("requests = %#v, want stop and start", requests)
	}
	if requests[0] != "PUT /apis/subresources.kubevirt.io/v1/namespaces/ani-tenant-tenant-a/virtualmachines/vm-test/stop" {
		t.Fatalf("stop request = %q", requests[0])
	}
	if requests[1] != "PUT /apis/subresources.kubevirt.io/v1/namespaces/ani-tenant-tenant-a/virtualmachines/vm-test/start" {
		t.Fatalf("start request = %q", requests[1])
	}
	for _, body := range bodies {
		if body == "{}" || !strings.Contains(body, `"apiVersion":"subresources.kubevirt.io/v1"`) {
			t.Fatalf("body = %s, want KubeVirt subresource options object", body)
		}
	}
}

func TestKubernetesLifecycleExecutorDetachesVMCDROM(t *testing.T) {
	var requests []string
	var patchBody string
	executor := newTestLifecycleExecutor(t, func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodGet {
			return jsonResponse(http.StatusOK, `{
				"spec": {
					"template": {
						"spec": {
							"domain": {
								"devices": {
									"disks": [
										{"name": "rootdisk", "disk": {"bus": "virtio"}},
										{"name": "iso", "cdrom": {"bus": "sata"}, "bootOrder": 1}
									]
								}
							},
							"volumes": [
								{"name": "rootdisk", "dataVolume": {"name": "vm-iso-01-root"}},
								{"name": "iso", "persistentVolumeClaim": {"claimName": "img-abc123"}}
							]
						}
					}
				}
			}`), nil
		}
		body, _ := io.ReadAll(r.Body)
		patchBody = string(body)
		return lifecycleResponse(), nil
	})
	record := lifecycleRecord()
	record.Kind = ports.WorkloadKindVM
	record.ResourceRefs = []string{"kubevirt/VirtualMachine/vm-iso-01"}

	request := lifecycleRequest(ports.WorkloadLifecycleDetachVolume)
	request.VolumeID = "iso"
	result, err := executor.Apply(context.Background(), request, record)
	if err != nil {
		t.Fatalf("DetachVolume Apply() error = %v", err)
	}
	if !result.Accepted {
		t.Fatalf("Accepted = false, reason = %s", result.Reason)
	}
	if len(requests) != 2 {
		t.Fatalf("requests = %#v, want GET then PATCH", requests)
	}
	for _, request := range requests {
		if !strings.Contains(request, "/apis/kubevirt.io/v1/namespaces/ani-tenant-tenant-a/virtualmachines/vm-iso-01") {
			t.Fatalf("request = %q, want KubeVirt VM path", request)
		}
	}
	if !strings.HasPrefix(requests[0], "GET ") || !strings.HasPrefix(requests[1], "PATCH ") {
		t.Fatalf("requests = %#v, want GET then PATCH", requests)
	}
	var patch map[string]any
	if err := json.Unmarshal([]byte(patchBody), &patch); err != nil {
		t.Fatalf("patch body is not JSON: %v\n%s", err, patchBody)
	}
	disks := nestedList(t, patch, "spec", "template", "spec", "domain", "devices", "disks")
	volumes := nestedList(t, patch, "spec", "template", "spec", "volumes")
	if hasNamedEntry(disks, "iso") || hasNamedEntry(volumes, "iso") {
		t.Fatalf("patch body still contains iso disk/volume: %s", patchBody)
	}
	if !hasNamedEntry(disks, "rootdisk") || !hasNamedEntry(volumes, "rootdisk") {
		t.Fatalf("patch body = %s, want rootdisk disk/volume preserved", patchBody)
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

func nestedList(t *testing.T, doc map[string]any, path ...string) []any {
	t.Helper()
	var current any = doc
	for _, key := range path {
		next, ok := current.(map[string]any)[key]
		if !ok {
			t.Fatalf("missing path %v in %#v", path, doc)
		}
		current = next
	}
	list, ok := current.([]any)
	if !ok {
		t.Fatalf("path %v = %#v, want list", path, current)
	}
	return list
}

func hasNamedEntry(items []any, name string) bool {
	for _, item := range items {
		entry, _ := item.(map[string]any)
		if entry["name"] == name {
			return true
		}
	}
	return false
}
