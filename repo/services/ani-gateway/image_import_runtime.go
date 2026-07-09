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

type gatewayImageImportRuntimeConfig struct {
	ProviderMode             string
	UploadProxyURL           string
	KubernetesHTTPClient     *http.Client
	KubernetesRequestTimeout time.Duration
}

func gatewayImageImportRuntimeConfigFromEnv() gatewayImageImportRuntimeConfig {
	return gatewayImageImportRuntimeConfig{
		ProviderMode:             os.Getenv("IMAGE_IMPORT_PROVIDER"),
		UploadProxyURL:           os.Getenv("CDI_UPLOADPROXY_URL"),
		KubernetesRequestTimeout: gatewayDurationFromEnv("KUBERNETES_REQUEST_TIMEOUT"),
	}
}

func newGatewayImageImportService(cfg gatewayImageImportRuntimeConfig) (ports.ImageImportService, error) {
	switch mode := strings.TrimSpace(cfg.ProviderMode); mode {
	case "", "local":
		if strings.TrimSpace(cfg.UploadProxyURL) == "" {
			return runtimeadapter.NewLocalImageImportService(), nil
		}
		return runtimeadapter.NewLocalImageImportService(runtimeadapter.WithImageImportUploadBaseURL(cfg.UploadProxyURL)), nil
	case "cdi_rest":
		if strings.TrimSpace(cfg.UploadProxyURL) == "" {
			return nil, fmt.Errorf("%w: image import provider cdi_rest requires CDI_UPLOADPROXY_URL", ports.ErrInvalid)
		}
		cdiClient, err := runtimeadapter.NewCDIKubernetesRESTClient(gatewayKubernetesRESTClientConfig(cfg.KubernetesHTTPClient, cfg.KubernetesRequestTimeout))
		if err != nil {
			return nil, err
		}
		return runtimeadapter.NewCDIImageImportService(cdiClient, cfg.UploadProxyURL), nil
	default:
		return nil, fmt.Errorf("%w: unsupported IMAGE_IMPORT_PROVIDER %q", ports.ErrUnsupported, mode)
	}
}
