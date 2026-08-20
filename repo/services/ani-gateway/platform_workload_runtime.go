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

type gatewayPlatformWorkloadRuntimeConfig struct {
	ProviderMode                      string
	DatabaseURL                       string
	MetadataStore                     ports.MetadataStore
	KubernetesAPIHost                 string
	KubernetesServiceHost             string
	KubernetesServicePort             string
	KubernetesBearerToken             string
	KubernetesServiceAccountTokenFile string
	KubernetesServiceAccountCAFile    string
	KubernetesProviderFieldManager    string
	KubernetesHTTPClient              *http.Client
	KubernetesRequestTimeout          time.Duration
}

func gatewayPlatformWorkloadRuntimeConfigFromEnv() gatewayPlatformWorkloadRuntimeConfig {
	return gatewayPlatformWorkloadRuntimeConfig{
		ProviderMode:                      os.Getenv("PLATFORM_WORKLOAD_PROVIDER"),
		DatabaseURL:                       os.Getenv("DATABASE_URL"),
		KubernetesAPIHost:                 os.Getenv("KUBERNETES_API_HOST"),
		KubernetesServiceHost:             os.Getenv("KUBERNETES_SERVICE_HOST"),
		KubernetesServicePort:             os.Getenv("KUBERNETES_SERVICE_PORT"),
		KubernetesBearerToken:             os.Getenv("KUBERNETES_BEARER_TOKEN"),
		KubernetesServiceAccountTokenFile: os.Getenv("KUBERNETES_SERVICE_ACCOUNT_TOKEN_FILE"),
		KubernetesServiceAccountCAFile:    os.Getenv("KUBERNETES_SERVICE_ACCOUNT_CA_FILE"),
		KubernetesProviderFieldManager:    firstGatewayEnv("PLATFORM_WORKLOAD_FIELD_MANAGER", "KUBERNETES_PROVIDER_FIELD_MANAGER"),
		KubernetesRequestTimeout:          gatewayDurationFromEnv("KUBERNETES_REQUEST_TIMEOUT"),
	}
}

func newGatewayPlatformWorkloadService(ctx context.Context, cfg gatewayPlatformWorkloadRuntimeConfig) (ports.PlatformWorkloadService, func(), error) {
	closeStore := func() {}
	switch mode := strings.TrimSpace(cfg.ProviderMode); mode {
	case "", "local":
		return runtimeadapter.NewLocalPlatformWorkloadService(), closeStore, nil
	case "kubernetes_rest":
		fieldManager := strings.TrimSpace(cfg.KubernetesProviderFieldManager)
		if fieldManager == "" {
			fieldManager = "ani-platform-workload"
		}
		client, err := runtimeadapter.NewKubernetesRESTClient(runtimeadapter.KubernetesRESTClientConfig{
			Host:            cfg.KubernetesAPIHost,
			ServiceHost:     cfg.KubernetesServiceHost,
			ServicePort:     cfg.KubernetesServicePort,
			BearerToken:     cfg.KubernetesBearerToken,
			BearerTokenFile: cfg.KubernetesServiceAccountTokenFile,
			CAFile:          cfg.KubernetesServiceAccountCAFile,
			FieldManager:    fieldManager,
			HTTPClient:      cfg.KubernetesHTTPClient,
			RequestTimeout:  cfg.KubernetesRequestTimeout,
		})
		if err != nil {
			return nil, closeStore, fmt.Errorf("platform workload kubernetes provider: %w", err)
		}
		runtime := runtimeadapter.NewKubernetesPlatformWorkloadRuntime(client)
		if cfg.MetadataStore != nil {
			return runtimeadapter.NewKubernetesPlatformWorkloadServiceWithStore(
				runtime,
				runtimeadapter.NewMetadataPlatformWorkloadStore(cfg.MetadataStore),
			), closeStore, nil
		}
		if strings.TrimSpace(cfg.DatabaseURL) != "" {
			store, closer, err := bootstrap.ConnectMetadataStore(ctx, cfg.DatabaseURL)
			if err != nil {
				return nil, closeStore, fmt.Errorf("platform workload metadata store: %w", err)
			}
			return runtimeadapter.NewKubernetesPlatformWorkloadServiceWithStore(
				runtime,
				runtimeadapter.NewMetadataPlatformWorkloadStore(store),
			), closer, nil
		}
		return runtimeadapter.NewKubernetesPlatformWorkloadService(runtime), closeStore, nil
	default:
		return nil, closeStore, fmt.Errorf("%w: unsupported PLATFORM_WORKLOAD_PROVIDER %q", ports.ErrUnsupported, mode)
	}
}
