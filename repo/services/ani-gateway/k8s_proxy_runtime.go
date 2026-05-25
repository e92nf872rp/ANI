package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/bootstrap"
	"github.com/kubercloud/ani/pkg/ports"
)

type gatewayK8sClusterMetadataConnector func(context.Context, string) (ports.MetadataStore, func(), error)

type gatewayK8sClusterRuntimeConfig struct {
	ProxyMode                        string
	ProviderMode                     string
	NodePoolProviderMode             string
	KubernetesAPIHost                string
	KubernetesBearerToken            string
	KubernetesProviderManager        string
	TargetServer                     string
	TargetBearerToken                string
	DatabaseURL                      string
	MetadataStore                    ports.MetadataStore
	MetadataConnector                gatewayK8sClusterMetadataConnector
	HTTPClient                       *http.Client
	VClusterHelmBinary               string
	VClusterBinary                   string
	VClusterChartName                string
	VClusterChartRepo                string
	VClusterProxyServerTemplate      string
	VClusterProxyBearerToken         string
	VClusterKubeconfigServerTemplate string
	VClusterHelmRunner               runtimeadapter.VClusterHelmRunner
}

func gatewayK8sClusterRuntimeConfigFromEnv() gatewayK8sClusterRuntimeConfig {
	return gatewayK8sClusterRuntimeConfig{
		ProxyMode:                        os.Getenv("K8S_CLUSTER_PROXY_MODE"),
		ProviderMode:                     os.Getenv("K8S_CLUSTER_PROVIDER_MODE"),
		NodePoolProviderMode:             os.Getenv("K8S_CLUSTER_NODE_POOL_PROVIDER_MODE"),
		KubernetesAPIHost:                os.Getenv("KUBERNETES_API_HOST"),
		KubernetesBearerToken:            os.Getenv("KUBERNETES_BEARER_TOKEN"),
		KubernetesProviderManager:        os.Getenv("KUBERNETES_PROVIDER_FIELD_MANAGER"),
		TargetServer:                     os.Getenv("K8S_CLUSTER_PROXY_TARGET_SERVER"),
		TargetBearerToken:                os.Getenv("K8S_CLUSTER_PROXY_BEARER_TOKEN"),
		DatabaseURL:                      os.Getenv("DATABASE_URL"),
		VClusterHelmBinary:               os.Getenv("VCLUSTER_HELM_BINARY"),
		VClusterBinary:                   os.Getenv("VCLUSTER_BINARY"),
		VClusterChartName:                os.Getenv("VCLUSTER_CHART_NAME"),
		VClusterChartRepo:                os.Getenv("VCLUSTER_CHART_REPO"),
		VClusterProxyServerTemplate:      os.Getenv("VCLUSTER_PROXY_SERVER_TEMPLATE"),
		VClusterProxyBearerToken:         os.Getenv("VCLUSTER_PROXY_BEARER_TOKEN"),
		VClusterKubeconfigServerTemplate: os.Getenv("VCLUSTER_KUBECONFIG_SERVER_TEMPLATE"),
	}
}

func newGatewayK8sClusterRuntime(ctx context.Context, cfg gatewayK8sClusterRuntimeConfig) (ports.K8sClusterService, func(), error) {
	closeRuntime := func() {}
	if strings.TrimSpace(cfg.ProxyMode) == "forwarding_metadata" && cfg.MetadataStore == nil {
		if strings.TrimSpace(cfg.DatabaseURL) == "" {
			return nil, closeRuntime, fmt.Errorf("%w: DATABASE_URL is required for forwarding_metadata", ports.ErrNotConfigured)
		}
		connector := cfg.MetadataConnector
		if connector == nil {
			connector = bootstrap.ConnectMetadataStore
		}
		store, closeStore, err := connector(ctx, cfg.DatabaseURL)
		if err != nil {
			return nil, closeRuntime, err
		}
		cfg.MetadataStore = store
		if closeStore != nil {
			closeRuntime = closeStore
		}
	}
	service, err := newGatewayK8sClusterService(cfg)
	if err != nil {
		closeRuntime()
		return nil, func() {}, err
	}
	return service, closeRuntime, nil
}

func newGatewayK8sClusterService(cfg gatewayK8sClusterRuntimeConfig) (ports.K8sClusterService, error) {
	mode := strings.TrimSpace(cfg.ProxyMode)
	var metadataTargetStore ports.K8sClusterProxyTargetStore
	if mode == "forwarding_metadata" && cfg.MetadataStore != nil {
		metadataTargetStore = runtimeadapter.NewMetadataK8sClusterProxyTargetStore(cfg.MetadataStore)
	}
	switch mode {
	case "", "local":
		if strings.TrimSpace(cfg.ProviderMode) != "" && strings.TrimSpace(cfg.ProviderMode) != "local" {
			return newGatewayK8sClusterBaseService(cfg, metadataTargetStore)
		}
		return nil, nil
	case "forwarding_static":
		if strings.TrimSpace(cfg.TargetServer) == "" {
			return nil, fmt.Errorf("%w: K8S_CLUSTER_PROXY_TARGET_SERVER is required for forwarding_static", ports.ErrNotConfigured)
		}
		base, err := newGatewayK8sClusterBaseService(cfg, metadataTargetStore)
		if err != nil {
			return nil, err
		}
		options := []runtimeadapter.K8sClusterProxyForwardingOption{}
		if cfg.HTTPClient != nil {
			options = append(options, runtimeadapter.WithK8sClusterProxyForwardingHTTPClient(cfg.HTTPClient))
		}
		return runtimeadapter.NewK8sClusterProxyForwardingService(
			base,
			staticGatewayK8sProxyTargetResolver{
				server:      cfg.TargetServer,
				bearerToken: cfg.TargetBearerToken,
			},
			options...,
		), nil
	case "forwarding_metadata":
		if cfg.MetadataStore == nil {
			return nil, fmt.Errorf("%w: MetadataStore is required for forwarding_metadata", ports.ErrNotConfigured)
		}
		base, err := newGatewayK8sClusterBaseService(cfg, metadataTargetStore)
		if err != nil {
			return nil, err
		}
		options := []runtimeadapter.K8sClusterProxyForwardingOption{}
		if cfg.HTTPClient != nil {
			options = append(options, runtimeadapter.WithK8sClusterProxyForwardingHTTPClient(cfg.HTTPClient))
		}
		return runtimeadapter.NewK8sClusterProxyForwardingService(
			base,
			metadataTargetStore,
			options...,
		), nil
	default:
		return nil, fmt.Errorf("%w: unsupported K8S_CLUSTER_PROXY_MODE %q", ports.ErrUnsupported, mode)
	}
}

func newGatewayK8sClusterBaseService(cfg gatewayK8sClusterRuntimeConfig, targetStore ports.K8sClusterProxyTargetStore) (ports.K8sClusterService, error) {
	options := []runtimeadapter.K8sClusterServiceOption{}
	if targetStore != nil {
		options = append(options, runtimeadapter.WithK8sClusterProxyTargetStore(targetStore))
	}
	nodePoolProvider, err := newGatewayK8sClusterNodePoolProvider(cfg)
	if err != nil {
		return nil, err
	}
	if nodePoolProvider != nil {
		options = append(options, runtimeadapter.WithK8sClusterNodePoolProvider(nodePoolProvider))
	}
	switch providerMode := strings.TrimSpace(cfg.ProviderMode); providerMode {
	case "", "local":
		return runtimeadapter.NewLocalK8sClusterService(options...), nil
	case "vcluster_helm":
		provider := runtimeadapter.NewVClusterHelmProviderAdapter(runtimeadapter.VClusterHelmProviderConfig{
			HelmBinary:               cfg.VClusterHelmBinary,
			VClusterBinary:           cfg.VClusterBinary,
			ChartName:                cfg.VClusterChartName,
			ChartRepo:                cfg.VClusterChartRepo,
			Runner:                   cfg.VClusterHelmRunner,
			ProxyServerTemplate:      cfg.VClusterProxyServerTemplate,
			ProxyBearerToken:         cfg.VClusterProxyBearerToken,
			KubeconfigServerTemplate: cfg.VClusterKubeconfigServerTemplate,
		})
		options = append(options, runtimeadapter.WithK8sClusterProviderApply(provider), runtimeadapter.WithK8sClusterProviderUpgrade(provider), runtimeadapter.WithK8sClusterKubeconfigProvider(provider))
		return runtimeadapter.NewLocalK8sClusterService(options...), nil
	default:
		return nil, fmt.Errorf("%w: unsupported K8S_CLUSTER_PROVIDER_MODE %q", ports.ErrUnsupported, providerMode)
	}
}

func newGatewayK8sClusterNodePoolProvider(cfg gatewayK8sClusterRuntimeConfig) (ports.K8sClusterNodePoolProvider, error) {
	switch mode := strings.TrimSpace(cfg.NodePoolProviderMode); mode {
	case "", "local":
		return nil, nil
	case "clusterapi_kubernetes_rest":
		client, err := runtimeadapter.NewKubernetesRESTClient(runtimeadapter.KubernetesRESTClientConfig{
			Host:         cfg.KubernetesAPIHost,
			BearerToken:  cfg.KubernetesBearerToken,
			FieldManager: cfg.KubernetesProviderManager,
			HTTPClient:   cfg.HTTPClient,
		})
		if err != nil {
			return nil, err
		}
		return runtimeadapter.NewKubernetesNodePoolProviderAdapter(client), nil
	default:
		return nil, fmt.Errorf("%w: unsupported K8S_CLUSTER_NODE_POOL_PROVIDER_MODE %q", ports.ErrUnsupported, mode)
	}
}

type staticGatewayK8sProxyTargetResolver struct {
	server      string
	bearerToken string
}

func (r staticGatewayK8sProxyTargetResolver) ResolveK8sClusterProxyTarget(_ context.Context, req ports.K8sClusterGetRequest) (ports.K8sClusterProxyTarget, error) {
	if strings.TrimSpace(r.server) == "" {
		return ports.K8sClusterProxyTarget{}, fmt.Errorf("%w: k8s proxy target server is required", ports.ErrNotConfigured)
	}
	return ports.K8sClusterProxyTarget{
		TenantID:    req.TenantID,
		ClusterID:   req.ClusterID,
		Server:      r.server,
		BearerToken: r.bearerToken,
	}, nil
}
