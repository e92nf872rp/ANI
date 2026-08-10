package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestKubernetesProviderAdapterUsesServerSideDryRun(t *testing.T) {
	client := &fakeKubernetesProviderClient{}
	manifests := renderedDeployment(t)
	admission, err := NewLocalAdmissionGuard().Review(context.Background(), manifests)
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}

	result, err := NewKubernetesProviderAdapter(client).DryRun(context.Background(), manifests, admission)
	if err != nil {
		t.Fatalf("DryRun() error = %v", err)
	}
	if !result.Accepted {
		t.Fatalf("Accepted = false, reason = %s", result.Reason)
	}
	if client.dryRuns != 1 {
		t.Fatalf("dryRuns = %d, want 1", client.dryRuns)
	}
}

func TestKubernetesProviderAdapterApplyFailsClosed(t *testing.T) {
	client := &fakeKubernetesProviderClient{}
	result, err := NewKubernetesProviderAdapter(client).Apply(context.Background(), validProviderApplyRequest(t))
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if result.Applied {
		t.Fatalf("Applied = true, want false")
	}
	if client.applies != 0 {
		t.Fatalf("applies = %d, want 0", client.applies)
	}
}

func TestKubernetesProviderAdapterAppliesWhenEnabled(t *testing.T) {
	client := &fakeKubernetesProviderClient{}
	result, err := NewKubernetesProviderAdapter(
		client,
		WithKubernetesProviderApplyEnabled(true),
		WithKubernetesProviderClock(func() time.Time { return time.Unix(700, 0) }),
	).Apply(context.Background(), validProviderApplyRequest(t))
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if !result.Applied {
		t.Fatalf("Applied = false, reason = %s", result.Reason)
	}
	if len(result.ResourceRefs) != 1 || !strings.Contains(result.ResourceRefs[0], "Deployment") {
		t.Fatalf("ResourceRefs = %#v, want Deployment ref", result.ResourceRefs)
	}
}

func TestKubernetesProviderAdapterApplyFallbackUsesPrimaryProvider(t *testing.T) {
	client := &fakeKubernetesProviderClient{returnEmptyProvider: true}
	request := validProviderApplyRequest(t)
	request.Manifests = []ports.WorkloadManifest{
		{
			Name: "ani-wi-vm-01", Kind: "Secret", Provider: "kubernetes",
			Content: `{"apiVersion":"v1","kind":"Secret","metadata":{"name":"ani-wi-vm-01","namespace":"ani-tenant-tenant-a"}}`,
		},
		{
			Name: "vm-01", Kind: "VirtualMachine", Provider: "kubevirt",
			Content: `{"apiVersion":"kubevirt.io/v1","kind":"VirtualMachine","metadata":{"name":"vm-01","namespace":"ani-tenant-tenant-a"}}`,
		},
	}
	request.DryRunResult.Provider = "kubevirt"
	request.DryRunResult.ManifestCount = len(request.Manifests)
	result, err := NewKubernetesProviderAdapter(client, WithKubernetesProviderApplyEnabled(true)).Apply(context.Background(), request)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if result.Provider != "kubevirt" {
		t.Fatalf("Provider = %q, want primary provider kubevirt", result.Provider)
	}
}

func TestKubernetesProviderAdapterObservesProviderStatus(t *testing.T) {
	client := &fakeKubernetesProviderClient{}
	observation, err := NewKubernetesProviderAdapter(client).Observe(context.Background(), ports.WorkloadProviderStatusRequest{
		TenantID:   "tenant-a",
		InstanceID: "instance-a",
		Kind:       ports.WorkloadKindContainer,
		ApplyResult: ports.WorkloadProviderApplyResult{
			Applied:      true,
			Provider:     "kubernetes",
			Operation:    ports.WorkloadLifecycleCreate,
			ResourceRefs: []string{"kubernetes/Deployment/app-01"},
		},
	})
	if err != nil {
		t.Fatalf("Observe() error = %v", err)
	}
	if observation.Phase != "Running" {
		t.Fatalf("Phase = %q, want Running", observation.Phase)
	}
	if client.observes != 1 {
		t.Fatalf("observes = %d, want 1", client.observes)
	}
}

func renderedDeployment(t *testing.T) []ports.WorkloadManifest {
	t.Helper()
	manifests, err := NewKubernetesDryRunRenderer(NewPlanningRuntime()).Render(context.Background(), ports.WorkloadSpec{
		TenantID: "tenant-a",
		Name:     "app-01",
		Kind:     ports.WorkloadKindContainer,
		Image:    "harbor/app:1",
	})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	return manifests
}

type fakeKubernetesProviderClient struct {
	dryRuns             int
	applies             int
	observes            int
	returnEmptyProvider bool
}

func (c *fakeKubernetesProviderClient) ServerSideDryRun(_ context.Context, manifests []ports.WorkloadManifest) (ports.WorkloadProviderDryRunResult, error) {
	c.dryRuns++
	return ports.WorkloadProviderDryRunResult{
		Accepted:      true,
		Provider:      manifests[0].Provider,
		ManifestCount: len(manifests),
		Reason:        "accepted by Kubernetes server-side dry-run dryRun=All",
	}, nil
}

func (c *fakeKubernetesProviderClient) Apply(_ context.Context, request ports.WorkloadProviderApplyRequest) (ports.WorkloadProviderApplyResult, error) {
	c.applies++
	provider := request.Manifests[0].Provider
	if c.returnEmptyProvider {
		provider = ""
	}
	return ports.WorkloadProviderApplyResult{
		Applied:       true,
		Provider:      provider,
		ManifestCount: len(request.Manifests),
		Operation:     request.Operation,
		ResourceRefs:  []string{request.Manifests[0].Provider + "/" + request.Manifests[0].Kind + "/" + request.Manifests[0].Name},
		Reason:        "applied by Kubernetes provider adapter",
	}, nil
}

func (c *fakeKubernetesProviderClient) Observe(_ context.Context, request ports.WorkloadProviderStatusRequest) (ports.WorkloadProviderObservation, error) {
	c.observes++
	return ports.WorkloadProviderObservation{
		TenantID:     request.TenantID,
		InstanceID:   request.InstanceID,
		Kind:         request.Kind,
		Provider:     request.ApplyResult.Provider,
		ResourceRefs: request.ApplyResult.ResourceRefs,
		Phase:        "Running",
	}, nil
}

var _ KubernetesProviderClient = (*fakeKubernetesProviderClient)(nil)
