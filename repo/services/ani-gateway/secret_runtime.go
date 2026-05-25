package main

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/ports"
)

type gatewaySecretRuntimeConfig struct {
	ProviderMode              string
	KubernetesAPIHost         string
	KubernetesBearerToken     string
	KubernetesProviderManager string
	HTTPClient                *http.Client
}

func gatewaySecretRuntimeConfigFromEnv() gatewaySecretRuntimeConfig {
	return gatewaySecretRuntimeConfig{
		ProviderMode:              os.Getenv("SECRET_PROVIDER_MODE"),
		KubernetesAPIHost:         os.Getenv("KUBERNETES_API_HOST"),
		KubernetesBearerToken:     os.Getenv("KUBERNETES_BEARER_TOKEN"),
		KubernetesProviderManager: os.Getenv("KUBERNETES_PROVIDER_FIELD_MANAGER"),
	}
}

func newGatewaySecretService(cfg gatewaySecretRuntimeConfig) (ports.SecretService, error) {
	switch strings.TrimSpace(cfg.ProviderMode) {
	case "", "local":
		return nil, nil
	case "kubernetes_rest":
		client, err := runtimeadapter.NewKubernetesRESTClient(runtimeadapter.KubernetesRESTClientConfig{
			Host:         cfg.KubernetesAPIHost,
			BearerToken:  cfg.KubernetesBearerToken,
			FieldManager: cfg.KubernetesProviderManager,
			HTTPClient:   cfg.HTTPClient,
		})
		if err != nil {
			return nil, err
		}
		return runtimeadapter.NewLocalSecretService(
			runtimeadapter.WithSecretProviderApply(runtimeadapter.NewKubernetesSecretProviderAdapter(client)),
		), nil
	default:
		return nil, fmt.Errorf("%w: unsupported SECRET_PROVIDER_MODE %q", ports.ErrUnsupported, cfg.ProviderMode)
	}
}
