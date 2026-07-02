package main

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/services/ani-gateway/internal/router"
)

type gatewayWorkloadRuntimeConfig struct {
	ProviderMode              string
	ProviderApplyEnabled      bool
	LifecycleProvider         string
	LifecycleApplyEnabled     bool
	OpsProvider               string
	OpsEnabled                bool
	KubernetesHTTPClient      *http.Client
	KubernetesRequestTimeout  time.Duration
}

func gatewayWorkloadRuntimeConfigFromEnv() gatewayWorkloadRuntimeConfig {
	return gatewayWorkloadRuntimeConfig{
		ProviderMode:             os.Getenv("WORKLOAD_PROVIDER"),
		ProviderApplyEnabled:     strings.EqualFold(strings.TrimSpace(os.Getenv("WORKLOAD_PROVIDER_APPLY_ENABLED")), "true"),
		LifecycleProvider:        os.Getenv("WORKLOAD_LIFECYCLE_PROVIDER"),
		LifecycleApplyEnabled:    strings.EqualFold(strings.TrimSpace(os.Getenv("WORKLOAD_LIFECYCLE_APPLY_ENABLED")), "true"),
		OpsProvider:              os.Getenv("WORKLOAD_OPS_PROVIDER"),
		OpsEnabled:               strings.EqualFold(strings.TrimSpace(os.Getenv("WORKLOAD_OPS_ENABLED")), "true"),
		KubernetesRequestTimeout: gatewayDurationFromEnv("KUBERNETES_REQUEST_TIMEOUT"),
	}
}

func newGatewayInstanceWorkloadRuntime(cfg gatewayWorkloadRuntimeConfig) (router.InstanceWorkloadRuntime, error) {
	switch mode := strings.TrimSpace(cfg.ProviderMode); mode {
	case "", "local", "not_configured":
		return router.DefaultInstanceWorkloadRuntime(), nil
	case "kubernetes_rest":
		client, err := newGatewayKubernetesRESTClient(cfg.KubernetesHTTPClient, cfg.KubernetesRequestTimeout)
		if err != nil {
			return router.InstanceWorkloadRuntime{}, err
		}
		provider := runtimeadapter.NewKubernetesProviderAdapter(
			client,
			runtimeadapter.WithKubernetesProviderApplyEnabled(cfg.ProviderApplyEnabled),
		)
		runtime := router.InstanceWorkloadRuntime{
			Provider:     "kubernetes_rest",
			DryRun:       provider,
			Apply:        provider,
			StatusReader: provider,
			Ops:          runtimeadapter.NewLocalInstanceOpsGuard(runtimeadapter.WithInstanceOpsEnabled(true)),
		}
		switch strings.TrimSpace(cfg.LifecycleProvider) {
		case "", "local":
		case "kubernetes_rest":
			runtime.Lifecycle = runtimeadapter.NewKubernetesLifecycleExecutor(
				client,
				runtimeadapter.WithKubernetesLifecycleEnabled(cfg.LifecycleApplyEnabled),
			)
		default:
			return router.InstanceWorkloadRuntime{}, fmt.Errorf("%w: unsupported WORKLOAD_LIFECYCLE_PROVIDER %q", ports.ErrUnsupported, cfg.LifecycleProvider)
		}
		switch strings.TrimSpace(cfg.OpsProvider) {
		case "", "local":
		case "kubernetes_rest":
			runtime.Ops = runtimeadapter.NewKubernetesInstanceOps(
				client,
				runtimeadapter.WithKubernetesInstanceOpsEnabled(cfg.OpsEnabled),
			)
		default:
			return router.InstanceWorkloadRuntime{}, fmt.Errorf("%w: unsupported WORKLOAD_OPS_PROVIDER %q", ports.ErrUnsupported, cfg.OpsProvider)
		}
		return runtime, nil
	default:
		return router.InstanceWorkloadRuntime{}, fmt.Errorf("%w: unsupported WORKLOAD_PROVIDER %q", ports.ErrUnsupported, mode)
	}
}
