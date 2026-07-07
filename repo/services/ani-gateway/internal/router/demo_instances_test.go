package router

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
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

func TestDemoSpecFromRequestMapsContainerCommandAndArgs(t *testing.T) {
	spec, err := demoSpecFromRequest(demoCreateInstanceRequest{
		Kind:    "container",
		Name:    "demo-command-app",
		Image:   "dockerproxy.net/library/busybox:1.36",
		Command: []string{"sh", "-c"},
		Args:    []string{"sleep 3600"},
	}, "tenant-a")
	if err != nil {
		t.Fatalf("demoSpecFromRequest error = %v", err)
	}
	if strings.Join(spec.Command, " ") != "sh -c" || strings.Join(spec.Args, " ") != "sleep 3600" {
		t.Fatalf("command=%#v args=%#v, want request command/args", spec.Command, spec.Args)
	}
}

func TestDemoInstanceServiceKeepsDisplayNameAndPrefixesProviderContainerName(t *testing.T) {
	api := newDemoInstanceAPI()
	spec, err := demoSpecFromRequest(demoCreateInstanceRequest{
		Kind:  "container",
		Name:  "qqqq",
		Image: "dockerproxy.net/library/busybox:1.36",
	}, "tenant-a")
	if err != nil {
		t.Fatalf("demoSpecFromRequest error = %v", err)
	}

	result, err := api.service.Create(context.Background(), ports.WorkloadInstanceCreateRequest{
		Spec:            spec,
		UserID:          "user-a",
		PermissionProof: "demo:test",
		RequestedAt:     time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("Create error = %v", err)
	}
	record, err := api.service.Get(context.Background(), ports.WorkloadInstanceGetRequest{
		TenantID:   result.Ref.TenantID,
		InstanceID: result.Ref.InstanceID,
	})
	if err != nil {
		t.Fatalf("Get error = %v", err)
	}
	if record.Name != "qqqq" {
		t.Fatalf("record name = %q, want display name qqqq", record.Name)
	}
	if len(record.ResourceRefs) == 0 {
		t.Fatalf("resource refs are empty, want provider Deployment ref")
	}
	found := false
	for _, ref := range record.ResourceRefs {
		if strings.HasPrefix(ref, "kubernetes/Deployment/container-qqqq-") {
			found = true
			break
		}
		if ref == "kubernetes/Deployment/qqqq" {
			t.Fatalf("resource ref = %q, want container-prefixed provider name", ref)
		}
	}
	if !found {
		t.Fatalf("resource refs = %#v, want kubernetes/Deployment/container-qqqq-*", record.ResourceRefs)
	}
}

func TestDemoInstanceNetworkSelectionValidation(t *testing.T) {
	ctx := context.Background()
	tenantID := "11111111-1111-1111-1111-111111111111"
	network := runtimeadapter.NewLocalNetworkService()
	vpc, err := network.CreateVPC(ctx, ports.NetworkVPCCreateRequest{
		TenantID:       tenantID,
		IdempotencyKey: "vpc-network-selection",
		Name:           "test",
		CIDR:           "10.72.0.0/24",
	})
	if err != nil {
		t.Fatalf("CreateVPC error = %v", err)
	}
	subnet, err := network.CreateSubnet(ctx, ports.NetworkSubnetCreateRequest{
		TenantID:       tenantID,
		IdempotencyKey: "subnet-network-selection",
		VPCID:          vpc.VPCID,
		Name:           "test-subnet",
		CIDR:           "10.72.0.0/25",
		Gateway:        "10.72.0.1",
	})
	if err != nil {
		t.Fatalf("CreateSubnet error = %v", err)
	}
	api := newDemoInstanceAPIWithNetworkService(network)

	tests := []struct {
		name    string
		tenant  string
		network demoCreateNetworkRequest
		wantErr string
	}{
		{
			name:    "subnet missing",
			tenant:  tenantID,
			network: demoCreateNetworkRequest{SubnetID: "subnet_missing"},
			wantErr: "network.subnet_id subnet_missing does not exist",
		},
		{
			name:    "subnet belongs to another tenant",
			tenant:  "22222222-2222-2222-2222-222222222222",
			network: demoCreateNetworkRequest{SubnetID: subnet.SubnetID},
			wantErr: "network.subnet_id " + subnet.SubnetID + " does not exist",
		},
		{
			name:    "vpc mismatch",
			tenant:  tenantID,
			network: demoCreateNetworkRequest{VPCID: "vpc_other", SubnetID: subnet.SubnetID},
			wantErr: "network.vpc_id must match subnet.vpc_id",
		},
		{
			name:    "private ip outside cidr",
			tenant:  tenantID,
			network: demoCreateNetworkRequest{SubnetID: subnet.SubnetID, PrivateIP: stringPtr("10.72.0.200")},
			wantErr: "network.private_ip must be inside subnet.cidr",
		},
		{
			name:    "private ip equals gateway",
			tenant:  tenantID,
			network: demoCreateNetworkRequest{SubnetID: subnet.SubnetID, PrivateIP: stringPtr("10.72.0.1")},
			wantErr: "network.private_ip must not equal subnet.gateway",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec, err := demoSpecFromRequest(demoCreateInstanceRequest{
				Kind:    "container",
				Name:    "net-test",
				Network: tt.network,
			}, tt.tenant)
			if err != nil {
				t.Fatalf("demoSpecFromRequest error = %v", err)
			}
			err = api.validateInstanceNetworkSelection(ctx, tt.tenant, &spec, tt.network)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateInstanceNetworkSelection error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestDemoInstanceNetworkSelectionRequiresAvailableSubnetAndVPC(t *testing.T) {
	ctx := context.Background()
	tenantID := "11111111-1111-1111-1111-111111111111"
	vpc := ports.NetworkVPCRecord{TenantID: tenantID, VPCID: "vpc_unavailable", Name: "test-unavailable-vpc", State: ports.NetworkResourceAvailable}
	subnet := ports.NetworkSubnetRecord{TenantID: tenantID, SubnetID: "subnet_unavailable", VPCID: vpc.VPCID, Name: "test-unavailable-subnet", CIDR: "10.73.0.0/25", Gateway: "10.73.0.1", State: ports.NetworkResourceFailed}
	network := &fakeInstanceNetworkService{vpc: vpc, subnet: subnet}
	api := newDemoInstanceAPIWithNetworkService(network)
	spec, err := demoSpecFromRequest(demoCreateInstanceRequest{
		Kind:    "container",
		Name:    "net-test",
		Network: demoCreateNetworkRequest{SubnetID: subnet.SubnetID},
	}, tenantID)
	if err != nil {
		t.Fatalf("demoSpecFromRequest error = %v", err)
	}
	if err := api.validateInstanceNetworkSelection(ctx, tenantID, &spec, demoCreateNetworkRequest{SubnetID: subnet.SubnetID}); err == nil || !strings.Contains(err.Error(), "network.subnet_id must reference an available subnet") {
		t.Fatalf("subnet unavailable error = %v", err)
	}

	network.subnet.State = ports.NetworkResourceAvailable
	network.vpc.State = ports.NetworkResourceFailed
	if err := api.validateInstanceNetworkSelection(ctx, tenantID, &spec, demoCreateNetworkRequest{SubnetID: subnet.SubnetID}); err == nil || !strings.Contains(err.Error(), "network.vpc_id must reference an available vpc") {
		t.Fatalf("vpc unavailable error = %v", err)
	}
}

func TestDemoInstanceValidNetworkSelectionIsSavedInRecord(t *testing.T) {
	ctx := context.Background()
	tenantID := "11111111-1111-1111-1111-111111111111"
	network := runtimeadapter.NewLocalNetworkService()
	vpc, err := network.CreateVPC(ctx, ports.NetworkVPCCreateRequest{
		TenantID:       tenantID,
		IdempotencyKey: "vpc-valid",
		Name:           "test",
		CIDR:           "10.72.0.0/24",
	})
	if err != nil {
		t.Fatalf("CreateVPC error = %v", err)
	}
	subnet, err := network.CreateSubnet(ctx, ports.NetworkSubnetCreateRequest{
		TenantID:       tenantID,
		IdempotencyKey: "subnet-valid",
		VPCID:          vpc.VPCID,
		Name:           "test-subnet",
		CIDR:           "10.72.0.0/25",
		Gateway:        "10.72.0.1",
	})
	if err != nil {
		t.Fatalf("CreateSubnet error = %v", err)
	}
	api := newDemoInstanceAPIWithNetworkService(network)
	req := demoCreateNetworkRequest{VPCID: vpc.VPCID, SubnetID: subnet.SubnetID, PrivateIP: stringPtr("10.72.0.10")}
	spec, err := demoSpecFromRequest(demoCreateInstanceRequest{
		Kind:    "container",
		Name:    "net-valid",
		Network: req,
	}, tenantID)
	if err != nil {
		t.Fatalf("demoSpecFromRequest error = %v", err)
	}
	if err := api.validateInstanceNetworkSelection(ctx, tenantID, &spec, req); err != nil {
		t.Fatalf("validateInstanceNetworkSelection error = %v", err)
	}
	result, err := api.service.Create(ctx, ports.WorkloadInstanceCreateRequest{
		IdempotencyKey:  "valid-network-create",
		Spec:            spec,
		UserID:          "user-a",
		PermissionProof: "demo:test",
		RequestedAt:     time.Unix(2200, 0),
	})
	if err != nil {
		t.Fatalf("Create error = %v", err)
	}
	record, err := api.service.Get(ctx, ports.WorkloadInstanceGetRequest{TenantID: tenantID, InstanceID: result.Ref.InstanceID})
	if err != nil {
		t.Fatalf("Get error = %v", err)
	}
	response := api.demoInstanceFromRecord(record)
	if response.VPCID == nil || *response.VPCID != vpc.VPCID || response.SubnetID == nil || *response.SubnetID != subnet.SubnetID || response.PrivateIP == nil || *response.PrivateIP != "10.72.0.10" {
		t.Fatalf("network response = vpc=%v subnet=%v ip=%v, want saved network selection", response.VPCID, response.SubnetID, response.PrivateIP)
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

type fakeInstanceNetworkService struct {
	ports.NetworkService
	vpc    ports.NetworkVPCRecord
	subnet ports.NetworkSubnetRecord
}

func (s *fakeInstanceNetworkService) GetSubnet(_ context.Context, request ports.NetworkResourceGetRequest) (ports.NetworkSubnetRecord, error) {
	if request.TenantID == s.subnet.TenantID && request.ResourceID == s.subnet.SubnetID {
		return s.subnet, nil
	}
	return ports.NetworkSubnetRecord{}, ports.ErrNotFound
}

func (s *fakeInstanceNetworkService) GetVPC(_ context.Context, request ports.NetworkResourceGetRequest) (ports.NetworkVPCRecord, error) {
	if request.TenantID == s.vpc.TenantID && request.ResourceID == s.vpc.VPCID {
		return s.vpc, nil
	}
	return ports.NetworkVPCRecord{}, ports.ErrNotFound
}

func stringPtr(value string) *string {
	return &value
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
	if execResponse.Token == "" || !strings.Contains(execResponse.WSURL, "token=") {
		t.Fatalf("exec response = %+v, want short-lived token embedded in websocket URL", execResponse)
	}
	requireLocalCoreDevProfile(t, execResponse.DevProfile, "local-instance-observability")
}

func TestDemoInstanceExecWebSocketRejectsMissingToken(t *testing.T) {
	h := server.New()
	RegisterWithOptions(h, RegisterOptions{})
	instanceID := createDemoInstanceForLogs(t, h)

	resp := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/instances/"+instanceID+"/exec/11111111-1111-1111-1111-111111111111", nil).Result()
	if resp.StatusCode() != http.StatusUnauthorized {
		t.Fatalf("exec websocket status = %d body=%s, want 401", resp.StatusCode(), resp.Body())
	}
}

func TestDemoInstanceLogsEndpointUsesFollowParameterForSSE(t *testing.T) {
	h := server.New()
	RegisterWithOptions(h, RegisterOptions{})
	instanceID := createDemoInstanceForLogs(t, h)

	listResp := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/instances/"+instanceID+"/logs?limit=1", nil).Result()
	if listResp.StatusCode() != http.StatusOK {
		t.Fatalf("list logs status = %d body=%s, want 200", listResp.StatusCode(), listResp.Body())
	}
	if got := string(listResp.Header.Get("Content-Type")); !strings.Contains(got, "text/plain") {
		t.Fatalf("list logs content-type = %q, want text/plain", got)
	}
	if got := string(listResp.Body()); strings.Contains(got, `"items"`) || strings.Contains(got, "event: log") || strings.TrimSpace(got) == "" {
		t.Fatalf("list logs body = %q, want plain text log content", got)
	}

	streamResp := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/instances/"+instanceID+"/logs?follow=true&tail_lines=2", nil).Result()
	if streamResp.StatusCode() != http.StatusOK {
		t.Fatalf("stream logs status = %d body=%s, want 200", streamResp.StatusCode(), streamResp.Body())
	}
	if got := string(streamResp.Header.Get("Content-Type")); !strings.Contains(got, "text/event-stream") {
		t.Fatalf("stream logs content-type = %q, want text/event-stream", got)
	}
	if got := string(streamResp.Body()); !strings.Contains(got, "event: log\n") || !strings.Contains(got, `"message"`) {
		t.Fatalf("stream logs body = %q, want SSE log events", got)
	}
}

func TestDemoInstanceLogsStreamEndpointIsNotRegistered(t *testing.T) {
	h := server.New()
	RegisterWithOptions(h, RegisterOptions{})
	instanceID := createDemoInstanceForLogs(t, h)

	resp := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/instances/"+instanceID+"/logs/stream", nil).Result()
	if resp.StatusCode() != http.StatusNotFound {
		t.Fatalf("legacy stream route status = %d, want 404", resp.StatusCode())
	}
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
	record.ResourceRefs = nil
	if got := api.observabilityTargetID(record); got != "s07-observability-live" {
		t.Fatalf("observability target = %q, want instance name", got)
	}

	localAPI := newDemoInstanceAPIWithObservability(nil, false)
	if got := localAPI.observabilityTargetID(record); got != created.Ref.InstanceID {
		t.Fatalf("local observability target = %q, want instance id %q", got, created.Ref.InstanceID)
	}
}

func TestDemoInstanceObservabilityUsesProviderWorkloadRefBeforeDisplayName(t *testing.T) {
	api := newDemoInstanceAPIWithObservability(nil, true)
	record := ports.WorkloadInstanceRecord{
		InstanceID:   "inst-ttt",
		Name:         "ttt",
		ResourceRefs: []string{"kubernetes/Secret/container-ttt-identity", "kubernetes/Deployment/container-ttt-094ae46b"},
	}
	if got := api.observabilityTargetID(record); got != "container-ttt-094ae46b" {
		t.Fatalf("observability target = %q, want provider deployment name", got)
	}
	localAPI := newDemoInstanceAPIWithObservability(nil, false)
	if got := localAPI.observabilityTargetID(record); got != "inst-ttt" {
		t.Fatalf("local observability target = %q, want instance id", got)
	}
}

func createDemoInstanceForLogs(t *testing.T, h *server.Hertz) string {
	t.Helper()
	body := `{"idempotency_key":"logs-follow-test","kind":"container","name":"logs-follow","image":"dockerproxy.net/library/busybox:1.36"}`
	resp := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/instances", &ut.Body{Body: bytes.NewBufferString(body), Len: len(body)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()
	if resp.StatusCode() != http.StatusCreated {
		t.Fatalf("create instance status = %d body=%s, want 201", resp.StatusCode(), resp.Body())
	}
	var payload struct {
		Instance struct {
			ID string `json:"id"`
		} `json:"instance"`
	}
	if err := json.Unmarshal(resp.Body(), &payload); err != nil {
		t.Fatalf("decode create response: %v body=%s", err, resp.Body())
	}
	if strings.TrimSpace(payload.Instance.ID) == "" {
		t.Fatalf("create response missing instance.id: %s", resp.Body())
	}
	return payload.Instance.ID
}

func TestDemoInstanceLogSSEEncoding(t *testing.T) {
	var buffer bytes.Buffer
	err := writeInstanceLogSSE(&buffer, ports.InstanceLogEntry{
		Timestamp: time.Date(2026, 7, 6, 16, 30, 0, 0, time.UTC),
		Level:     "info",
		Message:   "container ready",
		Container: "main",
		Stream:    "stdout",
	})
	if err != nil {
		t.Fatalf("writeInstanceLogSSE error = %v", err)
	}
	got := buffer.String()
	if !strings.HasPrefix(got, "event: log\n") || !strings.Contains(got, `"message":"container ready"`) || !strings.HasSuffix(got, "\n\n") {
		t.Fatalf("SSE payload = %q, want log event with JSON data", got)
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
	api := newDemoInstanceAPIWithOptions(nil, workload, nil, nil, nil, false)
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
