package router

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/kubercloud/ani/pkg/ports"
)

func TestInstanceServiceCreatesVMContainerAndGPUContainer(t *testing.T) {
	api := newInstanceAPI()
	for _, kind := range []string{"vm", "container", "gpu_container"} {
		spec, err := instanceSpecFromRequest(createInstanceRequest{
			Kind:   kind,
			Name:   "demo-" + kind,
			CPU:    "2",
			Memory: "4Gi",
		}, "tenant-a")
		if err != nil {
			t.Fatalf("instanceSpecFromRequest(%s) error = %v", kind, err)
		}
		result, err := api.service.Create(context.Background(), ports.WorkloadInstanceCreateRequest{
			IdempotencyKey:  "create-" + kind,
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
		if len(result.Manifests) < 1 {
			t.Fatalf("Create(%s) manifests = %d, want at least 1", kind, len(result.Manifests))
		}
		record, err := api.service.Get(context.Background(), ports.WorkloadInstanceGetRequest{
			TenantID:   result.Ref.TenantID,
			InstanceID: result.Ref.InstanceID,
		})
		if err != nil {
			t.Fatalf("Get(%s) error = %v", kind, err)
		}
		requireLocalCoreDevProfile(t, instanceResponseFromRecord(record).DevProfile, "local-instance-service")
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

func TestRegisterInstancesUsesInjectedRuntime(t *testing.T) {
	h := server.Default()
	injected := newInstanceAPI()

	got, _ := registerInstancesWithRuntime(
		h.Group("/api/v1"),
		nil,
		false,
		nil,
		nil,
		nil,
		&InstanceRuntime{
			Service:    injected.service,
			Store:      injected.store,
			Operations: injected.operations,
		},
		nil,
	)

	if got != injected.service {
		t.Fatalf("registered service = %T, want injected service %T", got, injected.service)
	}
}

func TestInstanceSpecFromRequestMapsSecretBindings(t *testing.T) {
	spec, err := instanceSpecFromRequest(createInstanceRequest{
		Kind: "container",
		Name: "demo-secret-app",
		SecretBindings: []secretBindingRequest{
			{
				SecretID:  "sec-db",
				EnvPrefix: "DB_",
				MountPath: "/etc/secrets/db",
			},
		},
	}, "tenant-a")
	if err != nil {
		t.Fatalf("instanceSpecFromRequest error = %v", err)
	}
	if len(spec.SecretBindings) != 1 {
		t.Fatalf("secret bindings = %d, want 1", len(spec.SecretBindings))
	}
	binding := spec.SecretBindings[0]
	if binding.SecretID != "sec-db" || binding.EnvPrefix != "DB_" || binding.MountPath != "/etc/secrets/db" {
		t.Fatalf("secret binding = %#v, want request values", binding)
	}
}

func TestInstanceSpecFromRequestMapsProviderNeutralContainerConfig(t *testing.T) {
	envValue := "plain"
	spec, err := instanceSpecFromRequest(createInstanceRequest{
		Kind:     "container",
		Name:     "app",
		Labels:   map[string]string{"team": "ml"},
		ImageID:  "img-1",
		ImageRef: "harbor.local/app@sha256:abc",
		ContainerConfig: &containerConfigRequest{
			Network: &instanceNetworkRequest{
				VPCID:            "vpc-1",
				SubnetID:         "subnet-1",
				SecurityGroupIDs: []string{"sg-1"},
				AssignPrivateIP:  true,
				PrivateIP:        "10.0.0.10",
			},
			Replicas:         2,
			Ports:            []instancePortRequest{{Name: "http", ContainerPort: 8080, Protocol: "tcp"}},
			Env:              []instanceEnvRequest{{Name: "MODE", Value: &envValue}},
			VolumeMounts:     []instanceVolumeMountRequest{{VolumeID: "vol-1", MountPath: "/data", ReadOnly: true}},
			FilesystemMounts: []instanceFilesystemMountRequest{{FilesystemID: "fs-1", MountPath: "/mnt"}},
			WorkloadIdentity: &instanceWorkloadIdentityRequest{Enabled: true, Scopes: []string{"scope:instances:read"}},
		},
	}, "tenant-1")
	if err != nil {
		t.Fatalf("instanceSpecFromRequest error = %v", err)
	}
	if spec.ImageID != "img-1" || spec.ImageRef != "harbor.local/app@sha256:abc" {
		t.Fatalf("image identity = %#v/%#v, want request values", spec.ImageID, spec.ImageRef)
	}
	if spec.Labels["team"] != "ml" {
		t.Fatalf("labels = %#v, want team=ml", spec.Labels)
	}
	if spec.Network.VPCID != "vpc-1" || spec.Network.SubnetID != "subnet-1" || spec.Network.PrivateIP != "10.0.0.10" || len(spec.Network.SecurityGroupIDs) != 1 {
		t.Fatalf("network = %+v, want provider-neutral network request", spec.Network)
	}
	if spec.Container == nil || spec.Container.Replicas != 2 || len(spec.Container.PortSpecs) != 1 || len(spec.Container.Env) != 1 || len(spec.Container.VolumeMounts) != 1 || len(spec.Container.FilesystemMounts) != 1 {
		t.Fatalf("container = %+v, want mapped ports/env/storage mounts", spec.Container)
	}
	if spec.Container.VolumeMounts[0].VolumeID != "vol-1" || spec.Container.FilesystemMounts[0].FilesystemID != "fs-1" || spec.Container.Env[0].Name != "MODE" {
		t.Fatalf("container details = %+v, want request values", spec.Container)
	}
	if !spec.Container.WorkloadIdentity.Enabled || len(spec.Container.WorkloadIdentity.Scopes) != 1 {
		t.Fatalf("workload identity = %+v, want enabled scope", spec.Container.WorkloadIdentity)
	}
}

func TestWorkloadLifecycleRequestFromHTTPMapsExtendedPayload(t *testing.T) {
	includeDataDisks := true
	readOnly := true
	replicas := int32(3)
	enabled := false
	lifecycle, err := workloadLifecycleRequestFromHTTP(instanceLifecycleRequest{
		Action:           "resize",
		CPU:              "8",
		Memory:           "16Gi",
		SnapshotName:     "checkpoint",
		SnapshotID:       "snap-1",
		IncludeDataDisks: &includeDataDisks,
		VolumeID:         "vol-1",
		FilesystemID:     "fs-1",
		MountPath:        "/mnt/data",
		ReadOnly:         &readOnly,
		Revision:         "rev-2",
		Replicas:         &replicas,
		ImageID:          "img-2",
		Strategy:         "rolling",
		SecretID:         "secret-1",
		BindingType:      "env",
		EnvName:          "DATABASE_URL",
		SecurityGroupIDs: []string{"sg-1"},
		Enabled:          &enabled,
		Duration:         "15m",
		IdempotencyKey:   "idem-1",
	}, "tenant-1", "instance-1", "user-1")
	if err != nil {
		t.Fatalf("workloadLifecycleRequestFromHTTP error = %v", err)
	}
	if lifecycle.Action != ports.WorkloadLifecycleResize || lifecycle.TenantID != "tenant-1" || lifecycle.InstanceID != "instance-1" || lifecycle.IdempotencyKey != "idem-1" {
		t.Fatalf("lifecycle identity = %+v", lifecycle)
	}
	if lifecycle.Resources.CPU != "8" || lifecycle.Resources.Memory != "16Gi" || lifecycle.SnapshotID != "snap-1" || lifecycle.VolumeID != "vol-1" || lifecycle.FilesystemID != "fs-1" || lifecycle.MountPath != "/mnt/data" {
		t.Fatalf("lifecycle resources = %+v", lifecycle)
	}
	if lifecycle.IncludeDataDisks == nil || !*lifecycle.IncludeDataDisks || lifecycle.ReadOnly == nil || !*lifecycle.ReadOnly || lifecycle.Replicas == nil || *lifecycle.Replicas != 3 || lifecycle.Enabled == nil || *lifecycle.Enabled {
		t.Fatalf("lifecycle optional fields = %+v", lifecycle)
	}
	if lifecycle.ImageID != "img-2" || lifecycle.Strategy != "rolling" || lifecycle.SecretID != "secret-1" || lifecycle.BindingType != "env" || lifecycle.EnvName != "DATABASE_URL" || len(lifecycle.SecurityGroupIDs) != 1 || lifecycle.Duration != 15*time.Minute {
		t.Fatalf("lifecycle operation fields = %+v", lifecycle)
	}
}

func TestInstanceResponseMarksKubernetesProviderAsReal(t *testing.T) {
	api := &instanceAPI{realProvider: true, providerName: "kubernetes_rest"}
	response := api.instanceResponseFromRecord(ports.WorkloadInstanceRecord{
		Provider: "kubernetes",
	})

	if response.DevProfile.Mode != "real" || !response.DevProfile.RealProvider || response.DevProfile.Provider != "kubernetes_rest" {
		t.Fatalf("dev profile = %+v, want real Kubernetes provider marker", response.DevProfile)
	}
}

func TestInstanceResponseIncludesContractSummaryFields(t *testing.T) {
	response := instanceResponseFromRecord(ports.WorkloadInstanceRecord{
		InstanceID:  "inst-1",
		TenantID:    "tenant-a",
		Name:        "app",
		Kind:        ports.WorkloadKindContainer,
		Provider:    "kubernetes",
		Description: "serving app",
		Labels:      map[string]string{"team": "ml"},
		Image: ports.InstanceImageSummary{
			ID:           "img-1",
			Ref:          "harbor.local/tenant-a/app@sha256:abc",
			Digest:       "sha256:abc",
			Name:         "app",
			Tag:          "prod",
			Purpose:      "container",
			Architecture: "amd64",
		},
		Compute: ports.InstanceComputeSummary{
			CPU:      "2",
			Memory:   "4Gi",
			SpecID:   "gpu-a100-full",
			NodeName: "ani-worker-1",
		},
		Network: ports.InstanceNetworkSummary{
			VPCID:     "vpc-1",
			SubnetID:  "subnet-1",
			PrivateIP: "10.0.0.10",
			SecurityGroups: []ports.InstanceSecurityGroupSummary{
				{ID: "sg-1", Name: "default"},
			},
			Endpoints: []ports.InstanceEndpointSummary{
				{Name: "http", Address: "10.0.0.10", Protocol: "tcp", Port: 8080},
			},
		},
		Access: ports.InstanceAccessSummary{
			ExecAvailable: true,
			Reason:        "running",
		},
		StorageAttachments: []ports.WorkloadStorageAttachment{
			{ResourceType: "volume", ResourceID: "vol-1", Name: "data", MountPath: "/data", ReadOnly: true, Status: "attached"},
		},
		Status:    ports.WorkloadStatus{State: ports.WorkloadStateRunning},
		CreatedAt: time.Unix(1000, 0).UTC(),
		UpdatedAt: time.Unix(1100, 0).UTC(),
	})

	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("Marshal response error = %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("Unmarshal response error = %v", err)
	}
	for _, field := range []string{"description", "labels", "image", "compute", "network", "access", "storage_attachments"} {
		if _, ok := body[field]; !ok {
			t.Fatalf("response JSON missing %q: %s", field, raw)
		}
	}
}

func TestRefreshOneStoreStatusSkipsDeletedInstance(t *testing.T) {
	api := &instanceAPI{}
	record := ports.WorkloadInstanceRecord{
		Name:     "deleted-app",
		Provider: "kubernetes",
		Status: ports.WorkloadStatus{
			State: ports.WorkloadStateDeleted,
		},
	}

	api.refreshOneStoreStatus(context.Background(), &record)

	if record.Status.State != ports.WorkloadStateDeleted {
		t.Fatalf("state = %s, want deleted", record.Status.State)
	}
}

func TestInstanceSpecFromRequestMapsSandboxConfig(t *testing.T) {
	spec, err := instanceSpecFromRequest(createInstanceRequest{
		Kind: "sandbox",
		Name: "agent-session",
		SandboxConfig: sandboxConfigRequest{
			RuntimeClass:        "sandbox-kata",
			SessionTimeout:      "45m",
			NetworkEgressPolicy: "deny_all",
		},
	}, "tenant-a")
	if err != nil {
		t.Fatalf("instanceSpecFromRequest error = %v", err)
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

func TestInstanceInstanceServiceSandboxResponseIncludesLocalProfile(t *testing.T) {
	api := newInstanceAPI()
	spec, err := instanceSpecFromRequest(createInstanceRequest{
		Kind: "sandbox",
		Name: "agent-session",
		SandboxConfig: sandboxConfigRequest{
			RuntimeClass:        "sandbox-kata",
			SessionTimeout:      "45m",
			NetworkEgressPolicy: "deny_all",
		},
	}, "tenant-a")
	if err != nil {
		t.Fatalf("instanceSpecFromRequest error = %v", err)
	}
	created, err := api.service.Create(context.Background(), ports.WorkloadInstanceCreateRequest{
		IdempotencyKey:  "create-sandbox-profile",
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
	response := instanceResponseFromRecord(record)
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

func TestInstanceInstanceServiceLifecycleAndOps(t *testing.T) {
	api := newInstanceAPI()
	spec, err := instanceSpecFromRequest(createInstanceRequest{Kind: "container", Name: "demo-app"}, "tenant-a")
	if err != nil {
		t.Fatalf("instanceSpecFromRequest error = %v", err)
	}
	created, err := api.service.Create(context.Background(), ports.WorkloadInstanceCreateRequest{
		IdempotencyKey:  "create-lifecycle-app",
		Spec:            spec,
		UserID:          "user-a",
		PermissionProof: "demo:test",
		RequestedAt:     time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("Create error = %v", err)
	}
	stopped, err := api.service.Stop(context.Background(), ports.WorkloadInstanceLifecycleRequest{
		IdempotencyKey:  "stop-lifecycle-app",
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
		IdempotencyKey:  "start-lifecycle-app",
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

func TestInstanceLifecycleErrorStatusMapsConflict(t *testing.T) {
	err := fmt.Errorf("%w: termination_protection is enabled", ports.ErrConflict)
	if got := instanceLifecycleErrorStatus(err); got != http.StatusConflict {
		t.Fatalf("status = %d, want 409", got)
	}
	if got := instanceLifecycleErrorCode(err); got != "CONFLICT" {
		t.Fatalf("code = %q, want CONFLICT", got)
	}
}

func TestInstanceGatewayRequiresIdempotencyKey(t *testing.T) {
	if hasIdempotencyKey("   ") {
		t.Fatalf("blank idempotency key should be rejected")
	}
	if !hasIdempotencyKey("create-123") {
		t.Fatalf("nonblank idempotency key should be accepted")
	}
}

func TestInstanceInstanceServiceContainerRolloutStatus(t *testing.T) {
	api := newInstanceAPI()
	spec, err := instanceSpecFromRequest(createInstanceRequest{
		Kind:     "container",
		Name:     "demo-rollout",
		Image:    "harbor/demo:2",
		Replicas: 3,
	}, "tenant-a")
	if err != nil {
		t.Fatalf("instanceSpecFromRequest error = %v", err)
	}
	created, err := api.service.Create(context.Background(), ports.WorkloadInstanceCreateRequest{
		IdempotencyKey:  "create-rollout-status",
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
	response := instanceResponseFromRecord(record)
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

func TestInstanceInstanceServiceGPUStatus(t *testing.T) {
	api := newInstanceAPI()
	spec, err := instanceSpecFromRequest(createInstanceRequest{
		Kind:  "gpu_container",
		Name:  "demo-gpu-status",
		Image: "harbor/gpu:2",
		GPU: createGPURequest{
			Vendor: "nvidia",
			Model:  "A100",
			Count:  2,
		},
	}, "tenant-a")
	if err != nil {
		t.Fatalf("instanceSpecFromRequest error = %v", err)
	}
	created, err := api.service.Create(context.Background(), ports.WorkloadInstanceCreateRequest{
		IdempotencyKey:  "create-gpu-status",
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
	response := instanceResponseFromRecord(record)
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

func TestInstanceInstanceOperationsAreQueryable(t *testing.T) {
	api := newInstanceAPI()
	spec, err := instanceSpecFromRequest(createInstanceRequest{Kind: "container", Name: "demo-ops"}, "tenant-a")
	if err != nil {
		t.Fatalf("instanceSpecFromRequest error = %v", err)
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

func TestInstanceInstanceObservabilityResponsesUseLocalProfile(t *testing.T) {
	api := newInstanceAPI()
	spec, err := instanceSpecFromRequest(createInstanceRequest{Kind: "sandbox", Name: "obs-sandbox"}, "tenant-a")
	if err != nil {
		t.Fatalf("instanceSpecFromRequest error = %v", err)
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
	logResponse := instanceLogListFromResult(logs)
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
	metricsResponse := instanceMetricsFromRecord(metrics)
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
	execResponse := instanceExecSessionFromRecord(execSession)
	if execResponse.InstanceID != created.Ref.InstanceID || execResponse.WSURL == "" {
		t.Fatalf("exec response = %+v, want websocket session", execResponse)
	}
	if execResponse.Token != "" {
		t.Fatalf("exec token = %q, want no long-lived credential", execResponse.Token)
	}
	requireLocalCoreDevProfile(t, execResponse.DevProfile, "local-instance-observability")
}

func TestInstanceInstanceObservabilityCanUseInstanceNameForProviderTarget(t *testing.T) {
	api := newInstanceAPIWithObservability(nil, true, nil, nil, nil, nil)
	spec, err := instanceSpecFromRequest(createInstanceRequest{Kind: "container", Name: "s07-observability-live"}, "tenant-a")
	if err != nil {
		t.Fatalf("instanceSpecFromRequest error = %v", err)
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

	localAPI := newInstanceAPIWithObservability(nil, false, nil, nil, nil, nil)
	if got := localAPI.observabilityTargetID(record); got != created.Ref.InstanceID {
		t.Fatalf("local observability target = %q, want instance id %q", got, created.Ref.InstanceID)
	}
}

func TestInstanceInstanceServiceVMConsoleSession(t *testing.T) {
	api := newInstanceAPI()
	spec, err := instanceSpecFromRequest(createInstanceRequest{Kind: "vm", Name: "demo-vm"}, "tenant-a")
	if err != nil {
		t.Fatalf("instanceSpecFromRequest error = %v", err)
	}
	created, err := api.service.Create(context.Background(), ports.WorkloadInstanceCreateRequest{
		IdempotencyKey:  "create-vm-console",
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

func TestInstanceInstanceServiceVMSnapshot(t *testing.T) {
	api := newInstanceAPI()
	spec, err := instanceSpecFromRequest(createInstanceRequest{Kind: "vm", Name: "demo-vm-snapshot"}, "tenant-a")
	if err != nil {
		t.Fatalf("instanceSpecFromRequest error = %v", err)
	}
	created, err := api.service.Create(context.Background(), ports.WorkloadInstanceCreateRequest{
		IdempotencyKey:  "create-vm-snapshot",
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
	response := instanceResponseFromRecord(record)
	if len(response.Snapshots) != 1 || response.Snapshots[0].Name != "before-upgrade" {
		t.Fatalf("response snapshots = %#v, want before-upgrade", response.Snapshots)
	}
}

func TestInstanceInstanceServiceVMVolumeBinding(t *testing.T) {
	api := newInstanceAPI()
	spec, err := instanceSpecFromRequest(createInstanceRequest{Kind: "vm", Name: "demo-vm-volume"}, "tenant-a")
	if err != nil {
		t.Fatalf("instanceSpecFromRequest error = %v", err)
	}
	created, err := api.service.Create(context.Background(), ports.WorkloadInstanceCreateRequest{
		IdempotencyKey:  "create-vm-volume",
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
		MountPath:       "/mnt/vol-data-demo",
		UserID:          "user-a",
		PermissionProof: "demo:test",
		RequestedAt:     time.Unix(1800, 0),
	})
	if err != nil {
		t.Fatalf("AttachVolume error = %v", err)
	}
	response := instanceResponseFromRecord(attached)
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
	if len(instanceResponseFromRecord(detached).Volumes) != 1 {
		t.Fatalf("volumes after detach = %#v, want root disk only", instanceResponseFromRecord(detached).Volumes)
	}
}

func TestInstanceInstanceServiceRealShellExecutesCommand(t *testing.T) {
	shell := firstNonEmpty(os.Getenv("ANI_DEMO_SHELL"), "/bin/sh")
	if _, err := exec.LookPath(shell); err != nil {
		t.Skipf("demo shell %q not available on %s: %v", shell, runtime.GOOS, err)
	}
	record := ports.WorkloadInstanceRecord{
		TenantID:   "tenant-a",
		InstanceID: "instance-shell",
		Name:       "demo-vm-shell",
		Kind:       ports.WorkloadKindVM,
		Provider:   "kubevirt",
		Status:     ports.WorkloadStatus{State: ports.WorkloadStateRunning},
	}
	result, err := runInstanceShellCommand(context.Background(), record, "printf hello")
	if err != nil {
		t.Fatalf("runInstanceShellCommand error = %v", err)
	}
	if result.ExitCode != 0 || strings.TrimSpace(result.Output) != "hello" {
		t.Fatalf("result exit=%d output=%q, want hello", result.ExitCode, result.Output)
	}
	if result.CWD == "" {
		t.Fatalf("CWD is empty")
	}
}

// newInstanceConsoleEngine builds a Hertz engine with the demo instance routes and
// a tenant-context middleware so createConsoleSession handler tests can issue
// real HTTP requests. It creates a running vm instance via HTTP and returns the
// engine plus the created instance id.
func newInstanceConsoleEngine(t *testing.T, denyScope bool) (*server.Hertz, string) {
	t.Helper()
	h := server.New()
	h.Use(func(ctx context.Context, c *app.RequestContext) {
		if denyScope {
			writeInstanceError(c, http.StatusForbidden, "FORBIDDEN", "permission denied")
			c.Abort()
			return
		}
		c.Set("tenant_id", "tenant-a")
		c.Set("user_id", "user-a")
		c.Next(ctx)
	})
	registerInstancesWithObservability(h.Group("/api/v1"), nil, false, nil, nil)
	if denyScope {
		return h, "irrelevant"
	}
	createBody := `{"kind":"vm","name":"demo-vm-console","idempotency_key":"create-vm-console"}`
	createResp := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/instances",
		&ut.Body{Body: bytes.NewBufferString(createBody), Len: len(createBody)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()
	if createResp.StatusCode() != http.StatusCreated {
		t.Fatalf("create vm status = %d, want 201; body=%s", createResp.StatusCode(), createResp.Body())
	}
	instanceID := extractInstanceID(string(createResp.Body()))
	if instanceID == "" {
		t.Fatalf("could not extract instance id from %s", createResp.Body())
	}
	return h, instanceID
}

func TestCreateConsoleSessionSuccessReturns200(t *testing.T) {
	h, instanceID := newInstanceConsoleEngine(t, false)
	body := `{"protocol":"vnc"}`
	resp := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/instances/"+instanceID+"/console",
		&ut.Body{Body: bytes.NewBufferString(body), Len: len(body)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode(), resp.Body())
	}
	if !strings.Contains(string(resp.Body()), "session_id") || !strings.Contains(string(resp.Body()), "connect_url") {
		t.Fatalf("body = %s, want session_id and connect_url", resp.Body())
	}
	if !strings.Contains(string(resp.Body()), `"protocol":"vnc"`) {
		t.Fatalf("body = %s, want protocol vnc", resp.Body())
	}
}

func TestCreateConsoleSessionNonVMReturns400(t *testing.T) {
	h := server.New()
	h.Use(func(ctx context.Context, c *app.RequestContext) {
		c.Set("tenant_id", "tenant-a")
		c.Set("user_id", "user-a")
		c.Next(ctx)
	})
	registerInstancesWithObservability(h.Group("/api/v1"), nil, false, nil, nil)
	// create a container instance via HTTP
	createBody := `{"kind":"container","name":"demo-console-nonvm","idempotency_key":"create-nonvm"}`
	createResp := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/instances",
		&ut.Body{Body: bytes.NewBufferString(createBody), Len: len(createBody)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()
	if createResp.StatusCode() != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", createResp.StatusCode(), createResp.Body())
	}
	instanceID := extractInstanceID(string(createResp.Body()))
	if instanceID == "" {
		t.Fatalf("could not extract instance id from %s", createResp.Body())
	}
	body := `{"protocol":"vnc"}`
	resp := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/instances/"+instanceID+"/console",
		&ut.Body{Body: bytes.NewBufferString(body), Len: len(body)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()
	if resp.StatusCode() != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", resp.StatusCode(), resp.Body())
	}
}

func TestCreateConsoleSessionInvalidProtocolReturns400(t *testing.T) {
	h, instanceID := newInstanceConsoleEngine(t, false)
	body := `{"protocol":"rdp"}`
	resp := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/instances/"+instanceID+"/console",
		&ut.Body{Body: bytes.NewBufferString(body), Len: len(body)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()
	if resp.StatusCode() != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", resp.StatusCode(), resp.Body())
	}
}

func TestCreateConsoleSessionNotRunningReturns422(t *testing.T) {
	h := server.New()
	h.Use(func(ctx context.Context, c *app.RequestContext) {
		c.Set("tenant_id", "tenant-a")
		c.Set("user_id", "user-a")
		c.Next(ctx)
	})
	registerInstancesWithObservability(h.Group("/api/v1"), nil, false, nil, nil)
	createBody := `{"kind":"vm","name":"demo-vm-stopped","idempotency_key":"create-stopped-vm"}`
	createResp := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/instances",
		&ut.Body{Body: bytes.NewBufferString(createBody), Len: len(createBody)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()
	if createResp.StatusCode() != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", createResp.StatusCode(), createResp.Body())
	}
	instanceID := extractInstanceID(string(createResp.Body()))
	if instanceID == "" {
		t.Fatalf("could not extract instance id from %s", createResp.Body())
	}
	// stop the vm
	stopBody := `{"action":"stop","idempotency_key":"stop-vm-console"}`
	stopResp := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/instances/"+instanceID+"/lifecycle",
		&ut.Body{Body: bytes.NewBufferString(stopBody), Len: len(stopBody)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()
	if stopResp.StatusCode() != http.StatusOK {
		t.Fatalf("stop status = %d, want 200; body=%s", stopResp.StatusCode(), stopResp.Body())
	}
	body := `{"protocol":"vnc"}`
	resp := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/instances/"+instanceID+"/console",
		&ut.Body{Body: bytes.NewBufferString(body), Len: len(body)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()
	if resp.StatusCode() != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", resp.StatusCode(), resp.Body())
	}
}

func TestCreateConsoleSessionForbiddenReturns403(t *testing.T) {
	h, _ := newInstanceConsoleEngine(t, true)
	body := `{"protocol":"vnc"}`
	resp := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/instances/any/console",
		&ut.Body{Body: bytes.NewBufferString(body), Len: len(body)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()
	if resp.StatusCode() != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", resp.StatusCode(), resp.Body())
	}
}

func TestCreateSandboxTokenReturnsIdempotentToken(t *testing.T) {
	h := server.New()
	h.Use(func(ctx context.Context, c *app.RequestContext) {
		c.Set("tenant_id", "tenant-a")
		c.Set("user_id", "user-a")
		c.Next(ctx)
	})
	registerInstancesWithObservability(h.Group("/api/v1"), nil, false, nil, nil)
	createBody := `{"kind":"sandbox","name":"agent-session","idempotency_key":"create-token-sandbox","sandbox_config":{"runtime_class":"sandbox-kata"}}`
	createResp := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/instances",
		&ut.Body{Body: bytes.NewBufferString(createBody), Len: len(createBody)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()
	if createResp.StatusCode() != http.StatusCreated {
		t.Fatalf("create sandbox status = %d, want 201; body=%s", createResp.StatusCode(), createResp.Body())
	}
	instanceID := extractInstanceID(string(createResp.Body()))
	if instanceID == "" {
		t.Fatalf("could not extract instance id from %s", createResp.Body())
	}

	body := `{"idempotency_key":"sandbox-token-a","expires_in":"15m","scopes":["connect","files"]}`
	first := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/instances/"+instanceID+"/sandbox/tokens",
		&ut.Body{Body: bytes.NewBufferString(body), Len: len(body)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()
	second := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/instances/"+instanceID+"/sandbox/tokens",
		&ut.Body{Body: bytes.NewBufferString(body), Len: len(body)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()

	if first.StatusCode() != http.StatusCreated || second.StatusCode() != http.StatusCreated {
		t.Fatalf("token statuses = %d/%d, want 201/201; first=%s second=%s", first.StatusCode(), second.StatusCode(), first.Body(), second.Body())
	}
	if string(first.Body()) != string(second.Body()) {
		t.Fatalf("idempotent token response mismatch: first=%s second=%s", first.Body(), second.Body())
	}
	bodyText := string(first.Body())
	if !strings.Contains(bodyText, `"token":"ani.sbx.`) || !strings.Contains(bodyText, `"expires_at":"`) || !strings.Contains(bodyText, `"connect"`) {
		t.Fatalf("token body = %s, want signed token/expires_at/scopes", first.Body())
	}
}

func TestCreateAndDeleteSandboxPort(t *testing.T) {
	h := server.New()
	h.Use(func(ctx context.Context, c *app.RequestContext) {
		c.Set("tenant_id", "tenant-a")
		c.Set("user_id", "user-a")
		c.Next(ctx)
	})
	registerInstancesWithObservability(h.Group("/api/v1"), nil, false, nil, nil)
	createBody := `{"kind":"sandbox","name":"agent-port-session","idempotency_key":"create-port-sandbox","sandbox_config":{"runtime_class":"sandbox-kata"}}`
	createResp := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/instances",
		&ut.Body{Body: bytes.NewBufferString(createBody), Len: len(createBody)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()
	if createResp.StatusCode() != http.StatusCreated {
		t.Fatalf("create sandbox status = %d, want 201; body=%s", createResp.StatusCode(), createResp.Body())
	}
	instanceID := extractInstanceID(string(createResp.Body()))
	if instanceID == "" {
		t.Fatalf("could not extract instance id from %s", createResp.Body())
	}

	body := `{"idempotency_key":"sandbox-port-a","port":8080,"name":"preview","protocol":"http"}`
	openResp := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/instances/"+instanceID+"/sandbox/ports",
		&ut.Body{Body: bytes.NewBufferString(body), Len: len(body)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()
	if openResp.StatusCode() != http.StatusCreated {
		t.Fatalf("open port status = %d, want 201; body=%s", openResp.StatusCode(), openResp.Body())
	}
	if !strings.Contains(string(openResp.Body()), `"port":8080`) || !strings.Contains(string(openResp.Body()), `"status":"available"`) || !strings.Contains(string(openResp.Body()), `"preview_url":"`) {
		t.Fatalf("open port body = %s, want available preview port", openResp.Body())
	}
	getAfterOpen := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/instances/"+instanceID, nil).Result()
	if getAfterOpen.StatusCode() != http.StatusOK || !strings.Contains(string(getAfterOpen.Body()), `"ports":[{"port":8080`) {
		t.Fatalf("instance after open = %d %s, want persisted port summary", getAfterOpen.StatusCode(), getAfterOpen.Body())
	}

	deleteResp := ut.PerformRequest(h.Engine, http.MethodDelete, "/api/v1/instances/"+instanceID+"/sandbox/ports/8080",
		nil,
		ut.Header{Key: "Idempotency-Key", Value: "sandbox-port-delete-a"},
	).Result()
	if deleteResp.StatusCode() != http.StatusOK {
		t.Fatalf("delete port status = %d, want 200; body=%s", deleteResp.StatusCode(), deleteResp.Body())
	}
	if !strings.Contains(string(deleteResp.Body()), `"port":8080`) || !strings.Contains(string(deleteResp.Body()), `"status":"closing"`) {
		t.Fatalf("delete port body = %s, want closing preview port", deleteResp.Body())
	}
	getAfterDelete := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/instances/"+instanceID, nil).Result()
	if getAfterDelete.StatusCode() != http.StatusOK || strings.Contains(string(getAfterDelete.Body()), `"port":8080`) {
		t.Fatalf("instance after delete = %d %s, want port summary removed", getAfterDelete.StatusCode(), getAfterDelete.Body())
	}
}

func TestWriteListAndDeleteSandboxFile(t *testing.T) {
	h := server.New()
	h.Use(func(ctx context.Context, c *app.RequestContext) {
		c.Set("tenant_id", "tenant-a")
		c.Set("user_id", "user-a")
		c.Next(ctx)
	})
	registerInstancesWithObservability(h.Group("/api/v1"), nil, false, nil, nil)
	createBody := `{"kind":"sandbox","name":"agent-file-session","idempotency_key":"create-file-sandbox","sandbox_config":{"runtime_class":"sandbox-kata"}}`
	createResp := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/instances",
		&ut.Body{Body: bytes.NewBufferString(createBody), Len: len(createBody)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()
	if createResp.StatusCode() != http.StatusCreated {
		t.Fatalf("create sandbox status = %d, want 201; body=%s", createResp.StatusCode(), createResp.Body())
	}
	instanceID := extractInstanceID(string(createResp.Body()))
	if instanceID == "" {
		t.Fatalf("could not extract instance id from %s", createResp.Body())
	}

	writeBody := `{"idempotency_key":"sandbox-file-a","path":"workspace/hello.txt","content_base64":"aGVsbG8="}`
	writeResp := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/instances/"+instanceID+"/sandbox/files",
		&ut.Body{Body: bytes.NewBufferString(writeBody), Len: len(writeBody)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()
	if writeResp.StatusCode() != http.StatusCreated {
		t.Fatalf("write file status = %d, want 201; body=%s", writeResp.StatusCode(), writeResp.Body())
	}
	if !strings.Contains(string(writeResp.Body()), `"path":"workspace/hello.txt"`) || !strings.Contains(string(writeResp.Body()), `"kind":"file"`) || !strings.Contains(string(writeResp.Body()), `"size_bytes":5`) {
		t.Fatalf("write file body = %s, want file metadata", writeResp.Body())
	}

	listResp := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/instances/"+instanceID+"/sandbox/files?path=workspace", nil).Result()
	if listResp.StatusCode() != http.StatusOK {
		t.Fatalf("list files status = %d, want 200; body=%s", listResp.StatusCode(), listResp.Body())
	}
	if !strings.Contains(string(listResp.Body()), `"path":"workspace/hello.txt"`) || !strings.Contains(string(listResp.Body()), `"total":1`) {
		t.Fatalf("list files body = %s, want written file", listResp.Body())
	}

	deleteResp := ut.PerformRequest(h.Engine, http.MethodDelete, "/api/v1/instances/"+instanceID+"/sandbox/files?path=workspace/hello.txt",
		nil,
		ut.Header{Key: "Idempotency-Key", Value: "sandbox-file-delete-a"},
	).Result()
	if deleteResp.StatusCode() != http.StatusNoContent {
		t.Fatalf("delete file status = %d, want 204; body=%s", deleteResp.StatusCode(), deleteResp.Body())
	}
}

func TestCreateListAndRestoreSandboxCheckpoint(t *testing.T) {
	h := server.New()
	h.Use(func(ctx context.Context, c *app.RequestContext) {
		c.Set("tenant_id", "tenant-a")
		c.Set("user_id", "user-a")
		c.Next(ctx)
	})
	registerInstancesWithObservability(h.Group("/api/v1"), nil, false, nil, nil)
	createBody := `{"kind":"sandbox","name":"agent-checkpoint-session","image":"registry.example/ani/sandbox:3.12","idempotency_key":"create-checkpoint-sandbox","sandbox_config":{"runtime_class":"sandbox-kata"}}`
	createResp := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/instances",
		&ut.Body{Body: bytes.NewBufferString(createBody), Len: len(createBody)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()
	if createResp.StatusCode() != http.StatusCreated {
		t.Fatalf("create sandbox status = %d, want 201; body=%s", createResp.StatusCode(), createResp.Body())
	}
	instanceID := extractInstanceID(string(createResp.Body()))
	if instanceID == "" {
		t.Fatalf("could not extract instance id from %s", createResp.Body())
	}

	checkpointBody := `{"idempotency_key":"sandbox-checkpoint-a","name":"before-run","keep_memory":false}`
	checkpointResp := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/instances/"+instanceID+"/sandbox/checkpoints",
		&ut.Body{Body: bytes.NewBufferString(checkpointBody), Len: len(checkpointBody)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()
	if checkpointResp.StatusCode() != http.StatusAccepted {
		t.Fatalf("create checkpoint status = %d, want 202; body=%s", checkpointResp.StatusCode(), checkpointResp.Body())
	}
	if got := string(checkpointResp.Header.Get("Location")); !strings.HasPrefix(got, "/api/v1/tasks/") {
		t.Fatalf("create checkpoint Location = %q, want task URL", got)
	}

	listResp := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/instances/"+instanceID+"/sandbox/checkpoints", nil).Result()
	if listResp.StatusCode() != http.StatusOK {
		t.Fatalf("list checkpoints status = %d, want 200; body=%s", listResp.StatusCode(), listResp.Body())
	}
	if !strings.Contains(string(listResp.Body()), `"name":"before-run"`) || !strings.Contains(string(listResp.Body()), `"status":"available"`) || !strings.Contains(string(listResp.Body()), `"total":1`) {
		t.Fatalf("list checkpoints body = %s, want available checkpoint", listResp.Body())
	}
	checkpointID := jsonNestedStringField(t, checkpointResp.Body(), "result", "checkpoint", "id")
	if checkpointID == "" {
		t.Fatalf("could not extract checkpoint id from %s", checkpointResp.Body())
	}

	restoreBody := `{"idempotency_key":"sandbox-checkpoint-restore-a"}`
	restoreResp := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/instances/"+instanceID+"/sandbox/checkpoints/"+checkpointID+"/restore",
		&ut.Body{Body: bytes.NewBufferString(restoreBody), Len: len(restoreBody)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()
	if restoreResp.StatusCode() != http.StatusAccepted {
		t.Fatalf("restore checkpoint status = %d, want 202; body=%s", restoreResp.StatusCode(), restoreResp.Body())
	}
	if !strings.Contains(string(restoreResp.Body()), `"task_type":"sandbox.checkpoint.restore"`) {
		t.Fatalf("restore checkpoint body = %s, want restore task", restoreResp.Body())
	}

	cloneBody := `{"idempotency_key":"sandbox-checkpoint-clone-a","name":"agent-checkpoint-clone"}`
	cloneResp := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/instances/"+instanceID+"/sandbox/checkpoints/"+checkpointID+"/clone",
		&ut.Body{Body: bytes.NewBufferString(cloneBody), Len: len(cloneBody)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()
	if cloneResp.StatusCode() != http.StatusCreated {
		t.Fatalf("clone checkpoint status = %d, want 201; body=%s", cloneResp.StatusCode(), cloneResp.Body())
	}
	if !strings.Contains(string(cloneResp.Body()), `"name":"agent-checkpoint-clone"`) || !strings.Contains(string(cloneResp.Body()), `"kind":"sandbox"`) || !strings.Contains(string(cloneResp.Body()), `"ref":"registry.example/ani/sandbox:3.12"`) {
		t.Fatalf("clone checkpoint body = %s, want cloned sandbox instance", cloneResp.Body())
	}
}

func TestCreateSandboxCodeRunReturnsAcceptedTask(t *testing.T) {
	h := server.New()
	h.Use(func(ctx context.Context, c *app.RequestContext) {
		c.Set("tenant_id", "tenant-a")
		c.Set("user_id", "user-a")
		c.Next(ctx)
	})
	registerInstancesWithObservability(h.Group("/api/v1"), nil, false, nil, nil)
	createBody := `{"kind":"sandbox","name":"agent-code-session","idempotency_key":"create-code-sandbox","sandbox_config":{"runtime_class":"sandbox-kata"}}`
	createResp := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/instances",
		&ut.Body{Body: bytes.NewBufferString(createBody), Len: len(createBody)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()
	if createResp.StatusCode() != http.StatusCreated {
		t.Fatalf("create sandbox status = %d, want 201; body=%s", createResp.StatusCode(), createResp.Body())
	}
	instanceID := extractInstanceID(string(createResp.Body()))
	codeBody := `{"idempotency_key":"sandbox-code-a","language":"python","code":"print('hello')","timeout_seconds":30}`
	runResp := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/instances/"+instanceID+"/sandbox/code-runs",
		&ut.Body{Body: bytes.NewBufferString(codeBody), Len: len(codeBody)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()
	if runResp.StatusCode() != http.StatusAccepted {
		t.Fatalf("code run status = %d, want 202; body=%s", runResp.StatusCode(), runResp.Body())
	}
	if got := string(runResp.Header.Get("Location")); !strings.HasPrefix(got, "/api/v1/tasks/") {
		t.Fatalf("code run Location = %q, want task URL", got)
	}
	if !strings.Contains(string(runResp.Body()), `"task_type":"sandbox.code_run.create"`) || !strings.Contains(string(runResp.Body()), `"language":"python"`) {
		t.Fatalf("code run body = %s, want accepted code-run task", runResp.Body())
	}
}

func TestListInstancesAppliesQueryFiltersSortAndCursor(t *testing.T) {
	h := server.New()
	h.Use(func(ctx context.Context, c *app.RequestContext) {
		c.Set("tenant_id", "tenant-a")
		c.Set("user_id", "user-a")
		c.Next(ctx)
	})
	registerInstancesWithObservability(h.Group("/api/v1"), nil, false, nil, nil)
	for _, name := range []string{"page-charlie", "page-alpha", "page-bravo"} {
		body := fmt.Sprintf(`{"kind":"container","name":%q,"description":"pagination api","idempotency_key":"create-%s"}`, name, name)
		resp := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/instances",
			&ut.Body{Body: bytes.NewBufferString(body), Len: len(body)},
			ut.Header{Key: "Content-Type", Value: "application/json"},
		).Result()
		if resp.StatusCode() != http.StatusCreated {
			t.Fatalf("create %s status = %d, want 201; body=%s", name, resp.StatusCode(), resp.Body())
		}
	}

	first := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/instances?kind=container&keyword=pagination&sort=name_asc&limit=2", nil).Result()
	if first.StatusCode() != http.StatusOK {
		t.Fatalf("list first page status = %d, want 200; body=%s", first.StatusCode(), first.Body())
	}
	firstBody := string(first.Body())
	if !strings.Contains(firstBody, `"name":"page-alpha"`) || !strings.Contains(firstBody, `"name":"page-bravo"`) || strings.Contains(firstBody, `"name":"page-charlie"`) {
		t.Fatalf("first page body = %s, want alpha/bravo only", first.Body())
	}
	if !strings.Contains(firstBody, `"total":3`) || !strings.Contains(firstBody, `"next_cursor":"2"`) {
		t.Fatalf("first page body = %s, want total 3 and next_cursor 2", first.Body())
	}

	second := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/instances?kind=container&keyword=pagination&sort=name_asc&limit=2&cursor=2", nil).Result()
	if second.StatusCode() != http.StatusOK {
		t.Fatalf("list second page status = %d, want 200; body=%s", second.StatusCode(), second.Body())
	}
	secondBody := string(second.Body())
	if !strings.Contains(secondBody, `"name":"page-charlie"`) || strings.Contains(secondBody, `"name":"page-alpha"`) || strings.Contains(secondBody, `"name":"page-bravo"`) {
		t.Fatalf("second page body = %s, want charlie only", second.Body())
	}
	if !strings.Contains(secondBody, `"total":3`) || !strings.Contains(secondBody, `"next_cursor":null`) {
		t.Fatalf("second page body = %s, want total 3 and null next_cursor", second.Body())
	}
}

func TestCreateInstanceResolvesImageIDThroughRegistry(t *testing.T) {
	h := server.New()
	h.Use(func(ctx context.Context, c *app.RequestContext) {
		c.Set("tenant_id", "tenant-a")
		c.Set("user_id", "user-a")
		c.Next(ctx)
	})
	registerInstancesWithObservability(h.Group("/api/v1"), nil, false, nil, nil)

	body := `{"kind":"container","name":"image-id-container","idempotency_key":"create-image-id-container","image_id":"tenant-a/runtime:latest"}`
	resp := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/instances",
		&ut.Body{Body: bytes.NewBufferString(body), Len: len(body)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()
	if resp.StatusCode() != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", resp.StatusCode(), resp.Body())
	}
	responseBody := string(resp.Body())
	if !strings.Contains(responseBody, `"ref":"registry.local/tenant-a/runtime:latest"`) || !strings.Contains(responseBody, `"digest":"sha256:local-runtime"`) || !strings.Contains(responseBody, `"purpose":"container"`) {
		t.Fatalf("create body = %s, want resolved registry image summary", resp.Body())
	}
}

func TestCreateInstanceRejectsRegistryPurposeMismatch(t *testing.T) {
	h := server.New()
	h.Use(func(ctx context.Context, c *app.RequestContext) {
		c.Set("tenant_id", "tenant-a")
		c.Set("user_id", "user-a")
		c.Next(ctx)
	})
	registerInstancesWithObservability(h.Group("/api/v1"), nil, false, nil, nil)

	body := `{"kind":"container","name":"bad-purpose-container","idempotency_key":"create-bad-purpose-container","image_id":"tenant-a/sandbox-runtime:kata-3.8"}`
	resp := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/instances",
		&ut.Body{Body: bytes.NewBufferString(body), Len: len(body)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()
	if resp.StatusCode() != http.StatusUnprocessableEntity {
		t.Fatalf("create status = %d, want 422; body=%s", resp.StatusCode(), resp.Body())
	}
	bodyText := string(resp.Body())
	if !strings.Contains(bodyText, `"code":"ImagePurposeMismatch"`) || !strings.Contains(bodyText, "image purpose") {
		t.Fatalf("create body = %s, want ImagePurposeMismatch", resp.Body())
	}
}

func TestCreateContainerInstanceAcceptsExistingSecretID(t *testing.T) {
	h := server.New()
	h.Use(func(ctx context.Context, c *app.RequestContext) {
		c.Set("tenant_id", "tenant-a")
		c.Set("user_id", "user-a")
		c.Next(ctx)
	})
	RegisterWithOptions(h, RegisterOptions{})

	secretBody := `{"idempotency_key":"create-secret-for-instance","name":"app-secret","data":{"TOKEN":"secret-value"}}`
	secretResp := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/secrets",
		&ut.Body{Body: bytes.NewBufferString(secretBody), Len: len(secretBody)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()
	if secretResp.StatusCode() != http.StatusCreated {
		t.Fatalf("secret create status = %d, want 201; body=%s", secretResp.StatusCode(), secretResp.Body())
	}
	var secret struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(secretResp.Body(), &secret); err != nil {
		t.Fatalf("decode secret response: %v", err)
	}
	if secret.ID == "" {
		t.Fatalf("secret response = %s, want id", secretResp.Body())
	}

	instanceBody := fmt.Sprintf(`{"kind":"container","name":"secret-container","idempotency_key":"create-secret-container","container_config":{"secret_ids":[%q]}}`, secret.ID)
	resp := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/instances",
		&ut.Body{Body: bytes.NewBufferString(instanceBody), Len: len(instanceBody)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()
	if resp.StatusCode() != http.StatusCreated {
		t.Fatalf("instance create status = %d, want 201; body=%s", resp.StatusCode(), resp.Body())
	}
}

func extractInstanceID(body string) string {
	// the create response serializes to {"instance":{"id":"..."},...}
	idx := strings.Index(body, `"id":"`)
	if idx < 0 {
		return ""
	}
	rest := body[idx+len(`"id":"`):]
	end := strings.Index(rest, `"`)
	if end < 0 {
		return ""
	}
	return rest[:end]
}

func TestInstanceInstanceLifecycleForwardsAttachVolumeMountPath(t *testing.T) {
	h := server.New()
	h.Use(func(ctx context.Context, c *app.RequestContext) {
		c.Set("tenant_id", "tenant-a")
		c.Set("user_id", "user-a")
		c.Next(ctx)
	})
	registerInstancesWithObservability(h.Group("/api/v1"), nil, false, nil, nil)

	createBody := `{"kind":"vm","name":"demo-volume-vm","idempotency_key":"create-volume-vm"}`
	createResp := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/instances",
		&ut.Body{Body: bytes.NewBufferString(createBody), Len: len(createBody)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()
	if createResp.StatusCode() != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", createResp.StatusCode(), createResp.Body())
	}
	instanceID := extractInstanceID(string(createResp.Body()))
	if instanceID == "" {
		t.Fatalf("could not extract instance id from %s", createResp.Body())
	}

	attachBody := `{"action":"attach_volume","volume_id":"volume-a","mount_path":"/data","idempotency_key":"attach-volume-a"}`
	attachResp := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/instances/"+instanceID+"/lifecycle",
		&ut.Body{Body: bytes.NewBufferString(attachBody), Len: len(attachBody)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()
	if attachResp.StatusCode() != http.StatusOK {
		t.Fatalf("attach status = %d, want 200; body=%s", attachResp.StatusCode(), attachResp.Body())
	}
	if !strings.Contains(string(attachResp.Body()), `"mount_path":"/data"`) {
		t.Fatalf("attach response = %s, want forwarded mount_path", attachResp.Body())
	}
}

func TestInstanceSpecFromRequestPrefersNestedConfigs(t *testing.T) {
	vmSpec, err := instanceSpecFromRequest(createInstanceRequest{
		Kind: "vm",
		Name: "vm-config-path",
		VMConfig: &vmConfigRequest{
			BootImage:   "images/custom.qcow2",
			SSHUsername: "ani",
			SSHKeyRef:   "secret/ssh-ani",
		},
	}, "tenant-a")
	if err != nil {
		t.Fatalf("vm config path error = %v", err)
	}
	if vmSpec.VM == nil || vmSpec.VM.BootImage != "images/custom.qcow2" || vmSpec.VM.SSHUsername != "ani" || vmSpec.VM.SSHKeySecret != "secret/ssh-ani" {
		t.Fatalf("vm config = %+v, want nested values", vmSpec.VM)
	}

	containerSpec, err := instanceSpecFromRequest(createInstanceRequest{
		Kind:            "container",
		Name:            "container-config-path",
		ContainerConfig: &containerConfigRequest{Replicas: 3},
	}, "tenant-a")
	if err != nil {
		t.Fatalf("container config path error = %v", err)
	}
	if containerSpec.Container == nil || containerSpec.Container.Replicas != 3 {
		t.Fatalf("container replicas = %+v, want 3", containerSpec.Container)
	}

	gpuSpec, err := instanceSpecFromRequest(createInstanceRequest{
		Kind: "gpu_container",
		Name: "gpu-config-path",
		GPUContainerConfig: &gpuContainerConfigRequest{
			Replicas: 2,
			GPU:      createGPURequest{Vendor: "nvidia", Model: "H100", Count: 4, QueueName: "ani-training", WorkloadClass: "training"},
		},
	}, "tenant-a")
	if err != nil {
		t.Fatalf("gpu config path error = %v", err)
	}
	if gpuSpec.Container == nil || gpuSpec.Container.Replicas != 2 {
		t.Fatalf("gpu replicas = %+v, want 2", gpuSpec.Container)
	}
	if gpuSpec.Resources.GPU.RequiredCount != 4 || len(gpuSpec.Resources.GPU.PreferredModels) != 1 || gpuSpec.Resources.GPU.PreferredModels[0] != "H100" {
		t.Fatalf("gpu resources = %+v, want H100 count=4", gpuSpec.Resources.GPU)
	}
	if gpuSpec.Resources.GPU.QueueName != "ani-training" {
		t.Fatalf("gpu queue_name = %q, want ani-training", gpuSpec.Resources.GPU.QueueName)
	}
	if gpuSpec.Resources.GPU.WorkloadClass != ports.WorkloadClassTraining {
		t.Fatalf("gpu workload_class = %q, want training", gpuSpec.Resources.GPU.WorkloadClass)
	}

	sandboxSpec, err := instanceSpecFromRequest(createInstanceRequest{
		Kind: "sandbox",
		Name: "sandbox-config-path",
		SandboxConfig: sandboxConfigRequest{
			RuntimeClass:        "sandbox-kata",
			SessionTimeout:      "20m",
			NetworkEgressPolicy: "allowlist",
		},
	}, "tenant-a")
	if err != nil {
		t.Fatalf("sandbox config path error = %v", err)
	}
	if sandboxSpec.Sandbox == nil || sandboxSpec.Sandbox.SessionTimeout != 20*time.Minute {
		t.Fatalf("sandbox = %+v, want 20m", sandboxSpec.Sandbox)
	}
}

func TestInstanceSpecFromRequestAcceptsFlatAliases(t *testing.T) {
	vmSpec, err := instanceSpecFromRequest(createInstanceRequest{
		Kind:        "vm",
		Name:        "vm-flat",
		BootImage:   "images/flat.qcow2",
		SSHUsername: "ubuntu",
		SSHKeyRef:   "secret/flat",
	}, "tenant-a")
	if err != nil {
		t.Fatalf("flat vm error = %v", err)
	}
	if vmSpec.VM == nil || vmSpec.VM.BootImage != "images/flat.qcow2" {
		t.Fatalf("flat vm = %+v", vmSpec.VM)
	}

	containerSpec, err := instanceSpecFromRequest(createInstanceRequest{
		Kind:     "container",
		Name:     "container-flat",
		Replicas: 5,
	}, "tenant-a")
	if err != nil {
		t.Fatalf("flat container error = %v", err)
	}
	if containerSpec.Container == nil || containerSpec.Container.Replicas != 5 {
		t.Fatalf("flat container = %+v", containerSpec.Container)
	}

	gpuSpec, err := instanceSpecFromRequest(createInstanceRequest{
		Kind:     "gpu_container",
		Name:     "gpu-flat",
		Replicas: 2,
		GPU:      createGPURequest{Vendor: "nvidia", Model: "A100", Count: 2},
	}, "tenant-a")
	if err != nil {
		t.Fatalf("flat gpu error = %v", err)
	}
	if gpuSpec.Resources.GPU.RequiredCount != 2 {
		t.Fatalf("flat gpu count = %d, want 2", gpuSpec.Resources.GPU.RequiredCount)
	}
}

func TestInstanceSpecFromRequestRejectsConfigConflictsAndCrossKind(t *testing.T) {
	_, err := instanceSpecFromRequest(createInstanceRequest{
		Kind:      "vm",
		Name:      "conflict-vm",
		BootImage: "images/flat.qcow2",
		VMConfig:  &vmConfigRequest{BootImage: "images/nested.qcow2"},
	}, "tenant-a")
	if err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("expected boot_image conflict, got %v", err)
	}

	_, err = instanceSpecFromRequest(createInstanceRequest{
		Kind:            "container",
		Name:            "conflict-container",
		Replicas:        2,
		ContainerConfig: &containerConfigRequest{Replicas: 3},
	}, "tenant-a")
	if err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("expected replicas conflict, got %v", err)
	}

	_, err = instanceSpecFromRequest(createInstanceRequest{
		Kind: "gpu_container",
		Name: "conflict-gpu",
		GPU:  createGPURequest{Model: "A100"},
		GPUContainerConfig: &gpuContainerConfigRequest{
			GPU: createGPURequest{Model: "H100"},
		},
	}, "tenant-a")
	if err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("expected gpu.model conflict, got %v", err)
	}

	_, err = instanceSpecFromRequest(createInstanceRequest{
		Kind:               "vm",
		Name:               "cross-kind",
		GPUContainerConfig: &gpuContainerConfigRequest{Replicas: 1},
	}, "tenant-a")
	if err == nil || !strings.Contains(err.Error(), "gpu_container_config") {
		t.Fatalf("expected cross-kind error, got %v", err)
	}

	_, err = instanceSpecFromRequest(createInstanceRequest{
		Kind:     "container",
		Name:     "cross-kind-vm-config",
		VMConfig: &vmConfigRequest{BootImage: "images/x.qcow2"},
	}, "tenant-a")
	if err == nil || !strings.Contains(err.Error(), "vm_config") {
		t.Fatalf("expected vm_config cross-kind error, got %v", err)
	}
}

func TestInstanceSpecFromRequestAllowsMatchingConfigAndFlatAlias(t *testing.T) {
	spec, err := instanceSpecFromRequest(createInstanceRequest{
		Kind:      "vm",
		Name:      "matching-alias",
		BootImage: "images/same.qcow2",
		VMConfig:  &vmConfigRequest{BootImage: "images/same.qcow2"},
	}, "tenant-a")
	if err != nil {
		t.Fatalf("matching alias error = %v", err)
	}
	if spec.VM == nil || spec.VM.BootImage != "images/same.qcow2" {
		t.Fatalf("matching alias vm = %+v", spec.VM)
	}
}

// metricsKindSpy 是一个 ports.InstanceObservability 实现，仅用于捕获 GetMetrics
// 调用时传入的 request.Kind，以验证 handler 是否正确透传 record.Kind。
// 其他方法走空实现，仅满足接口契约，保证 handler 不会因非 metrics 路径报错。
type metricsKindSpy struct {
	capturedKind           ports.WorkloadKind
	capturedEventCursor    string
	capturedSecurityCursor string
	capturedMu             sync.Mutex
}

func newMetricsKindSpy() *metricsKindSpy {
	return &metricsKindSpy{}
}

func (s *metricsKindSpy) GetMetrics(ctx context.Context, request ports.InstanceObservationGetRequest) (ports.InstanceMetricsRecord, error) {
	s.capturedMu.Lock()
	s.capturedKind = request.Kind
	s.capturedMu.Unlock()
	cpu := 18.5
	memUsed := 1536.0
	memTotal := 4096.0
	rx := int64(1048576)
	tx := int64(524288)
	return ports.InstanceMetricsRecord{
		InstanceID:        request.InstanceID,
		Timestamp:         time.Now().UTC(),
		CPUUtilizationPct: &cpu,
		MemoryUsedMB:      &memUsed,
		MemoryTotalMB:     &memTotal,
		NetworkRXBytes:    &rx,
		NetworkTXBytes:    &tx,
		DevProfile: ports.DevProfileInfo{
			Mode:         "local",
			Provider:     "metrics-kind-spy",
			RealProvider: false,
			Reason:       "spy adapter captures Kind for handler pass-through verification",
		},
	}, nil
}

func (s *metricsKindSpy) ListLogs(ctx context.Context, request ports.InstanceObservationListRequest) (ports.InstanceLogListResult, error) {
	return ports.InstanceLogListResult{}, nil
}

func (s *metricsKindSpy) ListEvents(ctx context.Context, request ports.InstanceObservationListRequest) (ports.InstanceEventListResult, error) {
	s.capturedMu.Lock()
	s.capturedEventCursor = request.Cursor
	s.capturedMu.Unlock()
	return ports.InstanceEventListResult{DevProfile: ports.DevProfileInfo{Mode: "local", Provider: "metrics-kind-spy"}}, nil
}

func (s *metricsKindSpy) ListSecurityEvents(ctx context.Context, request ports.InstanceObservationListRequest) (ports.InstanceSecurityEventListResult, error) {
	s.capturedMu.Lock()
	s.capturedSecurityCursor = request.Cursor
	s.capturedMu.Unlock()
	return ports.InstanceSecurityEventListResult{DevProfile: ports.DevProfileInfo{Mode: "local", Provider: "metrics-kind-spy"}}, nil
}

func (s *metricsKindSpy) CreateExecSession(ctx context.Context, request ports.InstanceExecSessionCreateRequest) (ports.InstanceExecSessionRecord, error) {
	return ports.InstanceExecSessionRecord{}, nil
}

func (s *metricsKindSpy) CreateConsoleSession(ctx context.Context, request ports.InstanceConsoleSessionCreateRequest) (ports.InstanceConsoleSessionRecord, error) {
	return ports.InstanceConsoleSessionRecord{}, nil
}

func (s *metricsKindSpy) capturedKindValue() ports.WorkloadKind {
	s.capturedMu.Lock()
	defer s.capturedMu.Unlock()
	return s.capturedKind
}

func (s *metricsKindSpy) capturedCursors() (string, string) {
	s.capturedMu.Lock()
	defer s.capturedMu.Unlock()
	return s.capturedEventCursor, s.capturedSecurityCursor
}

// TestInstanceInstanceGetMetricsHandlerPassesRecordKind 验证 getMetrics handler 把
// record.Kind 透传到 InstanceObservationGetRequest.Kind，覆盖 container/gpu_container/vm
// 三种路径。修复前 handler 未传 Kind，导致 adapter 的 GPU 分支恒不触发。
func TestInstanceInstanceGetMetricsHandlerPassesRecordKind(t *testing.T) {
	for _, tc := range []struct {
		kind     string
		expected ports.WorkloadKind
	}{
		{kind: "container", expected: ports.WorkloadKindContainer},
		{kind: "gpu_container", expected: ports.WorkloadKindGPUContainer},
		{kind: "vm", expected: ports.WorkloadKindVM},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			spy := newMetricsKindSpy()
			h := server.New()
			h.Use(func(ctx context.Context, c *app.RequestContext) {
				c.Set("tenant_id", "tenant-a")
				c.Set("user_id", "user-a")
				c.Next(ctx)
			})
			registerInstancesWithObservability(h.Group("/api/v1"), spy, false, nil, nil)

			createBody := fmt.Sprintf(`{"kind":%q,"name":"demo-metrics-%s","idempotency_key":"create-%s"}`, tc.kind, tc.kind, tc.kind)
			createResp := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/instances",
				&ut.Body{Body: bytes.NewBufferString(createBody), Len: len(createBody)},
				ut.Header{Key: "Content-Type", Value: "application/json"},
			).Result()
			if createResp.StatusCode() != http.StatusCreated {
				t.Fatalf("create %s status = %d, want 201; body=%s", tc.kind, createResp.StatusCode(), createResp.Body())
			}
			instanceID := extractInstanceID(string(createResp.Body()))
			if instanceID == "" {
				t.Fatalf("could not extract instance id from %s", createResp.Body())
			}

			metricsResp := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/instances/"+instanceID+"/metrics", nil).Result()
			if metricsResp.StatusCode() != http.StatusOK {
				t.Fatalf("getMetrics %s status = %d, want 200; body=%s", tc.kind, metricsResp.StatusCode(), metricsResp.Body())
			}

			got := spy.capturedKindValue()
			if got != tc.expected {
				t.Fatalf("captured Kind = %q, want %q (record.Kind should be passed through)", got, tc.expected)
			}
		})
	}
}

func TestInstanceObservationHandlersForwardEventCursors(t *testing.T) {
	spy := newMetricsKindSpy()
	h := server.New()
	h.Use(func(ctx context.Context, c *app.RequestContext) {
		c.Set("tenant_id", "tenant-a")
		c.Set("user_id", "user-a")
		c.Next(ctx)
	})
	registerInstancesWithObservability(h.Group("/api/v1"), spy, false, nil, nil)

	createBody := `{"kind":"sandbox","name":"demo-observation-cursor","idempotency_key":"create-observation-cursor","sandbox_config":{"runtime_class":"sandbox-kata"}}`
	createResp := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/instances",
		&ut.Body{Body: bytes.NewBufferString(createBody), Len: len(createBody)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()
	if createResp.StatusCode() != http.StatusCreated {
		t.Fatalf("create sandbox status = %d, want 201; body=%s", createResp.StatusCode(), createResp.Body())
	}
	instanceID := extractInstanceID(string(createResp.Body()))
	if instanceID == "" {
		t.Fatalf("could not extract instance id from %s", createResp.Body())
	}

	eventsResp := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/instances/"+instanceID+"/events?cursor=evt-cursor-a", nil).Result()
	if eventsResp.StatusCode() != http.StatusOK {
		t.Fatalf("events status = %d, want 200; body=%s", eventsResp.StatusCode(), eventsResp.Body())
	}
	securityResp := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/instances/"+instanceID+"/security-events?cursor=sec-cursor-a", nil).Result()
	if securityResp.StatusCode() != http.StatusOK {
		t.Fatalf("security-events status = %d, want 200; body=%s", securityResp.StatusCode(), securityResp.Body())
	}
	eventCursor, securityCursor := spy.capturedCursors()
	if eventCursor != "evt-cursor-a" {
		t.Fatalf("ListEvents cursor = %q, want evt-cursor-a", eventCursor)
	}
	if securityCursor != "sec-cursor-a" {
		t.Fatalf("ListSecurityEvents cursor = %q, want sec-cursor-a", securityCursor)
	}
}

func TestInstanceCreatePreconditionCodeMapsImageGateReasons(t *testing.T) {
	cases := []struct {
		err  error
		code string
		ok   bool
	}{
		{fmt.Errorf("%w: ImageScanning: image still scanning", ports.ErrFailedPrecondition), "ImageScanning", true},
		{fmt.Errorf("%w: ImageVulnerabilityBlocked: critical=1", ports.ErrFailedPrecondition), "ImageVulnerabilityBlocked", true},
		{fmt.Errorf("%w: ImagePurposeMismatch: purpose sandbox", ports.ErrConflict), "ImagePurposeMismatch", true},
		{fmt.Errorf("%w: ImageNotFound: missing", ports.ErrNotFound), "ImageNotFound", true},
		{fmt.Errorf("%w: unrelated", ports.ErrFailedPrecondition), "", false},
		{errors.New("plain"), "", false},
	}
	for _, tc := range cases {
		code, ok := instanceCreatePreconditionCode(tc.err)
		if ok != tc.ok || code != tc.code {
			t.Fatalf("instanceCreatePreconditionCode(%v) = (%q, %v), want (%q, %v)", tc.err, code, ok, tc.code, tc.ok)
		}
	}
}
