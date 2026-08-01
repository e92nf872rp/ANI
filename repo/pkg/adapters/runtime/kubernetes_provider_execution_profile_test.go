package runtime

import (
	"context"
	"testing"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestKubernetesProviderExecutionProfileOrchestratesCreate(t *testing.T) {
	planner := NewPlanningRuntime()
	client := &profileKubernetesProviderClient{}
	provider := NewKubernetesProviderAdapter(client, WithKubernetesProviderApplyEnabled(true))
	store := newMemoryInstanceStore()
	orchestrator := NewLocalInstanceOrchestrator(
		planner,
		NewKubernetesDryRunRenderer(planner),
		NewLocalAdmissionGuard(),
		fakePlanAuditStore{},
		provider,
		provider,
		provider,
		NewLocalStatusReconciler(),
		WithInstanceStore(store),
	)

	result, err := orchestrator.Create(context.Background(), ports.WorkloadInstanceCreateRequest{
		Spec: ports.WorkloadSpec{
			TenantID: "tenant-a",
			Name:     "app-01",
			Kind:     ports.WorkloadKindContainer,
			Image:    "harbor/app:1",
		},
		UserID:          "user-a",
		PermissionProof: "rbac:create:workload",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if !result.Orchestrated {
		t.Fatalf("Orchestrated = false, want true")
	}
	if result.FinalStatus.State != ports.WorkloadStateRunning {
		t.Fatalf("FinalStatus.State = %s, want running", result.FinalStatus.State)
	}
	if client.serverSideDryRuns != 1 {
		t.Fatalf("serverSideDryRuns = %d, want 1", client.serverSideDryRuns)
	}
	if client.applies != 1 {
		t.Fatalf("applies = %d, want 1", client.applies)
	}
	if client.observes != 1 {
		t.Fatalf("observes = %d, want 1", client.observes)
	}
	if client.lastApply.AuditID == "" || client.lastApply.PermissionProof == "" {
		t.Fatalf("apply missing audit or permission proof: audit=%q proof=%q", client.lastApply.AuditID, client.lastApply.PermissionProof)
	}
	if client.lastApply.DryRunResult.Reason != "accepted by Kubernetes server-side dry-run dryRun=All" {
		t.Fatalf("dry-run reason = %q, want server-side dryRun=All evidence", client.lastApply.DryRunResult.Reason)
	}
	if len(result.Apply.ResourceRefs) != 1 {
		t.Fatalf("resource refs = %#v, want one provider ref", result.Apply.ResourceRefs)
	}

	record, err := store.Get(context.Background(), "tenant-a", result.Ref.InstanceID)
	if err != nil {
		t.Fatalf("Get() stored record error = %v", err)
	}
	if len(record.ResourceRefs) != 1 || record.Status.State != ports.WorkloadStateRunning {
		t.Fatalf("stored record refs=%#v state=%s, want refs and running", record.ResourceRefs, record.Status.State)
	}
}

func TestKubernetesProviderAdapterAllowsAuxiliaryIdentitySecretForKubeVirtVM(t *testing.T) {
	client := &profileKubernetesProviderClient{}
	provider := NewKubernetesProviderAdapter(client, WithKubernetesProviderApplyEnabled(true))
	admission := ports.WorkloadAdmissionResult{Allowed: true}
	manifests := []ports.WorkloadManifest{
		{
			Name:     "vm-01",
			Kind:     "VirtualMachine",
			Provider: "kubevirt",
			Content:  `{"apiVersion":"kubevirt.io/v1","kind":"VirtualMachine","metadata":{"name":"vm-01","namespace":"ani-tenant-tenant-a"}}`,
		},
		{
			Name:     "ani-wi-vm-01",
			Kind:     "Secret",
			Provider: "kubernetes",
			Content:  `{"apiVersion":"v1","kind":"Secret","metadata":{"name":"ani-wi-vm-01","namespace":"ani-tenant-tenant-a"}}`,
		},
	}

	result, err := provider.DryRun(context.Background(), manifests, admission)
	if err != nil {
		t.Fatalf("DryRun() error = %v", err)
	}
	if !result.Accepted || result.Provider != "kubevirt" {
		t.Fatalf("DryRun() = %#v, want accepted kubevirt batch", result)
	}
}

func TestKubernetesProviderAdapterAllowsAuxiliaryPVCForKubeVirtVM(t *testing.T) {
	client := &profileKubernetesProviderClient{}
	provider := NewKubernetesProviderAdapter(client, WithKubernetesProviderApplyEnabled(true))
	admission := ports.WorkloadAdmissionResult{Allowed: true}
	manifests := []ports.WorkloadManifest{
		{
			Name:     "vm-01",
			Kind:     "VirtualMachine",
			Provider: "kubevirt",
			Content:  `{"apiVersion":"kubevirt.io/v1","kind":"VirtualMachine","metadata":{"name":"vm-01","namespace":"ani-tenant-tenant-a"}}`,
		},
		{
			Name:     "vm-01-root",
			Kind:     "PersistentVolumeClaim",
			Provider: "kubernetes",
			Content:  `{"apiVersion":"v1","kind":"PersistentVolumeClaim","metadata":{"name":"vm-01-root","namespace":"ani-tenant-tenant-a"},"spec":{"accessModes":["ReadWriteOnce"],"resources":{"requests":{"storage":"40Gi"}}}}`,
		},
	}

	result, err := provider.DryRun(context.Background(), manifests, admission)
	if err != nil {
		t.Fatalf("DryRun() error = %v", err)
	}
	if !result.Accepted || result.Provider != "kubevirt" {
		t.Fatalf("DryRun() = %#v, want accepted kubevirt batch", result)
	}
}

type profileKubernetesProviderClient struct {
	serverSideDryRuns int
	applies           int
	observes          int
	lastApply         ports.WorkloadProviderApplyRequest
}

func (c *profileKubernetesProviderClient) ServerSideDryRun(_ context.Context, manifests []ports.WorkloadManifest) (ports.WorkloadProviderDryRunResult, error) {
	c.serverSideDryRuns++
	return ports.WorkloadProviderDryRunResult{
		Accepted:      true,
		Provider:      manifests[0].Provider,
		ManifestCount: len(manifests),
		Reason:        "accepted by Kubernetes server-side dry-run dryRun=All",
	}, nil
}

func (c *profileKubernetesProviderClient) Apply(_ context.Context, request ports.WorkloadProviderApplyRequest) (ports.WorkloadProviderApplyResult, error) {
	c.applies++
	c.lastApply = request
	refs := make([]string, 0, len(request.Manifests))
	for _, manifest := range request.Manifests {
		refs = append(refs, manifest.Provider+"/"+manifest.Kind+"/"+manifest.Name)
	}
	return ports.WorkloadProviderApplyResult{
		Applied:       true,
		Provider:      request.Manifests[0].Provider,
		ManifestCount: len(request.Manifests),
		Operation:     request.Operation,
		ResourceRefs:  refs,
		Reason:        "applied by KubernetesProviderClient execution profile",
	}, nil
}

func (c *profileKubernetesProviderClient) Observe(_ context.Context, request ports.WorkloadProviderStatusRequest) (ports.WorkloadProviderObservation, error) {
	c.observes++
	return ports.WorkloadProviderObservation{
		TenantID:     request.TenantID,
		InstanceID:   request.InstanceID,
		Kind:         request.Kind,
		Provider:     request.ApplyResult.Provider,
		ResourceRefs: request.ApplyResult.ResourceRefs,
		Phase:        "Running",
		NodeName:     "node-a",
	}, nil
}

var _ KubernetesProviderClient = (*profileKubernetesProviderClient)(nil)
