package runtime

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestKubernetesSandboxRuntimeCreateAppliesDeploymentWithRuntimeClass(t *testing.T) {
	provider := &sandboxApplyTransport{}
	client := newTestKubernetesRESTClient(t, provider)
	runtime := NewKubernetesSandboxRuntime(
		client,
		WithKubernetesSandboxApplyEnabled(true),
		WithKubernetesSandboxClock(func() time.Time { return time.Unix(1700, 0) }),
	)

	instance, err := runtime.Create(context.Background(), ports.SandboxCreateRequest{
		TenantID:  "tenant-a",
		Name:      "sbx-01",
		Image:     "docker.kubercon.local/common/mirror/busybox:latest",
		AutoStart: true,
		CreatedAt: time.Unix(1700, 0),
		Config: ports.SandboxConfig{
			RuntimeClass: "sandbox-kata",
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if !instance.DevProfile.RealProvider || instance.Provider != "kubernetes_sandbox_runtime" {
		t.Fatalf("DevProfile/Provider = %#v", instance)
	}
	if len(instance.ResourceRefs) != 1 || !strings.Contains(instance.ResourceRefs[0], "Deployment/sbx-01") {
		t.Fatalf("ResourceRefs = %#v", instance.ResourceRefs)
	}
	if !strings.Contains(provider.applyBody, `"runtimeClassName":"sandbox-kata"`) && !strings.Contains(provider.applyBody, `"runtimeClassName": "sandbox-kata"`) {
		t.Fatalf("apply body missing runtimeClassName sandbox-kata: %s", provider.applyBody)
	}
}

func TestKubernetesSandboxRuntimePauseResumeDeleteScalesAndDeletes(t *testing.T) {
	provider := &recordingProviderTransport{}
	client := newTestKubernetesRESTClient(t, provider)
	runtime := NewKubernetesSandboxRuntime(client, WithKubernetesSandboxApplyEnabled(true))

	instance, err := runtime.Create(context.Background(), ports.SandboxCreateRequest{
		TenantID:  "tenant-a",
		Name:      "sbx-02",
		Image:     "busybox:1.36",
		AutoStart: true,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	paused, err := runtime.ApplyLifecycle(context.Background(), ports.SandboxLifecycleRequest{
		TenantID:   instance.TenantID,
		InstanceID: instance.InstanceID,
		Action:     ports.WorkloadLifecyclePause,
	})
	if err != nil {
		t.Fatalf("Pause() error = %v", err)
	}
	if paused.State != ports.SandboxStatePaused {
		t.Fatalf("paused state = %s", paused.State)
	}
	if !provider.seen("PATCH", "/apis/apps/v1/namespaces/ani-tenant-tenant-a/deployments/sbx-02/scale", "") {
		t.Fatalf("provider requests = %#v, want scale on pause", provider.requests)
	}

	if _, err := runtime.ApplyLifecycle(context.Background(), ports.SandboxLifecycleRequest{
		TenantID:   instance.TenantID,
		InstanceID: instance.InstanceID,
		Action:     ports.WorkloadLifecycleResume,
	}); err != nil {
		t.Fatalf("Resume() error = %v", err)
	}

	if _, err := runtime.ApplyLifecycle(context.Background(), ports.SandboxLifecycleRequest{
		TenantID:   instance.TenantID,
		InstanceID: instance.InstanceID,
		Action:     ports.WorkloadLifecycleDelete,
	}); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if !provider.seen("DELETE", "/apis/apps/v1/namespaces/ani-tenant-tenant-a/deployments/sbx-02", "") {
		t.Fatalf("provider requests = %#v, want delete", provider.requests)
	}
}

func TestKubernetesSandboxRuntimeDisabledStaysLocal(t *testing.T) {
	provider := &recordingProviderTransport{}
	client := newTestKubernetesRESTClient(t, provider)
	runtime := NewKubernetesSandboxRuntime(client, WithKubernetesSandboxApplyEnabled(false))

	instance, err := runtime.Create(context.Background(), ports.SandboxCreateRequest{
		TenantID:  "tenant-a",
		Name:      "sbx-local",
		Image:     "busybox:1.36",
		AutoStart: true,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if instance.DevProfile.RealProvider || len(provider.requests) != 0 {
		t.Fatalf("disabled runtime should stay local, got %#v requests=%d", instance.DevProfile, len(provider.requests))
	}
}

func TestKubernetesSandboxRuntimeCreateCodeRunExecutesInPod(t *testing.T) {
	provider := &sandboxPodListTransport{
		podList: `{"items":[{"metadata":{"name":"sbx-code-abc"},"status":{"phase":"Running","containerStatuses":[{"name":"sbx-code","ready":true}]}}]}`,
	}
	client := newTestKubernetesRESTClient(t, provider)
	executor := &fakeSandboxPodExecutor{
		result: sandboxPodExecResult{Stdout: "2\n", ExitCode: 0},
	}
	runtime := NewKubernetesSandboxRuntime(
		client,
		WithKubernetesSandboxApplyEnabled(true),
		WithKubernetesSandboxPodExecutor(executor),
		WithKubernetesSandboxClock(func() time.Time { return time.Unix(1800, 0) }),
	)

	instance, err := runtime.Create(context.Background(), ports.SandboxCreateRequest{
		TenantID:  "tenant-a",
		Name:      "sbx-code",
		Image:     "python:3.12-alpine",
		AutoStart: true,
		CreatedAt: time.Unix(1800, 0),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	result, err := runtime.CreateCodeRun(context.Background(), ports.SandboxCodeRunRequest{
		TenantID:       instance.TenantID,
		InstanceID:     instance.InstanceID,
		IdempotencyKey: "code-1",
		Language:       "python",
		Code:           "print(1+1)",
		TimeoutSeconds: 30,
		RequestedAt:    time.Unix(1800, 0),
	})
	if err != nil {
		t.Fatalf("CreateCodeRun() error = %v", err)
	}
	if result.Status != "succeeded" || result.Stdout != "2\n" || result.ExitCode == nil || *result.ExitCode != 0 {
		t.Fatalf("CreateCodeRun() = %#v", result)
	}
	if len(executor.requests) != 1 {
		t.Fatalf("executor requests = %#v", executor.requests)
	}
	got := executor.requests[0]
	if got.Pod != "sbx-code-abc" || got.Container != "sbx-code" {
		t.Fatalf("exec target = %#v", got)
	}
	if len(got.Command) < 3 || got.Command[0] != "python3" || got.Command[1] != "-c" || got.Command[2] != "print(1+1)" {
		t.Fatalf("exec command = %#v", got.Command)
	}

	again, err := runtime.CreateCodeRun(context.Background(), ports.SandboxCodeRunRequest{
		TenantID:       instance.TenantID,
		InstanceID:     instance.InstanceID,
		IdempotencyKey: "code-1",
		Language:       "python",
		Code:           "print(1+1)",
		TimeoutSeconds: 30,
	})
	if err != nil {
		t.Fatalf("idempotent CreateCodeRun() error = %v", err)
	}
	if again.ID != result.ID || len(executor.requests) != 1 {
		t.Fatalf("idempotency failed: again=%#v requests=%d", again, len(executor.requests))
	}
}

type sandboxPodListTransport struct {
	recordingProviderTransport
	podList string
}

func (t *sandboxPodListTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/pods") && !strings.Contains(request.URL.Path, "/exec") {
		t.requests = append(t.requests, providerRequest{method: request.Method, path: request.URL.Path, query: request.URL.RawQuery})
		return jsonResponse(http.StatusOK, t.podList), nil
	}
	return t.recordingProviderTransport.RoundTrip(request)
}

type fakeSandboxPodExecutor struct {
	requests []sandboxPodExecRequest
	result   sandboxPodExecResult
	err      error
}

func (e *fakeSandboxPodExecutor) Exec(_ context.Context, request sandboxPodExecRequest) (sandboxPodExecResult, error) {
	e.requests = append(e.requests, request)
	return e.result, e.err
}

type sandboxApplyTransport struct {
	recordingProviderTransport
	applyBody string
}

func (t *sandboxApplyTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Body != nil && request.Method == http.MethodPatch && strings.Contains(request.URL.Path, "/deployments/") && !strings.HasSuffix(request.URL.Path, "/scale") {
		buf := make([]byte, 1<<20)
		n, _ := request.Body.Read(buf)
		t.applyBody = string(buf[:n])
		request.Body = http.NoBody
	}
	return t.recordingProviderTransport.RoundTrip(request)
}
