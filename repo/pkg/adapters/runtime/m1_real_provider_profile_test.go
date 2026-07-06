package runtime

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestM1RealProviderProfileCreateLifecycleAndOps(t *testing.T) {
	provider := &recordingProviderTransport{}
	client := newTestKubernetesRESTClient(t, provider)
	planner := NewPlanningRuntime()
	store := newMemoryInstanceStore()
	providerAdapter := NewKubernetesProviderAdapter(client, WithKubernetesProviderApplyEnabled(true))
	lifecycle := NewKubernetesLifecycleExecutor(client, WithKubernetesLifecycleEnabled(true))
	ops := NewKubernetesInstanceOps(client, WithKubernetesInstanceOpsEnabled(true), WithKubernetesInstanceOpsClock(func() time.Time {
		return time.Unix(1200, 0)
	}))
	orchestrator := NewLocalInstanceOrchestrator(
		planner,
		NewKubernetesDryRunRenderer(planner),
		NewLocalAdmissionGuard(),
		fakePlanAuditStore{},
		providerAdapter,
		providerAdapter,
		providerAdapter,
		NewLocalStatusReconciler(),
		WithInstanceStore(store),
	)
	service := NewLocalInstanceServiceWithOptions(orchestrator, store, ops, WithInstanceLifecycleExecutor(lifecycle))

	created := createE2EInstance(t, service, ports.WorkloadSpec{
		TenantID: "tenant-a",
		Name:     "app-01",
		Kind:     ports.WorkloadKindContainer,
		Image:    "harbor/app:1",
	})
	if created.Apply.Provider != "kubernetes" || len(created.Apply.ResourceRefs) != 1 {
		t.Fatalf("provider apply = %#v, want kubernetes resource ref", created.Apply)
	}
	providerName := providerNameFromTestRef(t, created.Apply.ResourceRefs[0])
	deploymentPath := "/apis/apps/v1/namespaces/ani-tenant-tenant-a/deployments/" + providerName
	podPath := "/api/v1/namespaces/ani-tenant-tenant-a/pods/" + providerName
	if !provider.seen("PATCH", deploymentPath, "dryRun=All") {
		t.Fatalf("provider requests = %#v, want server-side dry-run", provider.requests)
	}
	if !provider.seen("PATCH", deploymentPath, "fieldManager=ani-test") {
		t.Fatalf("provider requests = %#v, want server-side apply", provider.requests)
	}

	if _, err := service.Stop(context.Background(), lifecycleRequestFor(created.Ref.InstanceID, ports.WorkloadLifecycleStop)); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if _, err := service.Start(context.Background(), lifecycleRequestFor(created.Ref.InstanceID, ports.WorkloadLifecycleStart)); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if !provider.seen("PATCH", deploymentPath+"/scale", "") {
		t.Fatalf("provider requests = %#v, want scale lifecycle call", provider.requests)
	}

	logs, err := service.Ops(context.Background(), ports.WorkloadInstanceOpsRequest{
		TenantID:        "tenant-a",
		InstanceID:      created.Ref.InstanceID,
		Action:          ports.WorkloadInstanceOpsLogs,
		UserID:          "user-a",
		PermissionProof: "rbac:ops:workload",
	})
	if err != nil {
		t.Fatalf("Ops(logs) error = %v", err)
	}
	if !logs.Accepted || logs.Output == "" {
		t.Fatalf("logs result = %#v, want accepted output", logs)
	}
	execResult, err := service.Ops(context.Background(), ports.WorkloadInstanceOpsRequest{
		TenantID:        "tenant-a",
		InstanceID:      created.Ref.InstanceID,
		Action:          ports.WorkloadInstanceOpsExec,
		Command:         []string{"env"},
		UserID:          "user-a",
		PermissionProof: "rbac:ops:workload",
	})
	if err != nil {
		t.Fatalf("Ops(exec) error = %v", err)
	}
	if execResult.SessionID == "" {
		t.Fatalf("exec session id is empty")
	}
	if !provider.seen("GET", podPath+"/log", "") {
		t.Fatalf("provider requests = %#v, want pod logs", provider.requests)
	}
	if !provider.seen("POST", podPath+"/exec", "command=env") {
		t.Fatalf("provider requests = %#v, want pod exec", provider.requests)
	}
}

func providerNameFromTestRef(t *testing.T, ref string) string {
	t.Helper()
	parts := strings.Split(ref, "/")
	if len(parts) != 3 || strings.TrimSpace(parts[2]) == "" {
		t.Fatalf("provider ref = %q, want provider/kind/name", ref)
	}
	return parts[2]
}

func lifecycleRequestFor(instanceID string, action ports.WorkloadLifecycleAction) ports.WorkloadInstanceLifecycleRequest {
	return ports.WorkloadInstanceLifecycleRequest{
		TenantID:        "tenant-a",
		InstanceID:      instanceID,
		Action:          action,
		UserID:          "user-a",
		PermissionProof: "rbac:update:workload",
	}
}

type providerRequest struct {
	method string
	path   string
	query  string
}

type recordingProviderTransport struct {
	requests []providerRequest
}

func (t *recordingProviderTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	t.requests = append(t.requests, providerRequest{
		method: request.Method,
		path:   request.URL.Path,
		query:  request.URL.RawQuery,
	})
	switch {
	case strings.HasSuffix(request.URL.Path, "/log"):
		return jsonResponse(http.StatusOK, "log line"), nil
	case strings.HasSuffix(request.URL.Path, "/exec"):
		return jsonResponse(http.StatusOK, "exec accepted"), nil
	case request.Method == http.MethodGet:
		return jsonResponse(http.StatusOK, `{"status":{"availableReplicas":1}}`), nil
	default:
		return jsonResponse(http.StatusOK, `{}`), nil
	}
}

func (t *recordingProviderTransport) seen(method string, path string, queryPart string) bool {
	for _, request := range t.requests {
		if request.method != method || request.path != path {
			continue
		}
		if queryPart == "" || strings.Contains(request.query, queryPart) {
			return true
		}
	}
	return false
}
