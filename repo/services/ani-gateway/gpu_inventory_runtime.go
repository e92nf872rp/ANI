package main

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/ports"
)

type gatewayGPUInventoryRuntimeConfig struct {
	ProviderMode                      string
	KubernetesAPIHost                 string
	KubernetesServiceHost             string
	KubernetesServicePort             string
	KubernetesBearerToken             string
	KubernetesServiceAccountTokenFile string
	KubernetesServiceAccountCAFile    string
	KubernetesProviderManager         string
	KubernetesHTTPClient              *http.Client
	KubernetesRequestTimeout          time.Duration
}

func gatewayGPUInventoryRuntimeConfigFromEnv() gatewayGPUInventoryRuntimeConfig {
	return gatewayGPUInventoryRuntimeConfig{
		ProviderMode:                      os.Getenv("GPU_INVENTORY_PROVIDER"),
		KubernetesAPIHost:                 os.Getenv("KUBERNETES_API_HOST"),
		KubernetesServiceHost:             os.Getenv("KUBERNETES_SERVICE_HOST"),
		KubernetesServicePort:             os.Getenv("KUBERNETES_SERVICE_PORT"),
		KubernetesBearerToken:             os.Getenv("KUBERNETES_BEARER_TOKEN"),
		KubernetesServiceAccountTokenFile: os.Getenv("KUBERNETES_SERVICE_ACCOUNT_TOKEN_FILE"),
		KubernetesServiceAccountCAFile:    os.Getenv("KUBERNETES_SERVICE_ACCOUNT_CA_FILE"),
		KubernetesProviderManager:         os.Getenv("KUBERNETES_PROVIDER_FIELD_MANAGER"),
		KubernetesRequestTimeout:          gatewayDurationFromEnv("KUBERNETES_REQUEST_TIMEOUT"),
	}
}

func newGatewayGPUInventory(cfg gatewayGPUInventoryRuntimeConfig) (ports.GPUInventory, error) {
	switch mode := strings.TrimSpace(cfg.ProviderMode); mode {
	case "", "local", "not_configured":
		return nil, nil
	case "kubernetes_rest":
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
			return nil, err
		}
		return runtimeadapter.NewKubernetesGPUInventory(client), nil
	default:
		return nil, fmt.Errorf("%w: unsupported GPU_INVENTORY_PROVIDER %q", ports.ErrUnsupported, mode)
	}
}

type gatewayGPUSchedulingQueueRuntimeConfig struct {
	ProviderMode                      string
	KubernetesAPIHost                 string
	KubernetesServiceHost             string
	KubernetesServicePort             string
	KubernetesBearerToken             string
	KubernetesServiceAccountTokenFile string
	KubernetesServiceAccountCAFile    string
	VolcanoQueueNamespace             string
	KubernetesHTTPClient              *http.Client
	KubernetesRequestTimeout          time.Duration
}

func gatewayGPUSchedulingQueueRuntimeConfigFromEnv() gatewayGPUSchedulingQueueRuntimeConfig {
	return gatewayGPUSchedulingQueueRuntimeConfig{
		ProviderMode:                      os.Getenv("GPU_SCHEDULING_QUEUE_PROVIDER"),
		KubernetesAPIHost:                 os.Getenv("KUBERNETES_API_HOST"),
		KubernetesServiceHost:             os.Getenv("KUBERNETES_SERVICE_HOST"),
		KubernetesServicePort:             os.Getenv("KUBERNETES_SERVICE_PORT"),
		KubernetesBearerToken:             os.Getenv("KUBERNETES_BEARER_TOKEN"),
		KubernetesServiceAccountTokenFile: os.Getenv("KUBERNETES_SERVICE_ACCOUNT_TOKEN_FILE"),
		KubernetesServiceAccountCAFile:    os.Getenv("KUBERNETES_SERVICE_ACCOUNT_CA_FILE"),
		VolcanoQueueNamespace:             os.Getenv("VOLCANO_QUEUE_NAMESPACE"),
		KubernetesRequestTimeout:          gatewayDurationFromEnv("KUBERNETES_REQUEST_TIMEOUT"),
	}
}

// newGatewayGPUSchedulingQueueStore builds the queue store for the current
// runtime profile. "local" and unset default to an in-memory store so the
// Console queue settings page is usable in dev without a real Volcano CRD.
// "volcano_rest" wires the real Volcano CRD adapter.
func newGatewayGPUSchedulingQueueStore(cfg gatewayGPUSchedulingQueueRuntimeConfig) (ports.GPUSchedulingQueueStore, error) {
	switch mode := strings.TrimSpace(cfg.ProviderMode); mode {
	case "", "local", "not_configured":
		return runtimeadapter.NewLocalGPUSchedulingQueueStore(), nil
	case "volcano_rest":
		client, err := runtimeadapter.NewKubernetesRESTClient(runtimeadapter.KubernetesRESTClientConfig{
			Host:            cfg.KubernetesAPIHost,
			ServiceHost:     cfg.KubernetesServiceHost,
			ServicePort:     cfg.KubernetesServicePort,
			BearerToken:     cfg.KubernetesBearerToken,
			BearerTokenFile: cfg.KubernetesServiceAccountTokenFile,
			CAFile:          cfg.KubernetesServiceAccountCAFile,
			HTTPClient:      cfg.KubernetesHTTPClient,
			RequestTimeout:  cfg.KubernetesRequestTimeout,
		})
		if err != nil {
			return nil, fmt.Errorf("kubernetes REST client for volcano queue store: %w", err)
		}
		return runtimeadapter.NewVolcanoQueueStore(runtimeadapter.VolcanoQueueStoreConfig{
			Doer:                   client,
			BaseURL:                client.Host(),
			Namespace:              cfg.VolcanoQueueNamespace,
			EnsurePlatformDefaults: true,
		}), nil
	default:
		return nil, fmt.Errorf("%w: unsupported GPU_SCHEDULING_QUEUE_PROVIDER %q", ports.ErrUnsupported, mode)
	}
}
