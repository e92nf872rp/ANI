package runtime

import (
	"context"
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
