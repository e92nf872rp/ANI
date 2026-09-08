package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/bootstrap"
	"github.com/kubercloud/ani/pkg/ports"
)

type gatewayNetworkRuntimeConfig struct {
	ProviderMode                      string
	ProviderApply                     bool
	ProviderUserID                    string
	ProviderProof                     string
	KubernetesAPIHost                 string
	KubernetesServiceHost             string
	KubernetesServicePort             string
	KubernetesBearerToken             string
	KubernetesServiceAccountTokenFile string
	KubernetesServiceAccountCAFile    string
	KubernetesProviderManager         string
	KubernetesHTTPClient              *http.Client
	KubernetesRequestTimeout          time.Duration
	DatabaseURL                       string
}

func gatewayNetworkRuntimeConfigFromEnv() gatewayNetworkRuntimeConfig {
	return gatewayNetworkRuntimeConfig{
		ProviderMode:                      os.Getenv("NETWORK_PROVIDER"),
		ProviderApply:                     strings.EqualFold(strings.TrimSpace(os.Getenv("NETWORK_PROVIDER_APPLY_ENABLED")), "true"),
		ProviderUserID:                    os.Getenv("NETWORK_PROVIDER_USER_ID"),
		ProviderProof:                     os.Getenv("NETWORK_PROVIDER_PERMISSION_PROOF"),
		KubernetesAPIHost:                 os.Getenv("KUBERNETES_API_HOST"),
		KubernetesServiceHost:             os.Getenv("KUBERNETES_SERVICE_HOST"),
		KubernetesServicePort:             os.Getenv("KUBERNETES_SERVICE_PORT"),
		KubernetesBearerToken:             os.Getenv("KUBERNETES_BEARER_TOKEN"),
		KubernetesServiceAccountTokenFile: os.Getenv("KUBERNETES_SERVICE_ACCOUNT_TOKEN_FILE"),
		KubernetesServiceAccountCAFile:    os.Getenv("KUBERNETES_SERVICE_ACCOUNT_CA_FILE"),
		KubernetesProviderManager:         os.Getenv("KUBERNETES_PROVIDER_FIELD_MANAGER"),
		KubernetesRequestTimeout:          gatewayDurationFromEnv("KUBERNETES_REQUEST_TIMEOUT"),
		DatabaseURL:                       os.Getenv("DATABASE_URL"),
	}
}

func newGatewayNetworkService(ctx context.Context, cfg gatewayNetworkRuntimeConfig) (ports.NetworkService, func(), error) {
	switch mode := strings.TrimSpace(cfg.ProviderMode); mode {
	case "", "local", "not_configured":
		dsn := strings.TrimSpace(cfg.DatabaseURL)
		if dsn == "" {
			return nil, func() {}, nil
		}
		metadata, closeStore, err := bootstrap.ConnectMetadataStore(ctx, dsn)
		if err != nil {
			return nil, func() {}, fmt.Errorf("connect network metadata store: %w", err)
		}
		store := runtimeadapter.NewMetadataNetworkStore(metadata)
		return runtimeadapter.NewLocalNetworkService(
			runtimeadapter.WithNetworkResourceStore(store),
		), closeStore, nil
	case "kubeovn_rest":
		if strings.TrimSpace(cfg.ProviderUserID) == "" || strings.TrimSpace(cfg.ProviderProof) == "" {
			return nil, func() {}, fmt.Errorf("%w: network provider requires NETWORK_PROVIDER_USER_ID and NETWORK_PROVIDER_PERMISSION_PROOF", ports.ErrInvalid)
		}
		client, err := runtimeadapter.NewKubernetesRESTClient(runtimeadapter.KubernetesRESTClientConfig{
			Host:            cfg.KubernetesAPIHost,
			ServiceHost:     cfg.KubernetesServiceHost,
			ServicePort:     cfg.KubernetesServicePort,
			BearerToken:     cfg.KubernetesBearerToken,
			BearerTokenFile: cfg.KubernetesServiceAccountTokenFile,
			CAFile:          cfg.KubernetesServiceAccountCAFile,
			FieldManager:    cfg.KubernetesProviderManager,
			HTTPClient:      cfg.KubernetesHTTPClient,
			RequestTimeout:  cfg.KubernetesRequestTimeout,
		})
		if err != nil {
			return nil, func() {}, err
		}
		provider := runtimeadapter.NewKubeOVNNetworkProviderAdapter(
			client,
			runtimeadapter.WithKubeOVNNetworkProviderApplyEnabled(cfg.ProviderApply),
		)
		opts := []runtimeadapter.NetworkServiceOption{
			runtimeadapter.WithNetworkRouteProvider(
				runtimeadapter.NewKubeOVNNetworkRenderer(),
				provider,
				provider,
				provider,
				runtimeadapter.NetworkProviderExecutionConfig{
					UserID:          cfg.ProviderUserID,
					PermissionProof: cfg.ProviderProof,
				},
			),
		}
		closeStore := func() {}
		if dsn := strings.TrimSpace(cfg.DatabaseURL); dsn != "" {
			metadata, closeFn, err := bootstrap.ConnectMetadataStore(ctx, dsn)
			if err != nil {
				return nil, func() {}, fmt.Errorf("connect network metadata store: %w", err)
			}
			store := runtimeadapter.NewMetadataNetworkStore(metadata)
			opts = append(opts, runtimeadapter.WithNetworkResourceStore(store))
			closeStore = closeFn
		}
		return runtimeadapter.NewLocalNetworkService(opts...), closeStore, nil
	default:
		return nil, func() {}, fmt.Errorf("%w: unsupported NETWORK_PROVIDER %q", ports.ErrUnsupported, mode)
	}
}
