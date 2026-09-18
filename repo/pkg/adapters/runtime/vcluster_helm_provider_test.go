package runtime

import (
	"context"
	"reflect"
	"testing"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestVClusterHelmProviderAdapterRunsHelmUpgradeInstall(t *testing.T) {
	runner := &fakeVClusterHelmRunner{}
	adapter := NewVClusterHelmProviderAdapter(VClusterHelmProviderConfig{
		Runner:              runner,
		ProxyServerTemplate: "https://{cluster_id}.{namespace}:443",
		ProxyBearerToken:    "tenant-token",
	})

	result, err := adapter.ApplyK8sCluster(context.Background(), ports.K8sClusterProviderApplyRequest{
		TenantID:  "tenant-a",
		ClusterID: "k8sclu-provider",
		Name:      "vc-a",
		Version:   "v1.30.0",
	})
	if err != nil {
		t.Fatalf("ApplyK8sCluster() error = %v", err)
	}

	wantArgs := []string{
		"upgrade",
		"--install",
		"k8sclu-provider",
		"vcluster",
		"--repo",
		"https://charts.loft.sh",
		"--namespace",
		"ani-tenant-tenant-a",
		"--create-namespace",
		"--repository-config=",
		"--set",
		"sync.toHost.services.enabled=true",
	}
	if runner.binary != "helm" || !reflect.DeepEqual(runner.args, wantArgs) {
		t.Fatalf("helm call = %s %#v, want helm %#v", runner.binary, runner.args, wantArgs)
	}
	if !result.Applied || result.Provider != "vcluster" {
		t.Fatalf("result = %+v, want applied vcluster provider", result)
	}
	if len(result.ResourceRefs) != 1 || result.ResourceRefs[0] != "vcluster/HelmRelease/k8sclu-provider" {
		t.Fatalf("resource refs = %#v", result.ResourceRefs)
	}
	if result.ProxyTarget.Server != "https://k8sclu-provider.ani-tenant-tenant-a:443" || result.ProxyTarget.BearerToken != "tenant-token" {
		t.Fatalf("proxy target = %+v, want templated target", result.ProxyTarget)
	}
}

func TestVClusterHelmProviderAdapterDerivesProxyTokenFromPrintedKubeconfig(t *testing.T) {
	runner := &fakeVClusterHelmRunner{
		outputByBinary: map[string][]byte{
			"vcluster": []byte(`apiVersion: v1
kind: Config
clusters:
- name: k8sclu-provider
  cluster:
    server: https://printed-vcluster.example
users:
- name: k8sclu-provider
  user:
    token: tenant-token
`),
		},
	}
	adapter := NewVClusterHelmProviderAdapter(VClusterHelmProviderConfig{
		Runner:              runner,
		ProxyServerTemplate: "https://{cluster_id}.{namespace}:443",
	})

	result, err := adapter.ApplyK8sCluster(context.Background(), ports.K8sClusterProviderApplyRequest{
		TenantID:  "tenant-a",
		ClusterID: "k8sclu-provider",
		Name:      "vc-a",
		Version:   "v1.30.0",
	})
	if err != nil {
		t.Fatalf("ApplyK8sCluster() error = %v", err)
	}

	if len(runner.calls) != 2 {
		t.Fatalf("runner calls = %#v, want helm apply and vcluster kubeconfig print", runner.calls)
	}
	wantPrintArgs := []string{
		"connect",
		"k8sclu-provider",
		"--namespace",
		"ani-tenant-tenant-a",
		"--print",
		"--server",
		"https://k8sclu-provider.ani-tenant-tenant-a:443",
	}
	if runner.calls[1].binary != "vcluster" || !reflect.DeepEqual(runner.calls[1].args, wantPrintArgs) {
		t.Fatalf("vcluster call = %s %#v, want vcluster %#v", runner.calls[1].binary, runner.calls[1].args, wantPrintArgs)
	}
	if result.ProxyTarget.Server != "https://k8sclu-provider.ani-tenant-tenant-a:443" || result.ProxyTarget.BearerToken != "tenant-token" {
		t.Fatalf("proxy target = %+v, want templated server with printed token", result.ProxyTarget)
	}
}

func TestVClusterHelmProviderAdapterDerivesProxyClientCertificateFromPrintedKubeconfig(t *testing.T) {
	runner := &fakeVClusterHelmRunner{
		outputByBinary: map[string][]byte{
			"vcluster": []byte(`apiVersion: v1
kind: Config
clusters:
- name: k8sclu-provider
  cluster:
    certificate-authority-data: ca-data
    server: https://printed-vcluster.example
users:
- name: k8sclu-provider
  user:
    client-certificate-data: cert-data
    client-key-data: key-data
`),
		},
	}
	adapter := NewVClusterHelmProviderAdapter(VClusterHelmProviderConfig{
		Runner:              runner,
		ProxyServerTemplate: "https://{cluster_id}.{namespace}:443",
	})

	result, err := adapter.ApplyK8sCluster(context.Background(), ports.K8sClusterProviderApplyRequest{
		TenantID:  "tenant-a",
		ClusterID: "k8sclu-provider",
		Name:      "vc-a",
		Version:   "v1.30.0",
	})
	if err != nil {
		t.Fatalf("ApplyK8sCluster() error = %v", err)
	}

	if result.ProxyTarget.BearerToken != "" || result.ProxyTarget.CAData != "ca-data" || result.ProxyTarget.ClientCertificateData != "cert-data" || result.ProxyTarget.ClientKeyData != "key-data" {
		t.Fatalf("proxy target = %+v, want client certificate credentials", result.ProxyTarget)
	}
}

func TestVClusterHelmProviderAdapterSupportsLocalChartArchive(t *testing.T) {
	runner := &fakeVClusterHelmRunner{}
	adapter := NewVClusterHelmProviderAdapter(VClusterHelmProviderConfig{
		Runner:              runner,
		ChartName:           "/tmp/vcluster-0.34.1.tgz",
		ChartRepo:           "none",
		ProxyServerTemplate: "https://{cluster_id}.{namespace}:443",
		ProxyBearerToken:    "tenant-token",
	})

	_, err := adapter.ApplyK8sCluster(context.Background(), ports.K8sClusterProviderApplyRequest{
		TenantID:  "tenant-a",
		ClusterID: "k8sclu-provider",
		Name:      "vc-a",
	})
	if err != nil {
		t.Fatalf("ApplyK8sCluster() error = %v", err)
	}

	wantArgs := []string{
		"upgrade",
		"--install",
		"k8sclu-provider",
		"/tmp/vcluster-0.34.1.tgz",
		"--namespace",
		"ani-tenant-tenant-a",
		"--create-namespace",
		"--repository-config=",
		"--set",
		"sync.toHost.services.enabled=true",
	}
	if runner.binary != "helm" || !reflect.DeepEqual(runner.args, wantArgs) {
		t.Fatalf("helm call = %s %#v, want helm %#v", runner.binary, runner.args, wantArgs)
	}
}

func TestVClusterHelmProviderAdapterPinsChartVersionForOCIRepository(t *testing.T) {
	runner := &fakeVClusterHelmRunner{}
	adapter := NewVClusterHelmProviderAdapter(VClusterHelmProviderConfig{
		Runner:              runner,
		ChartName:           "oci://docker.changqingyun.cn/ani/charts/vcluster",
		ChartRepo:           "none",
		ChartVersion:        "0.34.1",
		ProxyServerTemplate: "https://{cluster_id}.{namespace}:443",
		ProxyBearerToken:    "tenant-token",
	})

	_, err := adapter.ApplyK8sCluster(context.Background(), ports.K8sClusterProviderApplyRequest{
		TenantID:  "tenant-a",
		ClusterID: "k8sclu-provider",
		Name:      "vc-a",
	})
	if err != nil {
		t.Fatalf("ApplyK8sCluster() error = %v", err)
	}

	wantArgs := []string{
		"upgrade",
		"--install",
		"k8sclu-provider",
		"oci://docker.changqingyun.cn/ani/charts/vcluster",
		"--namespace",
		"ani-tenant-tenant-a",
		"--create-namespace",
		"--repository-config=",
		"--set",
		"sync.toHost.services.enabled=true",
		"--version",
		"0.34.1",
	}
	if runner.binary != "helm" || !reflect.DeepEqual(runner.args, wantArgs) {
		t.Fatalf("helm call = %s %#v, want helm %#v", runner.binary, runner.args, wantArgs)
	}
}

func TestVClusterHelmProviderAdapterPrintsKubeconfig(t *testing.T) {
	runner := &fakeVClusterHelmRunner{
		output: []byte(`apiVersion: v1
kind: Config
clusters:
- name: k8sclu-provider
  cluster:
    server: https://k8sclu-provider.example
contexts:
- name: k8sclu-provider
  context:
    namespace: ani-tenant-tenant-a
users:
- name: k8sclu-provider
  user:
    token: tenant-token
`),
	}
	adapter := NewVClusterHelmProviderAdapter(VClusterHelmProviderConfig{
		Runner:                   runner,
		VClusterBinary:           "vcluster",
		KubeconfigServerTemplate: "https://{cluster_id}.{namespace}:443",
	})

	record, err := adapter.GetK8sClusterKubeconfig(context.Background(), ports.K8sClusterKubeconfigProviderRequest{
		TenantID:  "tenant-a",
		ClusterID: "k8sclu-provider",
		Name:      "vc-a",
		Version:   "v1.30.0",
	})
	if err != nil {
		t.Fatalf("GetK8sClusterKubeconfig() error = %v", err)
	}

	wantArgs := []string{
		"connect",
		"k8sclu-provider",
		"--namespace",
		"ani-tenant-tenant-a",
		"--print",
		"--server",
		"https://k8sclu-provider.ani-tenant-tenant-a:443",
	}
	if runner.binary != "vcluster" || !reflect.DeepEqual(runner.args, wantArgs) {
		t.Fatalf("vcluster call = %s %#v, want vcluster %#v", runner.binary, runner.args, wantArgs)
	}
	if record.ClusterID != "k8sclu-provider" || record.TenantID != "tenant-a" {
		t.Fatalf("record identity = %+v, want request identity", record)
	}
	if record.Server != "https://k8sclu-provider.ani-tenant-tenant-a:443" || record.Namespace != "ani-tenant-tenant-a" {
		t.Fatalf("record target = %+v, want templated server/namespace", record)
	}
	if record.Token != "tenant-token" || record.Kubeconfig == "" || record.CreatedAt == 0 || record.ExpiresAt <= record.CreatedAt {
		t.Fatalf("record kubeconfig = %+v, want printed kubeconfig with token and expiry", record)
	}
}

func TestVClusterHelmProviderAdapterParsesServerFromPrintedKubeconfig(t *testing.T) {
	runner := &fakeVClusterHelmRunner{
		output: []byte(`apiVersion: v1
kind: Config
clusters:
- name: k8sclu-provider
  cluster:
    server: https://printed-vcluster.example
users:
- name: k8sclu-provider
  user:
    token: tenant-token
`),
	}
	adapter := NewVClusterHelmProviderAdapter(VClusterHelmProviderConfig{Runner: runner})

	record, err := adapter.GetK8sClusterKubeconfig(context.Background(), ports.K8sClusterKubeconfigProviderRequest{
		TenantID:  "tenant-a",
		ClusterID: "k8sclu-provider",
		Name:      "vc-a",
	})
	if err != nil {
		t.Fatalf("GetK8sClusterKubeconfig() error = %v", err)
	}
	if record.Server != "https://printed-vcluster.example" {
		t.Fatalf("record server = %q, want server parsed from printed kubeconfig", record.Server)
	}

	wantArgs := []string{
		"connect",
		"k8sclu-provider",
		"--namespace",
		"ani-tenant-tenant-a",
		"--print",
	}
	if runner.binary != "vcluster" || !reflect.DeepEqual(runner.args, wantArgs) {
		t.Fatalf("vcluster call = %s %#v, want vcluster %#v", runner.binary, runner.args, wantArgs)
	}
}

func TestVClusterHelmProviderAdapterRunsHelmUpgradeForClusterVersion(t *testing.T) {
	runner := &fakeVClusterHelmRunner{}
	adapter := NewVClusterHelmProviderAdapter(VClusterHelmProviderConfig{Runner: runner})

	result, err := adapter.UpgradeK8sCluster(context.Background(), ports.K8sClusterProviderUpgradeRequest{
		TenantID:       "tenant-a",
		ClusterID:      "k8sclu-provider",
		Name:           "vc-a",
		CurrentVersion: "v1.30.0",
		TargetVersion:  "v1.31.0",
	})
	if err != nil {
		t.Fatalf("UpgradeK8sCluster() error = %v", err)
	}

	wantArgs := []string{
		"upgrade",
		"--install",
		"k8sclu-provider",
		"vcluster",
		"--repo",
		"https://charts.loft.sh",
		"--namespace",
		"ani-tenant-tenant-a",
		"--create-namespace",
		"--repository-config=",
		"--set",
		"sync.toHost.services.enabled=true",
		"--set",
		"controlPlane.distro.k8s.version=v1.31.0",
	}
	if runner.binary != "helm" || !reflect.DeepEqual(runner.args, wantArgs) {
		t.Fatalf("helm call = %s %#v, want helm %#v", runner.binary, runner.args, wantArgs)
	}
	if !result.Applied || result.Provider != "vcluster" || result.Reason != "vCluster Helm release upgraded" {
		t.Fatalf("result = %+v, want upgraded vcluster provider", result)
	}
}

func TestVClusterHelmProviderAdapterUninstallsHelmRelease(t *testing.T) {
	runner := &fakeVClusterHelmRunner{}
	adapter := NewVClusterHelmProviderAdapter(VClusterHelmProviderConfig{Runner: runner})

	result, err := adapter.DeleteK8sCluster(context.Background(), ports.K8sClusterProviderDeleteRequest{
		TenantID:  "tenant-a",
		ClusterID: "k8sclu-provider",
		Name:      "vc-a",
	})
	if err != nil {
		t.Fatalf("DeleteK8sCluster() error = %v", err)
	}

	wantArgs := []string{
		"uninstall",
		"k8sclu-provider",
		"--namespace",
		"ani-tenant-tenant-a",
		"--ignore-not-found",
	}
	if runner.binary != "helm" || !reflect.DeepEqual(runner.args, wantArgs) {
		t.Fatalf("helm call = %s %#v, want helm %#v", runner.binary, runner.args, wantArgs)
	}
	if !result.Deleted || result.Provider != "vcluster" || result.Reason != "vCluster Helm release uninstalled" {
		t.Fatalf("result = %+v, want uninstalled vcluster provider", result)
	}
	if len(result.ResourceRefs) != 1 || result.ResourceRefs[0] != "vcluster/HelmRelease/k8sclu-provider" {
		t.Fatalf("resource refs = %#v", result.ResourceRefs)
	}
}

type fakeVClusterHelmRunner struct {
	binary         string
	args           []string
	output         []byte
	outputByBinary map[string][]byte
	calls          []fakeVClusterHelmCall
}

type fakeVClusterHelmCall struct {
	binary string
	args   []string
}

func (r *fakeVClusterHelmRunner) Run(_ context.Context, binary string, args ...string) ([]byte, error) {
	r.binary = binary
	r.args = append([]string(nil), args...)
	r.calls = append(r.calls, fakeVClusterHelmCall{binary: binary, args: append([]string(nil), args...)})
	if r.outputByBinary != nil {
		if output, ok := r.outputByBinary[binary]; ok {
			return output, nil
		}
	}
	if r.output != nil {
		return r.output, nil
	}
	return []byte("release applied"), nil
}
