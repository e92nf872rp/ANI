package runtime

import (
	"context"
	"errors"
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
