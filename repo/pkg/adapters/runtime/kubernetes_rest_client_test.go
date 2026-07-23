package runtime

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/adapters/resilience"
	"github.com/kubercloud/ani/pkg/ports"
)

func TestKubernetesRESTClientServerSideDryRunUsesDryRunAll(t *testing.T) {
	var gotPath string
	var gotAuth string
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotPath = r.URL.String()
		gotAuth = r.Header.Get("Authorization")
		if r.Method != http.MethodPatch {
			t.Fatalf("method = %s, want PATCH", r.Method)
		}
		if r.URL.Query().Get("dryRun") != "All" {
			t.Fatalf("dryRun = %q, want All", r.URL.Query().Get("dryRun"))
		}
		if r.Header.Get("Content-Type") != kubernetesApplyPatchContentType {
			t.Fatalf("content-type = %q, want apply patch", r.Header.Get("Content-Type"))
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
	if !strings.Contains(gotPath, "/apis/apps/v1/namespaces/ani-tenant-tenant-a/deployments/app-01") {
		t.Fatalf("path = %q, want Deployment resource path", gotPath)
	}
	if gotAuth != "Bearer token-a" {
		t.Fatalf("Authorization = %q, want bearer token", gotAuth)
	}
}

func TestKubernetesRESTClientEnforcesRequestTimeout(t *testing.T) {
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})

	client, err := NewKubernetesRESTClient(KubernetesRESTClientConfig{
		Host:           "https://kubernetes.test",
		HTTPClient:     &http.Client{Transport: transport},
		RequestTimeout: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewKubernetesRESTClient() error = %v", err)
	}

	_, err = client.ServerSideDryRun(context.Background(), renderedDeployment(t))
	if err == nil {
		t.Fatal("ServerSideDryRun() error = nil, want request timeout")
	}
	if !strings.Contains(err.Error(), context.DeadlineExceeded.Error()) {
		t.Fatalf("ServerSideDryRun() error = %v, want deadline exceeded", err)
	}
}

func TestKubernetesRESTClientHealthCallsVersion(t *testing.T) {
	var gotPath string
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotPath = r.URL.Path
		return jsonResponse(http.StatusOK, `{"gitVersion":"v1.36.1"}`), nil
	})

	client := newTestKubernetesRESTClient(t, transport)
	if err := client.Health(context.Background()); err != nil {
		t.Fatalf("Health() error = %v", err)
	}
	if gotPath != "/version" {
		t.Fatalf("path = %q, want /version", gotPath)
	}
}

func TestKubernetesRESTClientHealthUsesRetryPolicy(t *testing.T) {
	attempts := 0
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			return jsonResponse(http.StatusServiceUnavailable, `{"message":"temporarily unavailable"}`), nil
		}
		return jsonResponse(http.StatusOK, `{"gitVersion":"v1.36.1"}`), nil
	})

	client, err := NewKubernetesRESTClient(KubernetesRESTClientConfig{
		Host:       "https://kubernetes.example.test",
		HTTPClient: &http.Client{Transport: transport},
		RetryPolicy: resilience.Policy{
			MaxAttempts: 2,
			BaseBackoff: time.Nanosecond,
			MaxBackoff:  time.Nanosecond,
		},
	})
	if err != nil {
		t.Fatalf("NewKubernetesRESTClient() error = %v", err)
	}

	if err := client.Health(context.Background()); err != nil {
		t.Fatalf("Health() error = %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}

func TestKubernetesRESTErrorClassifiesRetryable(t *testing.T) {
	tests := []struct {
		name      string
		transport http.RoundTripper
		retryable bool
		invalid   bool
	}{
		{
			name: "503",
			transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return jsonResponse(http.StatusServiceUnavailable, `{"message":"temporarily unavailable"}`), nil
			}),
			retryable: true,
		},
		{
			name: "400",
			transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return jsonResponse(http.StatusBadRequest, `{"message":"bad request"}`), nil
			}),
			invalid: true,
		},
		{
			name: "network",
			transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
			}),
			retryable: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newTestKubernetesRESTClient(t, tt.transport)
			err := client.Health(context.Background())
			if err == nil {
				t.Fatal("Health() error = nil, want classified Kubernetes REST error")
			}
			if got := resilience.Retryable(err); got != tt.retryable {
				t.Fatalf("Retryable(%v) = %v, want %v", err, got, tt.retryable)
			}
			if got := errors.Is(err, ports.ErrInvalid); got != tt.invalid {
				t.Fatalf("errors.Is(err, ErrInvalid) = %v, want %v for %v", got, tt.invalid, err)
			}
		})
	}
}

func TestKubernetesRESTClientUsesInClusterServiceAccountWhenHostOmitted(t *testing.T) {
	tokenPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenPath, []byte("service-account-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var gotURL string
	var gotAuth string
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotURL = r.URL.String()
		gotAuth = r.Header.Get("Authorization")
		return jsonResponse(http.StatusCreated, `{"kind":"Deployment"}`), nil
	})

	client, err := NewKubernetesRESTClient(KubernetesRESTClientConfig{
		ServiceHost:     "10.96.0.1",
		ServicePort:     "443",
		BearerTokenFile: tokenPath,
		HTTPClient:      &http.Client{Transport: transport},
	})
	if err != nil {
		t.Fatalf("NewKubernetesRESTClient() error = %v", err)
	}
	if _, err := client.ServerSideDryRun(context.Background(), renderedDeployment(t)); err != nil {
		t.Fatalf("ServerSideDryRun() error = %v", err)
	}
	if !strings.HasPrefix(gotURL, "https://10.96.0.1:443/apis/apps/v1/") {
		t.Fatalf("url = %q, want in-cluster Kubernetes service URL", gotURL)
	}
	if gotAuth != "Bearer service-account-token" {
		t.Fatalf("Authorization = %q, want service account token", gotAuth)
	}
}

func TestKubernetesRESTClientRejectsInClusterConfigWithoutServiceAccountToken(t *testing.T) {
	_, err := NewKubernetesRESTClient(KubernetesRESTClientConfig{
		ServiceHost:     "10.96.0.1",
		ServicePort:     "443",
		BearerTokenFile: filepath.Join(t.TempDir(), "missing-token"),
	})
	if err == nil {
		t.Fatalf("NewKubernetesRESTClient() error = nil, want missing service account token error")
	}
}

func TestKubernetesRESTClientDoesNotReadAmbientInClusterEnvironment(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.96.0.1")
	t.Setenv("KUBERNETES_SERVICE_PORT", "443")

	if _, err := NewKubernetesRESTClient(KubernetesRESTClientConfig{}); err == nil {
		t.Fatalf("NewKubernetesRESTClient() error = nil, want explicit Kubernetes host or service host")
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

func TestKubernetesRESTClientApplyDoesNotRetryWrites(t *testing.T) {
	attempts := 0
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempts++
		return jsonResponse(http.StatusServiceUnavailable, `{"message":"temporarily unavailable"}`), nil
	})
	client, err := NewKubernetesRESTClient(KubernetesRESTClientConfig{
		Host:         "https://kubernetes.example.test",
		FieldManager: "ani-test",
		HTTPClient:   &http.Client{Transport: transport},
		RetryPolicy: resilience.Policy{
			MaxAttempts: 3,
			BaseBackoff: time.Nanosecond,
			MaxBackoff:  time.Nanosecond,
		},
	})
	if err != nil {
		t.Fatalf("NewKubernetesRESTClient() error = %v", err)
	}

	if _, err := client.Apply(context.Background(), validProviderApplyRequest(t)); err == nil {
		t.Fatal("Apply() error = nil, want Kubernetes write failure")
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want writes to avoid retry", attempts)
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

func TestKubernetesRESTClientApplyManifestsSupportsVolumeSnapshot(t *testing.T) {
	var gotPath string
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotPath = r.URL.String()
		if r.Method != http.MethodPatch {
			t.Fatalf("method = %s, want PATCH", r.Method)
		}
		return jsonResponse(http.StatusOK, `{"kind":"VolumeSnapshot"}`), nil
	})

	client := newTestKubernetesRESTClient(t, transport)
	refs, err := client.ApplyManifests(context.Background(), []ports.WorkloadManifest{{
		Provider: "kubernetes",
		Kind:     "VolumeSnapshot",
		Name:     "snap-daily",
		Content: `{
  "apiVersion": "snapshot.storage.k8s.io/v1",
  "kind": "VolumeSnapshot",
  "metadata": {
    "name": "snap-daily",
    "namespace": "ani-tenant-tenant-a"
  },
  "spec": {
    "source": {
      "persistentVolumeClaimName": "vol-data"
    }
  }
}`,
	}})
	if err != nil {
		t.Fatalf("ApplyManifests(VolumeSnapshot) error = %v", err)
	}
	if len(refs) != 1 || refs[0] != "kubernetes/VolumeSnapshot/snap-daily" {
		t.Fatalf("refs = %#v, want VolumeSnapshot ref", refs)
	}
	if !strings.Contains(gotPath, "/apis/snapshot.storage.k8s.io/v1/namespaces/ani-tenant-tenant-a/volumesnapshots/snap-daily") {
		t.Fatalf("path = %q, want VolumeSnapshot resource path", gotPath)
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
	if !strings.Contains(paths[0], "/apis/kubeovn.io/v1/vpcs/vpc-vpc-main") || !strings.Contains(paths[0], "dryRun=All") {
		t.Fatalf("dry-run path = %q, want KubeOVN Vpc resource dry-run", paths[0])
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

// TestKubernetesHTTPClientLoadsCAWhenExplicitlyConfigured 验证非 inCluster 模式下，
// 当 CAFile 显式非空时（如通过 KUBERNETES_API_HOST + KUBERNETES_SERVICE_ACCOUNT_CA_FILE
// 显式配置的真实集群，使用自签证书），仍加载该 CA 构造带自定义 RootCAs 的 client，
// 而不是返回无 CA 的 http.DefaultClient 导致 x509 unknown authority 失败。
func TestKubernetesHTTPClientLoadsCAWhenExplicitlyConfigured(t *testing.T) {
	// 动态生成自签 CA 证书 PEM，保证 AppendCertsFromPEM 能成功解析。
	caPath := filepath.Join(t.TempDir(), "ca.crt")
	caPEM := mustGenerateTestCA(t)
	if err := os.WriteFile(caPath, caPEM, 0o600); err != nil {
		t.Fatalf("write CA: %v", err)
	}

	client, err := kubernetesHTTPClient(caPath, false)
	if err != nil {
		t.Fatalf("kubernetesHTTPClient(explicit CA, not inCluster) error = %v", err)
	}
	if client == http.DefaultClient {
		t.Fatalf("client = http.DefaultClient, want custom client with RootCAs from explicit CA")
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T, want *http.Transport", client.Transport)
	}
	if transport.TLSClientConfig == nil || transport.TLSClientConfig.RootCAs == nil {
		t.Fatalf("TLSClientConfig.RootCAs = nil, want pool from explicit CA")
	}
}

// TestKubernetesHTTPClientReturnsDefaultWhenNoCAAndNotInCluster 验证非 inCluster 且
// 未配置 CAFile 时返回 http.DefaultClient（公网/标准 CA 场景，零回归）。
func TestKubernetesHTTPClientReturnsDefaultWhenNoCAAndNotInCluster(t *testing.T) {
	client, err := kubernetesHTTPClient("", false)
	if err != nil {
		t.Fatalf("kubernetesHTTPClient(empty, not inCluster) error = %v", err)
	}
	if client != http.DefaultClient {
		t.Fatalf("client = %p, want http.DefaultClient for public CA scenario", client)
	}
}

// TestKubernetesHTTPClientRejectsMissingCAInCluster 验证 inCluster=true 时 CA 读取
// 失败应返回错误，不伪造默认 client（已有行为，回归保护）。
func TestKubernetesHTTPClientRejectsMissingCAInCluster(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing-ca.crt")
	if _, err := kubernetesHTTPClient(missing, true); err == nil {
		t.Fatalf("kubernetesHTTPClient(missing, inCluster) expected error, got nil")
	}
}

// mustGenerateTestCA 动态生成一个自签 CA 证书并返回 PEM 编码，用于测试 CA 加载。
func mustGenerateTestCA(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test-ca"},
		NotBefore:    time.Unix(0, 0),
		NotAfter:     time.Now().Add(24 * time.Hour),
		IsCA:         true,
		KeyUsage:     x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}
