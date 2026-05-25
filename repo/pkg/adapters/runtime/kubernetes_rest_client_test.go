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

func TestKubernetesRESTClientServerSideDryRunUsesDryRunAll(t *testing.T) {
	var gotPath string
	var gotAuth string
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotPath = r.URL.String()
		gotAuth = r.Header.Get("Authorization")
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Query().Get("dryRun") != "All" {
			t.Fatalf("dryRun = %q, want All", r.URL.Query().Get("dryRun"))
		}
		return jsonResponse(http.StatusCreated, `{"kind":"Deployment"}`), nil
	})

	client := newTestKubernetesRESTClient(t, transport)
	client.bearerToken = "token-a"
	result, err := client.ServerSideDryRun(context.Background(), renderedDeployment(t))
	if err != nil {
		t.Fatalf("ServerSideDryRun() error = %v", err)
	}
	if !result.Accepted || result.Provider != "kubernetes" {
		t.Fatalf("result = %#v, want accepted kubernetes dry-run", result)
	}
	if !strings.Contains(gotPath, "/apis/apps/v1/namespaces/ani-tenant-tenant-a/deployments") {
		t.Fatalf("path = %q, want Deployment collection path", gotPath)
	}
	if gotAuth != "Bearer token-a" {
		t.Fatalf("Authorization = %q, want bearer token", gotAuth)
	}
}

func TestKubernetesRESTClientApplyUsesServerSideApply(t *testing.T) {
	var gotPath string
	var gotContentType string
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotPath = r.URL.String()
		gotContentType = r.Header.Get("Content-Type")
		if r.Method != http.MethodPatch {
			t.Fatalf("method = %s, want PATCH", r.Method)
		}
		if r.URL.Query().Get("fieldManager") != "ani-test" {
			t.Fatalf("fieldManager = %q, want ani-test", r.URL.Query().Get("fieldManager"))
		}
		if r.URL.Query().Get("force") != "true" {
			t.Fatalf("force = %q, want true", r.URL.Query().Get("force"))
		}
		return jsonResponse(http.StatusOK, `{"kind":"Deployment"}`), nil
	})

	client := newTestKubernetesRESTClient(t, transport)
	result, err := client.Apply(context.Background(), validProviderApplyRequest(t))
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if !result.Applied {
		t.Fatalf("Applied = false, reason = %s", result.Reason)
	}
	if len(result.ResourceRefs) != 1 || result.ResourceRefs[0] != "kubernetes/Deployment/app-01" {
		t.Fatalf("ResourceRefs = %#v, want deployment ref", result.ResourceRefs)
	}
	if !strings.Contains(gotPath, "/apis/apps/v1/namespaces/ani-tenant-tenant-a/deployments/app-01") {
		t.Fatalf("path = %q, want Deployment resource path", gotPath)
	}
	if gotContentType != kubernetesApplyPatchContentType {
		t.Fatalf("Content-Type = %q, want apply patch", gotContentType)
	}
}

func TestKubernetesRESTClientApplyManifestsSupportsSecret(t *testing.T) {
	var gotPath string
	var gotBody string
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotPath = r.URL.String()
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		gotBody = string(body)
		if r.Method != http.MethodPatch {
			t.Fatalf("method = %s, want PATCH", r.Method)
		}
		return jsonResponse(http.StatusOK, `{"kind":"Secret"}`), nil
	})

	client := newTestKubernetesRESTClient(t, transport)
	refs, err := client.ApplyManifests(context.Background(), []ports.WorkloadManifest{{
		Provider: "kubernetes",
		Kind:     "Secret",
		Name:     "sec-abc",
		Content: `{
  "apiVersion": "v1",
  "kind": "Secret",
  "metadata": {
    "name": "sec-abc",
    "namespace": "ani-tenant-tenant-a"
  },
  "type": "Opaque",
  "stringData": {
    "password": "secret-value"
  }
}`,
	}})
	if err != nil {
		t.Fatalf("ApplyManifests(Secret) error = %v", err)
	}
	if len(refs) != 1 || refs[0] != "kubernetes/Secret/sec-abc" {
		t.Fatalf("refs = %#v, want Kubernetes Secret ref", refs)
	}
	if !strings.Contains(gotPath, "/api/v1/namespaces/ani-tenant-tenant-a/secrets/sec-abc") {
		t.Fatalf("path = %q, want Secret resource path", gotPath)
	}
	if !strings.Contains(gotBody, `"stringData"`) || strings.Contains(gotBody, `"data"`) {
		t.Fatalf("body = %s, want stringData and no base64 data", gotBody)
	}
}

func TestKubernetesRESTClientSupportsClusterAPIMachineDeployment(t *testing.T) {
	var gotPath string
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotPath = r.URL.String()
		if r.Method != http.MethodPatch {
			t.Fatalf("method = %s, want PATCH", r.Method)
		}
		return jsonResponse(http.StatusOK, `{"kind":"MachineDeployment"}`), nil
	})

	client := newTestKubernetesRESTClient(t, transport)
	refs, err := client.ApplyManifests(context.Background(), []ports.WorkloadManifest{{
		Provider: "clusterapi",
		Kind:     "MachineDeployment",
		Name:     "gpu-pool",
		Content: `{
  "apiVersion": "cluster.x-k8s.io/v1beta1",
  "kind": "MachineDeployment",
  "metadata": {
    "name": "gpu-pool",
    "namespace": "ani-tenant-tenant-a"
  },
  "spec": {
    "clusterName": "cluster-a",
    "replicas": 2
  }
}`,
	}})
	if err != nil {
		t.Fatalf("ApplyManifests(MachineDeployment) error = %v", err)
	}
	if len(refs) != 1 || refs[0] != "clusterapi/MachineDeployment/gpu-pool" {
		t.Fatalf("refs = %#v, want Cluster API MachineDeployment ref", refs)
	}
	if !strings.Contains(gotPath, "/apis/cluster.x-k8s.io/v1beta1/namespaces/ani-tenant-tenant-a/machinedeployments/gpu-pool") {
		t.Fatalf("path = %q, want Cluster API MachineDeployment resource path", gotPath)
	}
}

func TestKubernetesRESTClientObserveDeploymentStatus(t *testing.T) {
	var gotPath string
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotPath = r.URL.String()
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		body, err := json.Marshal(map[string]any{
			"status": map[string]any{
				"availableReplicas": 1,
			},
		})
		if err != nil {
			t.Fatalf("marshal response: %v", err)
		}
		return jsonResponse(http.StatusOK, string(body)), nil
	})

	client := newTestKubernetesRESTClient(t, transport)
	observation, err := client.Observe(context.Background(), ports.WorkloadProviderStatusRequest{
		TenantID:   "tenant-a",
		InstanceID: "instance-a",
		Kind:       ports.WorkloadKindContainer,
		ApplyResult: ports.WorkloadProviderApplyResult{
			Applied:      true,
			Provider:     "kubernetes",
			ResourceRefs: []string{"kubernetes/Deployment/app-01"},
		},
	})
	if err != nil {
		t.Fatalf("Observe() error = %v", err)
	}
	if observation.Phase != "Running" {
		t.Fatalf("Phase = %q, want Running", observation.Phase)
	}
	if !strings.Contains(gotPath, "/apis/apps/v1/namespaces/ani-tenant-tenant-a/deployments/app-01") {
		t.Fatalf("path = %q, want Deployment resource path", gotPath)
	}
}

func TestKubernetesRESTClientSupportsKubeVirtVirtualMachine(t *testing.T) {
	var paths []string
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.URL.String())
		return jsonResponse(http.StatusOK, `{"kind":"VirtualMachine"}`), nil
	})

	client := newTestKubernetesRESTClient(t, transport)
	manifests, err := NewKubernetesDryRunRenderer(NewPlanningRuntime()).Render(context.Background(), ports.WorkloadSpec{
		TenantID: "tenant-a",
		Name:     "vm-01",
		Kind:     ports.WorkloadKindVM,
		VM: &ports.VMInstanceSpec{
			BootImage: "ubuntu.qcow2",
			RootDisk: ports.WorkloadStorageAttachment{
				Name:    "root",
				Kind:    ports.StorageAttachmentRootDisk,
				SizeGiB: 80,
			},
		},
	})
	if err != nil {
		t.Fatalf("Render(VM) error = %v", err)
	}
	if _, err := client.ServerSideDryRun(context.Background(), manifests); err != nil {
		t.Fatalf("ServerSideDryRun(VM) error = %v", err)
	}
	if len(paths) != 1 || !strings.Contains(paths[0], "/apis/kubevirt.io/v1/namespaces/ani-tenant-tenant-a/virtualmachines") {
		t.Fatalf("paths = %#v, want KubeVirt VirtualMachine collection", paths)
	}
}

func TestKubernetesRESTClientSupportsKubeOVNNetworkResources(t *testing.T) {
	var paths []string
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.URL.String())
		return jsonResponse(http.StatusOK, `{"kind":"accepted"}`), nil
	})

	client := newTestKubernetesRESTClient(t, transport)
	manifests, err := NewKubeOVNNetworkRenderer().RenderVPC(context.Background(), ports.NetworkVPCRecord{
		TenantID: "tenant-a",
		VPCID:    "vpc-main",
		Name:     "main",
		CIDR:     "10.40.0.0/16",
		State:    ports.NetworkResourceAvailable,
	})
	if err != nil {
		t.Fatalf("RenderVPC() error = %v", err)
	}
	if _, err := client.ServerSideDryRun(context.Background(), manifests); err != nil {
		t.Fatalf("ServerSideDryRun(Vpc) error = %v", err)
	}
	if _, err := client.ApplyManifests(context.Background(), manifests); err != nil {
		t.Fatalf("ApplyManifests(Vpc) error = %v", err)
	}
	if len(paths) != 2 {
		t.Fatalf("paths = %#v, want dry-run and apply calls", paths)
	}
	if !strings.Contains(paths[0], "/apis/kubeovn.io/v1/vpcs?dryRun=All") {
		t.Fatalf("dry-run path = %q, want KubeOVN Vpc collection", paths[0])
	}
	if !strings.Contains(paths[1], "/apis/kubeovn.io/v1/vpcs/vpc-vpc-main") {
		t.Fatalf("apply path = %q, want KubeOVN Vpc resource", paths[1])
	}
}

func TestKubernetesRESTClientObservesKubeOVNNetworkResourceStatus(t *testing.T) {
	var gotPath string
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotPath = r.URL.String()
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		return jsonResponse(http.StatusOK, `{"kind":"Vpc","status":{"conditions":[{"type":"Ready","status":"True"}]}}`), nil
	})

	client := newTestKubernetesRESTClient(t, transport)
	result, err := client.ObserveNetworkResource(context.Background(), ports.NetworkProviderStatusRequest{
		TenantID:     "tenant-a",
		ResourceKind: "vpc",
		ResourceID:   "vpc-main",
		ApplyResult: ports.NetworkProviderApplyResult{
			Applied:      true,
			Provider:     "kubeovn",
			ResourceRefs: []string{"kubeovn/Vpc/vpc-vpc-main"},
		},
	})
	if err != nil {
		t.Fatalf("ObserveNetworkResource() error = %v", err)
	}
	if result.State != ports.NetworkResourceAvailable {
		t.Fatalf("State = %q, want available", result.State)
	}
	if !strings.Contains(gotPath, "/apis/kubeovn.io/v1/vpcs/vpc-vpc-main") {
		t.Fatalf("path = %q, want KubeOVN Vpc resource path", gotPath)
	}
}

func TestKubernetesRESTClientMapsNetworkResourceFailure(t *testing.T) {
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"kind":"Service","status":{"conditions":[{"type":"Available","status":"False","reason":"NoVIP","message":"load balancer vip is not allocated"}]}}`), nil
	})

	client := newTestKubernetesRESTClient(t, transport)
	result, err := client.ObserveNetworkResource(context.Background(), ports.NetworkProviderStatusRequest{
		TenantID:     "tenant-a",
		ResourceKind: "load-balancer",
		ResourceID:   "lb-web",
		ApplyResult: ports.NetworkProviderApplyResult{
			Applied:      true,
			Provider:     "kubernetes",
			ResourceRefs: []string{"kubernetes/Service/lb-lb-web"},
		},
	})
	if err != nil {
		t.Fatalf("ObserveNetworkResource() error = %v", err)
	}
	if result.State != ports.NetworkResourceFailed {
		t.Fatalf("State = %q, want failed", result.State)
	}
	if result.Reason != "load balancer vip is not allocated" {
		t.Fatalf("Reason = %q, want condition message", result.Reason)
	}
}

func TestKubernetesRESTClientObservesStoragePVCStatus(t *testing.T) {
	var gotPath string
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotPath = r.URL.String()
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		return jsonResponse(http.StatusOK, `{"kind":"PersistentVolumeClaim","status":{"phase":"Bound"}}`), nil
	})

	client := newTestKubernetesRESTClient(t, transport)
	result, err := client.ObserveStorageResource(context.Background(), ports.StorageProviderStatusRequest{
		TenantID:     "tenant-a",
		ResourceKind: "volume",
		ResourceID:   "vol-data",
		ApplyResult: ports.StorageProviderApplyResult{
			Applied:      true,
			Provider:     "kubernetes",
			ResourceRefs: []string{"kubernetes/PersistentVolumeClaim/vol-vol-data"},
		},
	})
	if err != nil {
		t.Fatalf("ObserveStorageResource() error = %v", err)
	}
	if result.State != ports.StorageResourceAvailable {
		t.Fatalf("State = %q, want available", result.State)
	}
	if !strings.Contains(gotPath, "/api/v1/namespaces/ani-tenant-tenant-a/persistentvolumeclaims/vol-vol-data") {
		t.Fatalf("path = %q, want PVC resource path", gotPath)
	}
}

func TestKubernetesRESTClientMapsStoragePVCFailure(t *testing.T) {
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"kind":"PersistentVolumeClaim","status":{"phase":"Lost","reason":"VolumeLost"}}`), nil
	})

	client := newTestKubernetesRESTClient(t, transport)
	result, err := client.ObserveStorageResource(context.Background(), ports.StorageProviderStatusRequest{
		TenantID:     "tenant-a",
		ResourceKind: "volume",
		ResourceID:   "vol-data",
		ApplyResult: ports.StorageProviderApplyResult{
			Applied:      true,
			Provider:     "kubernetes",
			ResourceRefs: []string{"kubernetes/PersistentVolumeClaim/vol-vol-data"},
		},
	})
	if err != nil {
		t.Fatalf("ObserveStorageResource() error = %v", err)
	}
	if result.State != ports.StorageResourceFailed {
		t.Fatalf("State = %q, want failed", result.State)
	}
	if result.Reason != "VolumeLost" {
		t.Fatalf("Reason = %q, want PVC reason", result.Reason)
	}
}

func TestKubernetesRESTClientSupportsNetworkPolicyAndService(t *testing.T) {
	var paths []string
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.URL.String())
		return jsonResponse(http.StatusOK, `{"kind":"accepted"}`), nil
	})

	client := newTestKubernetesRESTClient(t, transport)
	sg, err := NewKubeOVNNetworkRenderer().RenderSecurityGroup(context.Background(), ports.NetworkSecurityGroupRecord{
		TenantID:        "tenant-a",
		SecurityGroupID: "sg-web",
		Name:            "web",
		State:           ports.NetworkResourceAvailable,
	})
	if err != nil {
		t.Fatalf("RenderSecurityGroup() error = %v", err)
	}
	lb, err := NewKubeOVNNetworkRenderer().RenderLoadBalancer(context.Background(), ports.NetworkLoadBalancerRecord{
		TenantID:       "tenant-a",
		LoadBalancerID: "lb-web",
		Name:           "web",
		VPCID:          "vpc-main",
		Scheme:         "public",
		State:          ports.NetworkResourceAvailable,
		Listeners:      []ports.NetworkLoadBalancerListener{{Protocol: "http", Port: 80}},
	})
	if err != nil {
		t.Fatalf("RenderLoadBalancer() error = %v", err)
	}
	manifests := append(sg, lb...)
	if _, err := client.ServerSideDryRun(context.Background(), manifests); err != nil {
		t.Fatalf("ServerSideDryRun(network manifests) error = %v", err)
	}
	if len(paths) != 2 {
		t.Fatalf("paths = %#v, want two dry-run calls", paths)
	}
	if !strings.Contains(paths[0], "/apis/networking.k8s.io/v1/namespaces/ani-tenant-tenant-a/networkpolicies") {
		t.Fatalf("network policy path = %q", paths[0])
	}
	if !strings.Contains(paths[1], "/api/v1/namespaces/ani-tenant-tenant-a/services") {
		t.Fatalf("service path = %q", paths[1])
	}
}

func newTestKubernetesRESTClient(t *testing.T, transport http.RoundTripper) *KubernetesRESTClient {
	t.Helper()
	client, err := NewKubernetesRESTClient(KubernetesRESTClientConfig{
		Host:         "https://kubernetes.example.test",
		FieldManager: "ani-test",
		HTTPClient:   &http.Client{Transport: transport},
		Now:          func() time.Time { return time.Unix(900, 0) },
	})
	if err != nil {
		t.Fatalf("NewKubernetesRESTClient() error = %v", err)
	}
	return client
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
