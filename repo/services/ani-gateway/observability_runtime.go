package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/ports"
)

// gatewayObservabilityRuntimeConfig 装配可观测性 PromQL 代理服务的运行时配置。
// 复用 INSTANCE_OBSERVABILITY_PROVIDER 与 INSTANCE_OBSERVABILITY_PROMETHEUS_URL
// （与实例观测快照同一组 env），避免运维同时维护两套可观测性 env。
// 当 INSTANCE_OBSERVABILITY_PROVIDER=prometheus_kubernetes 时，
// 快照指标（GetMetrics）与时序图（Query）链路同时启用真实 Prometheus。
type gatewayObservabilityRuntimeConfig struct {
	Provider       string
	PrometheusURL  string
	InstanceLookup runtimeadapter.InstanceLookup
	// K8s API 配置：用于实例 pod 精确解析（ani.kubercloud.io/instance label），
	// 与实例观测快照链路同源（KUBERNETES_API_HOST 等），根治实例名前缀重叠时
	// 趋势数据互相污染。构造失败时降级回前缀正则，不阻塞启动。
	KubernetesAPIHost                 string
	KubernetesServiceHost             string
	KubernetesServicePort             string
	KubernetesBearerToken             string
	KubernetesServiceAccountTokenFile string
	KubernetesServiceAccountCAFile    string
	HTTPClient                        *http.Client
}

func gatewayObservabilityRuntimeConfigFromEnv(instanceLookup runtimeadapter.InstanceLookup) gatewayObservabilityRuntimeConfig {
	return gatewayObservabilityRuntimeConfig{
		Provider:                          os.Getenv("INSTANCE_OBSERVABILITY_PROVIDER"),
		PrometheusURL:                     os.Getenv("INSTANCE_OBSERVABILITY_PROMETHEUS_URL"),
		InstanceLookup:                    instanceLookup,
		KubernetesAPIHost:                 os.Getenv("KUBERNETES_API_HOST"),
		KubernetesServiceHost:             os.Getenv("KUBERNETES_SERVICE_HOST"),
		KubernetesServicePort:             os.Getenv("KUBERNETES_SERVICE_PORT"),
		KubernetesBearerToken:             os.Getenv("KUBERNETES_BEARER_TOKEN"),
		KubernetesServiceAccountTokenFile: os.Getenv("KUBERNETES_SERVICE_ACCOUNT_TOKEN_FILE"),
		KubernetesServiceAccountCAFile:    os.Getenv("KUBERNETES_SERVICE_ACCOUNT_CA_FILE"),
	}
}

// newGatewayObservabilityService 按 env 装配可观测性 PromQL 代理服务。
// Provider 为 "" / "local" / "not_configured" → 返回 nil，router 回退 local 空结果闭环。
// Provider 为 "prometheus_kubernetes" → 返回真实 PrometheusObservabilityService。
// 其他值 → 返回 ErrUnsupported。
func newGatewayObservabilityService(cfg gatewayObservabilityRuntimeConfig) (ports.ObservabilityService, error) {
	switch provider := strings.TrimSpace(cfg.Provider); provider {
	case "", "local", "not_configured":
		return nil, nil
	case "prometheus_kubernetes":
		// InstanceLookup 允许为 nil：Gateway 启动时 demo instance store 尚未创建，
		// 先构造 service 占位，router 注册 demo instances 后通过 SetInstanceLookup
		// 注入真实 lookup；注入前 QueryRange 返回空结果（见 adapter nil 保护）。
		// PodMatcher：注入实例 pod 精确解析器（K8s instance label），根治实例名前缀
		// 重叠（如 sandbox 与 sandbox-dongjm）时 pod=~ 前缀正则互相命中导致的趋势数据
		// 静默污染；K8s 配置缺失/构造失败时保持 nil，重写层降级回前缀正则（可用性优先）。
		var podMatcher func(ctx context.Context, tenantID, instanceName string) string
		if resolver, err := runtimeadapter.NewInstancePodNamesResolver(runtimeadapter.KubernetesRESTClientConfig{
			Host:            cfg.KubernetesAPIHost,
			ServiceHost:     cfg.KubernetesServiceHost,
			ServicePort:     cfg.KubernetesServicePort,
			BearerToken:     cfg.KubernetesBearerToken,
			BearerTokenFile: cfg.KubernetesServiceAccountTokenFile,
			CAFile:          cfg.KubernetesServiceAccountCAFile,
			HTTPClient:      cfg.HTTPClient,
		}); err == nil {
			podMatcher = resolver.Matcher
		}
		service, err := runtimeadapter.NewPrometheusObservabilityService(runtimeadapter.PrometheusObservabilityServiceConfig{
			PrometheusURL:  cfg.PrometheusURL,
			InstanceLookup: cfg.InstanceLookup,
			PodMatcher:     podMatcher,
		})
		if err != nil {
			return nil, err
		}
		return service, nil
	default:
		return nil, fmt.Errorf("%w: unsupported INSTANCE_OBSERVABILITY_PROVIDER %q", ports.ErrUnsupported, provider)
	}
}
