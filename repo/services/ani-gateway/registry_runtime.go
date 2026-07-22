package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	registryadapter "github.com/kubercloud/ani/pkg/adapters/registry"
	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/bootstrap"
	"github.com/kubercloud/ani/pkg/ports"
)

type gatewayRegistryRuntimeConfig struct {
	ProviderMode                      string
	HarborEndpoint                    string
	HarborUsername                    string
	HarborPassword                    string
	HarborRequestTimeout              time.Duration
	KubernetesAPIHost                 string
	KubernetesServiceHost             string
	KubernetesServicePort             string
	KubernetesBearerToken             string
	KubernetesServiceAccountTokenFile string
	KubernetesServiceAccountCAFile    string
	KubernetesProviderManager         string
	KubernetesRequestTimeout          time.Duration
	DatabaseURL                       string
	HTTPClient                        *http.Client
	MetadataStore                     ports.MetadataStore
	MetadataConnector                 gatewayK8sClusterMetadataConnector
}

func gatewayRegistryRuntimeConfigFromEnv() gatewayRegistryRuntimeConfig {
	return gatewayRegistryRuntimeConfig{
		ProviderMode: os.Getenv("REGISTRY_PROVIDER_MODE"), HarborEndpoint: os.Getenv("HARBOR_ENDPOINT"), HarborUsername: os.Getenv("HARBOR_USERNAME"), HarborPassword: os.Getenv("HARBOR_PASSWORD"), HarborRequestTimeout: gatewayDurationFromEnv("HARBOR_REQUEST_TIMEOUT"),
		KubernetesAPIHost: os.Getenv("KUBERNETES_API_HOST"), KubernetesServiceHost: os.Getenv("KUBERNETES_SERVICE_HOST"), KubernetesServicePort: os.Getenv("KUBERNETES_SERVICE_PORT"), KubernetesBearerToken: os.Getenv("KUBERNETES_BEARER_TOKEN"), KubernetesServiceAccountTokenFile: os.Getenv("KUBERNETES_SERVICE_ACCOUNT_TOKEN_FILE"), KubernetesServiceAccountCAFile: os.Getenv("KUBERNETES_SERVICE_ACCOUNT_CA_FILE"), KubernetesProviderManager: firstGatewayEnv("REGISTRY_PULL_SECRET_FIELD_MANAGER", "KUBERNETES_PROVIDER_FIELD_MANAGER"), KubernetesRequestTimeout: gatewayDurationFromEnv("KUBERNETES_REQUEST_TIMEOUT"), DatabaseURL: os.Getenv("DATABASE_URL"),
	}
}

func newGatewayImageRegistry(ctx context.Context, cfg gatewayRegistryRuntimeConfig) (ports.ImageRegistry, func(), error) {
	switch strings.TrimSpace(cfg.ProviderMode) {
	case "", "local":
		return registryadapter.NewLocalImageRegistry(), nil, nil
	case "harbor":
		if strings.TrimSpace(cfg.DatabaseURL) == "" && cfg.MetadataStore == nil {
			return nil, nil, fmt.Errorf("%w: DATABASE_URL is required for Harbor tag reference checks", ports.ErrNotConfigured)
		}
		var closeRuntime func()
		metadata := cfg.MetadataStore
		if metadata == nil {
			connector := cfg.MetadataConnector
			if connector == nil {
				connector = bootstrap.ConnectMetadataStore
			}
			store, closeStore, err := connector(ctx, cfg.DatabaseURL)
			if err != nil {
				return nil, nil, err
			}
			metadata = store
			if closeStore != nil {
				closeRuntime = closeStore
			}
		}
		kubeClient, err := runtimeadapter.NewKubernetesRESTClient(runtimeadapter.KubernetesRESTClientConfig{Host: cfg.KubernetesAPIHost, ServiceHost: cfg.KubernetesServiceHost, ServicePort: cfg.KubernetesServicePort, BearerToken: cfg.KubernetesBearerToken, BearerTokenFile: cfg.KubernetesServiceAccountTokenFile, CAFile: cfg.KubernetesServiceAccountCAFile, FieldManager: cfg.KubernetesProviderManager, HTTPClient: cfg.HTTPClient, RequestTimeout: cfg.KubernetesRequestTimeout})
		if err != nil {
			if closeRuntime != nil {
				closeRuntime()
			}
			return nil, nil, err
		}
		service, err := registryadapter.NewHarborImageRegistry(registryadapter.HarborImageRegistryConfig{Endpoint: cfg.HarborEndpoint, Username: cfg.HarborUsername, Password: cfg.HarborPassword, HTTPClient: cfg.HTTPClient, RequestTimeout: cfg.HarborRequestTimeout, PullSecretWriter: registryadapter.NewKubernetesPullSecretWriter(kubeClient), ReferenceReader: registryadapter.NewWorkloadImageReferenceReader(runtimeadapter.NewMetadataInstanceStore(metadata))})
		if err != nil {
			if closeRuntime != nil {
				closeRuntime()
			}
			return nil, nil, err
		}
		return service, closeRuntime, nil
	default:
		return nil, nil, fmt.Errorf("%w: unsupported REGISTRY_PROVIDER_MODE %q", ports.ErrUnsupported, cfg.ProviderMode)
	}
}
