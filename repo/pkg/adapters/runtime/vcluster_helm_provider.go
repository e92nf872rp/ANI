package runtime

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

const (
	defaultVClusterHelmBinary = "helm"
	defaultVClusterBinary     = "vcluster"
	defaultVClusterChartName  = "vcluster"
	defaultVClusterChartRepo  = "https://charts.loft.sh"
)

type VClusterHelmRunner interface {
	Run(ctx context.Context, binary string, args ...string) ([]byte, error)
}

type VClusterHelmProviderConfig struct {
	HelmBinary               string
	VClusterBinary           string
	ChartName                string
	ChartRepo                string
	Runner                   VClusterHelmRunner
	ProxyServerTemplate      string
	ProxyBearerToken         string
	KubeconfigServerTemplate string
	Now                      func() time.Time
}

type VClusterHelmProviderAdapter struct {
	helmBinary               string
	vclusterBinary           string
	chartName                string
	chartRepo                string
	runner                   VClusterHelmRunner
	proxyServerTemplate      string
	proxyBearerToken         string
	kubeconfigServerTemplate string
	now                      func() time.Time
}

func NewVClusterHelmProviderAdapter(config VClusterHelmProviderConfig) *VClusterHelmProviderAdapter {
	runner := config.Runner
	if runner == nil {
		runner = execVClusterHelmRunner{}
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return &VClusterHelmProviderAdapter{
		helmBinary:               firstNonEmpty(config.HelmBinary, defaultVClusterHelmBinary),
		vclusterBinary:           firstNonEmpty(config.VClusterBinary, defaultVClusterBinary),
		chartName:                firstNonEmpty(config.ChartName, defaultVClusterChartName),
		chartRepo:                firstNonEmpty(config.ChartRepo, defaultVClusterChartRepo),
		runner:                   runner,
		proxyServerTemplate:      strings.TrimSpace(config.ProxyServerTemplate),
		proxyBearerToken:         strings.TrimSpace(config.ProxyBearerToken),
		kubeconfigServerTemplate: strings.TrimSpace(config.KubeconfigServerTemplate),
		now:                      now,
	}
}

func (a *VClusterHelmProviderAdapter) ApplyK8sCluster(ctx context.Context, request ports.K8sClusterProviderApplyRequest) (ports.K8sClusterProviderApplyResult, error) {
	if err := validateK8sClusterProviderApplyRequest(request); err != nil {
		return ports.K8sClusterProviderApplyResult{}, err
	}
	namespace := tenantNamespace(request.TenantID)
	releaseName := request.ClusterID
	args := []string{
		"upgrade",
		"--install",
		releaseName,
		a.chartName,
		"--repo",
		a.chartRepo,
		"--namespace",
		namespace,
		"--create-namespace",
		"--repository-config=",
		"--set",
		"sync.toHost.service.enabled=true",
	}
	if _, err := a.runner.Run(ctx, a.helmBinary, args...); err != nil {
		return ports.K8sClusterProviderApplyResult{}, fmt.Errorf("apply vCluster Helm release: %w", err)
	}
	return ports.K8sClusterProviderApplyResult{
		Applied:      true,
		Provider:     "vcluster",
		ResourceRefs: []string{"vcluster/HelmRelease/" + releaseName},
		ProxyTarget: ports.K8sClusterProxyTarget{
			Server:      a.proxyServer(request, namespace),
			BearerToken: a.proxyBearerToken,
		},
		Reason:    "vCluster Helm release applied",
		AppliedAt: a.now().UTC(),
	}, nil
}

func (a *VClusterHelmProviderAdapter) UpgradeK8sCluster(ctx context.Context, request ports.K8sClusterProviderUpgradeRequest) (ports.K8sClusterProviderUpgradeResult, error) {
	if err := validateK8sClusterProviderUpgradeRequest(request); err != nil {
		return ports.K8sClusterProviderUpgradeResult{}, err
	}
	namespace := tenantNamespace(request.TenantID)
	releaseName := request.ClusterID
	args := []string{
		"upgrade",
		"--install",
		releaseName,
		a.chartName,
		"--repo",
		a.chartRepo,
		"--namespace",
		namespace,
		"--create-namespace",
		"--repository-config=",
		"--set",
		"sync.toHost.service.enabled=true",
		"--set",
		"controlPlane.distro.k8s.version=" + request.TargetVersion,
	}
	if _, err := a.runner.Run(ctx, a.helmBinary, args...); err != nil {
		return ports.K8sClusterProviderUpgradeResult{}, fmt.Errorf("upgrade vCluster Helm release: %w", err)
	}
	return ports.K8sClusterProviderUpgradeResult{
		Applied:      true,
		Provider:     "vcluster",
		ResourceRefs: []string{"vcluster/HelmRelease/" + releaseName},
		Reason:       "vCluster Helm release upgraded",
		AppliedAt:    a.now().UTC(),
	}, nil
}

func (a *VClusterHelmProviderAdapter) GetK8sClusterKubeconfig(ctx context.Context, request ports.K8sClusterKubeconfigProviderRequest) (ports.K8sClusterKubeconfigRecord, error) {
	if err := validateK8sClusterKubeconfigProviderRequest(request); err != nil {
		return ports.K8sClusterKubeconfigRecord{}, err
	}
	namespace := tenantNamespace(request.TenantID)
	server := a.kubeconfigServer(request, namespace)
	args := []string{
		"connect",
		request.ClusterID,
		"--namespace",
		namespace,
		"--print",
	}
	if server != "" {
		args = append(args, "--server", server)
	}
	output, err := a.runner.Run(ctx, a.vclusterBinary, args...)
	if err != nil {
		return ports.K8sClusterKubeconfigRecord{}, fmt.Errorf("print vCluster kubeconfig: %w", err)
	}
	now := a.now().UTC().Unix()
	kubeconfig := string(output)
	if server == "" {
		server = parseKubeconfigServer(kubeconfig)
	}
	return ports.K8sClusterKubeconfigRecord{
		ClusterID:  request.ClusterID,
		TenantID:   request.TenantID,
		Server:     server,
		Namespace:  namespace,
		Token:      parseKubeconfigToken(kubeconfig),
		Kubeconfig: kubeconfig,
		CreatedAt:  now,
		ExpiresAt:  now + 3600,
	}, nil
}

func (a *VClusterHelmProviderAdapter) proxyServer(request ports.K8sClusterProviderApplyRequest, namespace string) string {
	if a.proxyServerTemplate == "" {
		return ""
	}
	replacer := strings.NewReplacer(
		"{tenant_id}", request.TenantID,
		"{cluster_id}", request.ClusterID,
		"{name}", request.Name,
		"{namespace}", namespace,
	)
	return replacer.Replace(a.proxyServerTemplate)
}

func (a *VClusterHelmProviderAdapter) kubeconfigServer(request ports.K8sClusterKubeconfigProviderRequest, namespace string) string {
	if strings.TrimSpace(request.Server) != "" {
		return strings.TrimSpace(request.Server)
	}
	template := firstNonEmpty(a.kubeconfigServerTemplate, a.proxyServerTemplate)
	if template == "" {
		return ""
	}
	replacer := strings.NewReplacer(
		"{tenant_id}", request.TenantID,
		"{cluster_id}", request.ClusterID,
		"{name}", request.Name,
		"{namespace}", namespace,
	)
	return replacer.Replace(template)
}

func validateK8sClusterProviderApplyRequest(request ports.K8sClusterProviderApplyRequest) error {
	if strings.TrimSpace(request.TenantID) == "" || strings.TrimSpace(request.ClusterID) == "" || strings.TrimSpace(request.Name) == "" {
		return fmt.Errorf("%w: tenant_id, cluster_id and name are required for vCluster apply", ports.ErrInvalid)
	}
	return nil
}

func validateK8sClusterProviderUpgradeRequest(request ports.K8sClusterProviderUpgradeRequest) error {
	if strings.TrimSpace(request.TenantID) == "" || strings.TrimSpace(request.ClusterID) == "" || strings.TrimSpace(request.Name) == "" || strings.TrimSpace(request.TargetVersion) == "" {
		return fmt.Errorf("%w: tenant_id, cluster_id, name and target_version are required for vCluster upgrade", ports.ErrInvalid)
	}
	return nil
}

func validateK8sClusterKubeconfigProviderRequest(request ports.K8sClusterKubeconfigProviderRequest) error {
	if strings.TrimSpace(request.TenantID) == "" || strings.TrimSpace(request.ClusterID) == "" || strings.TrimSpace(request.Name) == "" {
		return fmt.Errorf("%w: tenant_id, cluster_id and name are required for vCluster kubeconfig", ports.ErrInvalid)
	}
	return nil
}

func parseKubeconfigToken(kubeconfig string) string {
	for _, line := range strings.Split(kubeconfig, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "token:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "token:"))
		}
	}
	return ""
}

func parseKubeconfigServer(kubeconfig string) string {
	for _, line := range strings.Split(kubeconfig, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "server:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "server:"))
		}
	}
	return ""
}

type execVClusterHelmRunner struct{}

func (execVClusterHelmRunner) Run(ctx context.Context, binary string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, binary, args...)
	output, err := command.CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("%s %s failed: %w: %s", binary, strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

var _ ports.K8sClusterProviderApply = (*VClusterHelmProviderAdapter)(nil)
var _ ports.K8sClusterProviderUpgrade = (*VClusterHelmProviderAdapter)(nil)
var _ ports.K8sClusterKubeconfigProvider = (*VClusterHelmProviderAdapter)(nil)
