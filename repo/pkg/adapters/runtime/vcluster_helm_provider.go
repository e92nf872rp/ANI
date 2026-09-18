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
	ChartVersion             string
	HelmSetValues            []string
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
	chartVersion             string
	helmSetValues            []string
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
		chartRepo:                normalizeVClusterChartRepo(config.ChartRepo),
		chartVersion:             strings.TrimSpace(config.ChartVersion),
		helmSetValues:            normalizeVClusterHelmSetValues(config.HelmSetValues),
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
	args := a.helmUpgradeInstallArgs(releaseName, namespace)
	if _, err := a.runner.Run(ctx, a.helmBinary, args...); err != nil {
		return ports.K8sClusterProviderApplyResult{}, fmt.Errorf("apply vCluster Helm release: %w", err)
	}
	proxyServer := a.proxyServer(request, namespace)
	proxyCredentials := ports.K8sClusterProxyTarget{BearerToken: a.proxyBearerToken}
	if proxyServer != "" && proxyCredentials.BearerToken == "" {
		credentials, err := a.printProxyCredentials(ctx, request, namespace, proxyServer)
		if err != nil {
			return ports.K8sClusterProviderApplyResult{}, err
		}
		proxyCredentials = credentials
	}
	return ports.K8sClusterProviderApplyResult{
		Applied:      true,
		Provider:     "vcluster",
		ResourceRefs: []string{"vcluster/HelmRelease/" + releaseName},
		ProxyTarget: ports.K8sClusterProxyTarget{
			Server:                proxyServer,
			BearerToken:           proxyCredentials.BearerToken,
			CAData:                proxyCredentials.CAData,
			ClientCertificateData: proxyCredentials.ClientCertificateData,
			ClientKeyData:         proxyCredentials.ClientKeyData,
		},
		Reason:    "vCluster Helm release applied",
		AppliedAt: a.now().UTC(),
	}, nil
}

func (a *VClusterHelmProviderAdapter) helmUpgradeInstallArgs(releaseName string, namespace string) []string {
	args := []string{
		"upgrade",
		"--install",
		releaseName,
		a.chartName,
		"--namespace",
		namespace,
		"--create-namespace",
		"--repository-config=",
		"--set",
		"sync.toHost.services.enabled=true",
	}
	if a.chartRepo != "" {
		args = append(args[:4], append([]string{"--repo", a.chartRepo}, args[4:]...)...)
	}
	for _, value := range a.helmSetValues {
		args = append(args, "--set", value)
	}
	if a.chartVersion != "" {
		args = append(args, "--version", a.chartVersion)
	}
	return args
}

func (a *VClusterHelmProviderAdapter) UpgradeK8sCluster(ctx context.Context, request ports.K8sClusterProviderUpgradeRequest) (ports.K8sClusterProviderUpgradeResult, error) {
	if err := validateK8sClusterProviderUpgradeRequest(request); err != nil {
		return ports.K8sClusterProviderUpgradeResult{}, err
	}
	namespace := tenantNamespace(request.TenantID)
	releaseName := request.ClusterID
	args := append(a.helmUpgradeInstallArgs(releaseName, namespace),
		"--set",
		"controlPlane.distro.k8s.version="+request.TargetVersion,
	)
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

func (a *VClusterHelmProviderAdapter) DeleteK8sCluster(ctx context.Context, request ports.K8sClusterProviderDeleteRequest) (ports.K8sClusterProviderDeleteResult, error) {
	if err := validateK8sClusterProviderDeleteRequest(request); err != nil {
		return ports.K8sClusterProviderDeleteResult{}, err
	}
	namespace := tenantNamespace(request.TenantID)
	releaseName := request.ClusterID
	if _, err := a.runner.Run(ctx, a.helmBinary, a.helmUninstallArgs(releaseName, namespace)...); err != nil {
		return ports.K8sClusterProviderDeleteResult{}, fmt.Errorf("uninstall vCluster Helm release: %w", err)
	}
	return ports.K8sClusterProviderDeleteResult{
		Deleted:      true,
		Provider:     "vcluster",
		ResourceRefs: []string{"vcluster/HelmRelease/" + releaseName},
		Reason:       "vCluster Helm release uninstalled",
		DeletedAt:    a.now().UTC(),
	}, nil
}

func (a *VClusterHelmProviderAdapter) helmUninstallArgs(releaseName string, namespace string) []string {
	return []string{
		"uninstall",
		releaseName,
		"--namespace",
		namespace,
		"--ignore-not-found",
	}
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

func normalizeVClusterChartRepo(value string) string {
	trimmed := strings.TrimSpace(value)
	if strings.EqualFold(trimmed, "none") || strings.EqualFold(trimmed, "local") || trimmed == "-" {
		return ""
	}
	return firstNonEmpty(trimmed, defaultVClusterChartRepo)
}

func normalizeVClusterHelmSetValues(values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			normalized = append(normalized, trimmed)
		}
	}
	return normalized
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

func (a *VClusterHelmProviderAdapter) printProxyCredentials(ctx context.Context, request ports.K8sClusterProviderApplyRequest, namespace string, server string) (ports.K8sClusterProxyTarget, error) {
	args := []string{
		"connect",
		request.ClusterID,
		"--namespace",
		namespace,
		"--print",
		"--server",
		server,
	}
	output, err := a.runner.Run(ctx, a.vclusterBinary, args...)
	if err != nil {
		return ports.K8sClusterProxyTarget{}, fmt.Errorf("print vCluster proxy kubeconfig: %w", err)
	}
	kubeconfig := string(output)
	credentials := ports.K8sClusterProxyTarget{
		BearerToken:           parseKubeconfigToken(kubeconfig),
		CAData:                parseKubeconfigValue(kubeconfig, "certificate-authority-data:"),
		ClientCertificateData: parseKubeconfigValue(kubeconfig, "client-certificate-data:"),
		ClientKeyData:         parseKubeconfigValue(kubeconfig, "client-key-data:"),
	}
	if credentials.BearerToken == "" && (credentials.ClientCertificateData == "" || credentials.ClientKeyData == "") {
		return ports.K8sClusterProxyTarget{}, fmt.Errorf("%w: vCluster proxy kubeconfig missing bearer token or client certificate", ports.ErrInvalid)
	}
	return credentials, nil
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

func validateK8sClusterProviderDeleteRequest(request ports.K8sClusterProviderDeleteRequest) error {
	if strings.TrimSpace(request.TenantID) == "" || strings.TrimSpace(request.ClusterID) == "" {
		return fmt.Errorf("%w: tenant_id and cluster_id are required for vCluster delete", ports.ErrInvalid)
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
	return parseKubeconfigValue(kubeconfig, "token:")
}

func parseKubeconfigValue(kubeconfig string, prefix string) string {
	for _, line := range strings.Split(kubeconfig, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			return strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, prefix)), `"`)
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
var _ ports.K8sClusterProviderDelete = (*VClusterHelmProviderAdapter)(nil)
var _ ports.K8sClusterKubeconfigProvider = (*VClusterHelmProviderAdapter)(nil)
