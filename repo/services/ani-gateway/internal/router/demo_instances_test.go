package router

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestDemoInstanceServiceCreatesVMContainerAndGPUContainer(t *testing.T) {
	api := newDemoInstanceAPI()
	for _, kind := range []string{"vm", "container", "gpu_container"} {
		spec, err := demoSpecFromRequest(demoCreateInstanceRequest{
			Kind:   kind,
			Name:   "demo-" + kind,
			CPU:    "2",
			Memory: "4Gi",
		}, "tenant-a")
		if err != nil {
			t.Fatalf("demoSpecFromRequest(%s) error = %v", kind, err)
		}
		result, err := api.service.Create(context.Background(), ports.WorkloadInstanceCreateRequest{
			Spec:            spec,
			UserID:          "user-a",
			PermissionProof: "demo:test",
			RequestedAt:     time.Now().UTC(),
		})
		if err != nil {
			t.Fatalf("Create(%s) error = %v", kind, err)
		}
		if result.FinalStatus.State != ports.WorkloadStateRunning {
			t.Fatalf("Create(%s) state = %s, want running", kind, result.FinalStatus.State)
		}
		if len(result.Manifests) != 2 || result.Manifests[0].Kind != "Secret" {
			t.Fatalf("Create(%s) manifests = %#v, want workload identity Secret + workload manifest", kind, result.Manifests)
		}
		record, err := api.service.Get(context.Background(), ports.WorkloadInstanceGetRequest{
			TenantID:   result.Ref.TenantID,
			InstanceID: result.Ref.InstanceID,
		})
		if err != nil {
			t.Fatalf("Get(%s) error = %v", kind, err)
		}
		requireLocalCoreDevProfile(t, api.demoInstanceFromRecord(record).DevProfile, "local-instance-service")
		if kind == "vm" {
			if record.SSH == nil || record.SSH.Username == "" || record.SSH.Host == "" || record.SSH.Port != 22 {
				t.Fatalf("vm ssh = %+v, want connection metadata", record.SSH)
			}
		}
	}
	records, err := api.service.List(context.Background(), ports.WorkloadInstanceListRequest{TenantID: "tenant-a"})
	if err != nil {
		t.Fatalf("List error = %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("records = %d, want 3", len(records))
	}
}

func TestDemoSpecFromRequestMapsSecretBindings(t *testing.T) {
	spec, err := demoSpecFromRequest(demoCreateInstanceRequest{
		Kind: "container",
		Name: "demo-secret-app",
		SecretBindings: []demoSecretBindingRequest{
			{
				SecretID:  "sec-db",
				EnvPrefix: "DB_",
				MountPath: "/etc/secrets/db",
			},
		},
	}, "tenant-a")
	if err != nil {
		t.Fatalf("demoSpecFromRequest error = %v", err)
	}
	if len(spec.SecretBindings) != 1 {
		t.Fatalf("secret bindings = %d, want 1", len(spec.SecretBindings))
	}
	binding := spec.SecretBindings[0]
	if binding.SecretID != "sec-db" || binding.EnvPrefix != "DB_" || binding.MountPath != "/etc/secrets/db" {
		t.Fatalf("secret binding = %#v, want request values", binding)
	}
}

func TestDemoSpecFromRequestMapsSandboxConfig(t *testing.T) {
	spec, err := demoSpecFromRequest(demoCreateInstanceRequest{
		Kind: "sandbox",
		Name: "agent-session",
		SandboxConfig: demoSandboxConfigRequest{
			RuntimeClass:        "sandbox-kata",
			SessionTimeout:      "45m",
			NetworkEgressPolicy: "deny_all",
		},
	}, "tenant-a")
	if err != nil {
		t.Fatalf("demoSpecFromRequest error = %v", err)
	}
	if spec.Kind != ports.WorkloadKindSandbox {
		t.Fatalf("kind = %s, want sandbox", spec.Kind)
	}
	if spec.RuntimeClassName != "sandbox-kata" {
		t.Fatalf("runtime class = %q, want sandbox-kata", spec.RuntimeClassName)
	}
	if spec.Sandbox == nil {
		t.Fatalf("sandbox config is nil")
	}
	if spec.Sandbox.SessionTimeout != 45*time.Minute || spec.Sandbox.NetworkEgressPolicy != ports.SandboxNetworkEgressDenyAll {
		t.Fatalf("sandbox = %+v, want 45m deny_all", spec.Sandbox)
	}
}

func TestDemoInstanceServiceSandboxResponseIncludesLocalProfile(t *testing.T) {
	api := newDemoInstanceAPI()
	spec, err := demoSpecFromRequest(demoCreateInstanceRequest{
		Kind: "sandbox",
		Name: "agent-session",
		SandboxConfig: demoSandboxConfigRequest{
			RuntimeClass:        "sandbox-kata",
			SessionTimeout:      "45m",
			NetworkEgressPolicy: "deny_all",
		},
	}, "tenant-a")
	if err != nil {
		t.Fatalf("demoSpecFromRequest error = %v", err)
	}
	created, err := api.service.Create(context.Background(), ports.WorkloadInstanceCreateRequest{
		Spec:            spec,
		UserID:          "user-a",
		PermissionProof: "demo:test",
		RequestedAt:     time.Unix(2100, 0),
	})
	if err != nil {
		t.Fatalf("Create error = %v", err)
	}
	record, err := api.service.Get(context.Background(), ports.WorkloadInstanceGetRequest{
		TenantID:   "tenant-a",
		InstanceID: created.Ref.InstanceID,
	})
	if err != nil {
		t.Fatalf("Get error = %v", err)
	}
	response := api.demoInstanceFromRecord(record)
	if response.Sandbox == nil {
		t.Fatalf("response sandbox is nil")
	}
	if response.Sandbox.RuntimeClass != "sandbox-kata" || response.Sandbox.SessionState != "running" {
		t.Fatalf("sandbox = %+v, want sandbox-kata/running", response.Sandbox)
	}
	if response.Sandbox.DevProfile.Mode != "local" || response.Sandbox.DevProfile.RealProvider {
		t.Fatalf("sandbox dev profile = %+v, want local non-real marker", response.Sandbox.DevProfile)
	}
}

func TestDemoInstanceServiceLifecycleAndOps(t *testing.T) {
	api := newDemoInstanceAPI()
	spec, err := demoSpecFromRequest(demoCreateInstanceRequest{Kind: "container", Name: "demo-app"}, "tenant-a")
	if err != nil {
		t.Fatalf("demoSpecFromRequest error = %v", err)
	}
	created, err := api.service.Create(context.Background(), ports.WorkloadInstanceCreateRequest{
		Spec:            spec,
		UserID:          "user-a",
		PermissionProof: "demo:test",
		RequestedAt:     time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("Create error = %v", err)
	}
	stopped, err := api.service.Stop(context.Background(), ports.WorkloadInstanceLifecycleRequest{
		TenantID:        "tenant-a",
		InstanceID:      created.Ref.InstanceID,
		UserID:          "user-a",
		PermissionProof: "demo:test",
		RequestedAt:     time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("Stop error = %v", err)
	}
	if stopped.Status.State != ports.WorkloadStateStopped {
		t.Fatalf("stopped state = %s, want stopped", stopped.Status.State)
	}
	started, err := api.service.Start(context.Background(), ports.WorkloadInstanceLifecycleRequest{
		TenantID:        "tenant-a",
		InstanceID:      created.Ref.InstanceID,
		UserID:          "user-a",
		PermissionProof: "demo:test",
		RequestedAt:     time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("Start error = %v", err)
	}
	if started.Status.State != ports.WorkloadStateRunning {
		t.Fatalf("started state = %s, want running", started.Status.State)
	}
	ops, err := api.service.Ops(context.Background(), ports.WorkloadInstanceOpsRequest{
		TenantID:        "tenant-a",
		InstanceID:      created.Ref.InstanceID,
		Action:          ports.WorkloadInstanceOpsLogs,
		UserID:          "user-a",
		PermissionProof: "demo:test",
		RequestedAt:     time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("Ops error = %v", err)
	}
	if !ops.Accepted {
		t.Fatalf("ops accepted = false, want true")
	}
}

func TestDemoLifecycleErrorStatusMapsConflict(t *testing.T) {
	err := fmt.Errorf("%w: termination_protection is enabled", ports.ErrConflict)
	if got := demoLifecycleErrorStatus(err); got != http.StatusConflict {
		t.Fatalf("status = %d, want 409", got)
	}
	if got := demoLifecycleErrorCode(err); got != "CONFLICT" {
		t.Fatalf("code = %q, want CONFLICT", got)
	}
}

func TestDemoGatewayRequiresIdempotencyKey(t *testing.T) {
	if hasIdempotencyKey("   ") {
		t.Fatalf("blank idempotency key should be rejected")
	}
	if !hasIdempotencyKey("create-123") {
		t.Fatalf("nonblank idempotency key should be accepted")
	}
}

func TestDemoInstanceServiceContainerRolloutStatus(t *testing.T) {
	api := newDemoInstanceAPI()
	spec, err := demoSpecFromRequest(demoCreateInstanceRequest{
		Kind:     "container",
		Name:     "demo-rollout",
		Image:    "harbor/demo:2",
		Replicas: 3,
	}, "tenant-a")
	if err != nil {
		t.Fatalf("demoSpecFromRequest error = %v", err)
	}
	created, err := api.service.Create(context.Background(), ports.WorkloadInstanceCreateRequest{
		Spec:            spec,
		UserID:          "user-a",
		PermissionProof: "demo:test",
		RequestedAt:     time.Unix(1900, 0),
	})
	if err != nil {
		t.Fatalf("Create error = %v", err)
	}
	record, err := api.service.Get(context.Background(), ports.WorkloadInstanceGetRequest{
		TenantID:   "tenant-a",
		InstanceID: created.Ref.InstanceID,
	})
	if err != nil {
		t.Fatalf("Get error = %v", err)
	}
	response := api.demoInstanceFromRecord(record)
	if response.Container == nil {
		t.Fatalf("response container is nil")
	}
	if response.Container.Replicas != 3 || response.Container.ReadyReplicas != 3 || response.Container.RolloutStatus != "healthy" {
		t.Fatalf("container = %+v, want 3 ready healthy", response.Container)
	}
	if response.Container.Revision == "" || len(response.Container.History) != 1 {
		t.Fatalf("container revision=%q history=%#v, want one revision", response.Container.Revision, response.Container.History)
	}
}

func TestDemoInstanceServiceGPUStatus(t *testing.T) {
	api := newDemoInstanceAPI()
	spec, err := demoSpecFromRequest(demoCreateInstanceRequest{
		Kind:  "gpu_container",
		Name:  "demo-gpu-status",
		Image: "harbor/gpu:2",
		GPU: demoCreateGPURequest{
			Vendor: "nvidia",
			Model:  "A100",
			Count:  2,
		},
	}, "tenant-a")
	if err != nil {
		t.Fatalf("demoSpecFromRequest error = %v", err)
	}
	created, err := api.service.Create(context.Background(), ports.WorkloadInstanceCreateRequest{
		Spec:            spec,
		UserID:          "user-a",
		PermissionProof: "demo:test",
		RequestedAt:     time.Unix(1950, 0),
	})
	if err != nil {
		t.Fatalf("Create error = %v", err)
	}
	record, err := api.service.Get(context.Background(), ports.WorkloadInstanceGetRequest{
		TenantID:   "tenant-a",
		InstanceID: created.Ref.InstanceID,
	})
	if err != nil {
		t.Fatalf("Get error = %v", err)
	}
	response := api.demoInstanceFromRecord(record)
	if response.GPU == nil {
		t.Fatalf("response GPU is nil")
	}
	if response.GPU.Vendor != "nvidia" || response.GPU.Model != "A100" || response.GPU.Count != 2 {
		t.Fatalf("gpu = %+v, want nvidia/A100 x2", response.GPU)
	}
	if response.GPU.SchedulingReason == "" {
		t.Fatalf("gpu scheduling reason is empty")
	}
	if response.GPU.UtilizationPercent < 0 || response.GPU.UtilizationPercent > 100 {
		t.Fatalf("gpu utilization = %f, want 0..100", response.GPU.UtilizationPercent)
	}
}

func TestDemoInstanceOperationsAreQueryable(t *testing.T) {
	api := newDemoInstanceAPI()
	spec, err := demoSpecFromRequest(demoCreateInstanceRequest{Kind: "container", Name: "demo-ops"}, "tenant-a")
	if err != nil {
		t.Fatalf("demoSpecFromRequest error = %v", err)
	}
	created, err := api.service.Create(context.Background(), ports.WorkloadInstanceCreateRequest{
		IdempotencyKey:  "demo-create-ops",
		Spec:            spec,
		UserID:          "user-a",
		PermissionProof: "demo:test",
		RequestedAt:     time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("Create error = %v", err)
	}
	if created.OperationID == "" {
		t.Fatalf("OperationID is empty")
	}
	list, err := api.operations.ListOperations(context.Background(), ports.WorkloadOperationListRequest{
		TenantID:   "tenant-a",
		InstanceID: created.Ref.InstanceID,
	})
	if err != nil {
		t.Fatalf("ListOperations error = %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("operations = %d, want 1", len(list.Items))
	}
	if len(list.Items[0].Steps) == 0 {
		t.Fatalf("operation steps are empty")
	}
	got, err := api.operations.GetOperation(context.Background(), "tenant-a", created.OperationID)
	if err != nil {
		t.Fatalf("GetOperation error = %v", err)
	}
	if got.ID != created.OperationID || got.Status != ports.WorkloadOperationSucceeded {
		t.Fatalf("operation id=%q status=%s, want %q/succeeded", got.ID, got.Status, created.OperationID)
	}
}

func TestDemoInstanceObservabilityResponsesUseLocalProfile(t *testing.T) {
	api := newDemoInstanceAPI()
	spec, err := demoSpecFromRequest(demoCreateInstanceRequest{Kind: "sandbox", Name: "obs-sandbox"}, "tenant-a")
	if err != nil {
		t.Fatalf("demoSpecFromRequest error = %v", err)
	}
	created, err := api.service.Create(context.Background(), ports.WorkloadInstanceCreateRequest{
		IdempotencyKey:  "demo-observe-create",
		Spec:            spec,
		UserID:          "user-a",
		PermissionProof: "demo:test",
		RequestedAt:     time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("Create error = %v", err)
	}
	logs, err := api.observability.ListLogs(context.Background(), ports.InstanceObservationListRequest{
		TenantID:   "tenant-a",
		InstanceID: created.Ref.InstanceID,
		Limit:      5,
		Level:      "info",
	})
	if err != nil {
		t.Fatalf("ListLogs error = %v", err)
	}
	logResponse := demoInstanceLogListFromResult(logs)
	if len(logResponse.Items) == 0 || logResponse.Total != len(logResponse.Items) {
		t.Fatalf("log response = %+v, want items and total", logResponse)
	}
	requireLocalCoreDevProfile(t, logResponse.DevProfile, "local-instance-observability")

	metrics, err := api.observability.GetMetrics(context.Background(), ports.InstanceObservationGetRequest{
		TenantID:   "tenant-a",
		InstanceID: created.Ref.InstanceID,
	})
	if err != nil {
		t.Fatalf("GetMetrics error = %v", err)
	}
	metricsResponse := demoInstanceMetricsFromRecord(metrics)
	if metricsResponse.InstanceID != created.Ref.InstanceID || metricsResponse.CPUUtilizationPct == nil {
		t.Fatalf("metrics response = %+v, want instance metrics", metricsResponse)
	}
	requireLocalCoreDevProfile(t, metricsResponse.DevProfile, "local-instance-observability")

	execSession, err := api.observability.CreateExecSession(context.Background(), ports.InstanceExecSessionCreateRequest{
		TenantID:       "tenant-a",
		InstanceID:     created.Ref.InstanceID,
		IdempotencyKey: "exec-observe",
		Command:        []string{"/bin/sh"},
		TTY:            true,
		Rows:           24,
	})
	if err != nil {
		t.Fatalf("CreateExecSession error = %v", err)
	}
	execResponse := demoInstanceExecSessionFromRecord(execSession)
	if execResponse.InstanceID != created.Ref.InstanceID || execResponse.WSURL == "" {
		t.Fatalf("exec response = %+v, want websocket session", execResponse)
	}
	if execResponse.Token != "" {
		t.Fatalf("exec token = %q, want no long-lived credential", execResponse.Token)
	}
	requireLocalCoreDevProfile(t, execResponse.DevProfile, "local-instance-observability")
}

func TestDemoInstanceObservabilityCanUseInstanceNameForProviderTarget(t *testing.T) {
	api := newDemoInstanceAPIWithObservability(nil, true)
	spec, err := demoSpecFromRequest(demoCreateInstanceRequest{Kind: "container", Name: "s07-observability-live"}, "tenant-a")
	if err != nil {
		t.Fatalf("demoSpecFromRequest error = %v", err)
	}
	created, err := api.service.Create(context.Background(), ports.WorkloadInstanceCreateRequest{
		IdempotencyKey:  "demo-observe-provider-create",
		Spec:            spec,
		UserID:          "user-a",
		PermissionProof: "demo:test",
		RequestedAt:     time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("Create error = %v", err)
	}
	record, err := api.service.Get(context.Background(), ports.WorkloadInstanceGetRequest{
		TenantID:   "tenant-a",
		InstanceID: created.Ref.InstanceID,
	})
	if err != nil {
		t.Fatalf("Get error = %v", err)
	}
	if got := api.observabilityTargetID(record); got != "s07-observability-live" {
		t.Fatalf("observability target = %q, want instance name", got)
	}

	localAPI := newDemoInstanceAPIWithObservability(nil, false)
	if got := localAPI.observabilityTargetID(record); got != created.Ref.InstanceID {
		t.Fatalf("local observability target = %q, want instance id %q", got, created.Ref.InstanceID)
	}
}

func TestDemoInstanceServiceVMConsoleSession(t *testing.T) {
	api := newDemoInstanceAPI()
	spec, err := demoSpecFromRequest(demoCreateInstanceRequest{Kind: "vm", Name: "demo-vm"}, "tenant-a")
	if err != nil {
		t.Fatalf("demoSpecFromRequest error = %v", err)
	}
	created, err := api.service.Create(context.Background(), ports.WorkloadInstanceCreateRequest{
		Spec:            spec,
		UserID:          "user-a",
		PermissionProof: "demo:test",
		RequestedAt:     time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("Create error = %v", err)
	}
	console, err := api.service.Ops(context.Background(), ports.WorkloadInstanceOpsRequest{
		TenantID:        "tenant-a",
		InstanceID:      created.Ref.InstanceID,
		Action:          ports.WorkloadInstanceOpsVMVNC,
		Protocol:        "vnc",
		UserID:          "user-a",
		PermissionProof: "demo:test",
		RequestedAt:     time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("Ops(vm_vnc) error = %v", err)
	}
	if !console.Accepted || console.Protocol != "vnc" || console.ConnectURL == "" {
		t.Fatalf("console accepted=%v protocol=%q connect=%q, want vnc connect session", console.Accepted, console.Protocol, console.ConnectURL)
	}
}

func TestDemoInstanceServiceVMSnapshot(t *testing.T) {
	api := newDemoInstanceAPI()
	spec, err := demoSpecFromRequest(demoCreateInstanceRequest{Kind: "vm", Name: "demo-vm-snapshot"}, "tenant-a")
	if err != nil {
		t.Fatalf("demoSpecFromRequest error = %v", err)
	}
	created, err := api.service.Create(context.Background(), ports.WorkloadInstanceCreateRequest{
		Spec:            spec,
		UserID:          "user-a",
		PermissionProof: "demo:test",
		RequestedAt:     time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("Create error = %v", err)
	}
	record, err := api.service.Snapshot(context.Background(), ports.WorkloadInstanceLifecycleRequest{
		IdempotencyKey:  "demo-snapshot-vm",
		TenantID:        "tenant-a",
		InstanceID:      created.Ref.InstanceID,
		SnapshotName:    "before-upgrade",
		UserID:          "user-a",
		PermissionProof: "demo:test",
		RequestedAt:     time.Unix(1700, 0),
	})
	if err != nil {
		t.Fatalf("Snapshot error = %v", err)
	}
	if record.Status.State != ports.WorkloadStateRunning || len(record.Snapshots) != 1 {
		t.Fatalf("state=%s snapshots=%d, want running with one snapshot", record.Status.State, len(record.Snapshots))
	}
	response := api.demoInstanceFromRecord(record)
	if len(response.Snapshots) != 1 || response.Snapshots[0].Name != "before-upgrade" {
		t.Fatalf("response snapshots = %#v, want before-upgrade", response.Snapshots)
	}
}

func TestDemoInstanceServiceVMVolumeBinding(t *testing.T) {
	api := newDemoInstanceAPI()
	spec, err := demoSpecFromRequest(demoCreateInstanceRequest{Kind: "vm", Name: "demo-vm-volume"}, "tenant-a")
	if err != nil {
		t.Fatalf("demoSpecFromRequest error = %v", err)
	}
	created, err := api.service.Create(context.Background(), ports.WorkloadInstanceCreateRequest{
		Spec:            spec,
		UserID:          "user-a",
		PermissionProof: "demo:test",
		RequestedAt:     time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("Create error = %v", err)
	}
	attached, err := api.service.AttachVolume(context.Background(), ports.WorkloadInstanceLifecycleRequest{
		IdempotencyKey:  "demo-attach-volume",
		TenantID:        "tenant-a",
		InstanceID:      created.Ref.InstanceID,
		VolumeID:        "vol-data-demo",
		UserID:          "user-a",
		PermissionProof: "demo:test",
		RequestedAt:     time.Unix(1800, 0),
	})
	if err != nil {
		t.Fatalf("AttachVolume error = %v", err)
	}
	response := api.demoInstanceFromRecord(attached)
	if response.Status != "running" || len(response.Volumes) != 2 {
		t.Fatalf("status=%s volumes=%d, want running with root+data volume", response.Status, len(response.Volumes))
	}
	if response.Volumes[1].Name != "vol-data-demo" || response.Volumes[1].Kind != string(ports.StorageAttachmentDataDisk) {
		t.Fatalf("response volumes = %#v, want data volume", response.Volumes)
	}
	detached, err := api.service.DetachVolume(context.Background(), ports.WorkloadInstanceLifecycleRequest{
		IdempotencyKey:  "demo-detach-volume",
		TenantID:        "tenant-a",
		InstanceID:      created.Ref.InstanceID,
		VolumeID:        "vol-data-demo",
		UserID:          "user-a",
		PermissionProof: "demo:test",
		RequestedAt:     time.Unix(1810, 0),
	})
	if err != nil {
		t.Fatalf("DetachVolume error = %v", err)
	}
	if len(api.demoInstanceFromRecord(detached).Volumes) != 1 {
		t.Fatalf("volumes after detach = %#v, want root disk only", api.demoInstanceFromRecord(detached).Volumes)
	}
}

func TestDemoInstanceServiceRealShellExecutesCommand(t *testing.T) {
	record := ports.WorkloadInstanceRecord{
		TenantID:   "tenant-a",
		InstanceID: "instance-shell",
		Name:       "demo-vm-shell",
		Kind:       ports.WorkloadKindVM,
		Provider:   "kubevirt",
		Status:     ports.WorkloadStatus{State: ports.WorkloadStateRunning},
	}
	result, err := runDemoShellCommand(context.Background(), record, "printf hello")
	if err != nil {
		t.Fatalf("runDemoShellCommand error = %v", err)
	}
	if result.ExitCode != 0 || strings.TrimSpace(result.Output) != "hello" {
		t.Fatalf("result exit=%d output=%q, want hello", result.ExitCode, result.Output)
	}
	if result.CWD == "" {
		t.Fatalf("CWD is empty")
	}
}

func TestDemoInstanceResponseMarksRealProviderWhenKubernetesWorkloadRuntimeConfigured(t *testing.T) {
	workload := DefaultInstanceWorkloadRuntime()
	workload.Provider = "kubernetes_rest"
	api := newDemoInstanceAPIWithOptions(nil, workload, nil, nil, false)
	record := ports.WorkloadInstanceRecord{
		TenantID:     "tenant-a",
		InstanceID:   "instance-k8s",
		Name:         "demo-container",
		Kind:         ports.WorkloadKindContainer,
		Provider:     "kubernetes",
		ResourceRefs: []string{"kubernetes/Secret/ani-tenant-a/demo-container-identity", "kubernetes/Deployment/ani-tenant-a/demo-container"},
		Status:       ports.WorkloadStatus{State: ports.WorkloadStateProvisioning},
	}
	resp := api.demoInstanceFromRecord(record)
	if resp.DevProfile.Mode != "real" || !resp.DevProfile.RealProvider || resp.DevProfile.Provider != "kubernetes_rest" {
		t.Fatalf("dev profile = %+v, want real kubernetes_rest marker", resp.DevProfile)
	}
	if notice := api.instanceCreateDemoNotice(); !strings.Contains(notice, "kubernetes_rest") {
		t.Fatalf("demo notice = %q, want kubernetes_rest guidance", notice)
	}
}
